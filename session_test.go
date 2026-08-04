// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"errors"
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/go-webengine/engine"
)

// stubRender returns a RenderFunc that yields a fixed-height page whose title is
// the requested URL and which carries the given links. It records each URL it
// was asked to render.
func stubRender(height int, links []engine.Link, calls *[]string) RenderFunc {
	return func(_ context.Context, url string, w, _ int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error) {
		if calls != nil {
			*calls = append(*calls, url)
		}
		img := image.NewRGBA(image.Rect(0, 0, w, height))
		return img, &engine.RenderInfo{URL: url, Title: "title:" + url}, links, nil
	}
}

func newTestSession(r RenderFunc, opts Options) *Session {
	return newSession(1024, 768, r, opts)
}

func TestSession_NavigateAndState(t *testing.T) {
	s := newTestSession(stubRender(2000, nil, nil), Options{})
	if err := s.Navigate(context.Background(), "http://a.example/"); err != nil {
		t.Fatal(err)
	}
	st := s.StateMsg()
	if st.URL != "http://a.example/" || st.Title != "title:http://a.example/" {
		t.Errorf("state = %+v", st)
	}
	if st.CanBack || st.CanForward {
		t.Error("fresh nav should have no history")
	}
	// A frame is produced at the viewport size.
	png, w, h, off, err := s.FrameSlice()
	if err != nil || len(png) == 0 || w != 1024 || h != 768 || off != 0 {
		t.Fatalf("frame: len=%d w=%d h=%d off=%d err=%v", len(png), w, h, off, err)
	}
}

