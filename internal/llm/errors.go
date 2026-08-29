package llm

import (
	"errors"
	"fmt"
)

// Sentinel errors every adapter normalizes to, so callers can react to
// categories without matching on strings.
var (
	// ErrNotConfigured means a required endpoint or credential is missing.
	// Detected at startup, never mid-run.
	ErrNotConfigured = errors.New("llm: provider not configured")
	// ErrUnauthorized covers 401/403.
	ErrUnauthorized = errors.New("llm: authentication failed")
	// ErrRateLimited covers 429, after retries are exhausted.
	ErrRateLimited = errors.New("llm: rate limited")
	// ErrUnavailable covers 5xx, dial failures, and timeouts.
	ErrUnavailable = errors.New("llm: provider unavailable")
	// ErrBadResponse covers malformed bodies and empty completions. Empty
	// content is an error, not a short answer.
	ErrBadResponse = errors.New("llm: malformed or empty response")
	// ErrContextLength means the input exceeded the model's context window.
	ErrContextLength = errors.New("llm: input exceeds model context window")
	// ErrModelNotFound means the provider does not offer the configured model.
	ErrModelNotFound = errors.New("llm: model not available on provider")
	// ErrInvalidRequest means the request could not be sent as constructed.
	ErrInvalidRequest = errors.New("llm: invalid request")
	// ErrPreflightInconclusive means a startup check could not be completed.
	// It is a warning, not a failure.
	ErrPreflightInconclusive = errors.New("llm: preflight check inconclusive")
)

// Error carries the provider and operation alongside a sentinel, so
// errors.Is(err, llm.ErrRateLimited) works while messages stay readable.
type Error struct {
	Provider string
	Op       string
	Kind     error // one of the sentinels above
	Err      error // underlying cause, may be nil
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s %s: %s", e.Provider, e.Op, e.Kind.Error())
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}

// WrapErr builds an *Error. cause may be nil.
func WrapErr(provider, op string, kind, cause error) error {
	return &Error{Provider: provider, Op: op, Kind: kind, Err: cause}
}

// Errf builds an *Error whose cause is a formatted message.
func Errf(provider, op string, kind error, format string, args ...any) error {
	return &Error{Provider: provider, Op: op, Kind: kind, Err: fmt.Errorf(format, args...)}
}

// ClassifyStatus maps an HTTP status onto a sentinel.
func ClassifyStatus(status int) error {
	switch {
	case status == 401 || status == 403:
		return ErrUnauthorized
	case status == 404:
		return ErrModelNotFound
	case status == 429:
		return ErrRateLimited
	case status >= 500:
		return ErrUnavailable
	case status >= 400:
		return ErrInvalidRequest
	default:
		return ErrBadResponse
	}
}
