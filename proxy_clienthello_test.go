package tls_client

// THE https:// PROXY MUST SEE THE PROFILE'S ClientHello, NOT GO'S.
//
// `connect.go`'s TLS dial to the proxy used `crypto/tls`, so a client carrying a browser profile put
// a browser's ClientHello on its ORIGIN connection and a Go standard-library one on its connection
// to the PROXY — two TLS identities from one session, and the proxy operator sees the one that is
// not a browser, before any CONNECT line, on every tunnel.
//
// This is patch 2 one layer down: patch 2 gave the proxy the profile's HTTP/2, and this gives it the
// profile's TLS.
//
// The proxy below never completes a handshake. It does not need to: the ClientHello is on the wire
// before the server says anything, and reading it is the whole measurement.
//
//	go test -run TestProxyClientHello -v

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
	utls "github.com/Berserk-Automation-Hub/utls"
)

// recordProxyHello accepts one connection, reads the first TLS record, and returns the handshake
// bytes.
func recordProxyHello(t *testing.T) (addr string, get func() []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var hello []byte
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer once.Do(func() { close(done) })
		c, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
		hdr := make([]byte, 5)
		if _, rerr := io.ReadFull(c, hdr); rerr != nil || hdr[0] != 22 {
			return
		}
		body := make([]byte, binary.BigEndian.Uint16(hdr[3:5]))
		if _, rerr := io.ReadFull(c, body); rerr != nil {
			return
		}
		mu.Lock()
		hello = body
		mu.Unlock()
	}()

	return ln.Addr().String(), func() []byte {
		select {
		case <-done:
		case <-time.After(25 * time.Second):
			t.Fatal("no ClientHello reached the proxy within 25s")
		}
		mu.Lock()
		defer mu.Unlock()
		return hello
	}
}

// helloShape is the handful of facts that separate a browser hello from Go's.
type proxyHelloShape struct {
	ciphers    int
	extensions int
	greaseCiph int
	greaseExt  int
	sessionID  int
	// extTypes is the extension type ids in WIRE ORDER, which is what
	// WithRandomTlsExtensionOrder moves.
	extTypes []uint16
	// alpn is the protocol list inside extension 0x0010, which is what WithForceHttp1 and
	// WithDisableHttp3 rewrite.
	alpn []string
}

// parseALPN reads the ALPN extension body: a 2-byte list length, then length-prefixed names.
func parseALPN(b []byte) []string {
	if len(b) < 2 {
		return nil
	}
	out := []string{}
	for i := 2; i < len(b); {
		n := int(b[i])
		i++
		if i+n > len(b) {
			break
		}
		out = append(out, string(b[i:i+n]))
		i += n
	}
	return out
}

func parseProxyHello(t *testing.T, b []byte) proxyHelloShape {
	t.Helper()
	if len(b) < 4 || b[0] != 0x01 {
		t.Fatalf("not a ClientHello (%d bytes)", len(b))
	}
	isG := func(v uint16) bool { return (v&0x0f0f) == 0x0a0a && (v>>8) == (v&0xff) }
	var h proxyHelloShape
	i := 4 + 2 + 32
	h.sessionID = int(b[i])
	i += 1 + h.sessionID
	csLen := int(binary.BigEndian.Uint16(b[i:]))
	i += 2
	for j := 0; j+1 < csLen; j += 2 {
		h.ciphers++
		if isG(binary.BigEndian.Uint16(b[i+j:])) {
			h.greaseCiph++
		}
	}
	i += csLen
	i += 1 + int(b[i])
	extLen := int(binary.BigEndian.Uint16(b[i:]))
	i += 2
	end := i + extLen
	for i+4 <= end && i+4 <= len(b) {
		typ := binary.BigEndian.Uint16(b[i:])
		ln := int(binary.BigEndian.Uint16(b[i+2:]))
		if typ == 0x0010 && i+4+ln <= len(b) {
			h.alpn = parseALPN(b[i+4 : i+4+ln])
		}
		i += 4 + ln
		h.extensions++
		h.extTypes = append(h.extTypes, typ)
		if isG(typ) {
			h.greaseExt++
		}
	}
	return h
}

// recordProxyHellos is recordProxyHello for more than one dial: it accepts connections until the
// test is over and pushes every ClientHello it reads onto the returned channel. It exists because
// WithRandomTlsExtensionOrder is only visible across dials -- one hello cannot be "random".
func recordProxyHellos(t *testing.T) (addr string, hellos <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	out := make(chan []byte, 16)
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
				hdr := make([]byte, 5)
				if _, rerr := io.ReadFull(c, hdr); rerr != nil || hdr[0] != 22 {
					return
				}
				body := make([]byte, binary.BigEndian.Uint16(hdr[3:5]))
				if _, rerr := io.ReadFull(c, body); rerr != nil {
					return
				}
				select {
				case out <- body:
				default:
				}
			}(c)
		}
	}()
	return ln.Addr().String(), out
}

