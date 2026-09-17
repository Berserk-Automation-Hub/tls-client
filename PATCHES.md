# Sightglass fork of `bogdanfinn/tls-client`

**Eight functional patches** (below). They are not cosmetic: five `.go` files in this module change
behaviour, and two of those changes alter what goes on the wire for every request that uses them.
The rest of the fork is package-path identity, the same reason as the `websocket` fork.

Sightglass consumes patched forks of `utls`, `fhttp` and `quic-go-utls` by plain `require` with no
`replace` directive, so each carries its own module path. `tls-client` imports all three (plus
`websocket`, which imports two of them). Leaving tls-client on the upstream path would mean the
binary contains **two** utls and **two** fhttp — different packages, different types — and it does
not merely duplicate, it fails to compile. Reproduced on 2026-09-17 by putting
`github.com/bogdanfinn/fhttp v0.6.9` back in `go.mod` and reverting **`websocket_options.go`**'s
import alone:

```
# github.com/Berserk-Automation-Hub/tls-client
./websocket.go:55:22: cannot use config.cookieJar (variable of interface type
    "github.com/bogdanfinn/fhttp".CookieJar) as "github.com/Berserk-Automation-Hub/fhttp".CookieJar
    value in struct literal: "github.com/bogdanfinn/fhttp".CookieJar does not implement
    "github.com/Berserk-Automation-Hub/fhttp".CookieJar (wrong type for method Cookies)
        have Cookies(*url.URL) []*"github.com/bogdanfinn/fhttp".Cookie
        want Cookies(*url.URL) []*"github.com/Berserk-Automation-Hub/fhttp".Cookie
./websocket.go:75:55: cannot use w.config.headers (variable of map type
    "github.com/bogdanfinn/fhttp".Header) as "github.com/Berserk-Automation-Hub/fhttp".Header value
    in argument to w.dialer.DialContext
```

The error is REPORTED at `websocket.go` but is PRODUCED by `websocket_options.go`, which is where
the two fields' types are declared; reverting `websocket.go`'s own import instead compiles clean,
because that file uses only the string constant `http.HeaderOrderKey`. An earlier revision of this
document quoted the error with the two module paths the other way round and attributed it to
`websocket.go`; both are corrected here.

That is hard rule HR-2 — one TLS implementation, one HTTP implementation, one QUIC implementation in
the build graph — enforced by the compiler rather than by convention.

## Upstream base

