package webfetch

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

var (
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
	ula   = netip.MustParsePrefix("fc00::/7")
)

func blocked(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	ip := addr.Unmap()
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() {
		return true
	}
	return cgnat.Contains(ip) || ula.Contains(ip)
}

func blockedHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return blocked(ip)
	}
	return false
}

func resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("webfetch: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("webfetch: resolve %q: no addresses", host)
	}
	return ips, nil
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("webfetch: dial %q: %w", addr, err)
	}
	ips, err := resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if blocked(ip) {
			return nil, fmt.Errorf("webfetch: refusing %q: resolves to non-public address %s", host, ip)
		}
	}
	d := &net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		conn, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	return nil, fmt.Errorf("webfetch: dial %q: %w", host, lastErr)
}
