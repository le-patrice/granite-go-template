package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequireAuth(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)
	userID := uuid.New()
	valid, _, err := m.IssueAccess(userID, RoleMember)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	var seen Principal
	handler := RequireAuth(m)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = PrincipalFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name       string
		target     string
		header     string
		wantStatus int
	}{
		{"missing header", "/", "", http.StatusUnauthorized},
		{"wrong scheme", "/", "Basic " + valid, http.StatusUnauthorized},
		{"bearer without token", "/", "Bearer ", http.StatusUnauthorized},
		{"invalid token", "/", "Bearer v4.local.invalid", http.StatusUnauthorized},
		{"token only in query string", "/?access_token=" + valid, "", http.StatusUnauthorized},
		{"valid token", "/", "Bearer " + valid, http.StatusNoContent},
		{"scheme is case-insensitive", "/", "bearer " + valid, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen = Principal{}
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.target, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if rr.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Errorf("WWW-Authenticate = %q, want Bearer", rr.Header().Get("WWW-Authenticate"))
				}
				if seen != (Principal{}) {
					t.Error("handler ran for an unauthenticated request")
				}
				return
			}
			if seen.UserID != userID || seen.Role != RoleMember {
				t.Errorf("principal = %+v", seen)
			}
		})
	}
}

func TestRequireRole(t *testing.T) {
	m := newTestManager(t, testKey(1), testEpoch)
	adminToken, _, err := m.IssueAccess(uuid.New(), RoleAdmin)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	memberToken, _, err := m.IssueAccess(uuid.New(), RoleMember)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	adminOnly := RequireAuth(m)(RequireRole(RoleAdmin)(ok))
	anyRole := RequireAuth(m)(RequireRole(RoleAdmin, RoleMember)(ok))

	tests := []struct {
		name       string
		handler    http.Handler
		token      string
		wantStatus int
	}{
		{"admin on admin-only", adminOnly, adminToken, http.StatusNoContent},
		{"member on admin-only", adminOnly, memberToken, http.StatusForbidden},
		{"member where any role is allowed", anyRole, memberToken, http.StatusNoContent},
		{"no token on admin-only", adminOnly, "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			tt.handler.ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}

func TestRequireRoleWithoutAuthenticationIs401(t *testing.T) {
	handler := RequireRole(RoleAdmin)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when RequireAuth did not run", rr.Code)
	}
}