**`v1.16.0`**, commit `23b44420627619a30d09be5b6250e6cf2e350cf2` ("Add disableSessionTickets and
trustAnchorsPayload to the shared library payload"). Verified, not assumed:

```
$ git merge-base sightglass origin/master
23b44420627619a30d09be5b6250e6cf2e350cf2
$ git ls-remote --tags https://github.com/bogdanfinn/tls-client.git | grep 23b4442
23b44420627619a30d09be5b6250e6cf2e350cf2	refs/tags/v1.16.0
```

`v1.16.0` is also the newest tag upstream publishes (`git ls-remote --tags ... | sort -V | tail -1`),
so this fork is not behind.

## The exact file inventory

Mechanically derived from `git diff --name-status <base>..HEAD`, not written from memory. **57 files
in total: 9 added, 48 modified.**

### Added — 9 files

| file | what it is |
|---|---|
| `PATCHES.md` | this document |
| `h2identity.go` | patches 2, 3, 4, 5 — the HTTP/2 wire identity and the proxy leg's TLS identity |
| `enable_push_test.go` | guard for patch 4 |
| `h2_proxy_tunnel_identity_test.go` | guards for patches 2 and 3 |
| `hpack_indexing_policy_test.go` | guard for patch 1 |
| `hpack_static_name_index_test.go` | guard for patch 6 |
| `proxy_clienthello_test.go` | guard for patch 5 |
| `race_timeout_test.go` | guards for patch 7 |
| `http3_settings_order_test.go` | guards for patch 8 |

### Modified, and behaviour changes — 5 `.go` files

| file | change |
|---|---|
| `client_options.go` | `TransportOptions.HPACKIndexingPolicy` and `.HPACKStaticNameLastMatch` (patches 1, 6) |
| `roundtripper.go` | the origin Transport takes its identity from `h2Identity`; complete, deterministic HTTP/3 SETTINGS order (patches 2, 8) |
| `connect.go` | `connectDialer` gains `h2 *h2Identity` and `tlsVerify proxyTLSVerify`; the tunnel Transport and the proxy TLS dial use them (patches 2, 3, 5) |
| `client.go` | both `newConnectDialer` call sites pass `newH2Identity(...)` and `newProxyTLSVerify(...)` (patches 2, 3, 5) |
| `racer.go` | the HTTP/3 race waits on the CALLER's context, not a ten-second literal (patch 7) |

### Modified, import path only — 43 `.go` files, plus `go.mod` and `go.sum`

Every remaining modified file changes on no line that does not contain `bogdanfinn` or
`Berserk-Automation-Hub`. Verify it the way this table was produced:

```
for f in $(git diff --name-status <base> | awk '$1=="M"{print $2}'); do
  echo "$(git diff -U0 <base> -- "$f" | grep -E '^[+-][^+-]' \
          | grep -vcE 'bogdanfinn|Berserk-Automation-Hub') $f"
done | sort -rn
```

which prints a non-zero count for exactly `roundtripper.go`, `connect.go`, `go.sum`,
`client_options.go`, `go.mod`, `racer.go`, `client.go` and zero for all 43 others:

```
cffi_dist/main.go  cffi_src/factory.go  cffi_src/factory_test.go  cffi_src/types.go
example/main.go  ja3.go  ja3_trust_anchors_test.go  jar.go  mapper.go  pinner.go
profiles/{contributed_browser,contributed_custom,grease,internal_browser,internal_custom,profiles}.go
socks5_udp.go  socks5_udp_test.go  websocket.go  websocket_options.go
tests/*.go  (23 files)
```

An earlier revision of this document said **"Three functional patches"** and **"No `.go` file
changes behaviour"**. Both were false when written and both are corrected above: six patches existed
already, two more are added here, and five `.go` files change behaviour.

### `go.mod` / `go.sum`

```
module github.com/bogdanfinn/tls-client -> github.com/Berserk-Automation-Hub/tls-client
       bogdanfinn/utls         v1.7.8-barnius -> .../utls         v1.7.8-sightglass.6
       bogdanfinn/fhttp        v0.6.9         -> .../fhttp        v0.6.9-sightglass.21
       bogdanfinn/quic-go-utls v1.0.10-utls   -> .../quic-go-utls v1.0.10-sightglass.14
       bogdanfinn/websocket    v1.5.6-barnius -> .../websocket    v1.5.6-sightglass.1
go 1.24.1 -> 1.27.0
x/net 0.48.0 -> 0.59.0, testify 1.11.1 -> 1.12.1, brotli 1.2.0 -> 1.2.4,
circl 1.6.2 -> 1.6.5, x/crypto 0.46.0 -> 0.57.0, x/sys 0.39.0 -> 0.48.0,
klauspost/compress 1.18.2 -> 1.20.0, x/text 0.32.0 -> 0.42.0  (+ the rest to current)
```

The four sibling pins are the versions Sightglass's own `go/go.mod` ships, checked on every tag: a
fork that pinned an older sibling than the consumer would put two builds of that sibling in a
consumer's graph. Four dead `//replace ... => ../x` comment lines are removed, and `connect.go` is
re-gofmt'd because the longer path changed one import block's sort order.

## Verification — a REGRESSION DIFF, not a green-suite claim

Run on 2026-09-17, `go test ./... -count=1` in both trees, Go 1.27.0, darwin/arm64:

| | pristine `v1.16.0` | this fork |
|---|---|---|
| `github.com/.../tls-client` | ok | ok |
| `github.com/.../tls-client/cffi_src` | ok | ok |
| `github.com/.../tls-client/tests` | FAIL — 1 test | FAIL — 1 test |

```
pristine: --- FAIL: TestHTTP3FingerprintWithDefaultValuesForChrome
fork:     --- FAIL: TestHTTP3FingerprintWithDefaultValuesForChrome

NEW FAILURES IN THIS FORK: none
FAILURES FIXED BY THIS FORK: none
```

`gofmt -l .` prints exactly one file in **both** trees — `profiles/contributed_browser_profiles.go`,
an upstream file this fork does not touch — so the gofmt baseline is identical to upstream's.
`go build ./...` is clean. `go vet ./...` reports the same upstream `unkeyed fields` diagnostics in
`profiles/` in both trees and nothing else.

Runs that show one extra red are a **network flake**, not a regression: `tests/` dials live external
hosts. The extra failure is not stable (`TestClient_HeaderOrderWithContentLengthHttp1` in one run,
`TestHTTP3WithChromeOnCloudflare` in another) and both pass on re-run in **this fork and the pristine
baseline alike**.

### Coverage of this module's own package

`go test . -coverprofile` on `github.com/Berserk-Automation-Hub/tls-client`:

```
before patches 7 and 8:  43.0% of statements
after:                   47.0% of statements
```

Every function this fork adds is covered by this fork's own tests, not only by the consumer:

```
h2identity.go  newH2Identity 100.0%   enablesPush 100.0%   apply 100.0%   newProxyTLSVerify 100.0%
racer.go       raceContext   100.0%   startRace   100.0%
roundtripper.go completeHTTP3SettingsOrder 96.7%   buildHTTP3Transport 76.5%
```

## Pre-existing upstream failure (HR-7 — recorded, not ours), with its root cause

`TestHTTP3FingerprintWithDefaultValuesForChrome` fails in **pristine v1.16.0 and in this fork
alike**:

```
Chrome_133 HTTP/3 fingerprint mismatch.
  expected: 6:262144;51:1|m,a,s,p
  actual  : 51:1|m,a,s,p
```

The root cause, which was not previously recorded: `profiles.Chrome_133` declares **no HTTP/3 fields
at all** — no `http3Settings`, no `http3SettingsOrder`, and `http3PriorityParam` zero. The only thing
`buildHTTP3Transport` has to decide "is this Chrome?" with is
`profileDefaultMaxResponseHeaderBytes`, which keys off `http3PriorityParam > 0`; for Chrome_133 that
is false, so `MaxResponseHeaderBytes` becomes `-1`, the "do not send it" sentinel, and
`SETTINGS_MAX_FIELD_SECTION_SIZE` (0x6) is omitted. The fixture expects it.

It is **not fixed here**, deliberately. Fixing it means declaring Chrome 133's HTTP/3 identity, and
HR-1 forbids hand-writing a fingerprint value: the numbers in that fixture are upstream's assertion,
not a capture of ours. The test also dials `quic.browserleaks.com`. Sightglass does not use
tls-client's HTTP/3 path at all (`quich3` owns QUIC/H3 and builds its SETTINGS from the profile
document), so it is on no path we depend on. Recorded rather than hidden.

