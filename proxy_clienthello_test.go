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
	"io"
	"net"
	"sync"
	"testing"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
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
		i += 4 + ln
		h.extensions++
		if isG(typ) {
			h.greaseExt++
		}
	}
	return h
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
