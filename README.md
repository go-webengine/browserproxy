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
and streams frames (plus a hyperlink hit-map) to a thin client over a simple
JSON **WebSocket** protocol, forwarding the client's clicks, scrolls and keys
back as navigation. **No Chromium, no cgo, no host web view.**

Because rendering happens on the server:

* **any** site can be shown — including pages that set `X-Frame-Options: DENY`
  or a restrictive `frame-ancestors` CSP, which an `<iframe>` embed could never
  load;
* the client page can stay under `COEP: require-corp` (needed for
  `SharedArrayBuffer`) — a WebSocket is exempt from COEP/CORS.

It is the server half of the [wasmdesk](https://github.com/wasmdesk)
`clients/browser` in-desktop browser.

## Run it locally

```console
$ go run ./cmd/browserproxy -addr :8090
browserproxy: listening on :8090 (ws path /ws)
```

Then point a client at `ws://localhost:8090/ws`. Flags:

| flag | default | meaning |
|------|---------|---------|
| `-addr` | `:8090` | listen address |
| `-origins` | `""` (any) | comma-separated WebSocket `Origin` allowlist (`*` = any) |
| `-max-concurrent` | `4` | global cap on concurrent page renders |
| `-min-nav-interval` | `250ms` | per-session minimum time between navigations |
| `-width` / `-height` | `1024` / `768` | default viewport size |
| `-render-timeout` | `35s` | per-navigation render timeout |

## Protocol

Line-oriented JSON over one WebSocket per tab; frames are base64-PNG inside the
JSON. Client → server: `navigate`, `click`, `scroll`, `key`, `resize`, `back`,
`forward`. Server → client: `frame` (`w`,`h`,`offsetY`), `state`
(`url`,`title`,`loading`,`canBack`,`canForward`), `error`. Full spec:
[`docs/protocol.md`](docs/protocol.md).

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

The live `TestIntegration_ExampleCom` drives a real WebSocket client against a
real server, navigates to `https://example.com`, and asserts it receives a
non-blank frame and a `state` carrying the page title.

## License

BSD-3-Clause — see [LICENSE](LICENSE).
