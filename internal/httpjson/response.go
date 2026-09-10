package httpjson

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/LimeOnTop/voting-service/internal/entity"
)

var statusByCode = map[entity.ErrorCode]int{
	entity.CodeInvalidRequest:  http.StatusBadRequest,
	entity.CodeUnauthorized:    http.StatusUnauthorized,
	entity.CodeNotFound:        http.StatusNotFound,
	entity.CodeConflict:        http.StatusConflict,
	entity.CodeAlreadyVoted:    http.StatusConflict,
	entity.CodePayloadTooLarge: http.StatusRequestEntityTooLarge,
	entity.CodeRateLimited:     http.StatusTooManyRequests,
	entity.CodeQuotaExceeded:   http.StatusTooManyRequests,
	entity.CodeUnavailable:     http.StatusServiceUnavailable,
	entity.CodeInternal:        http.StatusInternalServerError,
}

// StatusFor maps a domain error code to an HTTP status.
func StatusFor(code entity.ErrorCode) int {
	if status, ok := statusByCode[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// ErrorBody is the uniform error envelope returned by endpoints.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable code and client-safe message.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.ErrorContext(r.Context(), "failed to encode response body",
			"request_id", RequestID(r.Context()), "error", err.Error())
	}
}

// WriteError writes err as a classified JSON error response.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	code := entity.CodeOf(err)
	status := StatusFor(code)

	if status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed",
			"request_id", RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"code", string(code),
			"error", err.Error())
	}

	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="voting-service"`)
	}

	WriteJSON(w, r, status, ErrorBody{Error: ErrorDetail{
		Code:      string(code),
		Message:   entity.MessageOf(err),
		RequestID: RequestID(r.Context()),
	}})
}

// DecodeJSON decodes a strict JSON request body into dst.
func DecodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return entity.WrapError(entity.CodePayloadTooLarge, err, "request body is too large")
		}
		return entity.WrapError(entity.CodeInvalidRequest, err, "request body is not valid JSON")
	}
	return nil
}
