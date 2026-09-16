package tls_client

// SERVER PUSH MUST FOLLOW THE SETTINGS FRAME THE IDENTITY ADVERTISES.
//
// `h2Identity.apply` set `t.PushHandler = &http2.DefaultPushHandler{}` unconditionally, so a profile
// sending SETTINGS_ENABLE_PUSH=0 — which every current browser profile does, because no current
// browser accepts push — told the peer "do not push" and then accepted a PUSH_PROMISE anyway,
// allocating a stream, spawning a goroutine and reading the pushed response body.
//
// fhttp already implements the correct behaviour with a NIL handler: readLoop returns
// ConnectionError(ErrCodeProtocol), and its own comment there reads "should not be receiving
// PUSH_PROMISE if ENABLE_PUSH is disabled". So the fix is to stop overriding it, and this test pins
// which identities get a handler at all.
//
//	go test -run TestEnablePush -v

import (
	"testing"

	http2 "github.com/Berserk-Automation-Hub/fhttp/http2"
)

func TestEnablePushFollowsTheAdvertisedSettings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings map[http2.SettingID]uint32
		want     bool
		why      string
	}{
		{
			name:     "browser profile: ENABLE_PUSH=0",
			settings: map[http2.SettingID]uint32{http2.SettingEnablePush: 0, http2.SettingInitialWindowSize: 6291456},
			want:     false,
			why:      "the identity advertises that it will not accept a push; accepting one contradicts its own SETTINGS frame",
		},
		{
			name:     "ENABLE_PUSH=1",
			settings: map[http2.SettingID]uint32{http2.SettingEnablePush: 1},
			want:     true,
			why:      "the identity advertises that it will accept a push",
		},
		{
			name:     "setting absent",
			settings: map[http2.SettingID]uint32{http2.SettingInitialWindowSize: 6291456},
			want:     false,
			why: "absent is not the same as 1. RFC 9113 6.5.2 defaults ENABLE_PUSH to 1, but a " +
				"profile carrying a browser's identity and omitting the setting is already not that " +
				"browser; treating absent as enabled would restore the old behaviour for exactly " +
				"the profiles that forgot to say",
		},
		{
			name:     "no settings at all (profile-less caller)",
			settings: nil,
			want:     true,
			why:      "this path advertises no SETTINGS frame of its own, so it contradicts nothing and keeps its historical behaviour",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			id := &h2Identity{settings: tc.settings}
			var tr http2.Transport
			id.apply(&tr)
			got := tr.PushHandler != nil
			if got != tc.want {
				t.Fatalf("PushHandler != nil = %v, want %v.\n%s", got, tc.want, tc.why)
			}
		})
	}
}

// TestEnablePushNilIdentityIsInert guards the early return: a nil identity must not touch the
// Transport at all, or applying "no identity" would silently install one.
func TestEnablePushNilIdentityIsInert(t *testing.T) {
	var tr http2.Transport
	var id *h2Identity
	id.apply(&tr)
	if tr.PushHandler != nil {
		t.Fatal("a nil identity installed a PushHandler")
	}
}
