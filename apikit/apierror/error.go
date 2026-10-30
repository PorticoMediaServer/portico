// Package apierror is the public error catalogue. Internal causes never become messages.
package apierror

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit/httpclientclose"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Retry string

const (
	Never        Retry = "never"
	SameRequest  Retry = "same_request"
	AfterRefresh Retry = "after_refresh"
	AfterReauth  Retry = "after_reauth"
)

type Definition struct {
	Code    string `json:"code"`
	Status  int    `json:"status"`
	Message string `json:"message"`
	Retry   Retry  `json:"retry"`
}
type Field struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   Retry  `json:"retry"`
	// RetryAfterSeconds drives the HTTP Retry-After header, which is whole
	// seconds by standard; the body carries the same wait as RetryAfterMs.
	RetryAfterSeconds int     `json:"-"`
	RetryAfterMs      int64   `json:"retryAfterMs,omitempty"`
	Fields            []Field `json:"fields,omitempty"`
	Current           any     `json:"current,omitempty"`
	// Reason refines Code for clients that act on it (e.g. prepared_canceled's
	// "queue_changed"); a stable lowercase word, never prose.
	Reason    string `json:"reason,omitempty"`
	RequestID string `json:"requestId"`
	Cause     error  `json:"-"`
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

type Registry struct {
	codes     map[string]Definition
	translate func(error) *Error
}

// Translate installs fn to name a public error for a cause that is not one
// already, such as a storage layer's typed overload. fn returns nil for an
// error it does not know, which then answers as internal. Install it before
// the registry serves requests.
func (r *Registry) Translate(fn func(error) *Error) { r.translate = fn }

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// LintMessage checks the public vocabulary at registration time. It deliberately
// does not rewrite messages: authors fix the definition so generated clients and
// server replies always agree.
func LintMessage(message string) error {
	if message == "" || len(message) > 240 || !utf8.ValidString(message) || strings.TrimSpace(message) != message || strings.ContainsAny(message, "\r\n\t") {
		return errors.New("invalid public message")
	}
	words := regexp.MustCompile(`[A-Za-z]+`).FindAllString(strings.ToLower(message), -1)
	for _, word := range words {
		switch word {
		case "authorisation", "authorised", "unauthorised", "cancelled", "cancelling", "colour", "behaviour", "initialise", "initialised", "synchronise", "synchronised", "sql", "sqlite", "postgres", "panic", "goroutine", "nil", "mutex", "stacktrace":
			return errors.New("public messages require US English without implementation jargon")
		}
	}
	return nil
}

func New(definitions ...Definition) (*Registry, error) {
	r := &Registry{codes: map[string]Definition{}}
	for _, d := range append(Defaults(), definitions...) {
		if !codePattern.MatchString(d.Code) || d.Status < 400 || d.Status > 599 || LintMessage(d.Message) != nil {
			return nil, errors.New("invalid error definition")
		}
		if _, exists := r.codes[d.Code]; exists {
			return nil, errors.New("duplicate error code")
		}
		switch d.Retry {
		case Never, SameRequest, AfterRefresh, AfterReauth:
		default:
			return nil, errors.New("invalid retry class")
		}
		r.codes[d.Code] = d
	}
	return r, nil
}
func Defaults() []Definition {
	return []Definition{
		{"internal", 500, "The request could not be completed.", Never},
		{"invalid_request", 400, "Check the request and try again.", Never},
		{"unauthorized", 401, "Authentication is required.", AfterReauth},
		{"not_permitted", 403, "This action is not permitted.", Never},
		{"not_found", 404, "This item is not available.", Never},
		{"invalid_cursor", 400, "This page link is not valid.", Never},
		{"cursor_expired", 409, "Refresh the list to continue.", AfterRefresh},
		{"revision_required", 428, "Refresh before making this change.", AfterRefresh},
		{"revision_mismatch", 412, "This information changed. Refresh and try again.", AfterRefresh},
		{"body_too_large", 413, "The request is too large.", Never},
		{"unsupported_media_type", 415, "This request format is not supported.", Never},
		{"idempotency_key_reused", 422, "This request conflicts with an earlier request.", Never},
		{"request_in_progress", 409, "This request is still being processed.", SameRequest},
	}
}
func (r *Registry) Definition(code string) (Definition, bool) { d, ok := r.codes[code]; return d, ok }
func (r *Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.codes))
	for _, d := range r.codes {
		out = append(out, d)
	}
	return out
}
func (r *Registry) Write(w http.ResponseWriter, requestID string, err error) {
	if httpclientclose.Writer(w) {
		w.WriteHeader(httpclientclose.Status)
		return
	}

	d := r.codes["internal"]
	out := Error{}
	var public *Error
	if !errors.As(err, &public) && err != nil && r.translate != nil {
		public = r.translate(err)
	}
	if public != nil {
		if known, ok := r.codes[public.Code]; ok {
			d = known
			out = *public
		}
	}
	out.Code, out.Message, out.Retry, out.Cause = d.Code, d.Message, d.Retry, nil
	if requestID == "" {
		var raw [16]byte
		_, _ = rand.Read(raw[:])
		requestID = hex.EncodeToString(raw[:])
	}
	out.RequestID = requestID
	// A 5xx the caller can't act on says nothing in the body; the cause goes to
	// the log once, under the request id the body carries, so a report of "the
	// request could not be completed" can be traced.
	if d.Status >= 500 && err != nil {
		cause := err
		if public != nil && public.Cause != nil {
			cause = public.Cause
		}
		log.Printf("request %s failed (%d %s): %v", requestID, d.Status, d.Code, cause)
	}
	if out.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(out.RetryAfterSeconds))
		out.RetryAfterMs = int64(out.RetryAfterSeconds) * 1000
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(d.Status)
	_ = json.NewEncoder(w).Encode(struct {
		Error Error `json:"error"`
	}{out})
}