// dialProxyOnce drives the SHIPPED NewHttpClient at a proxy that never answers and returns the
// ClientHello it put on the wire. The handshake cannot complete and does not need to: the hello is
// written before the server says anything.
func dialProxyOnce(t *testing.T, addr string, hellos <-chan []byte, opts ...HttpClientOption) proxyHelloShape {
	t.Helper()
	base := []HttpClientOption{
		WithClientProfile(profiles.Chrome_133),
		WithProxyUrl("https://" + addr),
		WithInsecureSkipVerify(),
		WithTimeoutSeconds(10),
		WithNotFollowRedirects(),
	}
	client, err := NewHttpClient(NewNoopLogger(), append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewHttpClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodGet, "https://origin.invalid/x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	go func() {
		resp, derr := client.Do(req)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		_ = derr
	}()
	select {
	case b := <-hellos:
		return parseProxyHello(t, b)
	case <-time.After(25 * time.Second):
		t.Fatal("no ClientHello reached the proxy within 25s")
	}
	return proxyHelloShape{}
}

// profileALPN is the ALPN list the profile's own ClientHelloSpec declares -- read from the spec, so
// this test states no protocol list of its own (HR-1).
func profileALPN(t *testing.T, p profiles.ClientProfile) []string {
	t.Helper()
	spec, err := p.GetClientHelloSpec()
	if err != nil {
		t.Fatalf("ClientHelloSpec: %v", err)
	}
	for _, e := range spec.Extensions {
		if a, ok := e.(*utls.ALPNExtension); ok {
			return a.AlpnProtocols
		}
	}
	t.Fatal("this profile declares no ALPN extension, so nothing here can be asserted")
	return nil
}

