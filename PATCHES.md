# Sightglass fork of `bogdanfinn/tls-client`

**There are no functional patches here.** Like the `websocket` fork, this exists for package-path
identity alone.

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
fork: 2 failing tests    pristine: 1 failing test
REGRESSIONS: none
gofmt: identical to upstream's baseline
go build ./... clean
```

The one-test difference is a **network flake**, not a regression:
`TestClient_HeaderOrderWithContentLengthHttp1` failed once with
`read tcp …->205.185.123.167:443: read: connection reset by peer` — it hits a live external host —
and passes 3/3 on re-run.

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

## Maintenance

Re-tagging `utls`, `fhttp`, `quic-go-utls` or `websocket` means bumping the matching `require` line
here and re-tagging this module. That chain is the deliberate cost of the one-stack rule.
