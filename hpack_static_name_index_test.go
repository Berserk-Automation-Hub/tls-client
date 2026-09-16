package tls_client

// TransportOptions.HPACKStaticNameLastMatch must reach the HPACK encoder of the FIRST connection.
//
// This is the sibling of hpack_indexing_policy_test.go. That one pins WHICH REPRESENTATION the
// encoder chooses (RFC 7541 §6.2.1 vs §6.2.2); this one pins WHICH STATIC ENTRY a duplicated header
// NAME resolves to, which is the other half of the same encoder signature.
//
// RFC 7541 Appendix A gives `:path` two entries — 4 (`/`) and 5 (`/index.html`). An encoder that
// spells the field out but cites the name by index emits whichever it resolved to, so the first
// octet of the `:path` field differs by exactly one bit-pattern between engines:
//
//	Chrome 153   first match  0x44 (incremental, name index 4)
//	Firefox 156  last  match  0x45 (incremental, name index 5)
//
// Measured: Chrome over 114 `:path` observations across two captures with zero exceptions; Firefox
// over 41 of 41 attributed HEADERS blocks, confirmed by the tshark HPACK dissector. A profile that
// cannot state this cannot emulate both engines.

import (
	"testing"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
)

const pathIncrementalNameIdx5 = 0x45 // 0100 0101 -- 6.2.1 with incremental indexing, name index 5

func runHPACKStaticNameRequest(t *testing.T, s *hpackPolicyTestServer, lastMatch bool) byte {
	t.Helper()
	client, err := NewHttpClient(NewNoopLogger(),
		WithClientProfile(profiles.Chrome_133),
		WithInsecureSkipVerify(),
		WithTimeoutSeconds(10),
		WithDisableHttp3(),
		WithTransportOptions(&TransportOptions{HPACKStaticNameLastMatch: lastMatch}),
	)
	if err != nil {
		t.Fatalf("NewHttpClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)

	req, err := http.NewRequest(http.MethodGet, "https://"+s.addr+"/hpack-static-name-index", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// The server never answers; the bytes it already read are the whole point.
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

func TestHPACKStaticNameLastMatchReachesTheEncoder(t *testing.T) {
	s := startHPACKPolicyServer(t)

	// Default (false): Chrome's choice — the FIRST static entry for `:path`, index 4.
	if got := runHPACKStaticNameRequest(t, s, false); got != pathIncrementalNameIdx4 {
		t.Fatalf("with HPACKStaticNameLastMatch=false, :path first octet = 0x%02x, want 0x%02x "+
			"(incremental indexing, name index 4)", got, pathIncrementalNameIdx4)
	}

	// True: Firefox's choice, and fhttp upstream's — the LAST static entry, index 5.
	if got := runHPACKStaticNameRequest(t, s, true); got != pathIncrementalNameIdx5 {
		t.Fatalf("with HPACKStaticNameLastMatch=true, :path first octet = 0x%02x, want 0x%02x "+
			"(incremental indexing, name index 5); TransportOptions.HPACKStaticNameLastMatch is not "+
			"reaching hpack.Encoder", got, pathIncrementalNameIdx5)
	}
}
