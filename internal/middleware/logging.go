package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/httpjson"
)

type recorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (rec *recorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
		rec.ResponseWriter.WriteHeader(status)
	}
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (rec *recorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func (rec *recorder) statusOrOK() int {
	if rec.status == 0 {
		return http.StatusOK
	}
	return rec.status
}

// Recover converts a panic into a 500 response.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				log.ErrorContext(r.Context(), "panic recovered",
					"request_id", httpjson.RequestID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", recovered,
					"stack", string(debug.Stack()))
				httpjson.WriteError(w, r, entity.NewError(entity.CodeInternal, "internal server error"))
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog emits one structured line per request.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &recorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			status := rec.statusOrOK()
			level := slog.LevelInfo
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}

			log.Log(r.Context(), level, "http request",
				"request_id", httpjson.RequestID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", rec.written,
				"duration_ms", time.Since(started).Milliseconds(),
				"client_ip", httpjson.ClientIP(r.Context()))
		})
	}
}

// Timeout bounds total handler execution time.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
