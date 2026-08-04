// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestCheckURL_Blocks(t *testing.T) {
	blocked := []string{
		"ftp://example.com/x",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html,<h1>x</h1>",
		"gopher://example.com",
		"://bad",                                  // unparseable
		"http://",                                 // empty host
		"http://localhost/",                       // localhost name
		"http://foo.internal/",                    // internal suffix
		"http://svc.local",                        // local suffix
		"http://box.lan",                          // lan suffix
		"http://api.home.arpa",                    // home.arpa suffix
		"http://x.localhost/",                     // localhost suffix
		"http://169.254.169.254/latest/meta-data", // cloud metadata (link-local)
		"http://127.0.0.1:8090",                   // loopback
		"http://10.0.0.5",                         // private
		"http://172.16.4.4",                       // private
		"http://192.168.1.1",                      // private
		"http://100.64.0.1",                       // CGNAT
		"http://[::1]/",                           // IPv6 loopback
		"http://0.0.0.0/",                         // unspecified
	}
	for _, u := range blocked {
		err := CheckURL(u)
		if err == nil {
			t.Errorf("CheckURL(%q) = nil, want blocked", u)
			continue
		}
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckURL(%q) error not ErrBlocked: %v", u, err)
		}
	}
}

func TestCheckURL_Allows(t *testing.T) {
	allowed := []string{
		"http://example.com",
		"https://example.com/path?q=1#frag",
		"http://8.8.8.8/", // public literal IP
		"https://sub.example.co.uk/x",
		"http://xn--r8jz45g.jp/", // punycode public host
	}
	for _, u := range allowed {
		if err := CheckURL(u); err != nil {
			t.Errorf("CheckURL(%q) = %v, want allowed", u, err)
		}
	}
}

func TestCheckAddr(t *testing.T) {
	cases := []struct {
		network, address string
		wantBlocked      bool
	}{
		{"tcp", "8.8.8.8:443", false},
		{"tcp4", "1.1.1.1:80", false},
		{"udp", "8.8.8.8:53", true},         // wrong network
		{"tcp", "10.0.0.1:80", true},        // private
		{"tcp", "127.0.0.1:80", true},       // loopback
		{"tcp", "169.254.169.254:80", true}, // metadata
		{"tcp", "[::1]:80", true},           // IPv6 loopback
		{"tcp", "224.0.0.1:80", true},       // link-local multicast
		{"tcp", "239.1.2.3:80", true},       // multicast
		{"tcp", "0.0.0.0:80", true},         // unspecified
		{"tcp", "100.64.0.1:80", true},      // CGNAT
		{"tcp", "not-an-ip", true},          // unresolved
		{"tcp", "::ffff:10.0.0.1", true},    // IPv4-mapped private (no port)
	}
	for _, c := range cases {
		err := CheckAddr(c.network, c.address)
		if c.wantBlocked && err == nil {
			t.Errorf("CheckAddr(%q,%q) = nil, want blocked", c.network, c.address)
		}
		if !c.wantBlocked && err != nil {
			t.Errorf("CheckAddr(%q,%q) = %v, want allowed", c.network, c.address, err)
		}
		if c.wantBlocked && err != nil && !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckAddr(%q,%q) error not ErrBlocked: %v", c.network, c.address, err)
		}
	}
}

func TestGuardedDialerControl(t *testing.T) {
	d := guardedDialer()
	if d.Control == nil {
		t.Fatal("guarded dialer has no Control hook")
	}
	// The Control hook must reject a private address and accept a public one.
	var rc syscall.RawConn
	if err := d.Control("tcp", "10.0.0.1:80", rc); err == nil {
		t.Error("Control allowed a private address")
	}
	if err := d.Control("tcp", "8.8.8.8:80", rc); err != nil {
		t.Errorf("Control blocked a public address: %v", err)
	}
}

// TestGuardedDialBlocksLoopback proves the dialer actually refuses to connect
// to a real loopback listener (end-to-end, not just the Control predicate).
func TestGuardedDialBlocksLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	_, err = guardedDialer().Dial("tcp", ln.Addr().String())
	if err == nil {
		t.Fatal("guarded dial connected to loopback; SSRF guard failed")
	}
}
