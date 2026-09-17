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
	"crypto/x509"
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
	settings       []http2.SettingID
	settingValues  map[http2.SettingID]uint32
	windowUpdate   uint32
	priorityFrames []http2.Priority
	headerStreamID uint32
	headerBlock    []byte
	headersFlags   byte
	priority       struct {
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
		case 0x02: // PRIORITY
			if len(payload) == 5 {
				obs.priorityFrames = append(obs.priorityFrames, http2.Priority{
					StreamID: (uint32(hdr[5])<<24 | uint32(hdr[6])<<16 |
						uint32(hdr[7])<<8 | uint32(hdr[8])) & 0x7fffffff,
					PriorityParam: http2.PriorityParam{
						StreamDep: (uint32(payload[0])<<24 | uint32(payload[1])<<16 |
							uint32(payload[2])<<8 | uint32(payload[3])) & 0x7fffffff,
						Exclusive: payload[0]&0x80 != 0,
						Weight:    payload[4],
					},
				})
			}
		case 0x08: // WINDOW_UPDATE
			if len(payload) == 4 {
				obs.windowUpdate = (uint32(payload[0])<<24 | uint32(payload[1])<<16 |
					uint32(payload[2])<<8 | uint32(payload[3])) & 0x7fffffff
			}
		case 0x01: // HEADERS
			obs.headersFlags = hdr[4]
			obs.headerStreamID = (uint32(hdr[5])<<24 | uint32(hdr[6])<<16 |
				uint32(hdr[7])<<8 | uint32(hdr[8])) & 0x7fffffff
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

			// HEADERS, END_HEADERS, one octet: 0x88 is RFC 7541 static index 8, ":status 200".
			// On the stream the client actually opened -- which is NOT always 1: the profile's
			// initialStreamID and its PRIORITY frames both move it.
			sid := obs.headerStreamID
			if _, err := conn.Write([]byte{0, 0, 1, 0x01, 0x04,
				byte(sid >> 24), byte(sid >> 16), byte(sid >> 8), byte(sid), 0x88}); err != nil {
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

// observeProxyTunnel drives the SHIPPED public API -- NewHttpClient with an https:// proxy -- at a
// loopback proxy that negotiates h2, and hands back the client's own first frames.
func observeProxyTunnel(t *testing.T, profile profiles.ClientProfile, policy func(hpack.HeaderField) bool) proxyTunnelObservation {
	t.Helper()
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

	select {
	case obs := <-out:
		return obs
	case err := <-errc:
		// "bad certificate" here means WithInsecureSkipVerify did not reach the proxy leg, which is
		// half of what patch 2 fixes -- say so rather than leaving a TLS error to be interpreted.
		t.Fatalf("proxy: %v (a certificate error here means the client's WithInsecureSkipVerify is not "+
			"reaching the TLS dial to the PROXY, only the one to the origin)", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the client sent no HEADERS to the proxy")
	}
	return proxyTunnelObservation{}
}

// expectedFirstStreamID is the stream id fhttp will open the CONNECT on, derived from the profile
// rather than assumed: newClientConn seeds nextStreamID from Transport.InitialStreamID when it is
// non-zero, and the PRIORITY loop in setupConn then leaves it at lastPriority.StreamID+2
// (fhttp http2/transport.go:939 and :976). With neither, fhttp's own 1.
func expectedFirstStreamID(p profiles.ClientProfile) uint32 {
	if prios := p.GetPriorities(); len(prios) > 0 {
		return prios[len(prios)-1].StreamID + 2
	}
	if id := p.GetStreamID(); id != 0 {
		return id
	}
	return 1
}

// TestH2ProxyTunnelSpeaksTheProfilesHTTP2Identity is the WIRE half: it drives the SHIPPED path --
// NewHttpClient with an https:// proxy -- against a loopback proxy that negotiates h2, and reads the
// client's own bytes off the socket.
//
// It reaches the proxy through the real client rather than through newConnectDialer because patch 2
// also plumbs the caller's certificate-verification identity to the proxy leg. Before that,
// WithInsecureSkipVerify -- documented as client-wide -- silently did not apply to the connection TO
// the proxy, so a self-signed proxy was unreachable from the public API and this test could only
// have been written against internals.
//
// THREE profiles, because ONE could not tell the patch from fhttp's defaults. Every assertion is
// against the profile under test, but a Chrome profile happens to agree with fhttp on three of the
// fields h2Identity carries -- connectionFlow (fhttp's transportDefaultConnFlow is literally
// 15663105), headerPriority (Chrome declares none and fhttp embeds its own) and priorities (Chrome
// declares none) -- so a Chrome-only wire test passes with those assignments deleted. Firefox_102
// disagrees with fhttp on all three. initialStreamID no in-tree profile can move at all, so the
// third case is a CUSTOM profile, the shape cffi_src/factory.go builds from a caller's JSON.
func TestH2ProxyTunnelSpeaksTheProfilesHTTP2Identity(t *testing.T) {
	// A custom client profile is the only thing that can move initialStreamID: all 83 profiles in
	// profiles.MappedTLSClients declare 0, which is fhttp's "use my own" value. It carries Chrome_133's
	// HELLO and H2 settings because it has to carry something real to reach the proxy at all; it
	// asserts PLUMBING, and claims to be no browser (HR-1: no fingerprint value is invented here --
	// streamID 7 is a caller's knob, and the assertion is that the caller's number arrives).
	base := profiles.Chrome_133
	customStreamID := profiles.NewClientProfile(
		base.GetClientHelloId(), base.GetSettings(), base.GetSettingsOrder(),
		base.GetPseudoHeaderOrder(), base.GetConnectionFlow(), base.GetPriorities(),
		base.GetHeaderPriority(), 7 /* streamID -- the one field under test */, base.GetAllowHTTP(),
		base.GetHttp3Settings(), base.GetHttp3SettingsOrder(), base.GetHttp3PriorityParam(),
		base.GetHttp3PseudoHeaderOrder(), base.GetHttp3SendGreaseFrames(),
	)

	cases := []struct {
		name    string
		profile profiles.ClientProfile
		// what this profile can prove that the others cannot
		distinguishes string
	}{
		{"chrome_133", profiles.Chrome_133, "SETTINGS set and order, HPACK indexing policy, pseudo-header order"},
		{"firefox_102", profiles.Firefox_102, "connectionFlow, headerPriority and the PRIORITY tree, none of which match fhttp's defaults"},
		{"custom profile with a caller's initialStreamID", customStreamID, "initialStreamID"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := tc.profile
			policy := func(f hpack.HeaderField) bool {
				if strings.HasPrefix(f.Name, ":") {
					return f.Name == ":authority"
				}
				return true
			}
			obs := observeProxyTunnel(t, profile, policy)

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
			// fhttp substitutes transportDefaultConnFlow = 15663105 when Transport.ConnectionFlow is 0
			// (http2/transport.go:875), so this assertion only carries the patch for a profile whose
			// own flow is not that number -- firefox_102's is 12517377.
			if want := profile.GetConnectionFlow(); obs.windowUpdate != want {
				t.Fatalf("the tunnel's stream-0 WINDOW_UPDATE delta is %d; the profile says %d. fhttp's "+
					"own default is 15663105, so this is the tunnel speaking fhttp's identity rather "+
					"than the profile's", obs.windowUpdate, want)
			}

			// ---- PRIORITY frames: the profile's tree, in order, right after SETTINGS. ----
			wantPrios := profile.GetPriorities()
			if len(wantPrios) != len(obs.priorityFrames) {
				t.Fatalf("the tunnel opened with %d PRIORITY frames; the profile declares %d (%+v). "+
					"fhttp writes one per entry immediately after SETTINGS, so a tunnel that writes "+
					"none is a tunnel carrying no Priorities at all",
					len(obs.priorityFrames), len(wantPrios), wantPrios)
			}
			for i := range wantPrios {
				if obs.priorityFrames[i] != wantPrios[i] {
					t.Fatalf("PRIORITY frame %d on the tunnel is %+v; the profile declares %+v",
						i, obs.priorityFrames[i], wantPrios[i])
				}
			}

			// ---- The CONNECT stream id: seeded by initialStreamID, then moved by the PRIORITY tree. ----
			if want := expectedFirstStreamID(profile); obs.headerStreamID != want {
				t.Fatalf("the tunnel opened the CONNECT on stream %d; this profile's identity puts it "+
					"on %d. fhttp seeds nextStreamID from Transport.InitialStreamID only when it is "+
					"non-zero, so a tunnel that ignores the profile's first stream id opens on fhttp's 1",
					obs.headerStreamID, want)
			}

			// ---- HEADERS-embedded PRIORITY: present, and the profile's shape. ----
			// fhttp embeds {Exclusive:true Weight:255 StreamDep:0} of its own when
			// Transport.HeaderPriority is nil (http2/transport.go:1543), so "present" proves nothing
			// on a Chrome profile; firefox_102 declares {false, 41, 13}, which fhttp never produces.
			if hp := profile.GetHeaderPriority(); hp != nil {
				if !obs.priority.present {
					t.Fatalf("the tunnel's CONNECT HEADERS carried no embedded PRIORITY; the profile declares "+
						"dep=%d exclusive=%v weight=%d", hp.StreamDep, hp.Exclusive, hp.Weight)
				}
				if obs.priority.streamDep != hp.StreamDep || obs.priority.exclusive != hp.Exclusive || obs.priority.weight != hp.Weight {
					t.Fatalf("the tunnel's embedded PRIORITY is dep=%d exclusive=%v weight=%d; the profile says "+
						"dep=%d exclusive=%v weight=%d. fhttp's own default is dep=0 exclusive=true "+
						"weight=255, which is what a Transport with no HeaderPriority emits",
						obs.priority.streamDep, obs.priority.exclusive, obs.priority.weight,
						hp.StreamDep, hp.Exclusive, hp.Weight)
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

			// ---- Pseudo-header order: RFC 9113 8.5 leaves CONNECT with {:method, :authority}. ----
			if order := pseudoHeaderOrderOf(t, obs.headerBlock); len(order) != 2 || order[0] != ":method" || order[1] != ":authority" {
				t.Fatalf("the tunnel's CONNECT carried pseudo-headers %v; RFC 9113 8.5 leaves exactly "+
					"[:method :authority]", order)
			}

			t.Logf("the proxy tunnel speaks this profile's HTTP/2 (%s): SETTINGS %v, WINDOW_UPDATE %d, "+
				"%d PRIORITY frames, CONNECT on stream %d, embedded PRIORITY present=%v dep=%d ex=%v w=%d, "+
				":method without indexing", tc.distinguishes, obs.settings, obs.windowUpdate,
				len(obs.priorityFrames), obs.headerStreamID, obs.priority.present,
				obs.priority.streamDep, obs.priority.exclusive, obs.priority.weight)
		})
	}
}

// TestProxyTLSVerificationIsOnUnlessTheCallerTurnedItOff is the SECURE direction of patch 3.
//
// The wire test above proves that a caller who ASKS for InsecureSkipVerify gets it on the proxy leg.
// That is only one half, and the dangerous half is the other one: nothing proved that a caller who
// does NOT ask still verifies the proxy's certificate. Hard-coding `insecureSkipVerify: true` in
// newProxyTLSVerify — which silently disables certificate verification on every https:// proxy
// connection for every caller of this library — used to leave the whole suite green.
//
// Two layers, because a projection that is right and a dial that ignores it are different failures:
// the table checks what newProxyTLSVerify projects, and the wire half drives the shipped
// NewHttpClient against a self-signed loopback proxy and requires the handshake to be REFUSED.
func TestProxyTLSVerificationIsOnUnlessTheCallerTurnedItOff(t *testing.T) {
	// EVERY field newProxyTLSVerify projects, both ways. Five of the six used to be asserted
	// nowhere: rootCAs, randomExtOrder, forceHTTP1 and disableHTTP3 could each be replaced with the
	// zero value and the whole suite stayed green, although the last three decide the ALPN list and
	// the extension order the PROXY sees -- i.e. they are the proxy leg's half of the very
	// "one identity at both layers" claim patches 3 and 5 make.
	t.Run("newProxyTLSVerify carries the caller's answer, both ways", func(t *testing.T) {
		pool := x509.NewCertPool()
		profile := profiles.Chrome_133

		for _, tc := range []struct {
			name string
			// every client-wide answer, set together so a projection that reads the wrong field is
			// visible as well as one that reads none
			on bool
		}{
			{"caller asked for none of them", false},
			{"caller asked for all of them", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cfg := &httpClientConfig{
					insecureSkipVerify:          tc.on,
					withRandomTlsExtensionOrder: tc.on,
					forceHttp1:                  tc.on,
					disableHttp3:                tc.on,
					clientProfile:               profile,
				}
				if tc.on {
					cfg.transportOptions = &TransportOptions{RootCAs: pool}
				}
				v := newProxyTLSVerify(cfg)

				if v.insecureSkipVerify != tc.on {
					t.Fatalf("the caller's insecureSkipVerify is %v and the proxy leg gets %v. "+
						"A proxy leg that skips verification the caller never asked for accepts ANY "+
						"certificate on every https:// proxy connection this library makes",
						tc.on, v.insecureSkipVerify)
				}
				wantPool := pool
				if !tc.on {
					wantPool = nil
				}
				if v.rootCAs != wantPool {
					t.Fatalf("the caller's TransportOptions.RootCAs is %p and the proxy leg gets %p. "+
						"RootCAs is documented as a property of the CLIENT; a proxy leg that drops it "+
						"verifies the proxy against the system roots instead of the caller's, so a "+
						"private CA the caller supplied is rejected on the proxy leg alone",
						wantPool, v.rootCAs)
				}
				// ClientHelloID carries a func field, so it is compared by its Str() identity.
				wantHello := profile.GetClientHelloId()
				if v.helloID.Str() != wantHello.Str() {
					t.Fatalf("the proxy leg's ClientHelloID is %q, the profile's is %q: a zero id "+
						"sends the connection down connect.go's crypto/tls path and the proxy sees a "+
						"Go standard-library hello from a client whose origin traffic is a browser",
						v.helloID.Str(), wantHello.Str())
				}
				if v.randomExtOrder != tc.on {
					t.Fatalf("the caller's withRandomTlsExtensionOrder is %v and the proxy leg gets "+
						"%v: one identity would emit a shuffled hello to the origin and a fixed one "+
						"to the proxy, which is two identities again", tc.on, v.randomExtOrder)
				}
				if v.forceHTTP1 != tc.on {
					t.Fatalf("the caller's forceHttp1 is %v and the proxy leg gets %v: forceHttp1 "+
						"rewrites the hello's ALPN list to exactly [http/1.1] (utls u_parrots.go), so "+
						"a dropped projection offers the proxy h2 and h3 from a client that has "+
						"disabled both", tc.on, v.forceHTTP1)
				}
				if v.disableHTTP3 != tc.on {
					t.Fatalf("the caller's disableHttp3 is %v and the proxy leg gets %v: disableHttp3 "+
						"removes h3 from the hello's ALPN and ALPS lists (utls u_parrots.go), so a "+
						"dropped projection advertises h3 to the proxy from a client that will never "+
						"speak it", tc.on, v.disableHTTP3)
				}
			})
		}
	})

	// The zero ClientHelloID case: a caller-supplied ProxyDialerFactory keeps connect.go's historical
	// crypto/tls path, and NONE of the three hello-shaping answers may be projected onto it -- they
	// are utls's, and there is no utls connection to put them on.
	t.Run("a profile with no ClientHelloID keeps the crypto/tls path", func(t *testing.T) {
		v := newProxyTLSVerify(&httpClientConfig{
			insecureSkipVerify:          true,
			withRandomTlsExtensionOrder: true,
			forceHttp1:                  true,
			disableHttp3:                true,
		})
		if v.helloID.Client != "" || v.randomExtOrder || v.forceHTTP1 || v.disableHTTP3 {
			t.Fatalf("a config with no client profile projected hello=%q randomExtOrder=%v "+
				"forceHTTP1=%v disableHTTP3=%v onto the proxy leg; with no ClientHelloID connect.go "+
				"dials with crypto/tls and these three answers have nowhere to land",
				v.helloID.Str(), v.randomExtOrder, v.forceHTTP1, v.disableHTTP3)
		}
		if !v.insecureSkipVerify {
			t.Fatal("the caller's insecureSkipVerify did not reach the crypto/tls proxy path, which " +
				"is the path a caller-supplied ProxyDialerFactory takes")
		}
	})

	// The WIRE half of rootCAs: a caller who supplies the proxy's own certificate in
	// TransportOptions.RootCAs and does NOT ask to skip verification must get through. This is the
	// direction that cannot be faked by turning verification off -- and the direction in which a
	// dropped `v.rootCAs = config.transportOptions.RootCAs` is silently fatal for every caller with
	// a private CA, because the proxy leg then verifies against the system roots alone.
	t.Run("the shipped client accepts a proxy whose certificate the caller supplied in RootCAs", func(t *testing.T) {
		ln, leaf := listenTLSALPNH2Cert(t)
		pool := x509.NewCertPool()
		pool.AddCert(leaf)

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

		client, err := NewHttpClient(NewNoopLogger(),
			WithClientProfile(profiles.Chrome_133),
			// deliberately NO WithInsecureSkipVerify: the caller's OWN trust anchor is the only
			// reason this handshake can succeed.
			WithTransportOptions(&TransportOptions{RootCAs: pool}),
			WithTimeoutSeconds(2),
			WithDisableHttp3(),
			WithProxyUrl("https://"+ln.Addr().String()),
		)
		if err != nil {
			t.Fatalf("NewHttpClient: %v", err)
		}
		t.Cleanup(client.CloseIdleConnections)

		req, err := http.NewRequest(http.MethodGet, "https://origin.invalid/", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		// The request past the tunnel never completes (nothing is listening on the other side), so
		// Do runs in the background and the CONNECT observation is what this test waits for.
		doErrCh := make(chan error, 1)
		go func() {
			resp, doErr := client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			doErrCh <- doErr
		}()

		select {
		case obs := <-out:
			t.Logf("the proxy leg trusted the caller's own CA and the tunnel completed: SETTINGS %v", obs.settings)
		case doErr := <-doErrCh:
			t.Fatalf("the caller put the proxy's certificate in TransportOptions.RootCAs and the "+
				"tunnel never reached CONNECT (Do returned %v). RootCAs is documented as a property "+
				"of the CLIENT: a proxy leg that does not carry it verifies against the system roots "+
				"alone, so every caller with a private CA gets a certificate error from their own "+
				"proxy and the only workaround is turning verification off entirely", doErr)
		case <-time.After(10 * time.Second):
			doErr := error(nil)
			select {
			case doErr = <-doErrCh:
			default:
			}
			t.Fatalf("the caller put the proxy's certificate in TransportOptions.RootCAs and the "+
				"tunnel never reached CONNECT (Do returned %v). RootCAs is documented as a property "+
				"of the CLIENT: a proxy leg that does not carry it verifies against the system roots "+
				"alone, so every caller with a private CA gets a certificate error from their own "+
				"proxy and the only workaround is turning verification off entirely", doErr)
		}
	})

	t.Run("the shipped client refuses a self-signed proxy it was not told to trust", func(t *testing.T) {
		ln := listenTLSALPNH2(t)
		// The SAME loopback proxy the insecure wire test drives, so the only difference between the
		// two is whether the caller asked to skip verification. It answers the CONNECT with 200,
		// which is what makes a client that got through observable on `out`.
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

		client, err := NewHttpClient(NewNoopLogger(),
			WithClientProfile(profiles.Chrome_133),
			// deliberately NO WithInsecureSkipVerify
			WithTimeoutSeconds(5),
			WithDisableHttp3(),
			WithProxyUrl("https://"+ln.Addr().String()),
		)
		if err != nil {
			t.Fatalf("NewHttpClient: %v", err)
		}
		t.Cleanup(client.CloseIdleConnections)

		req, err := http.NewRequest(http.MethodGet, "https://origin.invalid/", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, doErr := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}

		// The proxy only ever sees a CONNECT if the TLS handshake to it COMPLETED.
		select {
		case obs := <-out:
			t.Fatalf("the client tunnelled a CONNECT (%v) through a self-signed https:// proxy it "+
				"was never told to trust: certificate verification is off on the proxy leg for "+
				"callers who never asked for it, so this library would accept ANY certificate from "+
				"ANY https:// proxy", obs.settings)
		default:
		}

		if doErr == nil {
			t.Fatal("the request through a self-signed https:// proxy succeeded for a client that " +
				"never asked for InsecureSkipVerify")
		}
		if !strings.Contains(doErr.Error(), "certificate") && !strings.Contains(doErr.Error(), "x509") {
			t.Fatalf("the proxy dial failed with %q, which is not a certificate rejection. The proxy "+
				"leg is supposed to verify by default; a different failure means this test is no "+
				"longer observing verification at all", doErr)
		}
		t.Logf("the proxy leg refused the self-signed certificate: %v", doErr)
	})
}
