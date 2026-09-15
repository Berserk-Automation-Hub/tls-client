package tls_client

// TransportOptions.HPACKIndexingPolicy must reach the HPACK encoder of the FIRST connection.
//
// RFC 7541 lets an encoder represent one header field several ways, so the representation it picks
// is an encoder signature rather than a protocol fact. fhttp exposes the choice through
// http2.Transport.HPACKIndexingPolicy (its hpack.Encoder.SetIndexingPolicy hook, the same shape as
// quiche's HpackEncoder should_index_); this file proves the field survives the trip from a caller's
// TransportOptions, through newRoundTripper and getTransport, into the encoder that writes the
// bytes -- and that it is genuinely load-bearing, by running the identical request with the policy
// absent and watching the octet change.
//
// The assertion is made on the RAW header block fragment read off the socket, not through a decoder,
// because a decoder deliberately hides the difference the test is about.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/fhttp/http2/hpack"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
	tls "github.com/Berserk-Automation-Hub/utls"
)

// RFC 7541 Appendix A: ":path /" is entry 4 and ":path /index.html" is entry 5. An encoder is free
// to cite either when it needs the NAME only; these are the two first octets that result.
const (
	pathWithoutIndexingNameIdx4 = 0x04 // 0000 0100 -- 6.2.2 without indexing, name index 4
	pathIncrementalNameIdx4     = 0x44 // 0100 0100 -- 6.2.1 with incremental indexing, name index 4
)

// hpackPolicyTestServer is the smallest peer that makes a client send a request HEADERS block: it
// terminates TLS with ALPN h2, answers the preface with an empty SETTINGS frame, and hands back the
// first header block fragment it reads. It never answers the request, so every caller below expects
// the round trip itself to fail.
type hpackPolicyTestServer struct {
	addr  string
	block chan []byte
	errc  chan error
}