// TestProxyHelloCarriesTheCallersHelloShapingOptions is the WIRE guard on the last three fields
// newProxyTLSVerify projects.
//
// connect.go passes them straight into utls.UClient(conn, cfg, helloID, randomExtOrder, forceHTTP1,
// disableHTTP3), where each one REWRITES the hello: forceHttp1 replaces the ALPN list with exactly
// [http/1.1], disableHttp3 removes "h3" from the ALPN and ALPS lists, and
// withRandomTlsExtensionOrder shuffles the extension order (utls u_parrots.go). Each could be
// replaced by a constant false in newProxyTLSVerify with the whole suite green, although each one
// changes what the PROXY sees on every tunnel -- which is the identity patches 3 and 5 exist to make
// one. Everything here drives the shipped NewHttpClient -> WithProxyUrl("https://...") path.
func TestProxyHelloCarriesTheCallersHelloShapingOptions(t *testing.T) {
	profile := profiles.Chrome_133
	declared := profileALPN(t, profile)
	if len(declared) < 2 {
		t.Fatalf("this profile declares ALPN %v; with fewer than two protocols neither forceHttp1 "+
			"nor disableHttp3 could be distinguished on the wire", declared)
	}

	t.Run("no options: the profile's own ALPN reaches the proxy", func(t *testing.T) {
		addr, hellos := recordProxyHellos(t)
		got := dialProxyOnce(t, addr, hellos)
		if !reflect.DeepEqual(got.alpn, declared) {
			t.Fatalf("the hello to the proxy offers ALPN %v; the profile declares %v", got.alpn, declared)
		}
		t.Logf("proxy leg ALPN with no options: %v", got.alpn)
	})

	t.Run("WithForceHttp1 reaches the proxy leg's ALPN", func(t *testing.T) {
		addr, hellos := recordProxyHellos(t)
		got := dialProxyOnce(t, addr, hellos, WithForceHttp1())
		if len(got.alpn) != 1 || got.alpn[0] != "http/1.1" {
			t.Fatalf("the caller set WithForceHttp1 and the hello to the PROXY still offers ALPN %v "+
				"(the profile declares %v). forceHttp1 is a client-wide answer: a proxy leg that "+
				"drops it offers the proxy h2/h3 from a client that has disabled them, so the two "+
				"legs advertise different protocol sets on the same session",
				got.alpn, declared)
		}
		t.Logf("proxy leg ALPN with WithForceHttp1: %v", got.alpn)
	})

	t.Run("WithDisableHttp3 reaches the proxy leg's ALPN", func(t *testing.T) {
		var want []string
		for _, p := range declared {
			if p != "h3" {
				want = append(want, p)
			}
		}
		if len(want) == len(declared) {
			t.Skipf("this profile's declared ALPN %v carries no h3, so disableHttp3 changes nothing "+
				"on the wire and there is nothing here to observe", declared)
		}
		addr, hellos := recordProxyHellos(t)
		got := dialProxyOnce(t, addr, hellos, WithDisableHttp3())
		if !reflect.DeepEqual(got.alpn, want) {
			t.Fatalf("the caller set WithDisableHttp3 and the hello to the PROXY offers ALPN %v; "+
				"with h3 removed the profile's %v becomes %v. A proxy leg that drops disableHttp3 "+
				"advertises h3 to the proxy from a client that will never speak it",
				got.alpn, declared, want)
		}
		t.Logf("proxy leg ALPN with WithDisableHttp3: %v", got.alpn)
	})

	t.Run("WithRandomTlsExtensionOrder reaches the proxy leg's extension order", func(t *testing.T) {
		// GREASE extension ids are redrawn on every hello by design, and ShuffleChromeTLSExtensions
		// deliberately leaves GREASE and padding where they are -- so the ORDER is the measurement
		// and the ids are not. Normalise every GREASE id to one sentinel before comparing.
		key := func(h proxyHelloShape) string {
			norm := make([]uint16, len(h.extTypes))
			for i, v := range h.extTypes {
				if (v&0x0f0f) == 0x0a0a && (v>>8) == (v&0xff) {
					v = 0x0a0a
				}
				norm[i] = v
			}
			return fmt.Sprint(norm)
		}

		// Control first: WITHOUT the option the order is fixed, so a difference below is the option
		// and not this test watching something that moves on its own.
		addr, hellos := recordProxyHellos(t)
		fixed := key(dialProxyOnce(t, addr, hellos))
		for i := 0; i < 2; i++ {
			if again := key(dialProxyOnce(t, addr, hellos)); again != fixed {
				t.Fatalf("with no WithRandomTlsExtensionOrder the proxy leg's extension order moved "+
					"between dials: %s then %s", fixed, again)
			}
		}

		// Four dials with the option on. Every one of them is an independent shuffle of ~15
		// positionally-variant extensions, so "all four identical to the fixed order" has
		// probability about (1/15!)^4 -- this is not a flake budget, it is a certainty.
		addr2, hellos2 := recordProxyHellos(t)
		seen := map[string]int{}
		for i := 0; i < 4; i++ {
			seen[key(dialProxyOnce(t, addr2, hellos2, WithRandomTLSExtensionOrder()))]++
		}
		if len(seen) == 1 {
			for k := range seen {
				if k == fixed {
					t.Fatalf("the caller set WithRandomTlsExtensionOrder and all four hellos to the "+
						"PROXY carried the profile's FIXED extension order %s. The origin leg would "+
						"be shuffling while the proxy leg is not, which is two TLS identities from "+
						"one session again", k)
				}
			}
		}
		t.Logf("proxy leg extension order with WithRandomTlsExtensionOrder: %d distinct orders in 4 "+
			"dials (fixed order seen %d times)", len(seen), seen[fixed])
	})
}

func TestProxyClientHelloIsTheProfilesNotGos(t *testing.T) {
	addr, get := recordProxyHello(t)

	client, err := NewHttpClient(NewNoopLogger(),
		WithClientProfile(profiles.Chrome_133),
		WithProxyUrl("https://"+addr),
		WithInsecureSkipVerify(),
		WithTimeoutSeconds(10),
		WithNotFollowRedirects(),
	)
	if err != nil {
		t.Fatalf("NewHttpClient: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "https://origin.invalid/x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	go func() {
		resp, derr := client.Do(req)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		_ = derr // the proxy never answers; the hello is the measurement
	}()

	got := parseProxyHello(t, get())
	t.Logf("hello to the proxy: ciphers=%d(grease %d) extensions=%d(grease %d) session_id_len=%d",
		got.ciphers, got.greaseCiph, got.extensions, got.greaseExt, got.sessionID)

	// A browser profile GREASEs. crypto/tls never does — that single fact separates the two
	// identities without naming a codepoint or a count that a profile change would invalidate.
	if got.greaseCiph == 0 && got.greaseExt == 0 {
		t.Fatalf("the ClientHello sent to the https:// proxy carries NO GREASE in its ciphers or "+
			"extensions, so it is not this profile's hello — the proxy leg is dialling with "+
			"crypto/tls while the origin leg uses the browser identity. ciphers=%d extensions=%d",
			got.ciphers, got.extensions)
	}
	// Go's hello carries an empty legacy session id; a Chrome-family hello carries 32 bytes
	// (TLS 1.3 middlebox compatibility mode).
	if got.sessionID != 32 {
		t.Errorf("legacy_session_id is %d bytes; this profile's hello carries 32 (TLS 1.3 "+
			"middlebox compatibility), and an empty one is the standard library's shape", got.sessionID)
	}
}
