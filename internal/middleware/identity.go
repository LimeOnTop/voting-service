// Package middleware provides HTTP middleware for identity, auth, and limits.
package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/LimeOnTop/voting-service/internal/httpjson"
)

const tokenBytes = 16

// RealIP resolves the client IP and stores it in the request context.
func RealIP(trustedProxies []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := httpjson.WithClientIP(r.Context(), resolveClientIP(r, trustedProxies))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolveClientIP(r *http.Request, trustedProxies []netip.Prefix) string {
	peer := parseAddr(r.RemoteAddr)
	if !peer.IsValid() {
		return "unknown"
	}
	if len(trustedProxies) == 0 || !isTrusted(peer, trustedProxies) {
		return peer.String()
	}

	var hops []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(header, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !isTrusted(addr, trustedProxies) {
			return addr.Unmap().String()
		}
	}
	return peer.String()
}

func parseAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func isTrusted(addr netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, prefix := range trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Correlation attaches a request id to the context and response.
func Correlation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-Id"))
		if id == "" {
			id = randomToken()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(httpjson.WithRequestID(r.Context(), id)))
	})
}

func sanitizeRequestID(raw string) string {
	const maxLength = 64
	if raw == "" || len(raw) > maxLength {
		return ""
	}
	for _, char := range raw {
		allowed := char == '-' || char == '_' || char == '.' ||
			(char >= '0' && char <= '9') ||
			(char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z')
		if !allowed {
			return ""
		}
	}
	return raw
}

// VoterCookieConfig describes the anonymous device cookie.
type VoterCookieConfig struct {
	Name     string
	TTL      time.Duration
	Secure   bool
	SameSite http.SameSite
	Domain   string
}

// VoterCookie ensures the request carries an anonymous device token.
func VoterCookie(cfg VoterCookieConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if cookie, err := r.Cookie(cfg.Name); err == nil {
				token = sanitizeToken(cookie.Value)
			}
			if token == "" {
				token = randomToken()
				http.SetCookie(w, &http.Cookie{
					Name:     cfg.Name,
					Value:    token,
					Path:     "/",
					Domain:   cfg.Domain,
					MaxAge:   int(cfg.TTL.Seconds()),
					HttpOnly: true,
					Secure:   cfg.Secure,
					SameSite: cfg.SameSite,
				})
			}
			next.ServeHTTP(w, r.WithContext(httpjson.WithVoterToken(r.Context(), token)))
		})
	}
}

func sanitizeToken(raw string) string {
	const expectedLength = 22 // base64url of 16 bytes, unpadded
	if len(raw) != expectedLength {
		return ""
	}
	if _, err := base64.RawURLEncoding.DecodeString(raw); err != nil {
		return ""
	}
	return raw
}

func randomToken() string {
	buffer := make([]byte, tokenBytes)
	_, _ = rand.Read(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}
