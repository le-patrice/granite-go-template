// Package httpx is the HTTP toolkit: server lifecycle, middleware, request
// decoding, RFC 9457 error responses, health checks, and metrics.
package httpx

import (
	"encoding/json"
	"net/http"
)

const problemContentType = "application/problem+json"

// Problem is an RFC 9457 problem details object.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteProblem writes an application/problem+json response. For 5xx statuses
// the detail is always dropped, so internal error text never reaches clients.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	p := Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		RequestID: RequestIDFrom(r.Context()),
	}
	if p.RequestID == "" {
		p.RequestID = w.Header().Get(RequestIDHeader)
	}
	if status >= http.StatusInternalServerError {
		p.Detail = ""
	}

	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteInternal writes a generic 500 problem.
func WriteInternal(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, http.StatusInternalServerError, "")
}
