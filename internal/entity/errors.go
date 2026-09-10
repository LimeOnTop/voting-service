package entity

import "errors"

// ErrorCode classifies a domain failure for transport mapping.
type ErrorCode string

const (
	CodeInvalidRequest  ErrorCode = "invalid_request"
	CodeNotFound        ErrorCode = "not_found"
	CodeConflict        ErrorCode = "conflict"
	CodeAlreadyVoted    ErrorCode = "already_voted"
	CodeRateLimited     ErrorCode = "rate_limited"
	CodeQuotaExceeded   ErrorCode = "quota_exceeded"
	CodeUnauthorized    ErrorCode = "unauthorized"
	CodePayloadTooLarge ErrorCode = "payload_too_large"
	CodeUnavailable     ErrorCode = "unavailable"
	CodeInternal        ErrorCode = "internal"
)

// Error is a client-safe domain error with an optional wrapped cause.
type Error struct {
	Code    ErrorCode
	Message string
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.cause.Error()
	}
	return string(e.Code) + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// NewError builds an error with no underlying cause.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WrapError attaches a code and client-safe message to an internal cause.
func WrapError(code ErrorCode, cause error, message string) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// CodeOf returns the domain error code, or CodeInternal for unclassified errors.
func CodeOf(err error) ErrorCode {
	var domainErr *Error
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return CodeInternal
}

// MessageOf returns the client-safe message, or a generic fallback.
func MessageOf(err error) string {
	var domainErr *Error
	if errors.As(err, &domainErr) {
		return domainErr.Message
	}
	return "internal server error"
}

// ErrPollNotFound returns the canonical poll not-found error.
func ErrPollNotFound() *Error { return NewError(CodeNotFound, "poll not found") }
