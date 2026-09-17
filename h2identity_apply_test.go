package tls_client

// h2Identity.apply IS the HTTP/2 wire identity.
//
// Every browser-facing field this module puts on an http2.Transport passes through this one method,
// on BOTH paths -- the origin transport in roundtripper.go and the tunnel to an https:// proxy in
// connect.go. Nine assignments, and until this file existed five of them could be deleted with the
// whole suite green, because the only tests that observed them were WIRE tests and fhttp's own
// defaults happen to equal what the in-tree profiles declare:
//
//	t.ConnectionFlow    fhttp's transportDefaultConnFlow is LITERALLY 15663105, which is exactly
//	                    what every Chrome profile declares (http2/transport.go:44, used at :875
//	                    when t.ConnectionFlow == 0), so a WINDOW_UPDATE assertion against a Chrome
//	                    profile agrees with the default it is supposed to be replacing;
//	t.HeaderPriority    fhttp's embedded-PRIORITY default is {Exclusive:true, Weight:255, Dep:0}
//	                    (http2/transport.go:1543), and every Chrome profile declares no header
//	                    priority at all, so the frame is byte-identical either way;
//	t.InitialStreamID   all 83 in-tree profiles declare 0, which IS the "use fhttp's default"
//	                    value, so no profile can move it on the wire;
//	t.Priorities        every Chrome profile declares none.
//
// So the guard belongs HERE, one layer below the wire, where a dropped assignment is visible
// whatever a profile happens to contain. The values below are deliberate NON-browser sentinels:
// this file asserts that apply COPIES what it is given, and the wire tests assert that what it is
// given is the profile's (HR-1 -- no fingerprint value is written here, and none is claimed).

import (
	"reflect"
	"testing"

	"github.com/Berserk-Automation-Hub/fhttp/http2"
	"github.com/Berserk-Automation-Hub/fhttp/http2/hpack"
)

// sentinelIdentity is an h2Identity in which every field carries a value that is NOT any browser's
// and NOT fhttp's default, so each assignment in apply is distinguishable from both.
func sentinelIdentity() *h2Identity {
	return &h2Identity{
		settings: map[http2.SettingID]uint32{
			http2.SettingHeaderTableSize:   4242,
			http2.SettingInitialWindowSize: 424242,
		},
		settingsOrder: []http2.SettingID{
			http2.SettingInitialWindowSize, http2.SettingHeaderTableSize,
		},
		priorities: []http2.Priority{
			{StreamID: 11, PriorityParam: http2.PriorityParam{StreamDep: 0, Exclusive: false, Weight: 21}},
			{StreamID: 13, PriorityParam: http2.PriorityParam{StreamDep: 11, Exclusive: true, Weight: 22}},
		},
		headerPriority:      &http2.PriorityParam{StreamDep: 17, Exclusive: false, Weight: 19},
		pseudoHeaderOrder:   []string{":scheme", ":path", ":method", ":authority"},
		connectionFlow:      0x00ABCDEF, // 11259375 -- not fhttp's 15663105, not any profile's
		initialStreamID:     7,
		indexingPolicy:      func(f hpack.HeaderField) bool { return f.Name == "x-sentinel-indexed" },
		staticNameLastMatch: true,
	}
}

// dirtyTransport is a Transport whose identity fields are all already set to something ELSE, so an
// assertion after apply cannot pass by accident on a zero value.
func dirtyTransport() *http2.Transport {
	return &http2.Transport{
		ConnectionFlow:           1,
		HeaderPriority:           &http2.PriorityParam{StreamDep: 1, Exclusive: true, Weight: 1},
		InitialStreamID:          99,
		Priorities:               []http2.Priority{{StreamID: 1}},
		PseudoHeaderOrder:        []string{":authority"},
		HPACKIndexingPolicy:      func(hpack.HeaderField) bool { return false },
		HPACKStaticNameLastMatch: false,
		Settings:                 map[http2.SettingID]uint32{http2.SettingMaxFrameSize: 1},
		SettingsOrder:            []http2.SettingID{http2.SettingMaxFrameSize},
	}
}

