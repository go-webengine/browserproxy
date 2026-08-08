// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-webengine/browserproxy/browserpb"
	wstransport "github.com/grpc-transports/websocket"
	"google.golang.org/grpc"
)

// Config configures a Server.
type Config struct {
	// AllowedOrigins is the WebSocket Origin allowlist. An empty list allows any
	// origin (development default); a list containing "*" also allows any;
	// otherwise the browser Origin must match one entry. Non-browser clients
	// (no Origin header) are always allowed.
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
	// Logger, when non-nil, receives non-fatal transport diagnostics.
	Logger *log.Logger
}

const (
	defaultViewportW     = 1024
	defaultViewportH     = 768
	defaultRenderTimeout = 35 * time.Second
	keyScrollStep        = 40 // px per arrow-key scroll
	// DefaultPath is the HTTP path the gRPC-over-WebSocket endpoint is served on.
	DefaultPath = "/ws"
)

// Server implements the browserpb.Browser gRPC service: each Session stream
// drives one browser tab. It is transport-agnostic — mount it with
// [Server.HandlerListener] (gRPC over WebSocket, browser-reachable) or register
// it on any grpc.Server via [Server.Register].
type Server struct {
	browserpb.UnimplementedBrowserServer
	cfg     Config
	limiter chan struct{}
	timeout time.Duration
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
	opts := Options{GlobalLimiter: lim, MinNavInterval: cfg.MinNavInterval}
	srv.newSession = func() *Session { return NewSession(cfg.DefaultW, cfg.DefaultH, opts) }
	return srv
}

// originPatterns maps the Config allowlist onto the WebSocket transport's
// OriginPatterns. An empty allowlist becomes "*" (the development default:
// allow any origin), matching the previous gorilla-based behaviour.
func (srv *Server) originPatterns() []string {
	if len(srv.cfg.AllowedOrigins) == 0 {
		return []string{"*"}
	}
	return srv.cfg.AllowedOrigins
}

// Register registers the Server on gs as the browserpb.Browser service. Use it
// when embedding the service in a grpc.Server you own (e.g. alongside other
// services or a custom transport); otherwise prefer [Server.HandlerListener].
func (srv *Server) Register(gs *grpc.Server) {
	browserpb.RegisterBrowserServer(gs, srv)
}

// HandlerListener returns an http.Handler that upgrades WebSocket requests on
// path into gRPC connections for this service, plus a shutdown func that stops
// the backing grpc.Server gracefully. Mounting the handler on an http.ServeMux
// lets the gRPC endpoint and a wasm client share one origin. The empty path
// defaults to [DefaultPath].
func (srv *Server) HandlerListener(path string) (http.Handler, func()) {
	if path == "" {
		path = DefaultPath
	}
	h, lis := wstransport.HandlerListener(wstransport.ServerConfig{
		Path:           path,
		OriginPatterns: srv.originPatterns(),
		Logger:         srv.cfg.Logger,
	})
	gs := grpc.NewServer()
	srv.Register(gs)
	go gs.Serve(lis)
	return h, gs.GracefulStop
}

// Session implements browserpb.BrowserServer: it runs one browser tab for the
// lifetime of the bidirectional stream. It sends the initial (empty) chrome
// state, then loops receiving client input and streaming back the resulting
// frames and state until the client closes the stream or a send fails.
func (srv *Server) Session(stream browserpb.Browser_SessionServer) error {
	sess := srv.newSession()
	if err := stream.Send(stateMsg(sess.StateMsg())); err != nil {
		return err
	}
	for {
		in, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // client closed the stream cleanly
			}
			return err
		}
		for _, out := range srv.dispatch(stream.Context(), sess, in) {
			if err := stream.Send(out); err != nil {
				return err
			}
		}
	}
}

// dispatch applies one client message to sess and returns the ordered
// server→client messages to send back. Every handled message ends with a fresh
// frame and the current chrome state; failures prepend an error. It is the pure
// heart of the protocol and is fully unit-testable without a stream.
func (srv *Server) dispatch(ctx context.Context, sess *Session, msg *browserpb.ClientMsg) []*browserpb.ServerMsg {
	rctx, cancel := context.WithTimeout(ctx, srv.timeout)
	defer cancel()

	var out []*browserpb.ServerMsg
	fail := func(err error) { out = append(out, errMsg(err.Error())) }

	switch m := msg.GetMsg().(type) {
	case *browserpb.ClientMsg_Navigate:
		if err := sess.Navigate(rctx, m.Navigate.GetUrl()); err != nil {
			fail(err)
		}
	case *browserpb.ClientMsg_Click:
		if _, err := sess.Click(rctx, int(m.Click.GetX()), int(m.Click.GetY())); err != nil {
			fail(err)
		}
	case *browserpb.ClientMsg_Back:
		if err := sess.Back(rctx); err != nil {
			fail(err)
		}
	case *browserpb.ClientMsg_Forward:
		if err := sess.Forward(rctx); err != nil {
			fail(err)
		}
	case *browserpb.ClientMsg_Resize:
		if err := sess.Resize(rctx, int(m.Resize.GetW()), int(m.Resize.GetH())); err != nil {
			fail(err)
		}
	case *browserpb.ClientMsg_Scroll:
		sess.Scroll(int(m.Scroll.GetDy()))
	case *browserpb.ClientMsg_Key:
		sess.Scroll(keyScrollDelta(sess, m.Key.GetKey()))
	default:
		fail(errors.New("browserproxy: empty or unknown client message"))
	}

	// Always follow with the current frame and chrome state.
	if fr, ok := frameMsg(sess); ok {
		out = append(out, fr)
	} else {
		out = append(out, errMsg("browserproxy: frame encode failed"))
	}
	out = append(out, stateMsg(sess.StateMsg()))
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

// frameMsg encodes the session's current viewport as a Frame server message.
func frameMsg(sess *Session) (*browserpb.ServerMsg, bool) {
	png, w, h, offsetY, err := sess.FrameSlice()
	if err != nil {
		return nil, false
	}
	return &browserpb.ServerMsg{Msg: &browserpb.ServerMsg_Frame{Frame: &browserpb.Frame{
		Png:     png,
		W:       int32(w),
		H:       int32(h),
		OffsetY: int32(offsetY),
	}}}, true
}

// stateMsg wraps a State in a ServerMsg.
func stateMsg(s *browserpb.State) *browserpb.ServerMsg {
	return &browserpb.ServerMsg{Msg: &browserpb.ServerMsg_State{State: s}}
}

// errMsg wraps an error string in a ServerMsg.
func errMsg(message string) *browserpb.ServerMsg {
	return &browserpb.ServerMsg{Msg: &browserpb.ServerMsg_Error{Error: &browserpb.Error{Message: message}}}
}
