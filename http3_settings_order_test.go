package tls_client

import (
	"fmt"
	"sort"
	"testing"

	"github.com/Berserk-Automation-Hub/quic-go-utls/http3"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
)

// h3ConfigFor builds the http3Config that newRoundTripper builds for a profile, so these tests read
// the same value the shipped HTTP/3 path does rather than a hand-made one.
func h3ConfigFor(p profiles.ClientProfile) *http3Config {
	return &http3Config{
		http3Settings:          p.GetHttp3Settings(),
		http3SettingsOrder:     p.GetHttp3SettingsOrder(),
		http3PriorityParam:     p.GetHttp3PriorityParam(),
		http3PseudoHeaderOrder: p.GetHttp3PseudoHeaderOrder(),
		http3SendGreaseFrames:  p.GetHttp3SendGreaseFrames(),
	}
}

// emittedH3SettingIDs is the set of SETTINGS ids quic-go-utls's settingsFrame.Append will write for
// this transport: everything in AdditionalSettings, plus the two the frame contributes itself.
func emittedH3SettingIDs(t3 *http3.Transport) map[uint64]struct{} {
	ids := make(map[uint64]struct{}, len(t3.AdditionalSettings)+2)
	for id := range t3.AdditionalSettings {
		ids[id] = struct{}{}
	}
	if t3.MaxResponseHeaderBytes >= 0 {
		ids[settingH3MaxFieldSectionSize] = struct{}{}
	}
	if t3.EnableDatagrams {
		ids[settingH3Datagram] = struct{}{}
	}
	return ids
}

