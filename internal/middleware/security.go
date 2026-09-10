package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/httpjson"
)

// AdminAuth validates the admin bearer token.
func AdminAuth(adminToken string) func(http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(adminToken))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented := sha256.Sum256([]byte(bearerToken(r.Header.Get("Authorization"))))
			if subtle.ConstantTimeCompare(expected[:], presented[:]) != 1 {
				httpjson.WriteError(w, r, entity.NewError(entity.CodeUnauthorized, "invalid or missing admin credentials"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(header string) string {
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

// BodyLimit caps the request body size.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
