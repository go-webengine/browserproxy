// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"net/http"
	"sync"
	"time"

	"github.com/go-webengine/browserproxy/browserpb"
	"github.com/go-webengine/engine"
)

// Session-level errors (in addition to guard's ErrBlocked).
var (
	// ErrRateLimited is returned when a session navigates faster than the
	// configured per-session minimum interval.
	ErrRateLimited = fmt.Errorf("browserproxy: navigation rate limit exceeded")
	// ErrNoHistory is returned by Back/Forward when there is nowhere to go.
	ErrNoHistory = fmt.Errorf("browserproxy: no history entry")
)

// RenderFunc fetches and renders url at viewport w×h, returning the full-page
// image, page info and hyperlink hit-map. The default is engine-backed; tests
// inject a fake so all session logic is exercisable without the network.
type RenderFunc func(ctx context.Context, url string, w, h int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error)

// Session is one browser tab's server-side state: the current full-page image
// and its hit-map, the scroll offset, the viewport size, and a back/forward
// history. All exported methods are safe for concurrent use.
type Session struct {
	mu     sync.Mutex
	render RenderFunc

	w, h    int           // viewport (content area) size in pixels
	full    *image.RGBA   // current full-page render (nil before first nav)
	links   []engine.Link // hit-map for full, in full-page coords
	url     string        // current (final, post-redirect) URL
	title   string        // current page title
	scrollY int           // vertical scroll offset into full

	back    []string // pages behind the current one (top = most recent)
	forward []string // pages ahead (top = next forward)

	sem     chan struct{} // shared global render-concurrency limiter (nil = unlimited)
	minNav  time.Duration // per-session minimum interval between renders
	lastNav time.Time     // time of the last successful render
	maxHist int           // history-stack cap per direction
	now     func() time.Time
}

// Options configures a session's limits.
type Options struct {
	// GlobalLimiter caps the number of concurrent renders across all sessions
	// sharing it. A nil limiter means unlimited.
	GlobalLimiter chan struct{}
	// MinNavInterval is the minimum time between two successful renders in one
	// session (rate limit). Zero disables it.
	MinNavInterval time.Duration
	// MaxHistory caps each of the back/forward stacks. Zero uses defaultMaxHistory.
	MaxHistory int
}

const defaultMaxHistory = 100

// NewSession creates a session with a real engine-backed renderer whose HTTP
// client is wrapped by the SSRF-guarded dial control (so navigation and every
// subresource are guarded at dial time).
func NewSession(w, h int, opts Options) *Session {
	eng := engine.New()
	eng.Client = GuardedClient(eng.Client)
	return newSession(w, h, engineRender(eng), opts)
}

// newSession is the injectable constructor used by NewSession and by tests.
func newSession(w, h int, render RenderFunc, opts Options) *Session {
	if w <= 0 {
		w = 1024
	}
	if h <= 0 {
		h = 768
	}
	maxHist := opts.MaxHistory
	if maxHist <= 0 {
		maxHist = defaultMaxHistory
	}
	return &Session{
		render:  render,
		w:       w,
		h:       h,
		sem:     opts.GlobalLimiter,
		minNav:  opts.MinNavInterval,
		maxHist: maxHist,
		now:     time.Now,
	}
}

// engineRender adapts an *engine.Engine to a RenderFunc.
func engineRender(eng *engine.Engine) RenderFunc {
	return func(ctx context.Context, url string, w, h int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error) {
		return eng.RenderWithLinks(ctx, url, image.Rect(0, 0, w, h))
	}
}

// GuardedClient returns a copy of base whose transport dials through the SSRF
// guard (CheckAddr as the dialer Control). It keeps the browser User-Agent set
// by the engine but replaces the transport so that navigation, subresources,
// redirects and DNS-rebinding all pass the dial-time IP check. The Chrome-TLS
// fingerprint of browserhttp is traded for airtight SSRF containment, which
// this package treats as mandatory for any content it fetches on a caller's
// behalf.
//
// Exported so a host that drives an *engine.LiveDocument directly — instead
// of going through Session's remote-browsing/history/rate-limiting model —
// still gets this package's dial-time protection: `eng := engine.New();
// eng.Client = browserproxy.GuardedClient(eng.Client)`. NewSession uses
// exactly this to build its own engine.
func GuardedClient(base *http.Client) *http.Client {
	c := &http.Client{
		Transport: &http.Transport{
			DialContext:         guardedDialer().DialContext,
			MaxIdleConns:        20,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
			ForceAttemptHTTP2:   true,
		},
	}
	if base != nil {
		c.Timeout = base.Timeout
		c.Jar = base.Jar
	}
	return c
}

