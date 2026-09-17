package tls_client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	http "github.com/Berserk-Automation-Hub/fhttp"
	"github.com/Berserk-Automation-Hub/fhttp/http2"
	"github.com/Berserk-Automation-Hub/tls-client/bandwidth"
	tls "github.com/Berserk-Automation-Hub/utls"
)

type protocolRacer struct {
	protocolCache   map[string]string
	protocolCacheMu sync.RWMutex

	clientSessionCache  tls.ClientSessionCache
	insecureSkipVerify  bool
	serverNameOverwrite string
	transportOptions    *TransportOptions
	settings            map[http2.SettingID]uint32
	cachedTransports    map[string]http.RoundTripper
	cachedTransportsLck *sync.Mutex
	certificatePinner   CertificatePinner
	badPinHandlerFunc   BadPinHandlerFunc
	bandwidthTracker    bandwidth.BandwidthTracker

	// dropTransport forgets the transport cached for an address so the next
	// attempt builds one from a fresh handshake. It is the round tripper's own
	// eviction, handed over because a raced request returns from RoundTrip
	// before the branch that would otherwise do it.
	dropTransport func(addr string, stale http.RoundTripper)

	// HTTP/3 specific settings
	http3Settings          map[uint64]uint64
	http3SettingsOrder     []uint64
	http3PriorityParam     uint32
	http3PseudoHeaderOrder []string
	http3SendGreaseFrames  bool

	proxyURL string
}

func newProtocolRacer(
	clientSessionCache tls.ClientSessionCache,
	insecureSkipVerify bool,
	serverNameOverwrite string,
	transportOptions *TransportOptions,
	settings map[http2.SettingID]uint32,
	cachedTransports map[string]http.RoundTripper,
	cachedTransportsLck *sync.Mutex,
	dropTransport func(addr string, stale http.RoundTripper),
	certificatePinner CertificatePinner,
	badPinHandlerFunc BadPinHandlerFunc,
	bandwidthTracker bandwidth.BandwidthTracker,
	http3Settings map[uint64]uint64,
	http3SettingsOrder []uint64,
	http3PriorityParam uint32,
	http3PseudoHeaderOrder []string,
	http3SendGreaseFrames bool,
	proxyURL string,
) *protocolRacer {
	return &protocolRacer{
		protocolCache:          make(map[string]string),
		clientSessionCache:     clientSessionCache,
		insecureSkipVerify:     insecureSkipVerify,
		serverNameOverwrite:    serverNameOverwrite,
		transportOptions:       transportOptions,
		settings:               settings,
		cachedTransports:       cachedTransports,
		cachedTransportsLck:    cachedTransportsLck,
		dropTransport:          dropTransport,
		certificatePinner:      certificatePinner,
		badPinHandlerFunc:      badPinHandlerFunc,
		bandwidthTracker:       bandwidthTracker,
		http3Settings:          http3Settings,
		http3SettingsOrder:     http3SettingsOrder,
		http3PriorityParam:     http3PriorityParam,
		http3PseudoHeaderOrder: http3PseudoHeaderOrder,
		http3SendGreaseFrames:  http3SendGreaseFrames,
		proxyURL:               proxyURL,
	}
}

// race races HTTP/3 and HTTP/2 connections and uses whichever responds first.
// Similar to Chrome's "Happy Eyeballs" approach.
func (pr *protocolRacer) race(req *http.Request, addr string, getTransportFunc func(*http.Request, string) error) (*http.Response, error) {
	// Try cached protocol first if available
	if resp, shouldRace := pr.tryUseCachedProtocol(req, addr, getTransportFunc); !shouldRace {
		return resp, nil
	}

	// No cached protocol or it failed - start racing
	return pr.startRace(req, addr, getTransportFunc)
}

func (pr *protocolRacer) tryUseCachedProtocol(req *http.Request, addr string, getTransportFunc func(*http.Request, string) error) (*http.Response, bool) {
	pr.protocolCacheMu.RLock()
	cachedProtocol, found := pr.protocolCache[addr]
	pr.protocolCacheMu.RUnlock()

	if !found {
		return nil, true // No cache, proceed to racing
	}

	transport, err := pr.getOrCreateTransport(cachedProtocol, addr, req, getTransportFunc)
	if err != nil {
		pr.handleCachedProtocolError(err, addr, req)
		return nil, true // Cached protocol failed, proceed to racing
	}

	resp, err := pr.roundTrip(transport, req, addr)
	if err == nil {
		return resp, false // Success!
	}

	pr.clearProtocolCache(addr)
	return nil, true
}

func (pr *protocolRacer) getOrCreateTransport(protocol, addr string, req *http.Request, getTransportFunc func(*http.Request, string) error) (http.RoundTripper, error) {
	transportKey := pr.getTransportKey(protocol, addr)

	pr.cachedTransportsLck.Lock()
	defer pr.cachedTransportsLck.Unlock()

	if transport, exists := pr.cachedTransports[transportKey]; exists {
		return transport, nil
	}

	transport, err := pr.createTransportForProtocol(protocol, addr, req, getTransportFunc)
	if err != nil {
		return nil, err
	}

	pr.cachedTransports[transportKey] = transport
	return transport, nil
}

