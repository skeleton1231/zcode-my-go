package ai

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryableError marks a provider-declared transient failure (ZCode's
// isRetryable flag), so the agent loop can back off and try again.
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// retryableStatus classifies HTTP codes: rate limits and server failures
// pass, client errors do not.
func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusRequestTimeout ||
		status >= 500
}

// retryableKind classifies Anthropic SSE error types.
func retryableKind(kind string) bool {
	switch kind {
	case "overloaded_error", "rate_limit_error", "api_timeout", "timeout_error":
		return true
	default:
		return false
	}
}

// httpErrorStatus is httpError plus the status code it classified.
func httpErrorStatus(resp *http.Response) (string, int) {
	msg := httpError(resp)
	return msg, resp.StatusCode
}

// retryAfterMS reads Retry-After headers (ZCode's parseRetryAfterMs):
// milliseconds first, then seconds, then an HTTP date.
func retryAfterMS(header http.Header) int {
	if v := strings.TrimSpace(header.Get("retry-after-ms")); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			return ms
		}
	}
	v := strings.TrimSpace(header.Get("retry-after"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return int(secs * 1000)
	}
	if ts, err := http.ParseTime(v); err == nil {
		if d := time.Until(ts); d > 0 {
			return int(d.Milliseconds())
		}
	}
	return 0
}

// errRetryable reports whether an arbitrary error is transient.
func errRetryable(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}