func TestSession_BackForward(t *testing.T) {
	var calls []string
	s := newTestSession(stubRender(1000, nil, &calls), Options{})
	ctx := context.Background()
	mustNav := func(u string) {
		if err := s.Navigate(ctx, u); err != nil {
			t.Fatalf("nav %s: %v", u, err)
		}
	}
	mustNav("http://one.example/")
	mustNav("http://two.example/")
	if st := s.StateMsg(); !st.CanBack || st.CanForward {
		t.Errorf("after 2 navs: %+v", st)
	}
	if err := s.Back(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.StateMsg(); st.URL != "http://one.example/" || !st.CanForward || st.CanBack {
		t.Errorf("after back: %+v", st)
	}
	if err := s.Forward(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.StateMsg(); st.URL != "http://two.example/" || !st.CanBack || st.CanForward {
		t.Errorf("after forward: %+v", st)
	}
	// Back/Forward with an empty stack report ErrNoHistory.
	fresh := newTestSession(stubRender(10, nil, nil), Options{})
	if err := fresh.Back(ctx); !errors.Is(err, ErrNoHistory) {
		t.Errorf("Back empty = %v", err)
	}
	if err := fresh.Forward(ctx); !errors.Is(err, ErrNoHistory) {
		t.Errorf("Forward empty = %v", err)
	}
}

func TestSession_NavigateClearsForward(t *testing.T) {
	s := newTestSession(stubRender(10, nil, nil), Options{})
	ctx := context.Background()
	_ = s.Navigate(ctx, "http://one.example/")
	_ = s.Navigate(ctx, "http://two.example/")
	_ = s.Back(ctx) // forward now has two
	if !s.StateMsg().CanForward {
		t.Fatal("expected forward available")
	}
	_ = s.Navigate(ctx, "http://three.example/") // must clear forward
	if s.StateMsg().CanForward {
		t.Error("navigate did not clear forward stack")
	}
}

func TestSession_Click(t *testing.T) {
	link := engine.Link{Rect: image.Rect(10, 1500, 200, 1520), Href: "http://dst.example/"}
	s := newTestSession(stubRender(3000, []engine.Link{link}, nil), Options{})
	ctx := context.Background()

	// Click before any page loads is a no-op.
	if nav, err := s.Click(ctx, 5, 5); nav || err != nil {
		t.Fatalf("click pre-load: nav=%v err=%v", nav, err)
	}
	if err := s.Navigate(ctx, "http://src.example/"); err != nil {
		t.Fatal(err)
	}
	// Miss (no link at 0,0 in viewport → full-page (0,0)).
	if nav, _ := s.Click(ctx, 0, 0); nav {
		t.Error("click miss reported navigation")
	}
	// Scroll so the link at full-page y=1500 is under viewport y=5.
	s.Scroll(1500)
	if nav, err := s.Click(ctx, 15, 5); err != nil || !nav {
		t.Fatalf("click hit: nav=%v err=%v", nav, err)
	}
	if st := s.StateMsg(); st.URL != "http://dst.example/" || !st.CanBack {
		t.Errorf("after click-nav: %+v", st)
	}
}

func TestSession_Scroll(t *testing.T) {
	s := newTestSession(stubRender(2000, nil, nil), Options{})
	// Scroll before load stays at 0.
	if got := s.Scroll(500); got != 0 {
		t.Errorf("scroll pre-load = %d, want 0", got)
	}
	_ = s.Navigate(context.Background(), "http://a/")
	// max scroll = 2000-768 = 1232.
	if got := s.Scroll(5000); got != 1232 {
		t.Errorf("scroll clamp high = %d, want 1232", got)
	}
	if got := s.Scroll(-9999); got != 0 {
		t.Errorf("scroll clamp low = %d, want 0", got)
	}
	// The frame offset tracks scroll.
	s.Scroll(300)
	if _, _, _, off, _ := s.FrameSlice(); off != 300 {
		t.Errorf("frame offset = %d, want 300", off)
	}
}

func TestSession_Resize(t *testing.T) {
	var calls []string
	s := newTestSession(stubRender(2000, nil, &calls), Options{})
	ctx := context.Background()
	// Resize before any page just records the size (no render).
	if err := s.Resize(ctx, 800, 600); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Errorf("resize pre-load rendered: %v", calls)
	}
	if w, h := s.Viewport(); w != 800 || h != 600 {
		t.Errorf("viewport = %d×%d", w, h)
	}
	_ = s.Navigate(ctx, "http://a/")
	before := len(calls)
	if err := s.Resize(ctx, 1200, 900); err != nil {
		t.Fatal(err)
	}
	if len(calls) != before+1 { // resize re-renders the current URL
		t.Errorf("resize did not re-render: calls=%v", calls)
	}
	if w, h := s.Viewport(); w != 1200 || h != 900 {
		t.Errorf("viewport after resize = %d×%d", w, h)
	}
}

func TestSession_RateLimit(t *testing.T) {
	s := newTestSession(stubRender(10, nil, nil), Options{MinNavInterval: time.Second})
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	ctx := context.Background()
	if err := s.Navigate(ctx, "http://a/"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(300 * time.Millisecond) // still within the interval
	if err := s.Navigate(ctx, "http://b/"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	now = now.Add(2 * time.Second) // past the interval
	if err := s.Navigate(ctx, "http://c/"); err != nil {
		t.Fatalf("want success after interval, got %v", err)
	}
}

func TestSession_GuardBlocksNavigate(t *testing.T) {
	s := newTestSession(stubRender(10, nil, nil), Options{})
	if err := s.Navigate(context.Background(), "http://127.0.0.1/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
}

func TestSession_ConcurrencyLimiter(t *testing.T) {
	lim := make(chan struct{}, 1)
	s := newTestSession(stubRender(10, nil, nil), Options{GlobalLimiter: lim})
	// A successful render acquires and releases the token.
	if err := s.Navigate(context.Background(), "http://a/"); err != nil {
		t.Fatal(err)
	}
	// Fill the limiter and use a cancelled context: doRender must give up on ctx.
	lim <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Navigate(ctx, "http://b/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestSession_RenderError(t *testing.T) {
	boom := errors.New("boom")
	r := func(_ context.Context, _ string, _, _ int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error) {
		return nil, nil, nil, boom
	}
	s := newTestSession(r, Options{})
	if err := s.Navigate(context.Background(), "http://a/"); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
}

func TestSession_HistoryCap(t *testing.T) {
	s := newTestSession(stubRender(10, nil, nil), Options{MaxHistory: 2})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.Navigate(ctx, fmt.Sprintf("http://h%d.example/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.back) != 2 {
		t.Errorf("back stack len = %d, want capped at 2", len(s.back))
	}
}

func TestSession_FrameEncodeError(t *testing.T) {
	orig := encodePNG
	defer func() { encodePNG = orig }()
	encodePNG = func(image.Image) ([]byte, error) { return nil, errors.New("enc fail") }
	s := newTestSession(stubRender(10, nil, nil), Options{})
	_ = s.Navigate(context.Background(), "http://a/")
	if _, _, _, _, err := s.FrameSlice(); err == nil {
		t.Error("want encode error from FrameSlice")
	}
}

func TestSliceViewport_Edges(t *testing.T) {
	// Nil page → all-white frame of the requested size.
	out := sliceViewport(nil, 0, 4, 3)
	if out.Bounds().Dx() != 4 || out.Bounds().Dy() != 3 {
		t.Fatalf("nil slice size = %v", out.Bounds())
	}
	if out.Pix[0] != 255 {
		t.Error("nil slice not white")
	}
	// Offset past the page height → empty intersection → white frame.
	full := image.NewRGBA(image.Rect(0, 0, 4, 10))
	out = sliceViewport(full, 99999, 4, 3)
	if out.Pix[0] != 255 {
		t.Error("out-of-range slice not white")
	}
}

func TestNewSession_Defaults(t *testing.T) {
	s := newSession(0, 0, stubRender(10, nil, nil), Options{})
	if w, h := s.Viewport(); w != 1024 || h != 768 {
		t.Errorf("default viewport = %d×%d", w, h)
	}
	if s.maxHist != defaultMaxHistory {
		t.Errorf("default maxHist = %d", s.maxHist)
	}
	// NewSession wires a real guarded engine without panicking.
	if got := NewSession(-1, -1, Options{}); got == nil {
		t.Fatal("NewSession returned nil")
	}
}

func TestSession_ScrollShortPage(t *testing.T) {
	// A page shorter than the viewport has maxScroll 0 (the m>0 false path).
	s := newTestSession(stubRender(10, nil, nil), Options{})
	_ = s.Navigate(context.Background(), "http://a/")
	if got := s.Scroll(100); got != 0 {
		t.Errorf("short-page scroll = %d, want 0", got)
	}
}

func TestSession_ResizeKeepsDim(t *testing.T) {
	s := newTestSession(stubRender(2000, nil, nil), Options{})
	ctx := context.Background()
	_ = s.Navigate(ctx, "http://a/")
	// Non-positive dimensions are ignored (keep the current value).
	if err := s.Resize(ctx, 0, 500); err != nil {
		t.Fatal(err)
	}
	if w, h := s.Viewport(); w != 1024 || h != 500 {
		t.Errorf("after Resize(0,500) = %d×%d, want 1024×500", w, h)
	}
	if err := s.Resize(ctx, 700, 0); err != nil {
		t.Fatal(err)
	}
	if w, h := s.Viewport(); w != 700 || h != 500 {
		t.Errorf("after Resize(700,0) = %d×%d, want 700×500", w, h)
	}
}

func TestSession_BackRenderError(t *testing.T) {
	var n int
	r := func(_ context.Context, url string, w, h int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error) {
		n++
		if n >= 3 { // the Back re-render fails
			return nil, nil, nil, errors.New("boom")
		}
		return image.NewRGBA(image.Rect(0, 0, w, h)), &engine.RenderInfo{URL: url}, nil, nil
	}
	s := newTestSession(r, Options{})
	ctx := context.Background()
	_ = s.Navigate(ctx, "http://one/")
	_ = s.Navigate(ctx, "http://two/")
	if err := s.Back(ctx); err == nil {
		t.Fatal("want Back render error")
	}
	if s.StateMsg().URL != "http://two/" { // failed Back left state unchanged
		t.Errorf("Back error mutated state: %+v", s.StateMsg())
	}
}

func TestSession_ForwardRenderError(t *testing.T) {
	var n int
	r := func(_ context.Context, url string, w, h int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error) {
		n++
		if n >= 4 { // the Forward re-render fails
			return nil, nil, nil, errors.New("boom")
		}
		return image.NewRGBA(image.Rect(0, 0, w, h)), &engine.RenderInfo{URL: url}, nil, nil
	}
	s := newTestSession(r, Options{})
	ctx := context.Background()
	_ = s.Navigate(ctx, "http://one/") // 1
	_ = s.Navigate(ctx, "http://two/") // 2
	_ = s.Back(ctx)                    // 3 → forward=[two]
	if err := s.Forward(ctx); err == nil {
		t.Fatal("want Forward render error")
	}
}

func TestEngineRender_ClosureOffline(t *testing.T) {
	// engineRender's closure runs the engine; an unparseable URL fails during
	// request construction, exercising the adapter without the network.
	r := engineRender(engine.New())
	if _, _, _, err := r(context.Background(), "://bad", 320, 240); err == nil {
		t.Error("want error from engine render of a bad URL")
	}
}

func TestGuardedClient_CopiesJarAndTimeout(t *testing.T) {
	eng := engine.New()
	c := guardedClient(eng.Client)
	if c.Jar == nil {
		t.Error("guarded client dropped the cookie jar")
	}
	if c.Timeout != eng.Client.Timeout {
		t.Errorf("timeout = %v, want %v", c.Timeout, eng.Client.Timeout)
	}
	// A nil base must not panic and yields a usable client.
	if guardedClient(nil) == nil {
		t.Error("guardedClient(nil) returned nil")
	}
}
