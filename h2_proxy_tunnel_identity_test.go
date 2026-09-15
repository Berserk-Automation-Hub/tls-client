package tls_client

// The HTTP/2 tunnel to an https:// proxy must speak the SAME HTTP/2 identity as the origin.
//
// connect.go built a ZERO-VALUE http2.Transport for that tunnel, so a client carrying a browser
// profile spoke a browser's HTTP/2 to the origin and fhttp's own defaults to the proxy: different
// SETTINGS, a different SETTINGS ORDER, no connection WINDOW_UPDATE, no HEADERS-embedded PRIORITY, a
// different pseudo-header order and no HPACK indexing policy -- on the first frames of the
// connection, before any request. Two HTTP/2 identities in one binary, and the proxy operator sees
// the one that is not a browser.
//
// This test stands up a loopback https:// proxy that negotiates h2, drives one request through it,
// and reads the client's own bytes: the SETTINGS frame, the connection-preface WINDOW_UPDATE, and
// the CONNECT request's header block. Every assertion is against the PROFILE, so it fails if the
// tunnel ever drifts from it again rather than against a copy of today's output.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/fhttp/http2"
	"github.com/Berserk-Automation-Hub/fhttp/http2/hpack"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
)

type proxyTunnelObservation struct {
	settings      []http2.SettingID
	settingValues map[http2.SettingID]uint32
	windowUpdate  uint32
	headerBlock   []byte
	headersFlags  byte
	priority      struct {
		streamDep uint32
		exclusive bool
		weight    uint8
		present   bool
	}
}

// serveH2ProxyOnce reads the client half of one HTTP/2 connection far enough to see the CONNECT
// HEADERS, answers 200 so the tunnel completes the way a real h2 proxy would, and then holds the
// connection open until the test tears it down. Answering matters: a CONNECT stream carries a body
// pipe that stays open for the life of the tunnel, so a proxy that simply hung up would leave
// RoundTrip blocked on a body writer that has nowhere to go.
func serveH2ProxyOnce(conn net.Conn, out chan<- proxyTunnelObservation, errc chan<- error) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	preface := make([]byte, len("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	if _, err := io.ReadFull(conn, preface); err != nil {
		errc <- fmt.Errorf("read preface: %w", err)
		return
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0x04, 0, 0, 0, 0, 0}); err != nil {
		errc <- fmt.Errorf("write SETTINGS: %w", err)
		return
	}

	var obs proxyTunnelObservation
	obs.settingValues = map[http2.SettingID]uint32{}
	for {
		hdr := make([]byte, 9)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			if !errors.Is(err, io.EOF) {
				errc <- fmt.Errorf("read frame header: %w", err)
			}
			return
		}
		length := int(hdr[0])<<16 | int(hdr[1])<<8 | int(hdr[2])
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			errc <- fmt.Errorf("read frame payload: %w", err)
			return
		}
		switch hdr[3] {
		case 0x04: // SETTINGS
			if hdr[4]&0x01 != 0 { // ACK
				continue
			}
			for i := 0; i+6 <= len(payload); i += 6 {
				id := http2.SettingID(uint16(payload[i])<<8 | uint16(payload[i+1]))
				v := uint32(payload[i+2])<<24 | uint32(payload[i+3])<<16 | uint32(payload[i+4])<<8 | uint32(payload[i+5])
				obs.settings = append(obs.settings, id)
				obs.settingValues[id] = v
			}
		case 0x08: // WINDOW_UPDATE
			if len(payload) == 4 {
				obs.windowUpdate = (uint32(payload[0])<<24 | uint32(payload[1])<<16 |
					uint32(payload[2])<<8 | uint32(payload[3])) & 0x7fffffff
			}
		case 0x01: // HEADERS
			obs.headersFlags = hdr[4]
			body := payload
			var padLen int
			if hdr[4]&0x08 != 0 {
				padLen = int(body[0])
				body = body[1:]
			}
			if hdr[4]&0x20 != 0 {
				obs.priority.present = true
				obs.priority.streamDep = (uint32(body[0])<<24 | uint32(body[1])<<16 |
					uint32(body[2])<<8 | uint32(body[3])) & 0x7fffffff
				obs.priority.exclusive = body[0]&0x80 != 0
				obs.priority.weight = body[4]
				body = body[5:]
			}
			obs.headerBlock = body[:len(body)-padLen]
			out <- obs

			// HEADERS, END_HEADERS, stream 1, one octet: 0x88 is RFC 7541 static index 8, ":status 200".
			if _, err := conn.Write([]byte{0, 0, 1, 0x01, 0x04, 0, 0, 0, 1, 0x88}); err != nil {
				errc <- fmt.Errorf("write 200: %w", err)
				return
			}
			// Drain until the caller closes; the deadline above bounds it.
			_, _ = io.Copy(io.Discard, conn)
			return
		}
	}
}

