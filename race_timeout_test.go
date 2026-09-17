package tls_client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
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

			raceCtx := raceContext(req)

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

// raceStubTransport is the HTTP/2 attempt, stubbed: it records the request context it was handed
// and answers immediately, so it is the one that wins the race.
type raceStubTransport struct {
	mu  sync.Mutex
	ctx context.Context
}

func (s *raceStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.ctx = req.Context()
	s.mu.Unlock()
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Proto:      "HTTP/2.0",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func (s *raceStubTransport) seen() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

// http3AttemptStillRunning reports whether the goroutine startRace launched for the HTTP/3 attempt
// is still on a stack. The needle is the METHOD's qualified name, not the bare word, so this
// helper's own frame cannot match itself.
func http3AttemptStillRunning() bool {
	buf := make([]byte, 1<<20)
	return bytes.Contains(buf[:runtime.Stack(buf, true)], []byte("protocolRacer).attemptHTTP3"))
}

// TestStartRaceStopsTheLoserAndNotTheWinner is the guard on raceAttempt — the per-attempt request
// contexts and the loser-only cancel.
//
// Both attempts used to be launched with the caller's untouched *http.Request, so the cancel the
// racer held reached NEITHER of them: it cancelled only the context waitForRaceWinner was selecting
// on, one statement before returning. The loser was never stopped. Two things have to be true and
// they pull in opposite directions, which is why they are asserted together:
//
//   - the LOSER is cancelled the moment the winner returns, rather than left dialing;
//   - the WINNER is NOT, because both transports abort the stream on request-context cancellation
//     and the caller has not read the body yet.
//
// The HTTP/3 attempt is the loser, held in flight by a UDP socket that swallows its QUIC Initial
// packets and never answers: without a cancel it stays there for quic-go's handshake idle timeout
// (5s by default), which is far longer than this test's window.
func TestStartRaceStopsTheLoserAndNotTheWinner(t *testing.T) {
	blackhole, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open a loopback UDP socket here (%v), so the HTTP/3 attempt cannot be held "+
			"in flight and the loser-cancel has nothing to observe", err)
	}
	defer blackhole.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, rerr := blackhole.ReadFrom(buf); rerr != nil {
				return
			}
		}
	}()
	addr := blackhole.LocalAddr().String()

	winner := &raceStubTransport{}
	pr := newProtocolRacer(
		nil, false, "", nil, nil,
		make(map[string]http.RoundTripper),
		&sync.Mutex{},
		nil, nil, nil, nil,
		profiles.Chrome_144.GetHttp3Settings(),
		profiles.Chrome_144.GetHttp3SettingsOrder(),
		profiles.Chrome_144.GetHttp3PriorityParam(),
		profiles.Chrome_144.GetHttp3PseudoHeaderOrder(),
		profiles.Chrome_144.GetHttp3SendGreaseFrames(),
		"",
	)

	req, err := http.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	req = req.WithContext(callerCtx)

	resp, err := pr.startRace(req, addr, func(_ *http.Request, key string) error {
		// Called with pr.cachedTransportsLck already held by attemptHTTP2.
		pr.cachedTransports[key] = winner
		return nil
	})
	if err != nil || resp == nil {
		t.Fatalf("the stubbed HTTP/2 attempt should have won the race: resp=%v err=%v", resp, err)
	}

	// THE WINNER. Its context must be alive: fhttp's http2 transport and quic-go's http3 transport
	// both abort the stream when the request context is cancelled, so cancelling the winner here
	// would hand the caller a response whose body is already dead.
	got := winner.seen()
	if got == nil {
		t.Fatal("the winning attempt never reached the transport, so nothing was observed")
	}
	if got == callerCtx {
		t.Fatal("the winning attempt was launched with the CALLER's request unchanged. Both " +
			"attempts then share one context, so no cancel can stop one without stopping the other " +
			"— which is how the loser came to be left running")
	}
	if err := got.Err(); err != nil {
		t.Fatalf("the WINNER's request context is already cancelled (%v) when startRace returns. "+
			"The caller has not read the body yet and both transports abort the stream on "+
			"request-context cancellation, so this hands back a dead response", err)
	}

	// THE LOSER. It is cancelled, so its goroutine unwinds instead of waiting out the QUIC
	// handshake idle timeout against the black hole.
	deadline := time.Now().Add(2 * time.Second)
	for http3AttemptStillRunning() {
		if time.Now().After(deadline) {
			t.Fatalf("the HTTP/2 attempt won the race and the HTTP/3 attempt was still in flight 2s "+
				"later, against a UDP black hole at %s. The loser is not being cancelled: both "+
				"attempts are running on a context the racer cannot reach, so the losing dial holds "+
				"its socket until QUIC's own handshake idle timeout", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStartRaceStopsBothAttemptsWhenNobodyWins is the other half of raceAttempt's contract: when the
// race produces no response there is no body to protect, so BOTH derived contexts are released
// rather than left registered on the caller's context for the rest of the request.
//
// The HTTP/3 attempt is failed without touching the network — buildHTTP3Transport refuses a
// non-SOCKS5 proxy, because only SOCKS5 can tunnel QUIC's UDP — and the HTTP/2 attempt is a stub
// that returns an error, so both attempts are finished and the caller's context is still alive.
func TestStartRaceStopsBothAttemptsWhenNobodyWins(t *testing.T) {
	failing := &raceFailingTransport{}
	pr := newProtocolRacer(
		nil, false, "", nil, nil,
		make(map[string]http.RoundTripper),
		&sync.Mutex{},
		nil, nil, nil, nil,
		nil, nil, 0, nil, false,
		"http://127.0.0.1:9", // not SOCKS5: the HTTP/3 transport refuses to build
	)

	req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:9/", nil)
	if err != nil {
		t.Fatal(err)
	}
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	req = req.WithContext(callerCtx)

	resp, err := pr.startRace(req, "127.0.0.1:9", func(_ *http.Request, key string) error {
		pr.cachedTransports[key] = failing
		return nil
	})
	if err == nil || resp != nil {
		t.Fatalf("both attempts were rigged to fail; startRace returned resp=%v err=%v", resp, err)
	}

	got := failing.seen()
	if got == nil {
		t.Fatal("the HTTP/2 attempt never reached the transport, so nothing was observed")
	}
	if got.Err() == nil {
		t.Fatal("no attempt won, yet the HTTP/2 attempt's derived context is still uncancelled " +
			"after startRace returned. Nothing depends on it — there is no response body — so it " +
			"stays registered on the caller's context for the rest of the request, once per race")
	}
	if callerCtx.Err() != nil {
		t.Fatalf("startRace cancelled the CALLER's context (%v). It only ever gets to cancel the "+
			"children it created", callerCtx.Err())
	}
}

// raceFailingTransport is the HTTP/2 attempt, stubbed to lose: it records the request context it was
// handed and returns an error.
type raceFailingTransport struct {
	mu  sync.Mutex
	ctx context.Context
}

func (s *raceFailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.ctx = req.Context()
	s.mu.Unlock()
	return nil, errors.New("stubbed HTTP/2 attempt: refused")
}

func (s *raceFailingTransport) seen() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}
