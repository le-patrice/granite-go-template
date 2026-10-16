package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// MaxBodyBytes is the default request body cap (1 MiB).
const MaxBodyBytes int64 = 1 << 20

// DecodeError describes a request body that could not be decoded. Status is
// the HTTP status to answer with; Detail is safe to show to the client.
type DecodeError struct {
	Status int
	Detail string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("decode request (%d): %s", e.Status, e.Detail)
}

// Decode reads one JSON value from the request body into dst. It requires an
// application/json content type (415 otherwise), caps the body at
// MaxBodyBytes (413), rejects unknown fields and trailing data (400).
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	return DecodeLimit(w, r, dst, MaxBodyBytes)
}

// DecodeLimit is Decode with an explicit body cap in bytes.
func DecodeLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &DecodeError{
			Status: http.StatusUnsupportedMediaType,
			Detail: "Content-Type must be application/json",
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return classifyDecodeError(err)
	}

	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return classifyDecodeError(err)
		}
		return &DecodeError{
			Status: http.StatusBadRequest,
			Detail: "request body must contain a single JSON value",
		}
	}
	return nil
}

func classifyDecodeError(err error) *DecodeError {
	var (
		maxErr    *http.MaxBytesError
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &maxErr):
		return &DecodeError{Status: http.StatusRequestEntityTooLarge, Detail: "request body is too large"}
	case errors.Is(err, io.EOF):
		return &DecodeError{Status: http.StatusBadRequest, Detail: "request body must not be empty"}
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &DecodeError{Status: http.StatusBadRequest, Detail: "request body contains truncated JSON"}
	case errors.As(err, &syntaxErr):
		return &DecodeError{
			Status: http.StatusBadRequest,
			Detail: fmt.Sprintf("request body contains malformed JSON at offset %d", syntaxErr.Offset),
		}
	case errors.As(err, &typeErr):
		detail := "request body contains a value of the wrong type"
		if typeErr.Field != "" {
			detail = fmt.Sprintf("request body field %q has the wrong type", typeErr.Field)
		}
		return &DecodeError{Status: http.StatusBadRequest, Detail: detail}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return &DecodeError{
			Status: http.StatusBadRequest,
			Detail: "request body contains " + strings.TrimPrefix(err.Error(), "json: "),
		}
	default:
		return &DecodeError{Status: http.StatusBadRequest, Detail: "request body is not valid JSON"}
	}
}

// WriteError answers with the problem that matches err: a DecodeError keeps
// its status and detail, anything else becomes a generic 500.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var decodeErr *DecodeError
	if errors.As(err, &decodeErr) {
		WriteProblem(w, r, decodeErr.Status, decodeErr.Detail)
		return
	}
	WriteInternal(w, r)
}
