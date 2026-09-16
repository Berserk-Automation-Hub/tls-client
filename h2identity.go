package tls_client

import (
	"github.com/Berserk-Automation-Hub/fhttp/http2"
	"github.com/Berserk-Automation-Hub/fhttp/http2/hpack"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
)

// h2Identity is the HTTP/2 WIRE IDENTITY a ClientProfile describes, separated from the per-path
// plumbing (dialer, TLS config, timeouts, compression) that a Transport also carries.
//
// It exists because this module builds an http2.Transport in TWO places: one for the origin
// (roundtripper.go) and one for the HTTP/2 tunnel to an https:// proxy (connect.go). The second was
// a ZERO VALUE, so a client with a browser profile spoke a browser's HTTP/2 to the origin and
// fhttp's own defaults to the proxy: different SETTINGS, a different SETTINGS order, no connection
// WINDOW_UPDATE, no HEADERS-embedded PRIORITY, a different pseudo-header order and no HPACK indexing
// policy, on the very first frames of the connection. Two identities in one binary, and the proxy
// operator sees the one that is not a browser.
//
// Keeping the identity in one value means the two paths cannot drift again: a field added here
// reaches both, and a field added to only one Transport literal is visible as an asymmetry.
type h2Identity struct {
	settings          map[http2.SettingID]uint32
	settingsOrder     []http2.SettingID
	priorities        []http2.Priority
	headerPriority    *http2.PriorityParam
	pseudoHeaderOrder []string
	connectionFlow    uint32
	initialStreamID   uint32
	indexingPolicy    func(hpack.HeaderField) bool
}

func newH2Identity(p profiles.ClientProfile, to *TransportOptions) *h2Identity {
	id := &h2Identity{
		settings:          p.GetSettings(),
		settingsOrder:     p.GetSettingsOrder(),
		priorities:        p.GetPriorities(),
		headerPriority:    p.GetHeaderPriority(),
		pseudoHeaderOrder: p.GetPseudoHeaderOrder(),
		connectionFlow:    p.GetConnectionFlow(),
		initialStreamID:   p.GetStreamID(),
	}
	if to != nil {
		id.indexingPolicy = to.HPACKIndexingPolicy
	}
	return id
}

// enablesPush reports whether this identity's SETTINGS advertise SETTINGS_ENABLE_PUSH=1.
//
// Absent is NOT the same as 1. RFC 9113 6.5.2 gives ENABLE_PUSH a default of 1, but a client that
// omits the setting while carrying a browser's identity is a client whose SETTINGS frame does not
// match the browser it claims to be -- and every browser profile states the setting explicitly.
// Treating "absent" as "enabled" would restore the old behaviour for exactly the profiles that
// forgot to say, which is the wrong way round.
func (id *h2Identity) enablesPush() bool {
	v, ok := id.settings[http2.SettingEnablePush]
	return ok && v == 1
}

// apply writes the identity onto a Transport, leaving every per-path field alone. It never reads
// from t, so applying the same identity to two Transports produces two identical wire identities.
func (id *h2Identity) apply(t *http2.Transport) {
	if id == nil {
		return
	}
	t.ConnectionFlow = id.connectionFlow
	t.HeaderPriority = id.headerPriority
	t.InitialStreamID = id.initialStreamID
	t.Priorities = id.priorities
	t.HPACKIndexingPolicy = id.indexingPolicy

	// SERVER PUSH: accept one only if this identity ADVERTISES that it will.
	//
	// This was unconditional, which made the client contradict its own SETTINGS frame. A profile
	// that sends SETTINGS_ENABLE_PUSH=0 -- which every current browser profile does, because no
	// current browser accepts push -- told the peer "do not push" and then accepted a PUSH_PROMISE
	// anyway: allocating a stream, spawning a goroutine and reading the pushed response body.
	//
	// Two costs. A hostile or misconfigured origin can make the client allocate streams and
	// goroutines it advertised it would not accept, unbounded and driven entirely from the far end.
	// And it is a behavioural fingerprint: ONE PUSH_PROMISE distinguishes this client from the
	// browser it claims to be, because a browser that advertises ENABLE_PUSH=0 treats a push as a
	// PROTOCOL_ERROR and closes the connection.
	//
	// fhttp already implements exactly that with a nil handler -- readLoop returns
	// ConnectionError(ErrCodeProtocol), and its own comment there reads "should not be receiving
	// PUSH_PROMISE if ENABLE_PUSH is disabled" -- so the correct behaviour is reached by NOT
	// overriding it, and the override is what had to go.
	//
	// An identity with no settings at all keeps a handler, so a profile-less caller behaves as it
	// always has; that path advertises no SETTINGS frame of its own and therefore contradicts
	// nothing.
	if id.settings == nil || id.enablesPush() {
		t.PushHandler = &http2.DefaultPushHandler{}
	}

	// A nil order means "send none"; a nil slice would make fhttp fall back to its own, which is a
	// different pseudo-header order on the wire.
	if id.pseudoHeaderOrder == nil {
		t.PseudoHeaderOrder = []string{}
	} else {
		t.PseudoHeaderOrder = id.pseudoHeaderOrder
	}

	if id.settings == nil {
		// No profile settings: fhttp's historical defaults. The ORDER of these four is genuinely
		// random (Go map iteration), which is a fingerprint of its own -- it is kept only so a
		// profile-less client behaves as it always has, and no browser profile reaches it.
		t.Settings = map[http2.SettingID]uint32{
			http2.SettingMaxConcurrentStreams: 1000,
			http2.SettingMaxFrameSize:         16384,
			http2.SettingInitialWindowSize:    6291456,
			http2.SettingHeaderTableSize:      65536,
		}
		keys := make([]http2.SettingID, 0, len(t.Settings))
		for k := range t.Settings {
			keys = append(keys, k)
		}
		t.SettingsOrder = keys
		return
	}
	t.Settings = id.settings
	t.SettingsOrder = id.settingsOrder
}

// newProxyTLSVerify projects the caller's client-wide TLS configuration onto the connection to the
// proxy. Both fields it reads are documented as properties of the CLIENT, and upstream applied them
// only to the origin leg.
func newProxyTLSVerify(config *httpClientConfig) proxyTLSVerify {
	v := proxyTLSVerify{insecureSkipVerify: config.insecureSkipVerify}
	if config.transportOptions != nil {
		v.rootCAs = config.transportOptions.RootCAs
	}
	// The ClientHello identity for the PROXY leg is the same one the origin leg uses. Without it an
	// https:// proxy sees a Go standard-library hello from a client whose origin traffic is a
	// browser -- two TLS identities from one session, the proxy operator's one before any CONNECT.
	if config.clientProfile.GetClientHelloId().Client != "" {
		v.helloID = config.clientProfile.GetClientHelloId()
		v.randomExtOrder = config.withRandomTlsExtensionOrder
		v.forceHTTP1 = config.forceHttp1
		v.disableHTTP3 = config.disableHttp3
	}
	return v
}