// TestH2IdentityApplyWritesEveryFieldOntoTheTransport is the per-assignment guard on apply.
//
// One subtest per assignment, so a single deleted line names itself instead of reddening a blanket
// "identity differs".
func TestH2IdentityApplyWritesEveryFieldOntoTheTransport(t *testing.T) {
	id := sentinelIdentity()
	tr := dirtyTransport()
	id.apply(tr)

	t.Run("ConnectionFlow", func(t *testing.T) {
		if tr.ConnectionFlow != id.connectionFlow {
			t.Fatalf("apply left http2.Transport.ConnectionFlow = %d, the identity says %d. fhttp "+
				"falls back to transportDefaultConnFlow = 15663105 when this field is 0 "+
				"(http2/transport.go:875), which is exactly what every Chrome profile declares -- so "+
				"a dropped assignment is INVISIBLE to a Chrome wire test and changes the "+
				"connection-preface WINDOW_UPDATE for every other engine",
				tr.ConnectionFlow, id.connectionFlow)
		}
	})

	t.Run("HeaderPriority", func(t *testing.T) {
		if tr.HeaderPriority == nil || *tr.HeaderPriority != *id.headerPriority {
			t.Fatalf("apply left http2.Transport.HeaderPriority = %+v, the identity says %+v. With "+
				"this field nil fhttp embeds its own {Exclusive:true Weight:255 StreamDep:0} in every "+
				"HEADERS frame (http2/transport.go:1543), so the stream this client opens carries a "+
				"priority that is not the profile's",
				tr.HeaderPriority, id.headerPriority)
		}
	})

	t.Run("InitialStreamID", func(t *testing.T) {
		if tr.InitialStreamID != id.initialStreamID {
			t.Fatalf("apply left http2.Transport.InitialStreamID = %d, the identity says %d. fhttp "+
				"only honours it when it is non-zero (http2/transport.go:939), and every in-tree "+
				"profile declares 0, so nothing on the wire can catch this for a shipped profile -- "+
				"a custom profile's first stream id would silently become fhttp's",
				tr.InitialStreamID, id.initialStreamID)
		}
	})

	t.Run("Priorities", func(t *testing.T) {
		if !reflect.DeepEqual(tr.Priorities, id.priorities) {
			t.Fatalf("apply left http2.Transport.Priorities = %+v, the identity says %+v. fhttp "+
				"writes one PRIORITY frame per entry immediately after the SETTINGS frame "+
				"(http2/transport.go:976), so a dropped assignment removes the whole priority tree a "+
				"Firefox profile opens its connection with",
				tr.Priorities, id.priorities)
		}
	})

	t.Run("PseudoHeaderOrder", func(t *testing.T) {
		if !reflect.DeepEqual(tr.PseudoHeaderOrder, id.pseudoHeaderOrder) {
			t.Fatalf("apply left http2.Transport.PseudoHeaderOrder = %v, the identity says %v. fhttp "+
				"falls back to [:authority :method :path :scheme] when this is empty, which is not "+
				"the order any of the three engine families sends",
				tr.PseudoHeaderOrder, id.pseudoHeaderOrder)
		}
	})

	t.Run("HPACKIndexingPolicy", func(t *testing.T) {
		if tr.HPACKIndexingPolicy == nil {
			t.Fatal("apply left http2.Transport.HPACKIndexingPolicy nil: the profile's per-field " +
				"indexing rule never reaches the request encoder, so every field is indexed by " +
				"fhttp's own rule and the dynamic table diverges for the life of the connection")
		}
		if !tr.HPACKIndexingPolicy(hpack.HeaderField{Name: "x-sentinel-indexed"}) ||
			tr.HPACKIndexingPolicy(hpack.HeaderField{Name: "x-anything-else"}) {
			t.Fatal("http2.Transport.HPACKIndexingPolicy is not the identity's predicate: it " +
				"answers differently on the two fields the sentinel policy separates")
		}
	})

	t.Run("HPACKStaticNameLastMatch", func(t *testing.T) {
		if tr.HPACKStaticNameLastMatch != id.staticNameLastMatch {
			t.Fatalf("apply left http2.Transport.HPACKStaticNameLastMatch = %v, the identity says "+
				"%v: a duplicated static NAME resolves to the wrong index, which is one byte on the "+
				"wire for :path on every request",
				tr.HPACKStaticNameLastMatch, id.staticNameLastMatch)
		}
	})

	t.Run("Settings", func(t *testing.T) {
		if !reflect.DeepEqual(tr.Settings, id.settings) {
			t.Fatalf("apply left http2.Transport.Settings = %v, the identity says %v", tr.Settings, id.settings)
		}
	})

	t.Run("SettingsOrder", func(t *testing.T) {
		if !reflect.DeepEqual(tr.SettingsOrder, id.settingsOrder) {
			t.Fatalf("apply left http2.Transport.SettingsOrder = %v, the identity says %v: SETTINGS "+
				"order is the first thing a peer sees and it is fingerprint-bearing",
				tr.SettingsOrder, id.settingsOrder)
		}
	})
}

