package orders

import (
	"context"
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

// Routes registers the order endpoints on api. requireAuth guards all of them.
// idempotent wraps POST /v1/orders and may be nil, in which case creates are
// not idempotent.
func (h *Handler) Routes(api httpx.API, requireAuth, idempotent Middleware) {
	createOp := httpx.Operation{
		OperationID:   "createOrder",
		Method:        http.MethodPost,
		Path:          "/v1/orders",
		Summary:       "Create a new order",
		Tags:          []string{"Orders"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}
	if idempotent != nil {
		createOp.Middlewares = httpx.Middlewares{
			httpx.AdaptMiddleware(requireAuth),
			httpx.AdaptMiddleware(idempotent),
		}
	} else {
		createOp.Middlewares = httpx.Middlewares{
			httpx.AdaptMiddleware(requireAuth),
		}
	}
	httpx.Register(api, createOp, h.create)

	httpx.Register(api, httpx.Operation{
		OperationID: "getOrder",
		Method:      http.MethodGet,
		Path:        "/v1/orders/{id}",
		Summary:     "Get order by ID",
		Tags:        []string{"Orders"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: httpx.Middlewares{httpx.AdaptMiddleware(requireAuth)},
	}, h.get)

	httpx.Register(api, httpx.Operation{
		OperationID: "listOrders",
		Method:      http.MethodGet,
		Path:        "/v1/orders",
		Summary:     "List orders with keyset pagination",
		Tags:        []string{"Orders"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: httpx.Middlewares{httpx.AdaptMiddleware(requireAuth)},
	}, h.list)
}

type createOrderInput struct {
	IdempotencyKey string `header:"Idempotency-Key" doc:"Optional idempotency key" maxLength:"255"`
	Body           struct {
		Reference   string `json:"reference" doc:"Client reference" maxLength:"64"`
		AmountMinor int64  `json:"amount_minor" doc:"Amount in minor currency units" minimum:"1"`
		Currency    string `json:"currency" doc:"3-letter ISO currency code" minLength:"3" maxLength:"3"`
	}
}

type getOrderInput struct {
	ID int64 `path:"id" doc:"Order ID" minimum:"1"`
}

type listOrdersInput struct {
	Cursor string `query:"cursor" doc:"Opaque pagination cursor"`
	Limit  int    `query:"limit" doc:"Maximum items to return" minimum:"1" maximum:"100"`
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

type createOrderOutput struct {
	Location string        `header:"Location"`
	Body     orderResponse
}

type getOrderOutput struct {
	Body orderResponse
}

type listOrdersOutput struct {
	Body listResponse
}

func (h *Handler) create(ctx context.Context, input *createOrderInput) (*createOrderOutput, error) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return nil, httpx.NewProblem(http.StatusUnauthorized, "authentication required")
	}
	order, err := h.svc.Create(ctx, actor, NewOrder{
		Reference:   input.Body.Reference,
		AmountMinor: input.Body.AmountMinor,
		Currency:    input.Body.Currency,
	})
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return &createOrderOutput{
		Location: "/v1/orders/" + strconv.FormatInt(order.ID, 10),
		Body:     toResponse(order),
	}, nil
}

func (h *Handler) get(ctx context.Context, input *getOrderInput) (*getOrderOutput, error) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return nil, httpx.NewProblem(http.StatusUnauthorized, "authentication required")
	}
	order, err := h.svc.Get(ctx, actor, input.ID)
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	return &getOrderOutput{Body: toResponse(order)}, nil
}

func (h *Handler) list(ctx context.Context, input *listOrdersInput) (*listOrdersOutput, error) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return nil, httpx.NewProblem(http.StatusUnauthorized, "authentication required")
	}
	page, err := h.svc.List(ctx, actor, input.Cursor, input.Limit)
	if err != nil {
		return nil, h.toProblem(ctx, err)
	}
	items := make([]orderResponse, 0, len(page.Items))
	for _, order := range page.Items {
		items = append(items, toResponse(order))
	}
	return &listOrdersOutput{
		Body: listResponse{Items: items, NextCursor: page.NextCursor},
	}, nil
}

func actorFrom(ctx context.Context) (Actor, bool) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return Actor{}, false
	}
	return Actor{UserID: principal.UserID, Admin: principal.Role == auth.RoleAdmin}, true
}

func (h *Handler) toProblem(ctx context.Context, err error) error {
	p := h.mapError(err)
	if p.Status >= http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "orders request failed",
			"error", err,
			"request_id", httpx.RequestIDFrom(ctx),
		)
	}
	p.RequestID = httpx.RequestIDFrom(ctx)
	return p
}

func (h *Handler) mapError(err error) *httpx.Problem {
	var validation *ValidationError
	switch {
	case errors.As(err, &validation):
		return httpx.NewProblem(http.StatusUnprocessableEntity, validation.Error())
	case errors.Is(err, ErrReferenceTaken):
		return httpx.NewProblem(http.StatusConflict, err.Error())
	case errors.Is(err, ErrNotFound):
		return httpx.NewProblem(http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidCursor):
		return httpx.NewProblem(http.StatusBadRequest, err.Error())
	default:
		return httpx.NewProblem(http.StatusInternalServerError, "")
	}
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
