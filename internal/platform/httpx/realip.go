package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

// TrustedProxies is the set of proxy networks allowed to supply forwarded
// client addresses. The zero value trusts nobody.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies parses CIDR strings such as "10.0.0.0/8".
func ParseTrustedProxies(cidrs []string) (TrustedProxies, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return TrustedProxies{}, fmt.Errorf("httpx: trusted proxy %q: %w", cidr, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return TrustedProxies{prefixes: prefixes}, nil
}

func (t TrustedProxies) contains(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	for _, prefix := range t.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the client address for r. Forwarded headers are honored
// only when the direct peer is a trusted proxy; otherwise the socket address
// is used. CF-Connecting-IP wins over X-Forwarded-For.
func (t TrustedProxies) ClientIP(r *http.Request) string {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !t.contains(peer) {
		return peer.String()
	}
	if ip, ok := parseAddr(r.Header.Get("CF-Connecting-IP")); ok {
		return ip.String()
	}
	if ip, ok := t.fromForwardedFor(r.Header.Values("X-Forwarded-For")); ok {
		return ip.String()
	}
	return peer.String()
}

// fromForwardedFor walks the chain from the right and returns the first
// address that is not a trusted proxy. A malformed entry stops the walk, so a
// client cannot smuggle a value past the trusted hops.
func (t TrustedProxies) fromForwardedFor(values []string) (netip.Addr, bool) {
	parts := strings.Split(strings.Join(values, ","), ",")
	var leftmost netip.Addr
	found := false
	for i := len(parts) - 1; i >= 0; i-- {
		ip, ok := parseAddr(parts[i])
		if !ok {
			return netip.Addr{}, false
		}
		if !t.contains(ip) {
			return ip, true
		}
		leftmost = ip
		found = true
	}
	return leftmost, found
}

// parseAddr accepts "ip", "ip:port", and "[ip]:port".
func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}

// RealIP stores the resolved client address in the request context.
func RealIP(proxies TrustedProxies) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey{}, proxies.ClientIP(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIPFrom returns the client address stored by RealIP, or "".
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}
