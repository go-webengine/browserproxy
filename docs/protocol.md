# browserproxy WebSocket protocol

`browserproxy` is a **remote browser**: it renders web pages server-side with
the pure-Go [`go-webengine/engine`](https://github.com/go-webengine/engine) and
streams frames to a thin client (e.g. the wasmdesk `clients/browser`
front-end), forwarding the client's input back as navigation. Because rendering
happens on the server, **any** site can be shown — including pages that set
`X-Frame-Options: DENY` or `Content-Security-Policy: frame-ancestors`, which an
`<iframe>` embed could never load — and the client page can stay under
`COEP: require-corp` (a WebSocket is exempt from COEP/CORS).

## Transport

One **WebSocket** connection per browser tab. The server endpoint is `/ws`.
Every message in **both** directions is a single JSON object sent as a
WebSocket **text** frame (one JSON object per message). Frames (rendered
images) are PNG bytes **base64-encoded into the JSON**, so the client needs only
one JSON parse per message and never a second binary channel.

On connect the server immediately sends one `state` message with empty fields
(no page loaded yet) so the client can paint its chrome.

## Coordinate model

The server renders the **full page** at the current viewport **width** (height
grows to fit). It keeps a scroll offset `offsetY` and streams only the
**viewport-height slice** at that offset. All client input coordinates are in
**content-area viewport pixels** (0,0 = top-left of the streamed slice); the
server adds `offsetY` to map them onto the full page.

## Client → server

| kind       | fields        | meaning |
|------------|---------------|---------|
| `navigate` | `url`         | Load `url` as a new history entry. |
| `click`    | `x`, `y`      | A click at content-area pixel `(x,y)`. If it lands inside a link's hit rect the server navigates to the link. |
| `scroll`   | `dy`          | Scroll by `dy` pixels (positive = down). Re-slices the cached page **without** re-rendering. |
| `key`      | `key`         | A key name. Arrow/Page/Home/End scroll the page; other keys are ignored server-side. |
| `resize`   | `w`, `h`      | The content area is now `w×h`. Re-renders the current page at the new width. |
| `back`     | —             | Navigate back in history. |
| `forward`  | —             | Navigate forward in history. |

Example:

```json
{"kind":"navigate","url":"https://example.com"}
{"kind":"click","x":210,"y":148}
{"kind":"scroll","dy":240}
{"kind":"resize","w":1024,"h":720}
```

## Server → client

After every handled client message the server replies with a `frame` **and** a
`state` (in that order). A failure prepends an `error`.

### `frame`

```json
{"kind":"frame","frame":"<base64 PNG>","w":1024,"h":768,"offsetY":240}
```

* `frame` — base64-encoded PNG of the viewport slice.
* `w`, `h` — slice size in pixels (equals the viewport). The client blits this
  into its content-area canvas.
* `offsetY` — the scroll offset of this slice within the full page.

The slice is always exactly `w×h`; where the page is shorter than the viewport
(or before any page loads) the uncovered area is white, so the client canvas is
always fully painted.

### `state`

```json
{"kind":"state","url":"https://example.com/","title":"Example Domain",
 "loading":false,"canBack":true,"canForward":false}
```

Drives the address bar (`url`), the tab title (`title`) and the enabled state of
the back/forward buttons (`canBack`/`canForward`).

### `error`

```json
{"kind":"error","message":"browserproxy: request blocked by SSRF guard: private address 10.0.0.1"}
```

Reported for a blocked/unreachable navigation or a malformed client message.
The client should surface it (e.g. in the address bar or a banner) and keep the
last good frame.

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

The server also enforces a **per-session navigation rate limit** and a
**global concurrent-render cap**, and a configurable **WebSocket Origin
allowlist**.