func (pr *protocolRacer) createTransportForProtocol(protocol, addr string, req *http.Request, getTransportFunc func(*http.Request, string) error) (http.RoundTripper, error) {
	if protocol == "h3" {
		return buildHTTP3Transport(pr.getHTTP3Config())
	}

	// For HTTP/2, use the standard transport creation
	transportKey := pr.getTransportKey(protocol, addr)
	if err := getTransportFunc(req, transportKey); err != nil {
		return nil, err
	}

	return pr.cachedTransports[transportKey], nil
}

// raceContext is the context waitForRaceWinner waits on. It is the CALLER'S, and nothing else.
//
// It used to be context.WithTimeout(context.Background(), 10*time.Second): a literal, detached from
// the request. That is wrong in both directions, and silently.
//
//   - A caller who asked for LESS than ten seconds did not get it. WithTimeoutSeconds(3) puts a
//     3-second deadline on the request context (fhttp's Client.send -> setRequestCancel), both
//     attempts return on it, and then waitForRaceWinner went on waiting on a context that does not
//     expire until ten — so Do() returned at ten seconds for a client configured for three.
//   - A caller who asked for MORE than ten seconds did not get it either: at ten seconds the race
//     context fired and the request failed with "context deadline exceeded" while the caller's own
//     deadline, and both in-flight attempts, still had time left.
//
// Deriving it from req.Context() makes the race end exactly when the caller said, because the
// deadline WithTimeoutSeconds installs is already on that context.
//
// It is returned PLAIN, with no derived cancel of its own. A cancel here could only be called by
// the racer, and the racer has nothing to say about when the CALLER's wait should end. Stopping the
// loser is done on the loser's own request (see raceAttempt), which is the only place a cancel
// actually reaches an attempt that is still in flight.
func raceContext(req *http.Request) context.Context {
	return req.Context()
}

// raceAttempt gives ONE racing attempt its own copy of the request, carrying its own cancellable
// child of the caller's context.
//
// This is what makes stopping the loser real. Both attempts used to be launched with the caller's
// untouched *http.Request, so the cancel the racer held reached neither of them: it cancelled only
// the context waitForRaceWinner was itself selecting on, one statement before returning, and
// startRace's own deferred cancel fired a moment later anyway. The loser went on dialing,
// handshaking and holding a socket long after the request it belonged to had been answered — for
// the HTTP/3 attempt against an unresponsive host, for the whole QUIC handshake idle timeout.
//
// Per-attempt rather than one shared derived context, because the WINNER's context must SURVIVE:
// both fhttp's HTTP/2 transport and quic-go's HTTP/3 transport abort the stream when the request
// context is cancelled, so a single shared cancel would tear down the body the caller is about to
// read. The winner's child is deliberately left uncancelled and ends with its parent, the caller's
// request — exactly the lifetime the response body has.
func raceAttempt(req *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(req.Context())
	return req.WithContext(ctx), cancel
}

// raceAttemptHandle is one racing attempt: the protocol it speaks and the cancel that stops it.
//
// The two live in ONE list with ONE release policy on purpose. They used to be released by three
// separate statements -- stopHTTP2() on an h3 win, stopHTTP3() on an h2 win, and both again when
// nobody won -- and a statement that only runs on one of those three outcomes is a statement no
// single test observes: deleting the nobody-won stopHTTP3() left the whole suite green. Releasing
// every attempt except the winner, from one place, means each of these three facts is on a path
// some test already drives, and the outcomes cannot drift apart again.
type raceAttemptHandle struct {
	protocol string
	stop     context.CancelFunc
}

func (pr *protocolRacer) startRace(req *http.Request, addr string, getTransportFunc func(*http.Request, string) error) (*http.Response, error) {
	resultCh := make(chan racingResult, 2)

	h3Req, stopHTTP3 := raceAttempt(req)
	h2Req, stopHTTP2 := raceAttempt(req)
	attempts := []raceAttemptHandle{
		{protocol: "h3", stop: stopHTTP3},
		{protocol: "h2", stop: stopHTTP2},
	}

	// stopAllExcept releases every attempt but the named one. The WINNER is the exception because
	// its response body is still to be read on its own request context, and both transports abort
	// the stream when that context is cancelled.
	stopAllExcept := func(keep string) {
		for _, a := range attempts {
			if a.protocol != keep {
				a.stop()
			}
		}
	}

	go pr.attemptHTTP3(h3Req, resultCh)
	go pr.attemptHTTP2(h2Req, addr, getTransportFunc, resultCh)

	resp, err := pr.waitForRaceWinner(raceContext(req), addr, resultCh, stopAllExcept)

	if resp == nil {
		// Nobody won, so no response body depends on any of these contexts: "" keeps nothing, so
		// every attempt is released rather than left dialing for a request that has already failed.
		stopAllExcept("")
	}

	return resp, err
}