// doRender performs the guarded, rate-limited, concurrency-capped render and,
// on success, replaces the current page. It assumes s.mu is held. It does not
// touch history or scroll — callers decide those.
func (s *Session) doRender(ctx context.Context, url string) error {
	if s.minNav > 0 && !s.lastNav.IsZero() && s.now().Sub(s.lastNav) < s.minNav {
		return ErrRateLimited
	}
	if err := CheckURL(url); err != nil {
		return err
	}
	if s.sem != nil {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	img, info, links, err := s.render(ctx, url, s.w, s.h)
	if err != nil {
		return err
	}
	s.full, s.links = img, links
	s.url, s.title = info.URL, info.Title
	s.lastNav = s.now()
	return nil
}

// Navigate loads url as a new history entry: the current page (if any) is
// pushed onto the back stack and the forward stack is cleared.
func (s *Session) Navigate(ctx context.Context, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.url
	if err := s.doRender(ctx, url); err != nil {
		return err
	}
	if prev != "" && prev != s.url {
		s.back = pushCapped(s.back, prev, s.maxHist)
	}
	s.forward = nil
	s.scrollY = 0
	return nil
}

// Back re-loads the most recent back entry, moving the current page forward.
func (s *Session) Back(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.back) == 0 {
		return ErrNoHistory
	}
	target := s.back[len(s.back)-1]
	cur := s.url
	if err := s.doRender(ctx, target); err != nil {
		return err
	}
	s.back = s.back[:len(s.back)-1]
	// A non-empty back stack implies a page is loaded, so cur is non-empty.
	s.forward = pushCapped(s.forward, cur, s.maxHist)
	s.scrollY = 0
	return nil
}

// Forward re-loads the next forward entry, moving the current page back.
func (s *Session) Forward(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.forward) == 0 {
		return ErrNoHistory
	}
	target := s.forward[len(s.forward)-1]
	cur := s.url
	if err := s.doRender(ctx, target); err != nil {
		return err
	}
	s.forward = s.forward[:len(s.forward)-1]
	// A non-empty forward stack implies a page is loaded, so cur is non-empty.
	s.back = pushCapped(s.back, cur, s.maxHist)
	s.scrollY = 0
	return nil
}

// Click resolves a content-area click (viewport pixel coords) against the
// hit-map at the current scroll and, if it lands inside a link, navigates to
// it. It reports whether a navigation happened. A miss (or a click before any
// page loads) is a no-op returning (false, nil).
func (s *Session) Click(ctx context.Context, x, y int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full == nil {
		return false, nil
	}
	href, ok := engine.LinkAt(s.links, image.Pt(x, s.scrollY+y))
	if !ok {
		return false, nil
	}
	prev := s.url
	if err := s.doRender(ctx, href); err != nil {
		return false, err
	}
	if prev != "" && prev != s.url {
		s.back = pushCapped(s.back, prev, s.maxHist)
	}
	s.forward = nil
	s.scrollY = 0
	return true, nil
}

// Scroll adjusts the vertical scroll by dy pixels (positive = down), clamped to
// the page, without re-rendering. It reports the new offset.
func (s *Session) Scroll(dy int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scrollY = clamp(s.scrollY+dy, 0, s.maxScroll())
	return s.scrollY
}

// Resize sets a new viewport size and re-renders the current page at the new
// width (a width change changes layout). If no page is loaded it just records
// the size. The scroll offset is preserved and re-clamped.
func (s *Session) Resize(ctx context.Context, w, h int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w > 0 {
		s.w = w
	}
	if h > 0 {
		s.h = h
	}
	if s.url == "" {
		return nil
	}
	if err := s.doRender(ctx, s.url); err != nil {
		return err
	}
	s.scrollY = clamp(s.scrollY, 0, s.maxScroll())
	return nil
}

// FrameSlice returns the current viewport as a fresh w×h PNG taken at the
// current scroll offset, plus the slice size and offset. Before any page loads
// (or where the page is shorter than the viewport) the uncovered area is white,
// so the client canvas is always fully painted.
func (s *Session) FrameSlice() (png []byte, w, h, offsetY int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slice := sliceViewport(s.full, s.scrollY, s.w, s.h)
	data, err := encodePNG(slice)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return data, s.w, s.h, s.scrollY, nil
}

// encodePNG is a package var so the (otherwise untriggerable) encode-failure
// branch of FrameSlice can be exercised by tests. Production uses the engine's
// PNG encoder.
var encodePNG = engine.EncodePNG

// Viewport returns the current viewport (content-area) size in pixels.
func (s *Session) Viewport() (w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// StateMsg returns the chrome model for the current page.
func (s *Session) StateMsg() *browserpb.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &browserpb.State{
		Url:        s.url,
		Title:      s.title,
		Loading:    false,
		CanBack:    len(s.back) > 0,
		CanForward: len(s.forward) > 0,
	}
}

// maxScroll is the largest valid scroll offset (assumes s.mu held).
func (s *Session) maxScroll() int {
	if s.full == nil {
		return 0
	}
	if m := s.full.Bounds().Dy() - s.h; m > 0 {
		return m
	}
	return 0
}

// sliceViewport copies the vpW×vpH viewport at offsetY out of full into a fresh
// opaque-white RGBA. A nil full yields an all-white frame.
func sliceViewport(full *image.RGBA, offsetY, vpW, vpH int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, vpW, vpH))
	fillWhiteRGBA(out)
	if full == nil {
		return out
	}
	src := image.Rect(0, offsetY, vpW, offsetY+vpH).Intersect(full.Bounds())
	if src.Empty() {
		return out
	}
	draw.Draw(out, image.Rect(0, 0, src.Dx(), src.Dy()), full, image.Pt(src.Min.X, src.Min.Y), draw.Src)
	return out
}

func fillWhiteRGBA(img *image.RGBA) {
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 255, 255, 255, 255
	}
}

// pushCapped appends v and drops the oldest entries so the stack never exceeds
// max items.
func pushCapped(stack []string, v string, max int) []string {
	stack = append(stack, v)
	if len(stack) > max {
		stack = stack[len(stack)-max:]
	}
	return stack
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