## Patch 1 — `TransportOptions.HPACKIndexingPolicy`

### What it is

`fhttp v0.6.9-sightglass.2` added `http2.Transport.HPACKIndexingPolicy`, a per-field predicate that
decides whether the request encoder may insert a header into the connection's HPACK dynamic table.
It is the same hook quiche's `HpackEncoder` carries (`should_index_`, installed by
`SetIndexingPolicy`). This patch gives a tls-client caller a way to reach it.

```
client_options.go   TransportOptions gains HPACKIndexingPolicy func(hpack.HeaderField) bool
roundtripper.go     t2.HPACKIndexingPolicy = rt.transportOptions.HPACKIndexingPolicy
                    (inside the existing `if rt.transportOptions != nil` block, next to
                     DisableCompression, so it is set before the first ClientConn exists)
go.mod              fhttp v0.6.9-sightglass.1 -> v0.6.9-sightglass.2 (the pin AT THE TIME;
                    the current pin is in go.mod, which moves with every fhttp tag)
```

Two `.go` lines of behaviour, plus a doc comment and one import.

### Why it has to exist here

RFC 7541 lets an encoder represent one header field several ways, so the representation it picks is
an **encoder signature**, not a protocol fact. The choice for `:path` alone separates Chrome from
every Go client built on `x/net/http2` on every request whose path is not the literal `/`. And
because incremental indexing mutates **connection** state, one differing decision is not one wrong
byte: the dynamic table diverges for the life of the connection, so every later field's index and
every later block's length diverge too.

The policy cannot live on `ClientProfile`. That struct is the serialisable browser identity — it is
built from JSON by the CFFI layer (`cffi_src/factory.go`) and its constructor takes fourteen
positional arguments — and a predicate is neither serialisable nor expressible there.
`TransportOptions` is already the bag for code-supplied, non-serialisable knobs
(`KeyLogWriter io.Writer`, `RootCAs *x509.CertPool`, `Certificates`), which is exactly what this is.

