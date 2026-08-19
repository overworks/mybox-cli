package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrInvalidRequest marks a request this package rejected locally, before any
// API call was made, because the arguments could not possibly succeed. The CLI
// maps it to the same exit status as any other usage mistake.
var ErrInvalidRequest = errors.New("invalid request")

// invalidRequestf builds an error whose text is only the formatted message but
// which matches errors.Is(err, ErrInvalidRequest). Every pre-call argument
// check in this package must use it, or the mistake exits as a general failure
// instead of a usage error.
func invalidRequestf(format string, args ...any) error {
	return &invalidRequestError{msg: fmt.Sprintf(format, args...)}
}

type invalidRequestError struct{ msg string }

func (e *invalidRequestError) Error() string { return e.msg }
func (e *invalidRequestError) Unwrap() error { return ErrInvalidRequest }

// Error is the error body every MYBOX endpoint returns on a non-2xx status.
//
// Code looks like "PLAT-404" and Message like "NOT_FOUND". RequestID is the
// value to quote when reporting a problem to Naver.
type Error struct {
	Status    int    `json:"-"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
	Timestamp string `json:"timestamp"`

	// Body holds the raw response when it could not be parsed as the standard
	// error envelope (e.g. an HTML error page from an intermediary).
	Body string `json:"-"`
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d", e.Status)
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	switch {
	case e.Message != "":
		fmt.Fprintf(&b, " %s", e.Message)
	case e.Body != "":
		fmt.Fprintf(&b, " %s", truncate(e.Body, 200))
	default:
		fmt.Fprintf(&b, " %s", http.StatusText(e.Status))
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (requestId: %s)", e.RequestID)
	}
	return b.String()
}

// Retryable reports whether the request may be retried as-is. Only transient
// server-side conditions qualify: 429 plus the 5xx family that indicates the
// request never took effect. 507 (insufficient storage) is deliberately excluded
// because retrying cannot help until the user frees space.
func (e *Error) Retryable() bool {
	switch e.Status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable:
		return true
	}
	return false
}

// Convenience predicates used by callers to branch on well-known conditions.
func (e *Error) IsUnauthorized() bool { return e.Status == http.StatusUnauthorized }
func (e *Error) IsForbidden() bool    { return e.Status == http.StatusForbidden }
func (e *Error) IsNotFound() bool     { return e.Status == http.StatusNotFound }
func (e *Error) IsConflict() bool     { return e.Status == http.StatusConflict }
func (e *Error) IsRateLimited() bool  { return e.Status == http.StatusTooManyRequests }
func (e *Error) IsOutOfSpace() bool   { return e.Status == http.StatusInsufficientStorage }

// parseError builds an Error from a non-2xx response. It never returns nil and
// always consumes the body.
func parseError(resp *http.Response) *Error {
	e := &Error{Status: resp.StatusCode}
	// Error bodies are tiny; cap the read so a misbehaving proxy cannot make us
	// buffer an unbounded page.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || len(raw) == 0 {
		return e
	}
	if json.Unmarshal(raw, e) == nil && e.Code != "" {
		return e
	}
	e.Body = strings.TrimSpace(string(raw))
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