// TestHTTP3SettingsOrderNamesEverySettingEmitted is the guard on the HTTP/3 SETTINGS order being
// COMPLETE, for every profile this module ships.
//
// The defect it holds shut: quic-go-utls writes the ids named by AdditionalSettingsOrder and then
// writes everything LEFT by ranging over a Go map, whose iteration order Go randomises per process.
// Any id the order does not name therefore lands in a random position in the frame, and the HTTP/3
// SETTINGS order is a fingerprint (browserleaks' h3_text is the ids in arrival order). Before the
// fix no order at all was set for a profile that declares none — which is every in-tree profile bar
// two — and 0x6/0x33 could not be covered even by the two that do, because the frame adds those
// itself.
func TestHTTP3SettingsOrderNamesEverySettingEmitted(t *testing.T) {
	names := make([]string, 0, len(profiles.MappedTLSClients))
	for name := range profiles.MappedTLSClients {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 0 {
		t.Fatal("profiles.MappedTLSClients is empty; this test would assert nothing")
	}

	for _, name := range names {
		p := profiles.MappedTLSClients[name]
		t.Run(name, func(t *testing.T) {
			rt, err := buildHTTP3Transport(h3ConfigFor(p))
			if err != nil {
				t.Fatalf("buildHTTP3Transport: %v", err)
			}
			t3, ok := rt.(*http3.Transport)
			if !ok {
				t.Fatalf("buildHTTP3Transport returned %T, not *http3.Transport", rt)
			}

			named := make(map[uint64]struct{}, len(t3.AdditionalSettingsOrder))
			for _, id := range t3.AdditionalSettingsOrder {
				if _, dup := named[id]; dup {
					t.Fatalf("AdditionalSettingsOrder names 0x%x twice (%v); settingsFrame.Append "+
						"deletes an id once it is written, so the duplicate silently drops whatever "+
						"would otherwise have followed it", id, t3.AdditionalSettingsOrder)
				}
				named[id] = struct{}{}
			}

			var unnamed []uint64
			for id := range emittedH3SettingIDs(t3) {
				if _, ok := named[id]; !ok {
					unnamed = append(unnamed, id)
				}
			}
			sort.Slice(unnamed, func(i, j int) bool { return unnamed[i] < unnamed[j] })

			if len(unnamed) > 0 {
				pretty := make([]string, 0, len(unnamed))
				for _, id := range unnamed {
					pretty = append(pretty, fmt.Sprintf("0x%x", id))
				}
				t.Fatalf("profile %s: the HTTP/3 SETTINGS frame will carry %v, and "+
					"AdditionalSettingsOrder (%v) names none of them. quic-go-utls writes every "+
					"setting the order does not name by ranging over a Go map "+
					"(http3/frames.go, settingsFrame.Append), and Go randomises map iteration, so "+
					"those ids land in a different position on every process start: this profile's "+
					"HTTP/3 SETTINGS fingerprint is not stable from one run to the next",
					name, pretty, t3.AdditionalSettingsOrder)
			}
		})
	}
}

// wireSettingsOrder reproduces quic-go-utls's settingsFrame.Append EXACTLY: the ids named by
// AdditionalSettingsOrder, in that order, and then whatever is left, by ranging over a Go map.
// Calling it repeatedly on ONE transport is therefore a direct reading of what that transport puts
// on the wire, randomisation included.
func wireSettingsOrder(t3 *http3.Transport) []uint64 {
	remaining := emittedH3SettingIDs(t3)
	out := make([]uint64, 0, len(remaining))
	for _, id := range t3.AdditionalSettingsOrder {
		if _, ok := remaining[id]; ok {
			out = append(out, id)
			delete(remaining, id) // Append deletes as it writes
		}
	}
	for id := range remaining { // the Go map range Append falls back to
		out = append(out, id)
	}
	return out
}

// TestHTTP3SettingsOrderIsStableOnTheWire is the behavioural half of the guard: it reads the order
// the SETTINGS frame will actually carry, many times, from ONE transport, and requires it to be the
// same every time.
//
// The config is the shape a caller supplies through the CFFI surface when they give h3Settings and
// no h3SettingsOrder — which is also what 78 of the 83 in-tree profiles look like (all but
// chrome_144, chrome_144_PSK, firefox_147, firefox_147_PSK and firefox_148, the only five that
// declare an http3SettingsOrder). Before the fix that produced no AdditionalSettingsOrder at all,
// so all three ids fell through to the Go map range and the frame's order was redrawn on every
// connection.
func TestHTTP3SettingsOrderIsStableOnTheWire(t *testing.T) {
	cfg := &http3Config{
		http3Settings: map[uint64]uint64{
			1: 65536, // SETTINGS_QPACK_MAX_TABLE_CAPACITY
			7: 100,   // SETTINGS_QPACK_BLOCKED_STREAMS
		},
		// no http3SettingsOrder: the caller did not declare one
	}

	rt, err := buildHTTP3Transport(cfg)
	if err != nil {
		t.Fatalf("buildHTTP3Transport: %v", err)
	}
	t3 := rt.(*http3.Transport)

	first := wireSettingsOrder(t3)
	if len(first) < 2 {
		t.Fatalf("this transport emits only %v; with fewer than two settings the test could not "+
			"detect a random order at all", first)
	}
	for i := 1; i < 500; i++ {
		got := wireSettingsOrder(t3)
		if len(got) != len(first) {
			t.Fatalf("read %d: the SETTINGS frame carries %d ids, read 1 carried %d", i, len(got), len(first))
		}
		for k := range got {
			if got[k] != first[k] {
				t.Fatalf("read %d: the HTTP/3 SETTINGS frame carries %v, read 1 carried %v — the "+
					"ids AdditionalSettingsOrder does not name are written by ranging a Go map, so "+
					"this connection's h3 SETTINGS order is redrawn every time and the profile has "+
					"no stable HTTP/3 fingerprint", i, got, first)
			}
		}
	}
}

// TestHTTP3SettingsOrderKeepsTheProfilesOwnDeclarationFirst pins the one thing completing the order
// must never do: reorder what the caller actually declared.
//
// Completion appends the ids the declaration could not name, and it has to pick SOME order for them
// — ascending. That tie-break must not leak back onto the declaration, because the declaration is
// the measured half: a profile that states its browser's SETTINGS order is stating ground truth,
// and re-sorting it would replace a measured fingerprint with an invented one.
func TestHTTP3SettingsOrderKeepsTheProfilesOwnDeclarationFirst(t *testing.T) {
	t.Run("a declaration that is not already ascending survives verbatim", func(t *testing.T) {
		// h3SettingsOrder is caller-supplied (cffi_src/types.go, "h3SettingsOrder"), so a descending
		// declaration is a shape this module has to handle, and it is the only shape in which
		// re-sorting is visible at all.
		declared := []uint64{7, 1}
		cfg := &http3Config{
			http3Settings:      map[uint64]uint64{1: 65536, 7: 100},
			http3SettingsOrder: declared,
		}
		rt, err := buildHTTP3Transport(cfg)
		if err != nil {
			t.Fatalf("buildHTTP3Transport: %v", err)
		}
		got := rt.(*http3.Transport).AdditionalSettingsOrder
		if len(got) < len(declared) {
			t.Fatalf("SETTINGS order %v is shorter than the declaration %v", got, declared)
		}
		for i := range declared {
			if got[i] != declared[i] {
				t.Fatalf("SETTINGS order is %v; the caller declared %v and the declaration must be "+
					"copied verbatim in front. Completing the order must only APPEND the ids the "+
					"declaration could not name — re-sorting the declared ones replaces a measured "+
					"SETTINGS order with this code's own tie-break", got, declared)
			}
		}
	})

	t.Run("Chrome_144's own declaration survives verbatim", func(t *testing.T) {
		p := profiles.Chrome_144
		declared := p.GetHttp3SettingsOrder()
		if len(declared) == 0 {
			t.Fatal("Chrome_144 no longer declares an http3SettingsOrder; this test asserts nothing")
		}
		rt, err := buildHTTP3Transport(h3ConfigFor(p))
		if err != nil {
			t.Fatalf("buildHTTP3Transport: %v", err)
		}
		got := rt.(*http3.Transport).AdditionalSettingsOrder
		if len(got) < len(declared) {
			t.Fatalf("SETTINGS order %v is shorter than the profile's declaration %v", got, declared)
		}
		for i := range declared {
			if got[i] != declared[i] {
				t.Fatalf("SETTINGS order is %v; Chrome_144 declares %v", got, declared)
			}
		}
	})
}
