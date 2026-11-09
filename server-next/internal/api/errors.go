package api

import (
	"errors"
	"net/http"
	"time"
)

// Error is a refusal a client can act on. Every failure leaves the server in this one shape:
//
//	{"error": {"code": "...", "message": "...", "retry": "...", "requestId": "...", "fields": [...]}}
//
// Clients act on the HTTP status and the code. The message is for people.
type Error struct {
	Status  int     `json:"-"`
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Retry   Retry   `json:"retry"`
	Fields  []Field `json:"fields,omitempty"`
	// RetryAfterMs is how long to wait before trying again; it is also sent as Retry-After.
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
	RequestID    string `json:"requestId"`
}

// Retry tells a client whether sending the request again can help.
type Retry string

const (
	RetryNever        Retry = "never"
	RetrySame         Retry = "same_request"
	RetryAfterRefresh Retry = "after_refresh"
	RetryAfterSignIn  Retry = "after_reauth"
)

// Field names one part of the request that was refused.
type Field struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error Error `json:"error"`
}

func (e *Error) Error() string { return e.Code }

func refusal(status int, code, message string, retry Retry) *Error {
	return &Error{Status: status, Code: code, Message: message, Retry: retry}
}

// The refusals every area shares. An area with its own refusal declares it beside its routes.
func ErrInvalid(message string, fields ...Field) *Error {
	e := refusal(http.StatusBadRequest, "invalid_request", message, RetryNever)
	e.Fields = fields
	return e
}

// ErrField refuses one named part of the request.
func ErrField(path, message string) *Error {
	return ErrInvalid("Check the request and try again.", Field{Path: path, Code: "invalid", Message: message})
}

func ErrUnauthorized() *Error {
	return refusal(http.StatusUnauthorized, "unauthorized", "Sign in to continue.", RetryAfterSignIn)
}

func ErrNotPermitted() *Error {
	return refusal(http.StatusForbidden, "not_permitted", "This action is not permitted.", RetryNever)
}

// ErrNotFound is also the answer for a thing the caller may not know exists.
func ErrNotFound() *Error {
	return refusal(http.StatusNotFound, "not_found", "This is not available.", RetryNever)
}

func ErrConflict(code, message string) *Error {
	return refusal(http.StatusConflict, code, message, RetryNever)
}

// ErrTooMany asks the caller to wait before trying again.
func ErrTooMany(code, message string, wait time.Duration) *Error {
	e := refusal(http.StatusTooManyRequests, code, message, RetrySame)
	e.RetryAfterMs = max(wait.Milliseconds(), 1000)
	return e
}

func asError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