func (pr *protocolRacer) attemptHTTP3(req *http.Request, resultCh chan<- racingResult) {
	h3Transport, err := buildHTTP3Transport(pr.getHTTP3Config())
	if err != nil {
		resultCh <- racingResult{protocol: "h3", err: fmt.Errorf("failed to build HTTP/3 transport: %w", err)}
		return
	}

	resp, err := h3Transport.RoundTrip(req)
	if err != nil {
		resultCh <- racingResult{protocol: "h3", err: fmt.Errorf("HTTP/3 request failed: %w", err)}
	} else {
		resultCh <- racingResult{protocol: "h3", response: resp}
	}
}

func (pr *protocolRacer) attemptHTTP2(req *http.Request, addr string, getTransportFunc func(*http.Request, string) error, resultCh chan<- racingResult) {
	// Chrome-like 300ms delay before starting HTTP/2
	// https://groups.google.com/a/chromium.org/g/proto-quic/c/igD7dLSct24
	time.Sleep(300 * time.Millisecond)

	pr.cachedTransportsLck.Lock()
	if _, ok := pr.cachedTransports[addr]; !ok {
		if err := getTransportFunc(req, addr); err != nil {
			pr.cachedTransportsLck.Unlock()
			resultCh <- racingResult{protocol: "h2", err: err}
			return
		}
	}
	h2Transport := pr.cachedTransports[addr]
	pr.cachedTransportsLck.Unlock()

	resp, err := pr.roundTrip(h2Transport, req, addr)
	resultCh <- racingResult{protocol: "h2", response: resp, err: err}
}

// waitForRaceWinner returns the first attempt that produces a response, and calls stopLoser with the
// winning protocol so the other attempt is torn down instead of being left in flight.
func (pr *protocolRacer) waitForRaceWinner(ctx context.Context, addr string, resultCh <-chan racingResult, stopLoser func(winner string)) (*http.Response, error) {
	var lastErr error

	for i := 0; i < 2; i++ {
		select {
		case result := <-resultCh:
			if result.err == nil && result.response != nil {
				pr.cacheWinningProtocol(addr, result.protocol)
				stopLoser(result.protocol)
				return result.response, nil
			}
			lastErr = result.err

		case <-ctx.Done():
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ctx.Err()
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("http3 racing: both protocols failed to connect")
}

// roundTrip sends the request over transport and, when the dial underneath
// reports that the server has moved to a protocol this transport cannot speak,
// forgets the transport so the next attempt builds one that fits.
//
// Clearing the protocol cache alone does not recover from that: the race it
// falls back to reaches for the same cached transport and fails on the same
// mismatch, for every request from then on.
//
// addr is the dial address rather than the transport's cache key, because only
// the transport stored under that key dials through the round tripper. The
// HTTP/3 transport brings its own dialer and never reports this.
func (pr *protocolRacer) roundTrip(transport http.RoundTripper, req *http.Request, addr string) (*http.Response, error) {
	resp, err := transport.RoundTrip(req)
	if err != nil && errors.Is(err, errProtocolChanged) && pr.dropTransport != nil {
		pr.dropTransport(addr, transport)
	}

	return resp, err
}

func (pr *protocolRacer) getTransportKey(protocol, addr string) string {
	if protocol == "h3" {
		return addr + ":h3"
	}
	return addr
}

func (pr *protocolRacer) clearProtocolCache(addr string) {
	pr.protocolCacheMu.Lock()
	delete(pr.protocolCache, addr)
	pr.protocolCacheMu.Unlock()
}

func (pr *protocolRacer) cacheWinningProtocol(addr, protocol string) {
	pr.protocolCacheMu.Lock()
	pr.protocolCache[addr] = protocol
	pr.protocolCacheMu.Unlock()

	if protocol == "h3" {
		h3Transport, err := buildHTTP3Transport(pr.getHTTP3Config())
		if err != nil {
			// Never cache a nil transport, the next request would panic on it.
			return
		}

		pr.cachedTransportsLck.Lock()
		pr.cachedTransports[addr+":h3"] = h3Transport
		pr.cachedTransportsLck.Unlock()
	}
}

func (pr *protocolRacer) handleCachedProtocolError(err error, addr string, req *http.Request) {
	if errors.Is(err, ErrBadPinDetected) && pr.badPinHandlerFunc != nil {
		pr.badPinHandlerFunc(req)
	}
	pr.clearProtocolCache(addr)
}

func (pr *protocolRacer) getHTTP3Config() *http3Config {
	return &http3Config{
		clientSessionCache:     pr.clientSessionCache,
		insecureSkipVerify:     pr.insecureSkipVerify,
		serverNameOverwrite:    pr.serverNameOverwrite,
		transportOptions:       pr.transportOptions,
		http3Settings:          pr.http3Settings,
		http3SettingsOrder:     pr.http3SettingsOrder,
		http3PriorityParam:     pr.http3PriorityParam,
		http3PseudoHeaderOrder: pr.http3PseudoHeaderOrder,
		http3SendGreaseFrames:  pr.http3SendGreaseFrames,
		proxyURL:               pr.proxyURL,
	}
}

type racingResult struct {
	protocol string
	response *http.Response
	err      error
}