### Why a predicate and not a name list

Because that is the shape the decision has in the implementation being emulated: quiche's
`HpackEncoder` consults `should_index_(name, value)` per field. A caller that wants a name list can
close over one; a caller that wants a rule cannot recover it from a list.

### Additive by construction

`nil` keeps fhttp's own rule (index every field that is not `Sensitive` and fits the table), so a
client that never sets the field is byte-identical to one built before the field existed. Existing
callers, including the CFFI surface, are untouched.

### Test

`hpack_indexing_policy_test.go` — `TestHPACKIndexingPolicyReachesTheEncoder`. A loopback TLS+ALPN-h2
listener answers the client preface with an empty SETTINGS frame, never answers the request, and
hands back the first HEADERS block fragment. The assertion is made on the **raw** block, walked by
RFC 7541 prefix bits in the test itself — deliberately not through `hpack.Decoder`, which would
resolve every representation to the same header list and hide the difference. The same request runs
twice:

```
policy nil                          :path first octet 0x44  (6.2.1, incremental indexing, name idx 4)
policy refusing pseudo-headers      :path first octet 0x04  (6.2.2, without indexing,     name idx 4)
```

Ablated: deleting the `roundtripper.go` line makes the second case report `0x44`, so the test is
carrying the patch and not merely agreeing with it.

### The gap this patch opened, closed by patch 2

Patch 1 left `connect.go`'s proxy tunnel on a zero-value `http2.Transport`, so it would have been the
one HTTP/2 path in the module without an indexing policy. That is patch 2.

## Patch 2 — one HTTP/2 identity, not two

### What was wrong

This module builds an `http2.Transport` in **two** places:

```
roundtripper.go   the ORIGIN connection   — SETTINGS + order, connection flow, HEADERS priority,
                                            pseudo-header order, first stream id, HPACK policy
connect.go:341    the TUNNEL to an https:// proxy — http2.Transport{}   <- a ZERO VALUE
```

So a client carrying a browser profile spoke a browser's HTTP/2 to the origin and fhttp's own
defaults to the proxy. Measured, by ablating the fix and reading the bytes off a loopback proxy:

```
profile (Chrome_133)  SETTINGS [HEADER_TABLE_SIZE ENABLE_PUSH INITIAL_WINDOW_SIZE MAX_HEADER_LIST_SIZE]
                      WINDOW_UPDATE 15663105, HEADERS-embedded PRIORITY present
zero-value Transport  SETTINGS [ENABLE_PUSH]
                      no WINDOW_UPDATE, no embedded PRIORITY, no HPACK indexing policy
```

One setting against four, on the first frames of the connection, before any request — and the
`CONNECT` request's own header block encoded by a different rule than every origin request on the
same client. Two HTTP/2 identities in one binary, and the proxy operator sees the one that is not a
browser.

### The fix

```
h2identity.go     NEW — h2Identity: the wire identity a ClientProfile describes, apart from the
                  per-path plumbing (dialer, TLS config, timeouts, compression) a Transport carries
roundtripper.go   the origin Transport now gets its identity from that value
connect.go        connectDialer gains an h2 *h2Identity; the tunnel Transport applies it
client.go         both newConnectDialer call sites pass newH2Identity(clientProfile, transportOptions)
```

Keeping it in one value is the point: a field added to `h2Identity` reaches both paths, and a field
added to only one `Transport` literal shows up as an asymmetry. `nil` keeps the old behaviour, which
is what a caller-supplied `ProxyDialerFactory` gets — that dialer is the caller's, not ours.

### Tests

`h2_proxy_tunnel_identity_test.go`, two halves, because the failure modes are different:

- `TestH2ProxyTunnelCarriesTheClientsHTTP2Identity` — the WIRING. White-box on purpose: it asserts
  the dialer `client.go` builds carries the profile's SETTINGS order, connection flow and indexing
  policy. Those two `newConnectDialer` call sites are exactly what an upstream merge drops silently.
- `TestH2ProxyTunnelSpeaksTheProfilesHTTP2Identity` — the WIRE, through the SHIPPED public API
  (`NewHttpClient` + `WithProxyUrl("https://…")`, reachable because of patch 3). A loopback proxy
  negotiates h2, answers the CONNECT with `:status 200` and holds the tunnel open; the test reads the
  client's SETTINGS frame, its stream-0 WINDOW_UPDATE, its HEADERS-embedded PRIORITY and the first
  octet of `:method` off the socket. Every assertion is against the PROFILE, so it fails on drift
  rather than agreeing with a copy of today's output.

