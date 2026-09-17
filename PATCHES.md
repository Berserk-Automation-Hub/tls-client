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

Mechanically derived from `git diff --name-status <base>..HEAD`, not written from memory, and
CHECKED — `patches_doc_test.go` recomputes the diff and fails if this section does not match it.
**61 files in total: 11 added, 50 modified.** The 50 are 48 `.go` files plus `go.mod` and
`go.sum`.

### Added — 11 files

| file | what it is |
|---|---|
| `PATCHES.md` | this document |
| `h2identity.go` | patches 2, 3, 4, 5 — the HTTP/2 wire identity and the proxy leg's TLS identity |
| `h2identity_apply_test.go` | the per-assignment guard on `h2Identity.apply` (patches 2, 4, 6) |
| `enable_push_test.go` | guard for patch 4 |
| `h2_proxy_tunnel_identity_test.go` | guards for patches 2 and 3 |
| `hpack_indexing_policy_test.go` | guard for patch 1 |
| `hpack_static_name_index_test.go` | guard for patch 6 |
| `proxy_clienthello_test.go` | guard for patch 5 |
| `race_timeout_test.go` | guards for patch 7 |
| `http3_settings_order_test.go` | guards for patch 8 |
| `patches_doc_test.go` | the guard on THIS section and on the header's patch count |

### Modified, and behaviour changes — 5 `.go` files

| file | change |
|---|---|
| `client_options.go` | `TransportOptions.HPACKIndexingPolicy` and `.HPACKStaticNameLastMatch` (patches 1, 6) |
| `roundtripper.go` | the origin Transport takes its identity from `h2Identity`; complete, deterministic HTTP/3 SETTINGS order (patches 2, 8) |
| `connect.go` | `connectDialer` gains `h2 *h2Identity` and `tlsVerify proxyTLSVerify`; the tunnel Transport and the proxy TLS dial use them (patches 2, 3, 5) |
| `client.go` | both `newConnectDialer` call sites pass `newH2Identity(...)` and `newProxyTLSVerify(...)` (patches 2, 3, 5) |
| `racer.go` | the HTTP/3 race waits on the CALLER's context, not a ten-second literal, and each attempt runs on its own child of it so the winner can actually stop the loser (patch 7) |

### Modified, import path only — 43 `.go` files

(43 = 48 modified `.go` files minus the 5 above. `go.mod` and `go.sum` make up the other two of the
50 modified files and are described at the end of this section.)

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

Correcting them is not enough on its own — they were correct once too, and a later commit left them
behind. `patches_doc_test.go` now reads this document and fails when it drifts:

- `TestPATCHESMDPatchCountMatchesItsOwnSections` — the header's spelled-out count must equal the
  number of `## Patch N —` sections, and those must be numbered 1..N in order with no repeat or gap.
  Needs no git, so it runs everywhere, including from an extracted module zip.
- `TestPATCHESMDFileInventoryMatchesTheDiff` — recomputes `git diff --name-status` against the base
  commit named above and requires the "N files in total: A added, M modified" sentence to match it,
  requires every ADDED file to be named here, and re-derives the behaviour/rename split with the same
  rule the table below states, failing if a `.go` file that changes behaviour is not named. It skips
  — individually, printing the reason — only when there is no `.git` or no `git`, which is the
  extracted-module-zip case and never this repository. A base commit that this clone does NOT
  contain is a FAILURE, not a skip: it used to skip, which left the 40-hex SHA unguarded in the one
  direction the document has already been wrong in.
- `TestPATCHESMDUpstreamVersionIsTheTagAtItsBase` — the version NAME beside that SHA, which nothing
  checked. It prefers a tag already in the clone and otherwise asks the upstream URL this document
  names (`git ls-remote --tags`), requiring the tag at the base commit to be the version claimed and
  the claimed version to be upstream's newest release tag. With no local tag and no network it skips
  individually, printing the error that stopped it, rather than passing.
- `TestPATCHESMDHTTP3SettingsOrderFiguresMatchTheProfiles` — the two figures patch 8 states about
  the profile set, ENUMERATED from `profiles.MappedTLSClients`. The document once named the wrong
  pair of profiles here; the list now comes from the map.

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

Re-run for `v1.16.0-sightglass.12` on 2026-09-17, `go test ./... -count=1 -v` in both trees, Go
1.27.0, darwin/arm64. The pristine tree is a fresh `git clone --branch v1.16.0` of
`github.com/bogdanfinn/tls-client`, checked out at the same `23b44420627619a30d09be5b6250e6cf2e350cf2`
this document names as the base:

| | pristine `v1.16.0` | this fork |
|---|---|---|
| `github.com/.../tls-client` | ok | ok |
| `github.com/.../tls-client/cffi_src` | ok | ok |
| `github.com/.../tls-client/tests` | FAIL — 1 test | FAIL — 1 test |

```
                         pristine   fork
=== RUN  (incl. subtests)     113    113
--- PASS (top level)           74     74
--- SKIP                        4      4   (the four TestSocks5Proxy_* — no SOCKS5 server here)
--- FAIL                        1      1

pristine: --- FAIL: TestHTTP3FingerprintWithDefaultValuesForChrome (0.22s)
fork:     --- FAIL: TestHTTP3FingerprintWithDefaultValuesForChrome (0.28s)

NEW FAILURES IN THIS FORK: none
FAILURES FIXED BY THIS FORK: none
```

