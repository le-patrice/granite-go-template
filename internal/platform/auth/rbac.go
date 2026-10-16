package auth

import (
	"net/http"
	"slices"

	"example.com/app/internal/platform/httpx"
)

// Role names used by the starter. Fine-grained authorization decisions belong
// in each feature's service layer; roles here are for coarse route gates.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// HasRole reports whether p holds any of roles.
func HasRole(p Principal, roles ...string) bool {
	return slices.Contains(roles, p.Role)
}

// RequireRole allows the request only when the authenticated principal holds
// one of roles. It must run after RequireAuth: without a principal it answers
// 401, with the wrong role it answers 403.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFrom(r.Context())
			if !ok {
				unauthorized(w, r)
				return
			}
			if !HasRole(principal, roles...) {
				httpx.WriteProblem(w, r, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
