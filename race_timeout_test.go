package tls_client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/tls-client/profiles"
)

// TestRaceContextIsTheCallersNotATenSecondLiteral is the guard on WithTimeoutSeconds reaching the
// HTTP/3 race.
//
// startRace used to wait on context.WithTimeout(context.Background(), 10*time.Second): a literal,
// detached from the request, so the client's configured timeout did not reach the race in EITHER
// direction. This asserts the race waits on exactly the deadline the caller's request carries —
// which is where WithTimeoutSeconds puts it (fhttp Client.send -> setRequestCancel).
func TestRaceContextIsTheCallersNotATenSecondLiteral(t *testing.T) {
	cases := []struct {
		name string
		// timeout the caller configured, 0 meaning "no deadline at all"
		timeout time.Duration
	}{
		{"shorter than the old ten-second literal", 3 * time.Second},
		{"longer than the old ten-second literal", 60 * time.Second},
		{"no deadline at all", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
			if err != nil {
				t.Fatal(err)
			}
			var want time.Time
			if tc.timeout > 0 {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(tc.timeout))
				defer cancel()
				req = req.WithContext(ctx)
				want, _ = ctx.Deadline()
			}

			raceCtx, cancel := raceContext(req)
			defer cancel()

			got, has := raceCtx.Deadline()
			if tc.timeout == 0 {
				if has {
					t.Fatalf("the caller set no deadline, yet the race waits on one that expires in %v: "+
						"the race is imposing a timeout the client never configured",
						time.Until(got).Round(time.Millisecond))
				}
				return
			}
			if !has {
				t.Fatalf("the caller's request carries a %v deadline and the race waits on a context "+
					"with none: the client's WithTimeoutSeconds does not reach the race", tc.timeout)
			}
			if !got.Equal(want) {
				t.Fatalf("the race expires at %v but the caller's request expires at %v (a difference "+
					"of %v): the race is running on its own clock, so a client configured for %v "+
					"neither gets that long nor stops when it is up",
					got, want, got.Sub(want).Round(time.Millisecond), tc.timeout)
			}
		})
	}
}

// TestStartRaceStopsWhenTheCallersDeadlinePasses is the behavioural half: it runs the real
// startRace with an HTTP/2 attempt that never completes and a caller deadline far shorter than the
// old ten-second literal, and requires the race to end on the caller's deadline.
func TestStartRaceStopsWhenTheCallersDeadlinePasses(t *testing.T) {
	const callerTimeout = 250 * time.Millisecond

	pr := newProtocolRacer(
		nil,   // clientSessionCache
		false, // insecureSkipVerify
		"",    // serverNameOverwrite
		nil,   // transportOptions
		nil,   // settings
		make(map[string]http.RoundTripper),
		&sync.Mutex{},
		nil, // dropTransport
		nil, // certificatePinner
		nil, // badPinHandlerFunc
		nil, // bandwidthTracker
		profiles.Chrome_144.GetHttp3Settings(),
		profiles.Chrome_144.GetHttp3SettingsOrder(),
		profiles.Chrome_144.GetHttp3PriorityParam(),
		profiles.Chrome_144.GetHttp3PseudoHeaderOrder(),
		profiles.Chrome_144.GetHttp3SendGreaseFrames(),
		"", // proxyURL
	)

	// 127.0.0.1:1 has nothing on it, so the HTTP/3 attempt fails without reaching the network's
	// mercy; the HTTP/2 attempt is the one held open, by a transport factory that never returns.
	req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), callerTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	release := make(chan struct{})
	defer close(release)

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_, _ = pr.startRace(req, "127.0.0.1:1", func(*http.Request, string) error {
			<-release // the HTTP/2 attempt never finishes
			return errors.New("released")
		})
		done <- time.Since(start)
	}()

	// Generous: five seconds is still half the old ten-second literal, so a run that reaches it is
	// unambiguously the defect and not a slow machine.
	select {
	case elapsed := <-done:
		if elapsed > 2*time.Second {
			t.Fatalf("the caller's request expired after %v but the race ran for %v: startRace is "+
				"waiting on a ten-second literal of its own instead of the caller's context, so "+
				"WithTimeoutSeconds does not end the race", callerTimeout, elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the caller's request expired after %v and the race was still running 5s later: "+
			"startRace is waiting on context.WithTimeout(context.Background(), 10*time.Second), a "+
			"literal that the client's WithTimeoutSeconds cannot shorten", callerTimeout)
	}
}