// TestH2IdentityApplyNeverReadsTheTransport is the guard on the claim apply's doc comment makes:
// "It never reads from t, so applying the same identity to two Transports produces two identical
// wire identities." That claim is what makes ONE h2Identity enough for BOTH the origin transport and
// the proxy tunnel, which is the whole of patch 2.
func TestH2IdentityApplyNeverReadsTheTransport(t *testing.T) {
	id := sentinelIdentity()

	clean := &http2.Transport{}
	dirty := dirtyTransport()
	id.apply(clean)
	id.apply(dirty)

	type identityView struct {
		flow     uint32
		stream   uint32
		hp       http2.PriorityParam
		prios    []http2.Priority
		pseudo   []string
		settings map[http2.SettingID]uint32
		order    []http2.SettingID
		lastName bool
	}
	view := func(tr *http2.Transport) identityView {
		v := identityView{
			flow: tr.ConnectionFlow, stream: tr.InitialStreamID, prios: tr.Priorities,
			pseudo: tr.PseudoHeaderOrder, settings: tr.Settings, order: tr.SettingsOrder,
			lastName: tr.HPACKStaticNameLastMatch,
		}
		if tr.HeaderPriority != nil {
			v.hp = *tr.HeaderPriority
		}
		return v
	}
	if !reflect.DeepEqual(view(clean), view(dirty)) {
		t.Fatalf("the same identity produced two DIFFERENT wire identities: a zero-value Transport "+
			"became %+v and a pre-populated one became %+v. apply reads from t somewhere, so the "+
			"origin connection and the proxy tunnel no longer speak the same HTTP/2",
			view(clean), view(dirty))
	}
}

// TestH2IdentityApplyPassesAnEmptyPseudoHeaderOrderThrough records what the deleted nil-normalising
// branch was worth.
//
// apply used to rewrite a nil pseudoHeaderOrder to []string{}, on the stated grounds that nil would
// make fhttp fall back to its own order. fhttp's encodeHeaders actually reads
// `ok = len(pHeaderOrder) > 0`, so nil and []string{} take the SAME branch: the normalisation could
// not be reddened by any test because it changed nothing. It was deleted; this pins the equivalence
// that justified deleting it, so a future fhttp that DOES distinguish the two reddens here.
func TestH2IdentityApplyPassesAnEmptyPseudoHeaderOrderThrough(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
	}{
		{"nil", nil},
		{"empty", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := dirtyTransport()
			(&h2Identity{pseudoHeaderOrder: tc.order}).apply(tr)
			if len(tr.PseudoHeaderOrder) != 0 {
				t.Fatalf("an identity declaring %v left http2.Transport.PseudoHeaderOrder = %v",
					tc.order, tr.PseudoHeaderOrder)
			}
		})
	}
}
