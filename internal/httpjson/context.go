// Package httpjson provides request context helpers and JSON HTTP responses.
package httpjson

import "context"

type contextKey int

const (
	requestIDKey contextKey = iota
	clientIPKey
	voterTokenKey
)

// WithRequestID attaches a correlation id to the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the correlation id from the context, if any.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithClientIP attaches the resolved client address.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

// ClientIP returns the resolved client address.
func ClientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey).(string)
	return ip
}

// WithVoterToken attaches the anonymous voter token.
func WithVoterToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, voterTokenKey, token)
}

// VoterToken returns the anonymous voter token used for deduplication.
func VoterToken(ctx context.Context) string {
	token, _ := ctx.Value(voterTokenKey).(string)
	return token
}
