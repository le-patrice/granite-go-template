package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"10.0.0.0/8", "fd00::/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "untrusted peer ignores forwarded headers",
			remoteAddr: "198.51.100.7:4000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9", "CF-Connecting-IP": "203.0.113.10"},
			want:       "198.51.100.7",
		},
		{
			name:       "trusted peer uses cloudflare header",
			remoteAddr: "10.0.0.2:4000",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.10"},
			want:       "203.0.113.10",
		},
		{
			name:       "trusted peer uses rightmost untrusted forwarded entry",
			remoteAddr: "10.0.0.2:4000",
			headers:    map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.9, 10.0.0.3"},
			want:       "203.0.113.9",
		},
		{
			name:       "all forwarded entries trusted uses leftmost",
			remoteAddr: "10.0.0.2:4000",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.9, 10.0.0.3"},
			want:       "10.0.0.9",
		},
		{
			name:       "malformed forwarded header falls back to peer",
			remoteAddr: "10.0.0.2:4000",
			headers:    map[string]string{"X-Forwarded-For": "not-an-ip"},
			want:       "10.0.0.2",
		},
		{
			name:       "trusted peer without headers",
			remoteAddr: "10.0.0.2:4000",
			want:       "10.0.0.2",
		},
		{
			name:       "ipv6 trusted peer",
			remoteAddr: "[fd00::1]:4000",
			headers:    map[string]string{"X-Forwarded-For": "2001:db8::5"},
			want:       "2001:db8::5",
		},
		{
			name:       "ipv4-mapped trusted peer",
			remoteAddr: "[::ffff:10.0.0.2]:4000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "remote address without port",
			remoteAddr: "198.51.100.7",
			want:       "198.51.100.7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := proxies.ClientIP(r); got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPTrustsNobodyByDefault(t *testing.T) {
	var none TrustedProxies
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:4000"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := none.ClientIP(r); got != "10.0.0.2" {
		t.Errorf("ClientIP = %q, want the socket address", got)
	}
}

func TestParseTrustedProxiesRejectsBadCIDR(t *testing.T) {
	if _, err := ParseTrustedProxies([]string{"10.0.0.0/8", "nope"}); err == nil {
		t.Fatal("expected an error for an invalid CIDR")
	}
}

func TestRealIPMiddlewareStoresClientIP(t *testing.T) {
	proxies, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	var got string
	h := RealIP(proxies)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClientIPFrom(r.Context())
	}))

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:4000"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.9" {
		t.Errorf("ClientIPFrom = %q, want 203.0.113.9", got)
	}
}
