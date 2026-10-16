package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"example.com/app/internal/platform/httpx"
)

type principalKey struct{}

// Principal is the authenticated caller, taken from a verified access token.
type Principal struct {
	UserID  uuid.UUID
	Role    string
	TokenID string
}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored by RequireAuth.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// AccessTokenParser verifies an access token. *TokenManager implements it.
type AccessTokenParser interface {
	ParseAccess(token string) (Principal, error)
}

// RequireAuth rejects requests without a valid "Authorization: Bearer" access
// token and stores the principal in the context for the rest of the chain.
// Tokens in query strings are never read.
func RequireAuth(parser AccessTokenParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w, r)
				return
			}
			principal, err := parser.ParseAccess(token)
			if err != nil {
				unauthorized(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.WriteProblem(w, r, http.StatusUnauthorized, "authentication required")
}

// HumaRequireAuth returns a Huma operation middleware that validates the bearer token
// and stores the Principal in the request context.
func HumaRequireAuth(api huma.API, parser AccessTokenParser) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		scheme, token, found := strings.Cut(ctx.Header("Authorization"), " ")
		token = strings.TrimSpace(token)
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			ctx.SetHeader("WWW-Authenticate", "Bearer")
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
			return
		}
		principal, err := parser.ParseAccess(token)
		if err != nil {
			ctx.SetHeader("WWW-Authenticate", "Bearer")
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication required")
			return
		}
		next(huma.WithContext(ctx, WithPrincipal(ctx.Context(), principal)))
	}
}