Ablated, one each:

```
tunnel back to http2.Transport{}      "the tunnel sent SETTINGS [ENABLE_PUSH]; the profile's order
                                       is [HEADER_TABLE_SIZE ENABLE_PUSH INITIAL_WINDOW_SIZE
                                       MAX_HEADER_LIST_SIZE]"
client.go passes nil                  "the proxy dialer carries no HTTP/2 identity"
```

### What patch 2 does NOT claim (HR-7)

**No Chrome ground truth for this surface.** What it asserts is "one identity, not two", which is
provable locally. Whether Chrome's HTTP/2-to-proxy connection is byte-identical to its
HTTP/2-to-origin connection is *not* asserted: no capture of Chrome against an h2 proxy exists here.

## Patch 3 — the caller's TLS verification reaches the proxy leg

### What was wrong

`connect.go`'s `https://` proxy dial built its own `tls.Config{NextProtos, ServerName}` — no
verification hook of any kind. So `WithInsecureSkipVerify()` and `TransportOptions.RootCAs`, both
documented as properties of the CLIENT, applied to the origin connection and silently not to the
connection to the proxy. A caller who has said "do not verify" and still gets
`remote error: tls: bad certificate` from their own proxy has been given a surprise, not a safety
net: the decision was already made, one leg just did not hear it.

It is also what made patch 2 untestable through the public API — a self-signed loopback proxy was
unreachable from `NewHttpClient`, so the wire test could only have been written against internals.

### The fix

```
connect.go   connectDialer gains tlsVerify proxyTLSVerify {insecureSkipVerify, rootCAs}, applied to
             the proxy tls.Config
h2identity.go newProxyTLSVerify(config) projects the client-wide settings onto the proxy leg
client.go    both newConnectDialer call sites pass it
```

Nothing changes unless the caller set one of those options, and setting them already means this.

**Client certificates are deliberately NOT plumbed.** This dial uses `crypto/tls` while
`TransportOptions.Certificates` is `utls.Certificate` — utls is a fork, not an alias, so the two
types are unrelated. Converting on a guess would be inventing behaviour; it is recorded here instead.

### Test

The patch-2 wire test is the test: it drives `NewHttpClient` with `WithInsecureSkipVerify()` against a
self-signed loopback proxy. Ablated by deleting the two `tls.Config` lines, it reports
`remote error: tls: bad certificate` with the diagnosis attached, which is the failure a caller would
have hit.

## Patch 4 — server push follows the SETTINGS frame the identity advertises

`h2identity.go`.

`apply` set `t.PushHandler = &http2.DefaultPushHandler{}` **unconditionally**, so a client whose
profile sends `SETTINGS_ENABLE_PUSH=0` — which every current browser profile does, because no
current browser accepts push — told the peer *"do not push"* and then accepted a `PUSH_PROMISE`
anyway: allocating a stream, spawning a goroutine and reading the pushed response body.

Two costs, and they are different in kind.

**Resource.** A hostile or misconfigured origin can make the client allocate streams and goroutines
it advertised it would not accept, unbounded, driven entirely from the far end.

**Fingerprint.** It is a cross-layer contradiction: the wire says push is disabled and the
implementation accepts it. **One** `PUSH_PROMISE` distinguishes this client from the browser it
claims to be, because a browser advertising `ENABLE_PUSH=0` treats a push as a `PROTOCOL_ERROR` and
closes the connection.

**The fix is to stop overriding, not to add anything.** fhttp already implements exactly the right
behaviour for a nil handler — `readLoop` returns `ConnectionError(ErrCodeProtocol)`, and its own
comment there reads *"should not be receiving PUSH_PROMISE if ENABLE_PUSH is disabled"*. The
override was what prevented it.

```go
if id.settings == nil || id.enablesPush() {
    t.PushHandler = &http2.DefaultPushHandler{}
}
```

**Absent is not the same as 1.** RFC 9113 §6.5.2 gives `ENABLE_PUSH` a default of 1, but a profile
carrying a browser's identity while omitting the setting is already not that browser, and every
browser profile states it explicitly. Treating absent as enabled would restore the old behaviour for
exactly the profiles that forgot to say, which is the wrong way round.

