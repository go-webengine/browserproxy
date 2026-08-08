<p align="center"><img src="https://raw.githubusercontent.com/go-webengine/brand/main/social/go-webengine.png" alt="go-webengine/browserproxy" width="720"></p>

# go-webengine / browserproxy

[![CI](https://github.com/go-webengine/browserproxy/actions/workflows/ci.yml/badge.svg)](https://github.com/go-webengine/browserproxy/actions/workflows/ci.yml)
![coverage](https://img.shields.io/badge/coverage-100%25%20(root)-brightgreen)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-webengine/browserproxy.svg)](https://pkg.go.dev/github.com/go-webengine/browserproxy)
[![Docs](https://img.shields.io/badge/docs-mkdocs--material-0079A8)](https://go-webengine.github.io/docs/)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Go 1.26.4+](https://img.shields.io/badge/Go-1.26.4%2B-00ADD8?logo=go)](https://go.dev/dl/)

A pure-Go, **`CGO_ENABLED=0`** **remote-browser** service. It renders web pages
server-side with the pure-Go [`go-webengine/engine`](https://github.com/go-webengine/engine)
and streams frames (plus a hyperlink hit-map) to a thin client, forwarding the
client's clicks, scrolls and keys back as navigation. **No Chromium, no cgo, no
host web view.**

The wire protocol is **gRPC** carried over
[`grpc-transports/websocket`](https://github.com/grpc-transports/websocket): a
single bidirectional `Session` stream per tab. Because that transport ships a
zero-dependency `syscall/js` client, **the same client compiles to
`GOOS=js/GOARCH=wasm` and runs in the browser with no sidecar proxy** — the full
gRPC feature set (client- and bidi-streaming), which plain grpc-web cannot do.

Because rendering happens on the server:

* **any** site can be shown — including pages that set `X-Frame-Options: DENY`
  or a restrictive `frame-ancestors` CSP, which an `<iframe>` embed could never
  load;
* the client page can stay under `COEP: require-corp` (needed for
  `SharedArrayBuffer`) — a WebSocket is exempt from COEP/CORS.

It is the server half of the [wasmdesk](https://github.com/wasmdesk)
`clients/browser` in-desktop browser. See [`wasmclient/`](wasmclient/) for a
worked GOOS=js/wasm client.

## Run it locally

```console
$ go run ./cmd/browserproxy -addr :8090
browserproxy: listening on :8090 (gRPC/WebSocket path /ws)
```

Then dial the `browserproxy.v1.Browser` gRPC service over the WebSocket
transport at `ws://localhost:8090/ws` (native or GOOS=js/wasm):

```go
opt, _ := wstransport.DialOption("ws://localhost:8090/ws", wstransport.ClientConfig{})
cc, _ := grpc.NewClient("passthrough:///browserproxy",
    grpc.WithTransportCredentials(insecure.NewCredentials()), opt)
stream, _ := browserpb.NewBrowserClient(cc).Session(ctx)
```

Flags:

| flag | default | meaning |
|------|---------|---------|
| `-addr` | `:8090` | listen address |
| `-origins` | `""` (any) | comma-separated WebSocket `Origin` allowlist (`*` = any) |
| `-max-concurrent` | `4` | global cap on concurrent page renders |
| `-min-nav-interval` | `250ms` | per-session minimum time between navigations |
| `-width` / `-height` | `1024` / `768` | default viewport size |
| `-render-timeout` | `35s` | per-navigation render timeout |

## Protocol

One gRPC bidirectional `Session` stream per tab (`proto/browser.proto`). Client →
server `ClientMsg`: `navigate`, `click`, `scroll`, `key`, `resize`, `back`,
`forward`. Server → client `ServerMsg`: `frame` (`png`,`w`,`h`,`offset_y` — raw
PNG bytes, no base64), `state` (`url`,`title`,`loading`,`can_back`,
`can_forward`), `error`. Full spec: [`docs/protocol.md`](docs/protocol.md).

## Security (SSRF guard)

Every fetch — the top navigation **and** every subresource, redirect and
DNS-rebinding attempt — passes an SSRF guard **before** the socket connects:

* non-`http(s)` schemes rejected (`file:`, `data:`, `javascript:`, …);
* cloud metadata `169.254.169.254`, loopback, RFC1918 private, link-local,
  IPv6 ULA (`fc00::/7`), CGNAT (`100.64.0.0/10`), unspecified and multicast
  addresses blocked at **dial time** (post-DNS);
* internal namespaces (`localhost`, `*.internal`, `*.local`, `*.lan`,
  `*.home.arpa`) blocked by name.

Plus a per-session navigation rate limit, a global concurrent-render cap, and a
configurable WebSocket `Origin` allowlist.

## Testing

```console
$ go test -short ./...     # unit tests only (no network); root package at 100%
$ go test ./...            # also runs the live example.com integration test
```

Three layers of end-to-end proof, all over the real gRPC-over-WebSocket wire:

* `TestIntegration_StubbedTransport` — a real gRPC client drives a stub-rendered
  server (hermetic, no network);
* `TestWasmClientE2E` — compiles [`wasmclient/`](wasmclient/) to `js/wasm` and
  runs it under Node against a live server, asserting a full bidirectional
  Session — proof a pure-Go browser client actually runs;
* `TestIntegration_ExampleCom` (non-`-short`) — the same client and transport
  drive a real engine-backed server against `https://example.com`, asserting a
  non-blank frame and the page title.

## License

BSD-3-Clause — see [LICENSE](LICENSE).
