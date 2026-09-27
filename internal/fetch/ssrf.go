package fetch

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// blockedIP reports whether an IP is in a range we refuse to fetch. This is the
// SSRF guard: without it, a feed URL could be used to reach internal services
// (cloud metadata endpoints, admin panels, databases on the LAN).
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Unspecified addresses (0.0.0.0, ::) route nowhere useful and are a
	// classic way to confuse a resolver.
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		// Carrier-grade NAT, commonly routed internally.
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
			return true
		// RFC1918 private space.
		case v4[0] == 10:
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
			return true
		case v4[0] == 192 && v4[1] == 168:
			return true
		// IETF protocol assignments: "this network" and TEST-NET-1.
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0:
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2:
			return true
		// Benchmarking range 198.18.0.0/15.
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19):
			return true
		}
		return false
	}
	// Unique local IPv6 (fc00::/7).
	if ip[0]&0xfe == 0xfc {
		return true
	}
	return false
}

// dialControl builds a DialContext that resolves the host and refuses to
// connect if a resolved address is blocked. Validating at dial time rather
// than only on the initial URL means redirects are checked too, because every
// hop goes through the same transport.
func dialControl(base *net.Dialer, allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if allowPrivate {
			return base.DialContext(ctx, network, addr)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no addresses for %s", host)
		}
		var lastErr error
		for _, ip := range ips {
			if blockedIP(ip.IP) {
				lastErr = fmt.Errorf("refusing to connect to %s (%s): private or reserved address",
					host, ip.IP)
				continue
			}
			conn, err := base.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// validateURL checks the scheme and rejects obviously unusable hosts.
func validateURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("nil url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url has no host")
	}
	return nil
}

// normalizeURL applies small fixes that make cache keys and comparisons stable.
func normalizeURL(u *url.URL) string {
	c := *u
	c.Fragment = ""
	c.Scheme = strings.ToLower(c.Scheme)
	c.Host = strings.ToLower(c.Host)
	if c.Path == "" {
		c.Path = "/"
	}
	return c.String()
}