**An identity with no settings at all keeps its handler**, so a profile-less caller behaves as it
always has: that path advertises no `SETTINGS` frame of its own and therefore contradicts nothing.

Guard: `enable_push_test.go` — four identities (`ENABLE_PUSH=0`, `=1`, absent, no settings) plus a
nil-identity inertness case. Ablation, restoring the unconditional assignment:

```
--- FAIL: TestEnablePushFollowsTheAdvertisedSettings/browser_profile:_ENABLE_PUSH=0
        PushHandler != nil = true, want false.
--- FAIL: TestEnablePushFollowsTheAdvertisedSettings/setting_absent
```

## Patch 5 — the https:// proxy sees the profile's ClientHello, not Go's

`connect.go`, `h2identity.go`.

The TLS dial to an `https://` proxy used **`crypto/tls`**, so a client carrying a browser profile put
a browser's ClientHello on its ORIGIN connection and a **Go standard-library** one on its connection
to the PROXY. Two TLS identities from one session, and the proxy operator sees the one that is not a
browser — before any CONNECT line, on every tunnel.

This is patch 2 one layer down. Patch 2 gave the proxy the profile's HTTP/2; this gives it the
profile's TLS.

Measured by a loopback proxy that reads the first TLS record and never answers (the hello is on the
wire before a server says anything, so the handshake does not need to complete):

```
profile's hello   ciphers=16(grease 1)  extensions=17(grease 2)  session_id_len=32
crypto/tls        ciphers=13(grease 0)  extensions=11(grease 0)  session_id_len=32
```

The dial now uses `utls.UClient` with the client's own `ClientHelloID`, and carries the origin leg's
extension-order policy with it — one identity emitting a shuffled hello to the origin and a fixed one
to the proxy would contradict itself.

**A zero ClientHelloID keeps the `crypto/tls` path**, which is what a caller-supplied
`ProxyDialerFactory` gets: that dialer is the caller's, not ours.

**Client certificates for the proxy leg are still unplumbed** (HR-7, unchanged from patch 3):
`TransportOptions.Certificates` is utls's own type and this dial now has a utls path, so the
conversion is no longer type-blocked — but no ground truth exists here for what a browser does with
a client certificate on a proxy leg, and inventing it would be worse than recording it.

Guard: `proxy_clienthello_test.go`. It asserts GREASE presence rather than a cipher count, so a
profile change cannot invalidate it. Ablation, dropping the identity:

```
hello to the proxy: ciphers=13(grease 0) extensions=11(grease 0)
the ClientHello sent to the https:// proxy carries NO GREASE ... the proxy leg is dialling
with crypto/tls while the origin leg uses the browser identity
```

## Patch 6 — `TransportOptions.HPACKStaticNameLastMatch`

Patch 1 made WHICH REPRESENTATION the HPACK encoder chooses a caller decision. This makes WHICH
STATIC ENTRY a duplicated header NAME resolves to a caller decision too — the other half of the same
encoder signature, and the half that made a second engine unshippable.

RFC 7541 Appendix A gives `:method`, `:path`, `:scheme` and `:status` more than one static entry. An
encoder that spells a field out but cites its name by index emits whichever it resolved to. Invisible
for a name+value hit; **one byte on the wire** for everything else:

| engine | policy | `:path` first octet | evidence |
|---|---|---|---|
| Chrome 153 | first match | `0x44` (name index 4) | 114 `:path` + 8 `:method`, two captures, zero exceptions |
| Firefox 156 | last match | `0x45` (name index 5) | 41 of 41 attributed HEADERS blocks, confirmed by tshark |

`fhttp` carried Chrome's answer as a constant (`v0.6.9-sightglass.8` makes it a parameter). This
module threads it: `TransportOptions.HPACKStaticNameLastMatch` -> `h2Identity.staticNameLastMatch` ->
`http2.Transport.HPACKStaticNameLastMatch`, the same three-step path `HPACKIndexingPolicy` already
takes, so it lands on the FIRST connection rather than after one has been built.

### Tests

`hpack_static_name_index_test.go` reuses patch 1's loopback h2 server and reads the first octet of
the `:path` field out of the real HEADERS frame: `0x44` with the option false, `0x45` with it true.