// listenTLSALPNH2 is a loopback TLS listener with a throwaway self-signed certificate that
// negotiates h2. Callers use WithInsecureSkipVerify, so the certificate only has to parse.
func listenTLSALPNH2(t *testing.T) net.Listener {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "hpack-policy.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"hpack-policy.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("self-sign: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func startHPACKPolicyServer(t *testing.T) *hpackPolicyTestServer {
	t.Helper()
	ln := listenTLSALPNH2(t)

	s := &hpackPolicyTestServer{
		addr:  ln.Addr().String(),
		block: make(chan []byte, 8),
		errc:  make(chan error, 8),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *hpackPolicyTestServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	preface := make([]byte, len("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	if _, err := io.ReadFull(conn, preface); err != nil {
		s.errc <- fmt.Errorf("read preface: %w", err)
		return
	}
	// An empty SETTINGS frame: type 0x04, no flags, stream 0, length 0.
	if _, err := conn.Write([]byte{0, 0, 0, 0x04, 0, 0, 0, 0, 0}); err != nil {
		s.errc <- fmt.Errorf("write SETTINGS: %w", err)
		return
	}

	for {
		hdr := make([]byte, 9)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			if !errors.Is(err, io.EOF) {
				s.errc <- fmt.Errorf("read frame header: %w", err)
			}
			return
		}
		length := int(hdr[0])<<16 | int(hdr[1])<<8 | int(hdr[2])
		typ := hdr[3]
		flags := hdr[4]
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			s.errc <- fmt.Errorf("read frame payload: %w", err)
			return
		}
		if typ != 0x01 { // not HEADERS
			continue
		}
		// Strip padding and the optional embedded PRIORITY, leaving the header block fragment.
		var padLen int
		if flags&0x08 != 0 { // PADDED
			if len(payload) == 0 {
				s.errc <- errors.New("PADDED HEADERS with an empty payload")
				return
			}
			padLen = int(payload[0])
			payload = payload[1:]
		}
		if flags&0x20 != 0 { // PRIORITY
			if len(payload) < 5 {
				s.errc <- errors.New("PRIORITY HEADERS shorter than its own priority fields")
				return
			}
			_ = binary.BigEndian.Uint32(payload[:4])
			payload = payload[5:]
		}
		if padLen > len(payload) {
			s.errc <- errors.New("HEADERS padding longer than the block")
			return
		}
		s.block <- payload[:len(payload)-padLen]
		return
	}
}

// hpackStaticNames is RFC 7541 Appendix A, indices 1..61, names only. A protocol constant: every
// HPACK implementation shares it, so it carries no fingerprint.
var hpackStaticNames = []string{
	"", ":authority", ":method", ":method", ":path", ":path", ":scheme", ":scheme",
	":status", ":status", ":status", ":status", ":status", ":status", ":status",
	"accept-charset", "accept-encoding", "accept-language", "accept-ranges", "accept",
	"access-control-allow-origin", "age", "allow", "authorization", "cache-control",
	"content-disposition", "content-encoding", "content-language", "content-length",
	"content-location", "content-range", "content-type", "cookie", "date", "etag", "expect",
	"expires", "from", "host", "if-match", "if-modified-since", "if-none-match", "if-range",
	"if-unmodified-since", "last-modified", "link", "location", "max-forwards",
	"proxy-authenticate", "proxy-authorization", "range", "referer", "refresh", "retry-after",
	"server", "set-cookie", "strict-transport-security", "transfer-encoding", "user-agent",
	"vary", "via", "www-authenticate",
}

// readVarint reads an RFC 7541 5.1 integer with an n-bit prefix, returning the value and the number
// of octets it occupied.
func readVarint(t *testing.T, b []byte, prefixBits uint8) (uint64, int) {
	t.Helper()
	mask := uint64(1)<<prefixBits - 1
	v := uint64(b[0]) & mask
	if v < mask {
		return v, 1
	}
	var m uint
	i := 1
	for {
		if i >= len(b) {
			t.Fatal("truncated HPACK integer")
		}
		c := b[i]
		i++
		v += uint64(c&0x7f) << m
		if c&0x80 == 0 {
			return v, i
		}
		m += 7
		if m > 56 {
			t.Fatal("HPACK integer too long")
		}
	}
}

// skipString skips one RFC 7541 5.2 string literal, Huffman-coded or not, and returns its total
// length in octets. The contents are never decoded: this walker identifies representations, and the
// only names it has to recognise are static-table references.
func skipString(t *testing.T, b []byte) int {
	t.Helper()
	n, used := readVarint(t, b, 7)
	if used+int(n) > len(b) {
		t.Fatal("truncated HPACK string literal")
	}
	return used + int(n)
}

// firstOctetOfPath walks the header block by RFC 7541 prefix bits and returns the first octet of the
// representation whose name is ":path". It shares no code with the encoder under test, which is the
// point: a decoder would resolve every representation to the same header list and hide the
// difference this test is about.
//
// It resolves names from the STATIC table only, and fails loudly on a dynamic reference, because
// every call below reads the FIRST header block of a FRESH connection, where the dynamic table is
// still empty. A dynamic index here would mean the test is reading the wrong block.
func firstOctetOfPath(t *testing.T, block []byte) byte {
	t.Helper()
	return firstOctetOfName(t, block, ":path")
}

// firstOctetOfName is firstOctetOfPath for any header name.
func firstOctetOfName(t *testing.T, block []byte, want string) byte {
	t.Helper()
	nameAt := func(idx uint64) string {
		if idx == 0 || idx >= uint64(len(hpackStaticNames)) {
			t.Fatalf("header index %d is not a static-table entry; this is not the first block of a fresh connection", idx)
		}
		return hpackStaticNames[idx]
	}

	for i := 0; i < len(block); {
		start := block[i]
		switch {
		case start&0x80 != 0: // 6.1 indexed header field
			idx, used := readVarint(t, block[i:], 7)
			if nameAt(idx) == want {
				return start
			}
			i += used

		case start&0xe0 == 0x20: // 6.3 dynamic table size update
			_, used := readVarint(t, block[i:], 5)
			i += used

		default: // 6.2.1 / 6.2.2 / 6.2.3 literal
			prefixBits := uint8(4) // without indexing and never indexed
			if start&0xc0 == 0x40 {
				prefixBits = 6 // with incremental indexing
			}
			idx, used := readVarint(t, block[i:], prefixBits)
			j := i + used
			var name string
			if idx == 0 {
				// A literal NEW name. Never a pseudo-header in practice -- all four are in the static
				// table -- so it is enough to skip it.
				j += skipString(t, block[j:])
			} else {
				name = nameAt(idx)
			}
			j += skipString(t, block[j:]) // the value
			if name == want {
				return start
			}
			i = j
		}
	}
	t.Fatalf("no %s field in the header block", want)
	return 0
}

func runHPACKPolicyRequest(t *testing.T, s *hpackPolicyTestServer, policy func(hpack.HeaderField) bool) byte {
	t.Helper()
	client, err := NewHttpClient(NewNoopLogger(),
		WithClientProfile(profiles.Chrome_133),
		WithInsecureSkipVerify(),
		WithTimeoutSeconds(10),
		WithDisableHttp3(),
		WithTransportOptions(&TransportOptions{HPACKIndexingPolicy: policy}),
	)
	if err != nil {
		t.Fatalf("NewHttpClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)

	req, err := http.NewRequest(http.MethodGet, "https://"+s.addr+"/hpack-indexing-policy", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// The server never answers, so the round trip is expected to fail; the bytes it already wrote are
	// the whole point.
	_, _ = client.Do(req)

	select {
	case block := <-s.block:
		return firstOctetOfPath(t, block)
	case err := <-s.errc:
		t.Fatalf("server: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("no HEADERS frame arrived")
	}
	return 0
}

func TestHPACKIndexingPolicyReachesTheEncoder(t *testing.T) {
	s := startHPACKPolicyServer(t)

	// Absent policy: fhttp's own rule indexes the pseudo-header, so :path is 6.2.1.
	if got := runHPACKPolicyRequest(t, s, nil); got != pathIncrementalNameIdx4 {
		t.Fatalf("with no policy, :path first octet = 0x%02x, want 0x%02x (incremental indexing, name index 4)",
			got, pathIncrementalNameIdx4)
	}

	// Present policy: refuse to index any pseudo-header but :authority, which is what quiche's
	// HpackEncoder DefaultPolicy does. :path becomes 6.2.2.
	policy := func(f hpack.HeaderField) bool {
		if strings.HasPrefix(f.Name, ":") {
			return f.Name == ":authority"
		}
		return true
	}
	if got := runHPACKPolicyRequest(t, s, policy); got != pathWithoutIndexingNameIdx4 {
		t.Fatalf("with a policy that refuses :path, its first octet = 0x%02x, want 0x%02x (without indexing, "+
			"name index 4); TransportOptions.HPACKIndexingPolicy is not reaching hpack.Encoder", got, pathWithoutIndexingNameIdx4)
	}
}
