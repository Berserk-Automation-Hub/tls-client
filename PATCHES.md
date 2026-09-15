# Sightglass fork of `bogdanfinn/tls-client`

**Three functional patches** (below); otherwise this fork exists for package-path identity, the same
reason as the `websocket` fork.

Sightglass consumes patched forks of `utls`, `fhttp` and `quic-go-utls` by plain `require` with no
`replace` directive, so each carries its own module path. `tls-client` imports all three (plus
`websocket`, which imports two of them). Leaving tls-client on the upstream path would mean the
binary contains **two** utls and **two** fhttp — different packages, different types — and it does
not merely duplicate, it fails to compile:

```
websocket.go:55:22: cannot use config.cookieJar
    (variable of interface type "github.com/Berserk-Automation-Hub/fhttp".CookieJar)
    as "github.com/bogdanfinn/fhttp".CookieJar value in struct literal
```

That is hard rule HR-2 — one TLS implementation, one HTTP implementation, one QUIC implementation in
the build graph — enforced by the compiler rather than by convention.

## The diff, in full

```
module github.com/bogdanfinn/tls-client -> github.com/Berserk-Automation-Hub/tls-client
       bogdanfinn/utls         v1.7.8-barnius -> .../utls         v1.7.8-sightglass.1
       bogdanfinn/fhttp        v0.6.9         -> .../fhttp        v0.6.9-sightglass.1
       bogdanfinn/quic-go-utls v1.0.10-utls   -> .../quic-go-utls v1.0.10-sightglass.1
       bogdanfinn/websocket    v1.5.6-barnius -> .../websocket    v1.5.6-sightglass.1
go 1.24.1 -> 1.27.0
x/net 0.48.0 -> 0.59.0, testify 1.11.1 -> 1.12.1, brotli 1.2.0 -> 1.2.4,
circl 1.6.2 -> 1.6.5, x/crypto 0.46.0 -> 0.57.0, x/sys 0.39.0 -> 0.48.0  (+ the rest to current)
```

Every internal import moves with the module path. Four dead `//replace ... => ../x` comment lines are
removed, and `connect.go` is re-gofmt'd because the longer path changed one import block's sort
order. No `.go` file changes behaviour.

Based on **v1.16.0**, the latest upstream, not the v1.15.1 Sightglass pinned.

## Verification, against a pristine v1.16.0 baseline

```
fork: 1 failing test     pristine: 1 failing test
REGRESSIONS: none
gofmt: identical to upstream's baseline
go build ./... clean
```

The only failure is the pre-existing upstream one below; it fails in the pristine baseline too. Runs
that show one extra red are a **network flake**, not a regression: the extra failure is not stable
(`TestClient_HeaderOrderWithContentLengthHttp1` in one run, `TestHTTP3WithChromeOnCloudflare` in
another), both hit live external hosts, and both pass 3/3 on re-run in **this fork and the pristine
baseline alike**.

## Pre-existing upstream failure (HR-7 — recorded, not ours)

`TestHTTP3FingerprintWithDefaultValuesForChrome` fails in **pristine v1.16.0 and in this fork
alike**:

```
Chrome_133 HTTP/3 fingerprint mismatch.
  expected: 6:262144;51:1|m,a,s,p
  actual  : 51:1|m,a,s,p
```

tls-client's own Chrome-133 H3 profile emits no `SETTINGS_MAX_FIELD_SECTION_SIZE` (0x06) where its
own fixture expects one. Worth knowing because it is H3-fingerprint-adjacent — but Sightglass does
not use tls-client's HTTP/3 path at all (`quich3` owns QUIC/H3 and builds its SETTINGS from the
profile), so it is on no path we depend on. Recorded rather than hidden.

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
go.mod              fhttp v0.6.9-sightglass.1 -> v0.6.9-sightglass.2
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

## Maintenance

Re-tagging `utls`, `fhttp`, `quic-go-utls` or `websocket` means bumping the matching `require` line
here and re-tagging this module. That chain is the deliberate cost of the one-stack rule.
