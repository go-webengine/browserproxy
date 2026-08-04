// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Config configures a Server.
type Config struct {
	// AllowedOrigins is the WebSocket Origin allowlist. An empty list allows
	// any origin (development default); a list containing "*" also allows any;
	// otherwise the request Origin must match one entry exactly (scheme+host).
	AllowedOrigins []string
	// DefaultW, DefaultH are the initial viewport size for a new session.
	DefaultW, DefaultH int
	// MaxConcurrentRenders caps concurrent renders across all sessions. Zero
	// means unlimited.
	MaxConcurrentRenders int
	// MinNavInterval is the per-session minimum time between navigations.
	MinNavInterval time.Duration
	// RenderTimeout bounds a single navigation/render. Zero uses defaultRenderTimeout.
	RenderTimeout time.Duration
}

const (
	defaultViewportW     = 1024
	defaultViewportH     = 768
	defaultRenderTimeout = 35 * time.Second
	keyScrollStep        = 40 // px per arrow-key scroll
)

// Server upgrades WebSocket connections and drives one Session per connection.
type Server struct {
	cfg      Config
	limiter  chan struct{}
	upgrader websocket.Upgrader
	timeout  time.Duration
	// newSession is injectable so tests can drive the dispatch loop with a fake
	// renderer instead of the live engine.
	newSession func() *Session
}

// NewServer builds a Server from cfg.
func NewServer(cfg Config) *Server {
	if cfg.DefaultW <= 0 {
		cfg.DefaultW = defaultViewportW
	}
	if cfg.DefaultH <= 0 {
		cfg.DefaultH = defaultViewportH
	}
	var lim chan struct{}
	if cfg.MaxConcurrentRenders > 0 {
		lim = make(chan struct{}, cfg.MaxConcurrentRenders)
	}
	timeout := cfg.RenderTimeout
	if timeout <= 0 {
		timeout = defaultRenderTimeout
	}
	srv := &Server{cfg: cfg, limiter: lim, timeout: timeout}
	srv.upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return originAllowed(cfg.AllowedOrigins, r.Header.Get("Origin"))
		},
	}
	opts := Options{GlobalLimiter: lim, MinNavInterval: cfg.MinNavInterval}
	srv.newSession = func() *Session { return NewSession(cfg.DefaultW, cfg.DefaultH, opts) }
	return srv
}

// originAllowed reports whether origin passes the allowlist. An empty allowlist
// (or one containing "*") allows anything; an empty Origin header (a non-browser
// client) is allowed; otherwise origin must match an entry exactly.
func originAllowed(allowed []string, origin string) bool {
	if len(allowed) == 0 || origin == "" {
		return true
	}
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}

// ServeHTTP upgrades the request to a WebSocket and serves the session loop.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := srv.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error response.
	}
	defer conn.Close()
	srv.serveConn(r.Context(), conn)
}

// wsConn is the minimal WebSocket surface serveConn needs, so the loop can be
// driven by a fake in tests.
type wsConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(messageType int, data []byte) error
}

// serveConn runs the read→dispatch→write loop for one connection.
func (srv *Server) serveConn(ctx context.Context, conn wsConn) {
	sess := srv.newSession()
	// Send the initial (empty) chrome state so the client can render its shell.
	_ = writeMsg(conn, sess.StateMsg())
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		m, err := DecodeClient(data)
		if err != nil {
			if writeMsg(conn, ErrorMsg{Kind: KindError, Message: err.Error()}) != nil {
				return
			}
			continue
		}
		for _, out := range srv.dispatch(ctx, sess, m) {
			if writeMsg(conn, out) != nil {
				return
			}
		}
	}
}

// dispatch applies one client message to sess and returns the ordered
// server→client messages to send back. Every handled message ends with a fresh
// frame and the current chrome state; failures prepend an ErrorMsg. It is the
// pure heart of the protocol and is fully unit-testable without a socket.
func (srv *Server) dispatch(ctx context.Context, sess *Session, m ClientMsg) []any {
	rctx, cancel := context.WithTimeout(ctx, srv.timeout)
	defer cancel()

	var out []any
	switch m.Kind {
	case KindNavigate:
		if err := sess.Navigate(rctx, m.URL); err != nil {
			out = append(out, ErrorMsg{Kind: KindError, Message: err.Error()})
		}
	case KindClick:
		if _, err := sess.Click(rctx, m.X, m.Y); err != nil {
			out = append(out, ErrorMsg{Kind: KindError, Message: err.Error()})
		}
	case KindBack:
		if err := sess.Back(rctx); err != nil {
			out = append(out, ErrorMsg{Kind: KindError, Message: err.Error()})
		}
	case KindForward:
		if err := sess.Forward(rctx); err != nil {
			out = append(out, ErrorMsg{Kind: KindError, Message: err.Error()})
		}
	case KindResize:
		if err := sess.Resize(rctx, m.W, m.H); err != nil {
			out = append(out, ErrorMsg{Kind: KindError, Message: err.Error()})
		}
	case KindScroll:
		sess.Scroll(m.DY)
	case KindKey:
		sess.Scroll(keyScrollDelta(sess, m.Key))
	default:
		out = append(out, ErrorMsg{Kind: KindError, Message: "browserproxy: unknown message kind " + m.Kind})
	}

	// Always follow with the current frame and chrome state.
	if fr, ok := frameMsg(sess); ok {
		out = append(out, fr)
	} else {
		out = append(out, ErrorMsg{Kind: KindError, Message: "browserproxy: frame encode failed"})
	}
	out = append(out, sess.StateMsg())
	return out
}

// keyScrollDelta maps a key name to a scroll delta in pixels (0 = ignored).
func keyScrollDelta(sess *Session, key string) int {
	_, h := sess.Viewport()
	page := h * 9 / 10
	switch key {
	case "ArrowDown":
		return keyScrollStep
	case "ArrowUp":
		return -keyScrollStep
	case "PageDown", " ", "Spacebar":
		return page
	case "PageUp":
		return -page
	case "Home":
		return -1 << 30 // clamps to 0
	case "End":
		return 1 << 30 // clamps to page bottom
	default:
		return 0
	}
}

// frameMsg encodes the session's current viewport as a Frame message.
func frameMsg(sess *Session) (Frame, bool) {
	png, w, h, offsetY, err := sess.FrameSlice()
	if err != nil {
		return Frame{}, false
	}
	return NewFrame(png, w, h, offsetY), true
}

// writeMsg JSON-encodes v and writes it as one WebSocket text message.
func writeMsg(conn wsConn, v any) error {
	data, err := Encode(v)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}