Recorded rather than hidden: an earlier non-verbose run of the PRISTINE tree on the same day aborted
the whole `tests/` package with `panic: dialTLS returned no error when determining cachedTransports`
(`roundtripper.go:363`, reached from `RoundTrip` at `:326`). It did not reproduce on the verbose
re-run tabulated above, it is upstream code this fork does not touch, and it is a PRISTINE-tree
observation, so it is neither a regression of this fork's nor something this fork can fix without
patching upstream's transport cache. It is noted so the next person who sees it knows it has been
seen.

`gofmt -l .` prints exactly one file in **both** trees — `profiles/contributed_browser_profiles.go`,
an upstream file this fork does not touch — so the gofmt baseline is identical to upstream's.
`go build ./...` is clean. `go vet -composites=true ./...` reports **56** `unkeyed fields`
diagnostics in **both** trees and nothing else — 5 in `example/main.go`, 9 in
`profiles/contributed_browser_profiles.go`, 40 in `profiles/internal_browser_profiles.go`, 2 in
`profiles/internal_custom_profiles.go`, all upstream literals this fork does not touch. (The flag is
spelled explicitly because a plain `go vet ./...` can serve a CACHED result for a package and print
fewer; an earlier revision of this document recorded that cached output as if it were the tree's.)

Runs that show one extra red are a **network flake**, not a regression: `tests/` dials live external
hosts. The extra failure is not stable (`TestClient_HeaderOrderWithContentLengthHttp1` in one run,
`TestHTTP3WithChromeOnCloudflare` in another) and both pass on re-run in **this fork and the pristine
baseline alike**. The run recorded above for this tag did show that one extra red in the fork's
`tests/`; it was then re-run **3 times in each tree and passed 3/3 in both**, and a second full
`tests/` run in the fork came back to the single shared pre-existing failure. It is recorded rather
than hidden.

### Coverage of this module's own package

`go test . -coverprofile` on `github.com/Berserk-Automation-Hub/tls-client`:

```
before patches 7 and 8:                 43.0% of statements
after patches 7 and 8:                  47.0% of statements
after the patch-7 loser-cancel, the
  secure direction of patch 3 and the
  two new documentation guards:         48.9% of statements
after the per-assignment apply guard,
  the six-field proxyTLSVerify
  projection, the proxy-leg ALPN and
  extension-order wire guards and the
  three h3-completion guards:           49.8% of statements
```

Every function this fork adds is covered by this fork's own tests, not only by the consumer:

```
h2identity.go   newH2Identity 100.0%  enablesPush 100.0%  apply 100.0%  newProxyTLSVerify 100.0%
racer.go        raceContext   100.0%  raceAttempt 100.0%  startRace 100.0%  waitForRaceWinner 87.5%
                attemptHTTP2  100.0%  attemptHTTP3 87.5%
roundtripper.go completeHTTP3SettingsOrder 100.0%   buildHTTP3Transport 91.2%
```

**A percentage is not a guard, and this fork has already been caught by that.** At
`v1.16.0-sightglass.11` `newProxyTLSVerify` read **100.0%** and `h2Identity.apply` read **100.0%**
in this same profile, and nine of the statements inside them could be neutered ONE AT A TIME with
the whole suite green: the statements EXECUTED, and nothing asserted the values they produced. The
figures above are reported because they are asked for, and the thing that is actually load-bearing
is the ablation table in each patch section — every element in this fork has now been reverted on
its own, at `v1.16.0-sightglass.12`, and the failure text it produced is recorded beside it.

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

THREE layers, because the failure modes are different — and because two of them were not enough.

- `h2identity_apply_test.go` — `TestH2IdentityApplyWritesEveryFieldOntoTheTransport`, the
  PER-ASSIGNMENT guard, one subtest per field `apply` writes. This layer did not exist until
  `v1.16.0-sightglass.12` and its absence is why four identity-bearing assignments could be deleted
  ONE AT A TIME with `go test . -count=1` still at `ok`. The reason is not that nobody looked: it is
  that the only tests that observed those fields were WIRE tests driven by a CHROME profile, and
  **fhttp's defaults are Chrome's values**.

  | assignment | why a Chrome wire test cannot see it go |
  |---|---|
  | `t.ConnectionFlow` | fhttp's `transportDefaultConnFlow` is **literally 15663105** (`http2/transport.go:44`, substituted at `:875` when the field is 0), which is what every Chrome profile declares. With the assignment ablated the wire test still LOGGED `WINDOW_UPDATE 15663105` and PASSED. |
  | `t.HeaderPriority` | with the field nil fhttp embeds its own `{Exclusive:true, Weight:255, StreamDep:0}` (`http2/transport.go:1543`), and every Chrome profile declares no header priority, so the frame is byte-identical. |
  | `t.InitialStreamID` | fhttp honours it only when non-zero (`http2/transport.go:939`); **all 83** in-tree profiles declare 0. |
  | `t.Priorities` | every Chrome profile declares none. |

  The sentinel values in this file are deliberately no browser's and not fhttp's: the file asserts
  that `apply` COPIES what it is handed, and the wire tests assert that what it is handed is the
  profile's. It also guards `apply`'s own doc claim — that it never READS from the Transport — by
  applying one identity to a clean Transport and to a pre-populated one and requiring the two wire
  identities to be equal. That claim is what makes ONE `h2Identity` enough for BOTH paths.
- `TestH2ProxyTunnelCarriesTheClientsHTTP2Identity` — the WIRING. White-box on purpose: it asserts
  the dialer `client.go` builds carries the profile's SETTINGS order, connection flow and indexing
  policy. Those two `newConnectDialer` call sites are exactly what an upstream merge drops silently.
  It asserts the `h2Identity` STRUCT, one layer above the field that reaches the wire, which is why
  the per-assignment layer above is needed as well as this one and not instead of it.
- `TestH2ProxyTunnelSpeaksTheProfilesHTTP2Identity` — the WIRE, through the SHIPPED public API
  (`NewHttpClient` + `WithProxyUrl("https://…")`, reachable because of patch 3). A loopback proxy
  negotiates h2, answers the CONNECT with `:status 200` on **the stream the client actually opened**
  and holds the tunnel open; the test reads the client's SETTINGS frame, its stream-0 WINDOW_UPDATE,
  its PRIORITY frames, the CONNECT stream id, its HEADERS-embedded PRIORITY and the first octet of
  `:method` off the socket.

  It is a table over **three** profiles, and that is the correction of a sentence this document used
  to carry. It said *"Every assertion is against the PROFILE, so it fails on drift rather than
  agreeing with a copy of today's output."* The first half was true and the second half did not
  follow: every assertion **was** against the profile, and the profile agreed with fhttp, so four of
  them could not fail. The three profiles are chosen so that each field has at least one case in
  which the profile and fhttp disagree:

  ```
  chrome_133   SETTINGS set and order, HPACK indexing policy, pseudo-header order
  firefox_102  connectionFlow 12517377 (fhttp: 15663105), headerPriority {dep 13, !exclusive, w 41}
               (fhttp: {dep 0, exclusive, w 255}), and a 6-entry PRIORITY tree (fhttp: none)
  a CUSTOM profile   initialStreamID 7 — the shape cffi_src/factory.go builds from a caller's JSON,
               and the only way to move a field all 83 in-tree profiles declare as 0. It carries
               Chrome_133's real hello and settings because it has to reach the proxy at all; it
               claims to be no browser (HR-1) and asserts plumbing.
  ```

Ablated, one element at a time, against `v1.16.0-sightglass.12`:

```
tunnel back to http2.Transport{}   the tunnel sent SETTINGS [ENABLE_PUSH]; the profile's order is
(connect.go drops c.h2.apply)      [HEADER_TABLE_SIZE ENABLE_PUSH INITIAL_WINDOW_SIZE
                                   MAX_HEADER_LIST_SIZE]. A zero-value http2.Transport sends fhttp's
                                   own four in Go map order, which is both a different SET and a
                                   RANDOM order
client.go passes nil               the proxy dialer carries no HTTP/2 identity: its tunnel would
                                   build a zero-value http2.Transport and speak fhttp's defaults to
                                   the proxy while the origin speaks the profile
roundtripper.go drops rt.h2.apply  with a policy that refuses :path, its first octet = 0x44, want
                                   0x04 ...; TransportOptions.HPACKIndexingPolicy is not reaching
                                   hpack.Encoder
t.ConnectionFlow dropped           [firefox_102] the tunnel's stream-0 WINDOW_UPDATE delta is
                                   15663105; the profile says 12517377. fhttp's own default is
                                   15663105, so this is the tunnel speaking fhttp's identity rather
                                   than the profile's
                                   [apply] apply left http2.Transport.ConnectionFlow = 1, the
                                   identity says 11259375 ...
t.HeaderPriority dropped           [firefox_102] the tunnel's embedded PRIORITY is dep=0
                                   exclusive=true weight=255; the profile says dep=13
                                   exclusive=false weight=41. fhttp's own default is dep=0
                                   exclusive=true weight=255, which is what a Transport with no
                                   HeaderPriority emits
t.InitialStreamID dropped          [custom profile] the tunnel opened the CONNECT on stream 1; this
                                   profile's identity puts it on 7. fhttp seeds nextStreamID from
                                   Transport.InitialStreamID only when it is non-zero ...
t.Priorities dropped               [firefox_102] the tunnel opened with 0 PRIORITY frames; the
                                   profile declares 6 ([... StreamID:3 ... StreamID:13]). fhttp
                                   writes one per entry immediately after SETTINGS ...
t.PseudoHeaderOrder dropped        the tunnel's CONNECT carried pseudo-headers [:authority :method];
                                   RFC 9113 §8.5 leaves exactly [:method :authority]
t.Settings/SettingsOrder dropped   the tunnel sent SETTINGS [ENABLE_PUSH]; the profile's order is
                                   [HEADER_TABLE_SIZE INITIAL_WINDOW_SIZE MAX_FRAME_SIZE]
t.HPACKStaticNameLastMatch dropped apply left http2.Transport.HPACKStaticNameLastMatch = false, the
                                   identity says true ...
apply reads from t (keeps an       the same identity produced two DIFFERENT wire identities: a
already-set ConnectionFlow)        zero-value Transport became {flow:11259375 ...} and a
                                   pre-populated one became {flow:1 ...}. apply reads from t
                                   somewhere, so the origin connection and the proxy tunnel no
                                   longer speak the same HTTP/2
```

### One element was DELETED rather than guarded

`apply` also carried a nil-normalising branch:

```go
// A nil order means "send none"; a nil slice would make fhttp fall back to its own, which is a
// different pseudo-header order on the wire.
if id.pseudoHeaderOrder == nil { t.PseudoHeaderOrder = []string{} } else { ... }
```

The comment was wrong. fhttp's `encodeHeaders` reads
`pHeaderOrder = cc.t.PseudoHeaderOrder; ok = len(pHeaderOrder) > 0` (`http2/transport.go:1882`), so
`nil` and `[]string{}` take the SAME branch and put the SAME bytes on the wire, and nothing else in
either module reads the field. No test could redden it because it changed nothing. It is deleted in
`v1.16.0-sightglass.12`; `TestH2IdentityApplyPassesAnEmptyPseudoHeaderOrderThrough` pins the
equivalence that justified deleting it, so a future fhttp that DOES distinguish the two reddens here
rather than silently changing the wire.

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

### Tests — BOTH directions, because only one of them is dangerous

The patch-2 wire test covers the direction a caller asks for: it drives `NewHttpClient` with
`WithInsecureSkipVerify()` against a self-signed loopback proxy. Ablated by deleting the two
`tls.Config` lines, it reports `remote error: tls: bad certificate` with the diagnosis attached,
which is the failure a caller would have hit.

That is half a guard, and it was the half that mattered least. Nothing proved the SECURE direction —
that a caller who does NOT ask for `InsecureSkipVerify` still verifies the proxy's certificate — so
hard-coding `insecureSkipVerify: true` in `newProxyTLSVerify`, which silently accepts any
certificate from any `https://` proxy for every caller of this library, left the whole suite green.

`TestProxyTLSVerificationIsOnUnlessTheCallerTurnedItOff` closes it at two layers, because a
projection that is right and a dial that ignores it are different failures:

- the projection — `newProxyTLSVerify` over both answers, asserting the proxy leg gets exactly what
  the caller said. **All six fields**, both ways: `insecureSkipVerify`, `rootCAs`, `helloID`,
  `randomExtOrder`, `forceHTTP1`, `disableHTTP3`. Until `v1.16.0-sightglass.12` this subtest asserted
  `.insecureSkipVerify` alone, so `v.rootCAs = config.transportOptions.RootCAs` could be replaced by
  `nil` and each of the other three by `false` with the whole suite green — although `rootCAs` is
  named in this very section as half of what the patch adds, and the other three are the ALPN list
  and the extension order the PROXY sees. A seventh case covers the zero-`ClientHelloID` config: the
  three hello-shaping answers must NOT be projected there, because that config takes connect.go's
  `crypto/tls` path and there is no utls connection to put them on;
- the wire — the SHIPPED `NewHttpClient` with **no** `WithInsecureSkipVerify()`, against the same
  self-signed loopback proxy the insecure test uses. The proxy answers the CONNECT with 200, so a
  client that got through is visible as an observation; the test requires none, and requires the
  error to be a certificate rejection rather than any other failure.
- the wire, the other way — `RootCAs` is the one verification answer that cannot be faked by turning
  verification OFF, so a second wire subtest puts the loopback proxy's own self-signed certificate in
  the caller's `TransportOptions.RootCAs`, passes **no** `WithInsecureSkipVerify()`, and requires the
  CONNECT to arrive. Ablated (`v.rootCAs = nil`) it reports: *"the caller put the proxy's certificate
  in TransportOptions.RootCAs and the tunnel never reached CONNECT (Do returned ... x509: certificate
  signed by unknown authority)"*.

The three hello-shaping answers are guarded on the WIRE too, in `proxy_clienthello_test.go`, because
what they change is the hello the proxy reads —
`TestProxyHelloCarriesTheCallersHelloShapingOptions`, every case driving `NewHttpClient` +
`WithProxyUrl("https://…")` at a proxy that records the first TLS record and never answers:

```
no options            ALPN to the proxy == the profile's own declaration, read from
                      GetClientHelloSpec() rather than written here   -> [h3 h2 http/1.1]
WithForceHttp1()      -> [http/1.1]                    (utls rewrites the ALPNExtension)
WithDisableHttp3()    -> [h2 http/1.1]                 (utls removes "h3" from ALPN and ALPS)
WithRandomTLSExtensionOrder()
                      4 dials, 4 DISTINCT extension orders, against a 3-dial control that shows the
                      order is otherwise fixed. GREASE ids are normalised first: their VALUES are
                      redrawn per hello by design and their POSITIONS are deliberately not shuffled,
                      so the order is the measurement and the ids are not.
```

Ablations, one field at a time in `newProxyTLSVerify`:

```
v.rootCAs = nil          the caller's TransportOptions.RootCAs is 0x… and the proxy leg gets 0x0.
                         RootCAs is documented as a property of the CLIENT; a proxy leg that drops
                         it verifies the proxy against the system roots instead of the caller's
                         + the shipped-client RootCAs wire subtest, above
v.randomExtOrder = false the caller set WithRandomTlsExtensionOrder and all four hellos to the PROXY
                         carried the profile's FIXED extension order [2570 35 13 17613 51 18 11 43 5
                         16 65037 27 10 45 23 65281 2570]
v.forceHTTP1 = false     the caller set WithForceHttp1 and the hello to the PROXY still offers ALPN
                         [h3 h2 http/1.1] (the profile declares [h3 h2 http/1.1])
v.disableHTTP3 = false   the caller set WithDisableHttp3 and the hello to the PROXY offers ALPN
                         [h3 h2 http/1.1]; with h3 removed the profile's becomes [h2 http/1.1]
v.helloID dropped        the hello to the proxy offers ALPN [h2 http/1.1]; the profile declares
                         [h3 h2 http/1.1]   +   ciphers=13(grease 0) extensions=11(grease 0): the
                         ClientHello sent to the https:// proxy carries NO GREASE …
connect.go drops the two the tunnel never completes: "proxy: read preface: remote error: tls: bad
tls.Config lines         certificate (a certificate error here means the client's
                         WithInsecureSkipVerify is not reaching the TLS dial to the PROXY …)"
```

And because this is the one patch in this fork that IS on Sightglass's shipped path —
`newProxyTLSVerify` is 100.0% in `go test ./sightglass/... -coverpkg=.../tls-client`, against every
function in `racer.go` at 0.0% — it is guarded there too, by
`TestParityHTTPSProxyCertificateIsVerifiedOnTheShippedPath` in `go/parity/`, which drives
`sightglass.NewSessionFactory -> Open -> Session.Do` with `Identity.Proxy` pointing at a self-signed
loopback proxy and asserts BOTH directions of `TransportPolicy.Insecure`. Its two ablations, run
against this fork through a scratchpad `go.work` so Sightglass's own `go.mod` keeps zero `replace`
directives:

```
newProxyTLSVerify hard-codes true      "the session tunnelled \"CONNECT origin.invalid:443
                                        HTTP/1.1\" through a self-signed https:// proxy although
                                        TransportPolicy.Insecure is false"
connect.go drops the two config lines  "TransportPolicy.Insecure is true and the session still did
                                        not complete the TLS handshake to the self-signed proxy"
```

Ablation, `newProxyTLSVerify` hard-coding `insecureSkipVerify: true`:

```
--- FAIL: .../newProxyTLSVerify_carries_the_caller's_answer,_both_ways/caller_did_not_ask_to_skip_verification
    the caller's insecureSkipVerify is false and the proxy leg gets true. A proxy leg that skips
    verification the caller never asked for accepts ANY certificate on every https:// proxy
    connection this library makes
--- FAIL: .../the_shipped_client_refuses_a_self-signed_proxy_it_was_not_told_to_trust (5.00s)
    the client tunnelled a CONNECT ([HEADER_TABLE_SIZE ENABLE_PUSH INITIAL_WINDOW_SIZE
    MAX_HEADER_LIST_SIZE]) through a self-signed https:// proxy it was never told to trust:
    certificate verification is off on the proxy leg for callers who never asked for it, so this
    library would accept ANY certificate from ANY https:// proxy
```

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

## Patch 7 — the HTTP/3 race waits on the CALLER's deadline, and actually stops the loser

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

### And what was ALSO wrong, and was NOT fixed by the first attempt at this patch

The old code paired that literal with a `cancel()` the comments described as "the winner stops the
loser". **It never did.** Both attempts were launched with the caller's untouched `*http.Request`:

```go
go pr.attemptHTTP3(req, resultCh)
go pr.attemptHTTP2(req, addr, getTransportFunc, resultCh)
```

so the only context either attempt could observe was `req.Context()`, and the context the racer held
a cancel for was one *neither of them had ever seen*. `cancel()` in `waitForRaceWinner` cancelled
only the context that function was itself selecting on, one statement before returning — and
`startRace`'s own `defer cancel()` fired a moment later regardless. The loser was never stopped, and
switching the parent from `context.Background()` to `req.Context()` did not change that: it made the
cancel *derived from the right context* and still *reaching nobody*.

The first revision of this document claimed otherwise ("so the winner can still stop the loser the
moment it wins — which is what the old `cancel()` did"). That sentence was false in both halves.
The loser goes on dialing and handshaking, holding a socket and a goroutine, for as long as its own
stack allows: for the HTTP/3 attempt against a host that answers nothing, that is QUIC's whole
handshake idle timeout after the caller's request has already been answered.

### The fix

TWO elements, because the deadline and the loser are different defects that happened to share a
line.

```go
// the context the race WAITS on: the caller's, plain, with no derived cancel of its own
func raceContext(req *http.Request) context.Context {
	return req.Context()
}

// the context each ATTEMPT RUNS on: its own cancellable child, on its own copy of the request
func raceAttempt(req *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(req.Context())
	return req.WithContext(ctx), cancel
}
```

The deadline `WithTimeoutSeconds` installs is already on `req.Context()`, so waiting on it directly
makes the race end exactly when the caller said, in both directions, with nothing new to plumb. A
derived context there would only be cancellable by the racer, and the racer has nothing to say about
when the CALLER's wait should end.

Stopping the loser happens where a cancel can actually reach an attempt: on the attempt's own
request. `startRace` builds one per attempt and `waitForRaceWinner` cancels **the loser's, and only
the loser's**, keyed on the winning protocol:

```go
h3Req, stopHTTP3 := raceAttempt(req)
h2Req, stopHTTP2 := raceAttempt(req)
go pr.attemptHTTP3(h3Req, resultCh)
go pr.attemptHTTP2(h2Req, addr, getTransportFunc, resultCh)
```

**Per-attempt and not one shared child**, because the winner's context must SURVIVE: fhttp's HTTP/2
transport and quic-go's HTTP/3 transport both abort the stream when the request context is
cancelled, so one shared cancel would hand the caller a response whose body is already dead. The
winner's child is deliberately left uncancelled and ends with its parent — the caller's request —
which is exactly the lifetime the response body has. When NOBODY wins there is no body to protect,
so `startRace` releases both.

### ONE release policy, because three statements were three chances to be wrong

The first version of this patch released the attempts from three separate statements: `stopHTTP2()`
on an h3 win, `stopHTTP3()` on an h2 win, and both again when nobody won. A statement that runs on
only one of three outcomes is a statement no single test observes, and **deleting the nobody-won
`stopHTTP3()` left the whole suite green** — which is how it was found. Worse, it is not fixable by
adding a test at that layer: when the HTTP/3 attempt is still IN FLIGHT the only way
`waitForRaceWinner` can return with no response is the caller's context expiring, and that cancels
the attempt's child anyway, so the statement is unobservable from outside `startRace` by
construction.

The fix is structural rather than a new assertion. The two attempts live in one list with one policy:

```go
attempts := []raceAttemptHandle{{"h3", stopHTTP3}, {"h2", stopHTTP2}}
stopAllExcept := func(keep string) {
	for _, a := range attempts {
		if a.protocol != keep { a.stop() }
	}
}
...
resp, err := pr.waitForRaceWinner(raceContext(req), addr, resultCh, stopAllExcept)
if resp == nil { stopAllExcept("") }   // "" keeps nothing
```

Identical behaviour in all three outcomes, and every fact in it is now on a path some test already
drives — see the four ablations below.

### Guards

`race_timeout_test.go`, four of them, because the failure modes differ:

- `TestRaceContextIsTheCallersNotATenSecondLiteral` — the DEADLINE. Three cases (3s, 60s, none) and
  the assertion is that the race's deadline **equals the request's**, so it catches the
  too-short direction, the too-long direction and the "imposed a timeout nobody asked for" direction
  in one, instantly.
- `TestStartRaceStopsWhenTheCallersDeadlinePasses` — the BEHAVIOUR. It drives the real `startRace`
  with an HTTP/2 attempt held open by a transport factory that never returns, a 250ms caller
  deadline, and a 5-second ceiling — still half the old literal, so a run that reaches it is the
  defect and not a slow machine.
- `TestStartRaceStopsTheLoserAndNotTheWinner` — the LOSER, and the winner's survival, asserted
  together because they pull in opposite directions. It drives the real `startRace`: the HTTP/2
  attempt is a stub transport that answers immediately and therefore wins, and the HTTP/3 attempt is
  held in flight by a loopback UDP socket that swallows its QUIC Initial packets and never answers.
  Three assertions — the winner's request context is NOT the caller's (so the two attempts do not
  share one), the winner's context is NOT cancelled when `startRace` returns, and the goroutine
  running the HTTP/3 attempt is off the stack within 2s instead of waiting out QUIC's handshake idle
  timeout.
- `TestStartRaceStopsBothAttemptsWhenNobodyWins` — the other end of the same contract. The HTTP/3
  attempt is failed without touching the network (`buildHTTP3Transport` refuses a non-SOCKS5 proxy,
  because only SOCKS5 can tunnel QUIC's UDP) and the HTTP/2 stub returns an error, so the caller's
  context is still alive when the race gives up; both derived contexts must be released and the
  caller's must not be.

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

Ablation, launching both attempts with the caller's untouched `req` — which is what the code did
before this revision, and the state in which this patch was once called complete:

```
--- FAIL: TestStartRaceStopsTheLoserAndNotTheWinner (0.30s)
    the winning attempt was launched with the CALLER's request unchanged. Both attempts then share
    one context, so no cancel can stop one without stopping the other — which is how the loser came
    to be left running
```

Ablation, keeping the per-attempt contexts but never cancelling the loser:

```
--- FAIL: TestStartRaceStopsTheLoserAndNotTheWinner (2.31s)
    the HTTP/2 attempt won the race and the HTTP/3 attempt was still in flight 2s later, against a
    UDP black hole at 127.0.0.1:55255. The loser is not being cancelled: both attempts are running
    on a context the racer cannot reach, so the losing dial holds its socket until QUIC's own
    handshake idle timeout
```

Ablation, cancelling BOTH on a win — the naive fix, and the reason the winner's survival is asserted:

```
--- FAIL: TestStartRaceStopsTheLoserAndNotTheWinner (0.30s)
    the WINNER's request context is already cancelled (context canceled) when startRace returns.
    The caller has not read the body yet and both transports abort the stream on request-context
    cancellation, so this hands back a dead response
```

Ablation, deleting the nobody-won cleanup (`if resp == nil { stopAllExcept("") }`):

```
--- FAIL: TestStartRaceStopsBothAttemptsWhenNobodyWins (0.30s)
    race_timeout_test.go:310: no attempt won, yet the HTTP/2 attempt's derived context is still
    uncancelled after startRace returned. Nothing depends on it — there is no response body — so it
    stays registered on the caller's context for the rest of the request, once per race
```

Ablation, dropping the `"h3"` entry from the attempt list — the one that used to be green:

```
--- FAIL: TestStartRaceStopsTheLoserAndNotTheWinner (2.30s)
    race_timeout_test.go:262: the HTTP/2 attempt won the race and the HTTP/3 attempt was still in
    flight 2s later, against a UDP black hole at 127.0.0.1:52747. The loser is not being cancelled:
    both attempts are running on a context the racer cannot reach, so the losing dial holds its
    socket until QUIC's own handshake idle timeout
```

Ablation, dropping the `"h2"` entry from the attempt list:

```
--- FAIL: TestStartRaceStopsBothAttemptsWhenNobodyWins (0.30s)
    race_timeout_test.go:310: no attempt won, yet the HTTP/2 attempt's derived context is still
    uncancelled after startRace returned …
```

Ablation, `stopAllExcept` keeping nothing (`a.stop()` unconditionally) — the naive fix again:

```
--- FAIL: TestStartRaceStopsTheLoserAndNotTheWinner (0.30s)
    race_timeout_test.go:252: the WINNER's request context is already cancelled (context canceled)
    when startRace returns. The caller has not read the body yet and both transports abort the
    stream on request-context cancellation, so this hands back a dead response
```

### Reachability (HR-7), measured

This is **not** on the Sightglass shipped path, and that is a coverage reading rather than an
argument. `sightglass.buildClient` — the one client builder, reached from
`NewSessionFactory -> Session.Do` — always passes `WithDisableHttp3()` and never
`WithProtocolRacing()`, so `roundTripper.racer` is nil and `RoundTrip` never calls `race`.

```
$ go test ./sightglass/... -coverpkg=github.com/Berserk-Automation-Hub/tls-client -coverprofile=...
ok  ...Sightglass/go/sightglass  coverage: 28.9% of statements in .../tls-client

racer.go:47   newProtocolRacer   0.0%      racer.go:173  raceContext    0.0%
racer.go:90   race               0.0%      racer.go:177  startRace      0.0%
racer.go:203  attemptHTTP2       0.0%      racer.go:188  attemptHTTP3   0.0%
racer.go:223  waitForRaceWinner  0.0%      ... every function in racer.go: 0.0%
```

against, in the same profile, the patch 1-6 elements that ARE on that path:

```
h2identity.go:34   newH2Identity     100.0%
h2identity.go:58   enablesPush       100.0%
h2identity.go:65   apply              65.6%
h2identity.go:135  newProxyTLSVerify 100.0%
```

Re-measured for `v1.16.0-sightglass.12` on 2026-09-17 (`ok .../go/sightglass coverage: 28.7% of
statements in .../tls-client`): every function in `racer.go` is still **0.0%**, as are
`buildHTTP3Transport` and `completeHTTP3SettingsOrder`.

The SIGHTGLASS-side proof for the part of this fork that IS on that path is
`go/parity/h2_identity_shipped_wire_test.go` `TestParityShippedPathHTTP2Identity`, which drives
`sightglass.NewSessionFactory -> Open -> Session.Do` and reads the SETTINGS, the stream-0
WINDOW_UPDATE, the standalone-PRIORITY count and the HEADERS-embedded PRIORITY block off the socket.
Dropping `rt.h2.apply(&t2)` here reddens it: *"the shipped session's SETTINGS carries 1 entries (2);
the capture says 4"*. Dropping `t.ConnectionFlow` alone does NOT, and that is arithmetic rather than
a hole — Sightglass's profile is Chrome 152, whose connection flow IS fhttp's default 15663105 — so
that assignment is guarded here, where `firefox_102` can tell the two apart.

The fix is made here because it is a real defect in this module's own public API, which other
consumers use, and `racer.go` is upstream code we do not get to delete. The guard therefore lives in
this fork, where the defect is, and not in Sightglass parity — putting it there would be guarding a
path Sightglass does not take, which is the exact failure mode this repository keeps finding.

## Patch 8 — the HTTP/3 SETTINGS order is COMPLETE and DETERMINISTIC

`roundtripper.go`.

### What was wrong

HTTP/3 SETTINGS order is fingerprint-bearing — browserleaks' `h3_text` is literally the setting ids
in the order they arrive — and it was **random**.

`quic-go-utls`'s `settingsFrame.Append` writes the ids named by `AdditionalSettingsOrder` first and
then writes everything LEFT by ranging over a Go map, whose iteration order Go deliberately
randomises. Upstream set `AdditionalSettingsOrder` only `if len(cfg.http3SettingsOrder) > 0`, and two
things made that insufficient:

1. **A profile that declares no order got no order at all.** Exactly five profiles declare an
   `http3SettingsOrder` — `chrome_144`, `chrome_144_PSK`, `firefox_147`, `firefox_147_PSK` and
   `firefox_148` — so for 78 of the 83 in `profiles.MappedTLSClients` the whole SETTINGS frame went
   out in Go map order, a different H3 fingerprint on every process start. (An earlier revision of
   this document named the five as "`Chrome_144` and `Chrome_133_PSK`". `Chrome_133_PSK` declares no
   order at all. The set is now ENUMERATED from the map by
   `TestPATCHESMDHTTP3SettingsOrderFiguresMatchTheProfiles`, which fails if this paragraph and the
   map disagree again.)
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

For `chrome_144` the bytes are unchanged, but NOT because its declaration already covered
everything — it declares `[1, 0x6, 7, 0x33]` and does not name the random GREASE id, which is
contributed by `buildHTTP3Transport` itself. Completing the order appends that one id, and one
leftover id ranges deterministically whichever way the map is walked, so the frame happened to be
stable already; it is now stable BY CONSTRUCTION rather than by there being nothing to shuffle. The
ablation below reddens `chrome_144` and `chrome_144_PSK` for exactly that reason. For the 78
profiles that declare no order at all, a random order becomes a fixed one.

### Guards

`http3_settings_order_test.go`, four:

- `TestHTTP3SettingsOrderNamesEverySettingEmitted` — COMPLETENESS, over every profile in
  `profiles.MappedTLSClients`, and also rejects a duplicate id (`Append` deletes as it writes, so a
  duplicate silently drops whatever would have followed).
- `TestHTTP3SettingsOrderIsStableOnTheWire` — the WIRE. `wireSettingsOrder` reproduces
  `settingsFrame.Append` exactly, map range included, and reads one transport 500 times.
- `TestHTTP3SettingsOrderKeepsTheProfilesOwnDeclarationFirst` — that completing an order never
  RE-SORTS a declared one. Its first case uses a descending caller declaration (`h3SettingsOrder` is
  caller-supplied through `cffi_src/types.go`), because an already-ascending declaration cannot show
  the difference.
- `TestHTTP3SettingsOrderCompletionIsAscendingAndGreaseLast` — added in `v1.16.0-sightglass.12`,
  for the three decisions the other three could not see. "Stable" is not "right": the wire test asks
  only that the order not MOVE, so flipping the tie-break to DESCENDING left every HTTP/3 subtest
  green, because a stable wrong order is still stable. Nothing asserted that the random GREASE id
  goes LAST rather than into numeric position, although its VALUE is redrawn per transport, so
  sorting it by value would move a different id into a different slot on every process start — the
  exact nondeterminism this patch removes. Nothing declared a duplicate id, so the dedup was green
  when deleted. And nothing exercised `t3.MaxResponseHeaderBytes >= 0` for a profile that does not
  already name `0x6`: only `chrome_144` and `chrome_144_PSK` have a positive
  `MaxResponseHeaderBytes` in-tree and both declare `0x6` themselves, while a CALLER reaches it for
  any profile through `TransportOptions.MaxResponseHeaderBytes` — including the 78 that declare no
  order at all.

Ablations, one per element:

```
restore upstream's `if len(cfg.http3SettingsOrder) > 0` logic
  80 of 83 subtests red — the three that survive are firefox_147, firefox_147_PSK and firefox_148,
  whose declared order names every id they emit (they send no GREASE setting) — e.g.:
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

flip the tie-break to DESCENDING (`rest[i] > rest[j]`)
  the HTTP/3 SETTINGS order for a caller who declared none is [51 7 1]; completing an order appends
  the ids the declaration could not name in ASCENDING numeric order, which is [1 7 51]. The
  tie-break is arbitrary but it is not free: it is the only thing standing between this profile and
  a Go map's iteration order, so it has to be ONE fixed rule that every build of every process
  agrees on

delete the declared-id dedup (`if _, dup := named[id]; dup { continue }`)
  the caller declared [7 1 7] and the completed order is [7 1 7 51]: 0x7 is named twice.
  quic-go-utls deletes an id as it writes it, so the repeat writes nothing and drops the id that
  would have followed it out of the position the caller asked for

delete the GREASE hold-back (`if hasGrease && id == greaseID { continue }`)
  [chrome_144] AdditionalSettingsOrder names 0x2704dadc9f twice ([1 6 7 51 167585176735
  167585176735]); settingsFrame.Append deletes an id once it is written, so the duplicate silently
  drops whatever would otherwise have followed it

delete the trailing GREASE append
  [chrome_144] profile chrome_144: the HTTP/3 SETTINGS frame will carry [0x21dba9bf6f], and
  AdditionalSettingsOrder ([1 6 7 51]) names none of them …

delete `if t3.MaxResponseHeaderBytes >= 0 { emitted[0x6] = … }`
  the HTTP/3 SETTINGS frame will carry 0x6 SETTINGS_MAX_FIELD_SECTION_SIZE because the caller set
  MaxResponseHeaderBytes, and AdditionalSettingsOrder ([1 7 51]) does not name it

delete `if t3.EnableDatagrams { emitted[0x33] = … }`
  the HTTP/3 SETTINGS frame will carry [0x33], and AdditionalSettingsOrder names none of them …
```

### Reachability (HR-7), measured

As with patch 7, **not** on the Sightglass shipped path, and measured the same way — in the coverage
profile above, `roundtripper.go:210 buildHTTP3Transport` and `roundtripper.go:335
completeHTTP3SettingsOrder` are both **0.0%** when the whole `sightglass` package suite drives
`tls-client`. `buildClient` always passes `WithDisableHttp3()`, and Sightglass's own `quich3` builds
its H3 SETTINGS from the profile document. This is a defect in this module's own HTTP/3 path, fixed
and guarded where it lives; in this fork's own suite the two functions are 91.2% and 100.0%.
