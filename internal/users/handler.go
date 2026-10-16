package users

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
)

// Middleware wraps a handler.
type Middleware = func(http.Handler) http.Handler

// Handler is the HTTP adapter for the identity use-cases.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns a Handler. logger receives unexpected failures.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the identity endpoints on api. authLimit is the stricter rate
// limit for /v1/auth/*; requireAuth guards /v1/me.
func (h *Handler) Routes(api httpx.API, authLimit, requireAuth Middleware) {
	httpx.Register(api, httpx.Operation{
		OperationID:   "authRegister",
		Method:        http.MethodPost,
		Path:          "/v1/auth/register",
		Summary:       "Register a new user account",
		Tags:          []string{"Auth"},
		DefaultStatus: http.StatusCreated,
		Middlewares:   httpx.Middlewares{httpx.AdaptMiddleware(authLimit)},
	}, h.register)

	httpx.Register(api, httpx.Operation{
		OperationID: "authLogin",
		Method:      http.MethodPost,
		Path:        "/v1/auth/login",
		Summary:     "Authenticate with email and password",
		Tags:        []string{"Auth"},
		Middlewares: httpx.Middlewares{httpx.AdaptMiddleware(authLimit)},
	}, h.login)

	httpx.Register(api, httpx.Operation{
		OperationID: "authRefresh",
		Method:      http.MethodPost,
		Path:        "/v1/auth/refresh",
		Summary:     "Rotate a refresh token for a new session",
		Tags:        []string{"Auth"},
		Middlewares: httpx.Middlewares{httpx.AdaptMiddleware(authLimit)},
	}, h.refresh)

	httpx.Register(api, httpx.Operation{
		OperationID:   "authLogout",
		Method:        http.MethodPost,
		Path:          "/v1/auth/logout",
		Summary:       "Revoke a refresh token family",
		Tags:          []string{"Auth"},
		DefaultStatus: http.StatusNoContent,
		Middlewares:   httpx.Middlewares{httpx.AdaptMiddleware(authLimit)},
	}, h.logout)

	httpx.Register(api, httpx.Operation{
		OperationID: "usersMe",
		Method:      http.MethodGet,
		Path:        "/v1/me",
		Summary:     "Get current user profile",
		Tags:        []string{"Users"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: httpx.Middlewares{httpx.AdaptMiddleware(requireAuth)},
	}, h.me)
}

type credentialsInput struct {
	Body struct {
		Email    string `json:"email" doc:"User email address" maxLength:"254"`
		Password string `json:"password" doc:"User password" maxLength:"1024"`
	}
}

type refreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" doc:"Opaque refresh token"`
	}
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type sessionResponse struct {
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type userOutput struct {
	Body userResponse
}

type sessionOutput struct {
	Body sessionResponse
}

func (h *Handler) register(ctx context.Context, req *credentialsInput) (*userOutput, error) {
	user, err := h.svc.Register(ctx, req.Body.Email, req.Body.Password)
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return &userOutput{Body: toUserResponse(user)}, nil
}

func (h *Handler) login(ctx context.Context, req *credentialsInput) (*sessionOutput, error) {
	session, err := h.svc.Login(ctx, req.Body.Email, req.Body.Password)
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return &sessionOutput{Body: toSessionResponse(session)}, nil
}

func (h *Handler) refresh(ctx context.Context, req *refreshInput) (*sessionOutput, error) {
	session, err := h.svc.Refresh(ctx, req.Body.RefreshToken)
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return &sessionOutput{Body: toSessionResponse(session)}, nil
}

func (h *Handler) logout(ctx context.Context, req *refreshInput) (*struct{}, error) {
	if err := h.svc.Logout(ctx, req.Body.RefreshToken); err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return nil, nil
}

func (h *Handler) me(ctx context.Context, _ *struct{}) (*userOutput, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, httpx.NewProblem(http.StatusUnauthorized, "authentication required")
	}
	user, err := h.svc.Me(ctx, principal.UserID)
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return &userOutput{Body: toUserResponse(user)}, nil
}

func (h *Handler) toProblem(ctx context.Context, err error) error {
	p := h.mapError(err)
	if p.Status >= http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "users request failed",
			"error", err,
			"request_id", httpx.RequestIDFrom(ctx),
		)
	}
	p.RequestID = httpx.RequestIDFrom(ctx)
	return p
}

func (h *Handler) mapError(err error) *httpx.Problem {
	switch {
	case errors.Is(err, ErrInvalidEmail), errors.Is(err, ErrWeakPassword):
		return httpx.NewProblem(http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrEmailTaken):
		return httpx.NewProblem(http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalidCredentials):
		return httpx.NewProblem(http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, ErrInvalidToken), errors.Is(err, ErrTokenReuse):
		return httpx.NewProblem(http.StatusUnauthorized, "invalid refresh token")
	case errors.Is(err, ErrNotFound):
		return httpx.NewProblem(http.StatusUnauthorized, "authentication required")
	default:
		return httpx.NewProblem(http.StatusInternalServerError, "")
	}
}

func toUserResponse(user User) userResponse {
	return userResponse{
		ID:        user.ID.String(),
		Email:     user.Email,
		Role:      string(user.Role),
		CreatedAt: user.CreatedAt,
	}
}

func toSessionResponse(s Session) sessionResponse {
	return sessionResponse{
		AccessToken:      s.AccessToken,
		TokenType:        "Bearer",
		ExpiresAt:        s.AccessExpiresAt,
		RefreshToken:     s.RefreshToken,
		RefreshExpiresAt: s.RefreshExpiresAt,
	}
}