Ablation, dropping the one line in `apply`:

```
with HPACKStaticNameLastMatch=true, :path first octet = 0x44, want 0x45;
TransportOptions.HPACKStaticNameLastMatch is not reaching hpack.Encoder
```

## Maintenance

Re-tagging `utls`, `fhttp`, `quic-go-utls` or `websocket` means bumping the matching `require` line
here and re-tagging this module. That chain is the deliberate cost of the one-stack rule, and it is
not optional: this fork's `go.mod` must never pin an older sibling than Sightglass's own `go/go.mod`
ships, or a consumer's build graph gets two of that sibling.

This document's front matter — the patch count, the behaviour claim, the file inventory and the
regression diff — is part of the patch, not commentary on it. Every one of those was wrong at least
once; they are now derived mechanically (the commands are in "The exact file inventory" and
"Verification") and every change to this fork must re-derive them.

## Patch 7 — the HTTP/3 race waits on the CALLER's deadline, not a ten-second literal

`racer.go`.

### What was wrong

`startRace` opened its own context:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
```

`context.Background()`, so nothing the caller configured reached it, and `WithTimeoutSeconds` — the
one timeout knob this library exposes — was silently ignored by the HTTP/3 race. Wrong in **both**
directions:

- **A caller who asked for LESS did not get it.** `WithTimeoutSeconds(3)` puts a 3-second deadline on
  the request's context (fhttp `Client.send` -> `setRequestCancel`). Both racing attempts return on
  it — and then `waitForRaceWinner` went on waiting on a context that does not expire until ten. A
  client configured for three seconds returned at ten.
- **A caller who asked for MORE did not get it either.** At ten seconds the race context fired and
  the request failed with `context deadline exceeded` while the caller's own deadline, and both
  in-flight attempts, still had time left.

### The fix

```go
func raceContext(req *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(req.Context())
}
```

The deadline `WithTimeoutSeconds` installs is already on `req.Context()`, so deriving from it makes
the race end exactly when the caller said, in both directions, with nothing new to plumb.
`WithCancel` rather than plain `req.Context()` so the winner can still stop the loser the moment it
wins — which is what the old `cancel()` did and is the only thing that context was good for.

### Guards

`race_timeout_test.go`, two of them, because the failure modes differ:

- `TestRaceContextIsTheCallersNotATenSecondLiteral` — the DEADLINE. Three cases (3s, 60s, none) and
  the assertion is that the race's deadline **equals the request's**, so it catches the
  too-short direction, the too-long direction and the "imposed a timeout nobody asked for" direction
  in one, instantly.
- `TestStartRaceStopsWhenTheCallersDeadlinePasses` — the BEHAVIOUR. It drives the real `startRace`
  with an HTTP/2 attempt held open by a transport factory that never returns, a 250ms caller
  deadline, and a 5-second ceiling — still half the old literal, so a run that reaches it is the
  defect and not a slow machine.

Ablation, restoring `context.WithTimeout(context.Background(), 10*time.Second)`:

```
race_timeout_test.go:63: the race expires at ...m=+10.002487126 but the caller's request expires
    at ...m=+3.002484917 (a difference of 7s): the race is running on its own clock, so a client
    configured for 3s neither gets that long nor stops when it is up
race_timeout_test.go:63: ... (a difference of -50s): ... a client configured for 1m0s neither gets
    that long nor stops when it is up
race_timeout_test.go:52: the caller set no deadline, yet the race waits on one that expires in 10s:
    the race is imposing a timeout the client never configured
race_timeout_test.go:131: the caller's request expired after 250ms and the race was still running
    5s later: startRace is waiting on context.WithTimeout(context.Background(), 10*time.Second), a
    literal that the client's WithTimeoutSeconds cannot shorten
