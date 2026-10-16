package orders

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"example.com/app/internal/platform/auth"
	"example.com/app/internal/platform/httpx"
)

// Middleware wraps a handler.
type Middleware = func(http.Handler) http.Handler

// Handler is the HTTP adapter for the order use-cases.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns a Handler. logger receives unexpected failures.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the order endpoints. requireAuth guards all of them.
// idempotent wraps POST /v1/orders and may be nil, in which case creates are
// not idempotent.
func (h *Handler) Routes(router *httpx.Router, requireAuth, idempotent Middleware) {
	create := http.Handler(http.HandlerFunc(h.create))
	if idempotent != nil {
		create = idempotent(create)
	}
	router.Handle("POST /v1/orders", requireAuth(create))
	router.Handle("GET /v1/orders/{id}", requireAuth(http.HandlerFunc(h.get)))
	router.Handle("GET /v1/orders", requireAuth(http.HandlerFunc(h.list)))
}

type createRequest struct {
	Reference   string `json:"reference"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type orderResponse struct {
	ID          int64     `json:"id"`
	OwnerID     string    `json:"owner_id"`
	Reference   string    `json:"reference"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	CreatedAt   time.Time `json:"created_at"`
}

type listResponse struct {
	Items      []orderResponse `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorFrom(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	order, err := h.svc.Create(r.Context(), actor, NewOrder{
		Reference:   req.Reference,
		AmountMinor: req.AmountMinor,
		Currency:    req.Currency,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/orders/"+strconv.FormatInt(order.ID, 10))
	writeJSON(w, http.StatusCreated, toResponse(order))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorFrom(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "order id must be a positive integer")
		return
	}
	order, err := h.svc.Get(r.Context(), actor, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(order))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorFrom(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	limit := 0
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = n
	}
	page, err := h.svc.List(r.Context(), actor, query.Get("cursor"), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	items := make([]orderResponse, 0, len(page.Items))
	for _, order := range page.Items {
		items = append(items, toResponse(order))
	}
	writeJSON(w, http.StatusOK, listResponse{Items: items, NextCursor: page.NextCursor})
}

func actorFrom(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "authentication required")
		return Actor{}, false
	}
	return Actor{UserID: principal.UserID, Admin: principal.Role == auth.RoleAdmin}, true
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *ValidationError
	switch {
	case errors.As(err, &validation):
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, validation.Error())
	case errors.Is(err, ErrReferenceTaken):
		httpx.WriteProblem(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidCursor):
		httpx.WriteProblem(w, r, http.StatusBadRequest, err.Error())
	default:
		h.logger.ErrorContext(r.Context(), "orders request failed",
			"error", err,
			"request_id", httpx.RequestIDFrom(r.Context()),
		)
		httpx.WriteInternal(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func toResponse(order Order) orderResponse {
	return orderResponse{
		ID:          order.ID,
		OwnerID:     order.OwnerID.String(),
		Reference:   order.Reference,
		AmountMinor: order.AmountMinor,
		Currency:    order.Currency,
		CreatedAt:   order.CreatedAt,
	}
}
