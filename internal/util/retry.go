package util

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxHTTPRetryAttempts = 3

func shouldRetryHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// retryDelay returns a valid server-provided retry delay, or the existing
// attempt-based fallback when Retry-After is missing or invalid.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return delay
		}
	}
	if attempt == 0 {
		return 0
	}
	return time.Duration(attempt+1) * time.Second
}

// parseRetryAfter parses the Retry-After header's delay-seconds or HTTP-date
// form. The boolean distinguishes an explicit zero delay from an invalid
// header, which must use the local fallback.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	if isDecimalDigits(value) {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}

	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := retryAt.Sub(now)
	delay = max(delay, 0)
	return delay, true
}

func isDecimalDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func isStaleConnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		if errors.Is(opErr.Err, syscall.ECONNRESET) || errors.Is(opErr.Err, syscall.EPIPE) {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed network connection")
}

func sleepWithCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// DoWithRetry retries transient HTTP failures (429/5xx, stale keep-alive).
func DoWithRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	hasBody := req.Body != nil
	if hasBody {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}

	newAttempt := func() *http.Request {
		r := req.Clone(req.Context())
		if hasBody {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		return r
	}

	var (
		resp *http.Response
		err  error
	)
	for attempt := range maxHTTPRetryAttempts {
		if resp != nil {
			_ = resp.Body.Close()
			resp = nil
		}
		resp, err = client.Do(newAttempt())
		if err != nil {
			if attempt == 0 && isStaleConnError(err) {
				continue
			}
			return nil, err
		}
		if !shouldRetryHTTPStatus(resp.StatusCode) {
			return resp, nil
		}
		if attempt < maxHTTPRetryAttempts-1 {
			delay := retryDelay(resp, attempt)

			// We are definitely retrying, so do not hold the response body while sleeping.
			_ = resp.Body.Close()
			resp = nil
			if err := sleepWithCtx(req.Context(), delay); err != nil {
				return nil, err
			}
		}
	}
	return resp, nil
}