// pseudoHeaderOrderOf returns the pseudo-header names of a block in wire order.
func pseudoHeaderOrderOf(t *testing.T, block []byte) []string {
	t.Helper()
	var order []string
	dec := hpack.NewDecoder(4096, func(f hpack.HeaderField) {
		if strings.HasPrefix(f.Name, ":") {
			order = append(order, f.Name)
		}
	})
	if _, err := dec.Write(block); err != nil {
		t.Fatalf("decode header block: %v", err)
	}
	if err := dec.Close(); err != nil {
		t.Fatalf("close decoder: %v", err)
	}
	return order
}

// TestH2ProxyTunnelCarriesTheClientsHTTP2Identity is the WIRING half: the identity a caller's
// profile and TransportOptions describe must reach the dialer client.go builds. It is white-box on
// purpose -- the two lines it guards (client.go's newConnectDialer calls) are exactly the ones an
// upstream merge silently drops.
func TestH2ProxyTunnelCarriesTheClientsHTTP2Identity(t *testing.T) {
	policy := func(f hpack.HeaderField) bool { return !strings.HasPrefix(f.Name, ":") }
	profile := profiles.Chrome_133

	client, err := NewHttpClient(NewNoopLogger(),
		WithClientProfile(profile),
		WithTimeoutSeconds(10),
		WithDisableHttp3(),
		WithProxyUrl("https://user:pass@127.0.0.1:65535"),
		WithTransportOptions(&TransportOptions{HPACKIndexingPolicy: policy}),
	)
	if err != nil {
		t.Fatalf("NewHttpClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)

	hc, ok := client.(*httpClient)
	if !ok {
		t.Fatalf("NewHttpClient returned %T, not *httpClient", client)
	}
	cd, ok := hc.dialer.(*connectDialer)
	if !ok {
		t.Fatalf("an https:// proxy produced dialer %T, not *connectDialer", hc.dialer)
	}
	if cd.h2 == nil {
		t.Fatal("the proxy dialer carries no HTTP/2 identity: its tunnel would build a zero-value " +
			"http2.Transport and speak fhttp's defaults to the proxy while the origin speaks the profile")
	}
	if !reflect.DeepEqual(cd.h2.settingsOrder, profile.GetSettingsOrder()) {
		t.Fatalf("the dialer's SETTINGS order is %v, the profile's is %v", cd.h2.settingsOrder, profile.GetSettingsOrder())
	}
	if cd.h2.connectionFlow != profile.GetConnectionFlow() {
		t.Fatalf("the dialer's connection flow is %d, the profile's is %d", cd.h2.connectionFlow, profile.GetConnectionFlow())
	}
	if cd.h2.indexingPolicy == nil {
		t.Fatal("the dialer carries no HPACK indexing policy, so the CONNECT header block would be " +
			"encoded by a different rule than every origin request on the same client")
	}
}

// TestH2ProxyTunnelSpeaksTheProfilesHTTP2Identity is the WIRE half: it drives the SHIPPED path —
// NewHttpClient with an https:// proxy — against a loopback proxy that negotiates h2, and reads the
// client's own bytes off the socket.
//
// It reaches the proxy through the real client rather than through newConnectDialer because patch 2
// also plumbs the caller's certificate-verification identity to the proxy leg. Before that,
// WithInsecureSkipVerify — documented as client-wide — silently did not apply to the connection TO
// the proxy, so a self-signed proxy was unreachable from the public API and this test could only
// have been written against internals.
func TestH2ProxyTunnelSpeaksTheProfilesHTTP2Identity(t *testing.T) {
	ln := listenTLSALPNH2(t)
	out := make(chan proxyTunnelObservation, 4)
	errc := make(chan error, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveH2ProxyOnce(conn, out, errc)
		}
	}()

	profile := profiles.Chrome_133
	policy := func(f hpack.HeaderField) bool {
		if strings.HasPrefix(f.Name, ":") {
			return f.Name == ":authority"
		}
		return true
	}
	client, err := NewHttpClient(NewNoopLogger(),
		WithClientProfile(profile),
		WithInsecureSkipVerify(),
		// Short: the tunnel completes, then the request past it hangs because nothing is listening on
		// the other side. Every byte this test reads was written before that point.
		WithTimeoutSeconds(2),
		WithDisableHttp3(),
		WithProxyUrl("https://"+ln.Addr().String()),
		WithTransportOptions(&TransportOptions{HPACKIndexingPolicy: policy}),
	)
	if err != nil {
		t.Fatalf("NewHttpClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)

	req, err := http.NewRequest(http.MethodGet, "https://origin.invalid/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// The tunnel completes (the proxy answers the CONNECT with 200); the request beyond it does not,
	// because nothing is listening on the other side. The bytes the client already wrote to the proxy
	// are the point.
	_, _ = client.Do(req)

	var obs proxyTunnelObservation
	select {
	case obs = <-out:
	case err := <-errc:
		// "bad certificate" here means WithInsecureSkipVerify did not reach the proxy leg, which is
		// half of what patch 2 fixes — say so rather than leaving a TLS error to be interpreted.
		t.Fatalf("proxy: %v (a certificate error here means the client's WithInsecureSkipVerify is not "+
			"reaching the TLS dial to the PROXY, only the one to the origin)", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the client sent no HEADERS to the proxy")
	}

	// ---- SETTINGS: the profile's set, in the profile's order. ----
	wantOrder := profile.GetSettingsOrder()
	wantValues := profile.GetSettings()
	if !reflect.DeepEqual(obs.settings, wantOrder) {
		t.Fatalf("the tunnel sent SETTINGS %v; the profile's order is %v. A zero-value http2.Transport "+
			"sends fhttp's own four in Go map order, which is both a different SET and a RANDOM order",
			obs.settings, wantOrder)
	}
	for id, want := range wantValues {
		if got := obs.settingValues[id]; got != want {
			t.Fatalf("the tunnel sent SETTINGS[%v] = %d; the profile says %d", id, got, want)
		}
	}

	// ---- Connection-preface WINDOW_UPDATE: the profile's connection flow. ----
	if want := profile.GetConnectionFlow(); obs.windowUpdate != want {
		t.Fatalf("the tunnel's stream-0 WINDOW_UPDATE delta is %d; the profile says %d (0 means it sent "+
			"none at all, which is what a zero-value Transport does)", obs.windowUpdate, want)
	}

	// ---- HEADERS-embedded PRIORITY: present, and the profile's shape. ----
	if hp := profile.GetHeaderPriority(); hp != nil {
		if !obs.priority.present {
			t.Fatalf("the tunnel's CONNECT HEADERS carried no embedded PRIORITY; the profile declares "+
				"dep=%d exclusive=%v weight=%d", hp.StreamDep, hp.Exclusive, hp.Weight)
		}
		if obs.priority.streamDep != hp.StreamDep || obs.priority.exclusive != hp.Exclusive || obs.priority.weight != hp.Weight {
			t.Fatalf("the tunnel's embedded PRIORITY is dep=%d exclusive=%v weight=%d; the profile says "+
				"dep=%d exclusive=%v weight=%d", obs.priority.streamDep, obs.priority.exclusive,
				obs.priority.weight, hp.StreamDep, hp.Exclusive, hp.Weight)
		}
	}

	// ---- HPACK indexing policy: :method CONNECT is spelled out WITHOUT indexing. ----
	// Without the policy the same field is 0x43 (incremental indexing). This is the one assertion
	// that can only be satisfied by the policy having reached the TUNNEL's encoder specifically.
	if got := firstOctetOfName(t, obs.headerBlock, ":method"); got != 0x02 {
		t.Fatalf("the tunnel spelled :method CONNECT with first octet 0x%02x; with the indexing policy "+
			"installed it is 0x02 (without indexing, static name index 2). 0x43 means the policy never "+
			"reached the tunnel's hpack.Encoder", got)
	}

	// ---- Pseudo-header order: RFC 9113 §8.5 leaves CONNECT with {:method, :authority}. ----
	if order := pseudoHeaderOrderOf(t, obs.headerBlock); len(order) != 2 || order[0] != ":method" || order[1] != ":authority" {
		t.Fatalf("the tunnel's CONNECT carried pseudo-headers %v; RFC 9113 §8.5 leaves exactly "+
			"[:method :authority]", order)
	}

	t.Logf("the proxy tunnel speaks the profile's HTTP/2: SETTINGS %v, WINDOW_UPDATE %d, embedded "+
		"PRIORITY present, :method without indexing", obs.settings, obs.windowUpdate)
}
