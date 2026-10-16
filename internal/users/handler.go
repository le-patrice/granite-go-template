package users

import (
	"encoding/json"
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

// Routes registers the identity endpoints. authLimit is the stricter rate
// limit for /v1/auth/*; requireAuth guards /v1/me.
func (h *Handler) Routes(router *httpx.Router, authLimit, requireAuth Middleware) {
	router.Handle("POST /v1/auth/register", authLimit(http.HandlerFunc(h.register)))
	router.Handle("POST /v1/auth/login", authLimit(http.HandlerFunc(h.login)))
	router.Handle("POST /v1/auth/refresh", authLimit(http.HandlerFunc(h.refresh)))
	router.Handle("POST /v1/auth/logout", authLimit(http.HandlerFunc(h.logout)))
	router.Handle("GET /v1/me", requireAuth(http.HandlerFunc(h.me)))
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
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

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, err := h.svc.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	session, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSessionResponse(session))
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	session, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSessionResponse(session))
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	user, err := h.svc.Me(r.Context(), principal.UserID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// writeError maps domain errors to problem responses. Credential and token
// failures share one message each, so responses never reveal account state.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidEmail), errors.Is(err, ErrWeakPassword):
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteProblem(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalidCredentials):
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, ErrInvalidToken), errors.Is(err, ErrTokenReuse):
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "invalid refresh token")
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "authentication required")
	default:
		h.logger.ErrorContext(r.Context(), "users request failed",
			"error", err,
			"request_id", httpx.RequestIDFrom(r.Context()),
		)
		httpx.WriteInternal(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
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
