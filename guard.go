// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package browserproxy renders web pages server-side with the pure-Go
// go-webengine/engine and streams frames (plus a hyperlink hit-map) to a
// client over a simple JSON WebSocket protocol, forwarding the client's input
// back as navigation. This file is the SSRF guard: the security boundary that
// keeps a proxied page from reaching the host's own network.
package browserproxy

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// guardedDialer returns a net.Dialer whose Control hook runs CheckAddr on the
// resolved address immediately before the socket connects, so every dial —
// navigation, subresource, redirect — is IP-checked post-DNS.
func guardedDialer() *net.Dialer {
	return &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			return CheckAddr(network, address)
		},
	}
}

// ErrBlocked is the sentinel wrapped by every guard rejection, so callers can
// test errors.Is(err, ErrBlocked) without matching on message text.
var ErrBlocked = fmt.Errorf("browserproxy: request blocked by SSRF guard")

// blockedSuffixes are host suffixes that name internal/service-discovery
// namespaces a proxied page must never reach.
var blockedSuffixes = []string{".internal", ".local", ".localhost", ".lan", ".home.arpa"}

// CheckURL is the static (pre-DNS) half of the SSRF guard. It rejects any URL
// that is not a plain http(s) request to a public host: a non-http(s) scheme
// (file:, data:, gopher:, ftp:, javascript:, …), a missing host, an
// internal-namespace host suffix, or a host given as a literal
// private/loopback/link-local IP. DNS names that *resolve* to a blocked
// address are caught later by CheckAddr at dial time (so DNS-rebinding cannot
// slip past this static check).
func CheckURL(rawurl string) error {
	u, err := url.Parse(strings.TrimSpace(rawurl))
	if err != nil {
		return fmt.Errorf("%w: unparseable URL %q", ErrBlocked, rawurl)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: scheme %q is not http(s)", ErrBlocked, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrBlocked)
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return fmt.Errorf("%w: host %q", ErrBlocked, host)
	}
	for _, suf := range blockedSuffixes {
		if strings.HasSuffix(lower, suf) {
			return fmt.Errorf("%w: host suffix of %q is internal", ErrBlocked, host)
		}
	}
	// A host given as a literal IP is checked immediately; a DNS name is left
	// for CheckAddr once resolved.
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// CheckAddr is the dynamic (post-DNS) half of the guard, wired as a
// net.Dialer.Control so it runs after resolution and immediately before the
// socket connects — covering the top navigation, every subresource, every
// redirect target and DNS-rebinding. address is "host:port" with host already
// an IP literal (Control always receives a resolved address).
func CheckAddr(network, address string) error {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return fmt.Errorf("%w: network %q not allowed", ErrBlocked, network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: unresolved dial address %q", ErrBlocked, address)
	}
	return checkIP(ip)
}

// checkIP rejects any non-global-unicast address a proxied page must never be
// able to reach: loopback, RFC1918 private, link-local (incl. the
// 169.254.169.254 cloud metadata endpoint), unique-local IPv6 (fc00::/7),
// carrier-grade NAT (100.64.0.0/10), the unspecified address, and multicast.
// IPv4-mapped IPv6 addresses are unmapped and re-checked as IPv4.
func checkIP(ip net.IP) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("%w: loopback address %s", ErrBlocked, ip)
	case ip.IsUnspecified():
		return fmt.Errorf("%w: unspecified address %s", ErrBlocked, ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf("%w: link-local address %s", ErrBlocked, ip)
	case ip.IsPrivate():
		return fmt.Errorf("%w: private address %s", ErrBlocked, ip)
	case ip.IsMulticast():
		return fmt.Errorf("%w: multicast address %s", ErrBlocked, ip)
	case isCGNAT(ip):
		return fmt.Errorf("%w: carrier-grade NAT address %s", ErrBlocked, ip)
	}
	return nil
}

// isCGNAT reports whether ip is in the 100.64.0.0/10 shared address space
// (RFC 6598), which net.IP.IsPrivate does not cover.
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}