--- FAIL: TestRaceContextIsTheCallersNotATenSecondLiteral (0.00s)
--- FAIL: TestStartRaceStopsWhenTheCallersDeadlinePasses (5.00s)
```

### Reachability (HR-7)

This is **not** on the Sightglass shipped path. `sightglass.buildClient` — the one client builder,
reached from `NewSessionFactory -> Session.Do` — always passes `WithDisableHttp3()` and never
`WithProtocolRacing()`, so `roundTripper.racer` is nil and `RoundTrip` never calls `race`. The fix is
here because it is a real defect in this module's own public API, which other consumers use; the
guard for it therefore lives in this fork, where the defect is, and not in Sightglass parity.

## Patch 8 — the HTTP/3 SETTINGS order is COMPLETE and DETERMINISTIC

`roundtripper.go`.

### What was wrong

HTTP/3 SETTINGS order is fingerprint-bearing — browserleaks' `h3_text` is literally the setting ids
in the order they arrive — and it was **random**.

`quic-go-utls`'s `settingsFrame.Append` writes the ids named by `AdditionalSettingsOrder` first and
then writes everything LEFT by ranging over a Go map, whose iteration order Go deliberately
randomises. Upstream set `AdditionalSettingsOrder` only `if len(cfg.http3SettingsOrder) > 0`, and two
things made that insufficient:

1. **A profile that declares no order got no order at all.** That is every in-tree profile except
   `Chrome_144` and `Chrome_133_PSK` — 78 of the 83 in `profiles.MappedTLSClients` — so the whole
   SETTINGS frame went out in Go map order, a different H3 fingerprint on every process start.
2. **Two ids could not be named even by a profile that does declare an order.** `0x6`
   `SETTINGS_MAX_FIELD_SECTION_SIZE` and `0x33` `SETTINGS_H3_DATAGRAM` are contributed by the frame
   itself, not by `AdditionalSettings`, so they are invisible to this module's maps.

### The fix

`completeHTTP3SettingsOrder` builds an order that names **every** id the frame will carry: the
profile's own declaration first and verbatim, then every remaining emitted id in ascending numeric
order, then the random GREASE id last. It runs at the END of `buildHTTP3Transport`, because
`MaxResponseHeaderBytes` — which decides whether `0x6` is emitted at all — is not settled until then.

**Ascending is a TIE-BREAK, not a claim about any browser** (HR-1). A profile that knows its
browser's order states it and is followed exactly, which is why the declaration is copied in front
untouched; ascending only decides the ids the profile could not name, and the alternative to a
tie-break here is not "the browser's order", it is a coin flip per connection.

For `Chrome_144` the result is byte-identical to before (`[1, 0x6, 7, 0x33, GREASE]` — its
declaration already covered everything). For the other 78 profiles a random order becomes a fixed
one.

### Guards

`http3_settings_order_test.go`, three:

- `TestHTTP3SettingsOrderNamesEverySettingEmitted` — COMPLETENESS, over every profile in
  `profiles.MappedTLSClients`, and also rejects a duplicate id (`Append` deletes as it writes, so a
  duplicate silently drops whatever would have followed).
- `TestHTTP3SettingsOrderIsStableOnTheWire` — the WIRE. `wireSettingsOrder` reproduces
  `settingsFrame.Append` exactly, map range included, and reads one transport 500 times.
- `TestHTTP3SettingsOrderKeepsTheProfilesOwnDeclarationFirst` — that completing an order never
  RE-SORTS a declared one. Its first case uses a descending caller declaration (`h3SettingsOrder` is
  caller-supplied through `cffi_src/types.go`), because an already-ascending declaration cannot show
  the difference.

Ablations, one per element:

```
restore upstream's `if len(cfg.http3SettingsOrder) > 0` logic
  78 of 83 subtests red, e.g.:
  profile chrome_133: the HTTP/3 SETTINGS frame will carry [0x33], and AdditionalSettingsOrder ([])
  names none of them. quic-go-utls writes every setting the order does not name by ranging over a
  Go map (http3/frames.go, settingsFrame.Append), and Go randomises map iteration, so those ids land
  in a different position on every process start: this profile's HTTP/3 SETTINGS fingerprint is not
  stable from one run to the next
  read 2: the HTTP/3 SETTINGS frame carries [7 51 1], read 1 carried [1 7 51] — ...

sort the DECLARED ids too
  SETTINGS order is [1 7 51]; the caller declared [7 1] and the declaration must be copied verbatim
  in front. Completing the order must only APPEND the ids the declaration could not name —
  re-sorting the declared ones replaces a measured SETTINGS order with this code's own tie-break
```

### Reachability (HR-7)

As with patch 7, **not** on the Sightglass shipped path: `buildClient` always passes
`WithDisableHttp3()`, and Sightglass's own `quich3` builds its H3 SETTINGS from the profile document.
This is a defect in this module's own HTTP/3 path, fixed and guarded where it lives.
