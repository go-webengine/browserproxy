# browserproxy protocol

`browserproxy` is a **remote browser**: it renders web pages server-side with
the pure-Go [`go-webengine/engine`](https://github.com/go-webengine/engine) and
streams frames to a thin client (e.g. the wasmdesk `clients/browser`
front-end), forwarding the client's input back as navigation. Because rendering
happens on the server, **any** site can be shown — including pages that set
`X-Frame-Options: DENY` or `Content-Security-Policy: frame-ancestors`, which an
`<iframe>` embed could never load — and the client page can stay under
`COEP: require-corp` (a WebSocket is exempt from COEP/CORS).

## Transport

The wire protocol is the **gRPC** service `browserproxy.v1.Browser`
(`proto/browser.proto`), carried over
[`grpc-transports/websocket`](https://github.com/grpc-transports/websocket) — a
gRPC transport that tunnels the HTTP/2 framing over a single **WebSocket** per
tab. The server endpoint is `/ws`.

```proto
service Browser {
  rpc Session(stream ClientMsg) returns (stream ServerMsg);
}
```

One long-lived **bidirectional stream** models one browser tab: the client sends
`ClientMsg` input events, the server streams back `ServerMsg` frames and state.
The transport matters: because `grpc-transports/websocket` ships a
zero-dependency `syscall/js` client, the **same** Go client compiles to
`GOOS=js/GOARCH=wasm` and runs in the browser with the full gRPC feature set
(client- and bidi-streaming) — something plain grpc-web, which needs a sidecar
and cannot client-stream, does not provide. See [`../wasmclient`](../wasmclient)
for a worked browser client.

On connect the server immediately sends one `state` message with empty fields
(no page loaded yet) so the client can paint its chrome.

Frames (rendered images) travel as **raw PNG `bytes`** inside the protobuf
message — gRPC carries binary natively, so there is no base64 expansion.

## Coordinate model

The server renders the **full page** at the current viewport **width** (height
grows to fit). It keeps a scroll offset `offset_y` and streams only the
**viewport-height slice** at that offset. All client input coordinates are in
**content-area viewport pixels** (0,0 = top-left of the streamed slice); the
server adds `offset_y` to map them onto the full page.

## Client → server: `ClientMsg`

`ClientMsg` is a `oneof` — exactly one of:

| case       | fields   | meaning |
|------------|----------|---------|
| `navigate` | `url`    | Load `url` as a new history entry. |
| `click`    | `x`, `y` | A click at content-area pixel `(x,y)`. If it lands inside a link's hit rect the server navigates to the link. |
| `scroll`   | `dy`     | Scroll by `dy` pixels (positive = down). Re-slices the cached page **without** re-rendering. |
| `key`      | `key`    | A key name. Arrow/Page/Home/End scroll the page; other keys are ignored server-side. |
| `resize`   | `w`, `h` | The content area is now `w×h`. Re-renders the current page at the new width. |
| `back`     | —        | Navigate back in history. |
| `forward`  | —        | Navigate forward in history. |

An empty or unrecognised `ClientMsg` yields an `error` (followed, as always, by
the current frame and state).

## Server → client: `ServerMsg`

`ServerMsg` is a `oneof` of `frame`, `state`, `error`. After every handled
client message the server sends a `frame` **and** a `state` (in that order); a
failure prepends an `error`.

### `Frame`

* `png` — raw PNG bytes of the viewport slice.
* `w`, `h` — slice size in pixels (equals the viewport). The client blits this
  into its content-area canvas.
* `offset_y` — the scroll offset of this slice within the full page.

The slice is always exactly `w×h`; where the page is shorter than the viewport
(or before any page loads) the uncovered area is white, so the client canvas is
always fully painted.

### `State`

* `url` — drives the address bar.
* `title` — the tab title.
* `loading` — whether a render is in flight.
* `can_back`, `can_forward` — enabled state of the history buttons.

### `Error`

* `message` — e.g. `browserproxy: request blocked by SSRF guard: private address 10.0.0.1`.

Reported for a blocked/unreachable navigation or an empty/unknown client
message. The client should surface it (e.g. in the address bar or a banner) and
keep the last good frame.

## Security

Before **any** fetch — the top navigation **and** every subresource
(stylesheets, images), redirect target and DNS-rebinding attempt — the request
is checked by the SSRF guard:

* non-`http(s)` schemes are rejected (`file:`, `data:`, `javascript:`, …);
* the cloud metadata endpoint `169.254.169.254`, all loopback, RFC1918 private,
  link-local, unique-local (IPv6 `fc00::/7`), carrier-grade-NAT
  (`100.64.0.0/10`), unspecified and multicast addresses are blocked, at **dial
  time** (post-DNS), so a hostname that resolves to a private IP cannot slip
  through;
* internal namespaces (`localhost`, `*.internal`, `*.local`, `*.lan`,
  `*.home.arpa`) are blocked by name.

The server also enforces a **per-session navigation rate limit**, a **global
concurrent-render cap**, and a configurable **WebSocket Origin allowlist**
(`AllowedOrigins`; empty or `*` allows any origin).
