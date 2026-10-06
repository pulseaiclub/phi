package util

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	future := now.Add(3 * time.Second).Format(http.TimeFormat)
	past := now.Add(-3 * time.Second).Format(http.TimeFormat)
	overflow := strconv.FormatUint(uint64(math.MaxInt64/int64(time.Second))+1, 10)

	tests := []struct {
		name  string
		value string
		want  time.Duration
		valid bool
	}{
		{name: "missing", value: "", want: 0, valid: false},
		{name: "seconds with whitespace", value: " 3 ", want: 3 * time.Second, valid: true},
		{name: "zero seconds", value: "0", want: 0, valid: true},
		{name: "negative seconds", value: "-1", want: 0, valid: false},
		{name: "fractional seconds", value: "1.5", want: 0, valid: false},
		{name: "signed seconds", value: "+3", want: 0, valid: false},
		{name: "seconds overflow", value: overflow, want: 0, valid: false},
		{name: "future date", value: future, want: 3 * time.Second, valid: true},
		{name: "past date", value: past, want: 0, valid: true},
		{name: "invalid date", value: "tomorrow", want: 0, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := parseRetryAfter(tt.value, now)
			require.Equal(t, tt.want, got, "parseRetryAfter(%q) duration", tt.value)
			require.Equal(t, tt.valid, valid, "parseRetryAfter(%q) valid", tt.value)
		})
	}
}

func TestRetryDelayFallbackAndHeaderPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		attempt   int
		header    string
		wantDelay time.Duration
	}{
		{name: "first fallback is immediate", attempt: 0, wantDelay: 0},
		{name: "second fallback is two seconds", attempt: 1, wantDelay: 2 * time.Second},
		{name: "zero header overrides fallback", attempt: 1, header: "0", wantDelay: 0},
		{name: "invalid header uses fallback", attempt: 1, header: "1.5", wantDelay: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp *http.Response
			if tt.header != "" {
				resp = &http.Response{Header: http.Header{"Retry-After": []string{tt.header}}}
			}
			require.Equal(
				t,
				tt.wantDelay,
				retryDelay(resp, tt.attempt),
				"retryDelay(attempt=%d, header=%q)",
				tt.attempt,
				tt.header,
			)
		})
	}
}

func TestDoWithRetryRetryAfterBehavior(t *testing.T) {
	pastDate := time.Now().UTC().Add(-time.Minute).Format(http.TimeFormat)
	futureDate := time.Now().UTC().Add(time.Minute).Format(http.TimeFormat)

	type scriptedResponse struct {
		status     int
		retryAfter string
		body       string
	}
	tests := []struct {
		name         string
		responses    []scriptedResponse
		timeout      time.Duration
		wantErr      bool
		wantStatus   int
		wantRequests int32
	}{
		{
			name: "zero header overrides second fallback",
			responses: []scriptedResponse{
				{status: http.StatusServiceUnavailable},
				{status: http.StatusTooManyRequests, retryAfter: "0"},
				{status: http.StatusOK, body: "ok"},
			},
			timeout:      250 * time.Millisecond,
			wantStatus:   http.StatusOK,
			wantRequests: 3,
		},
		{
			name: "past date overrides second fallback",
			responses: []scriptedResponse{
				{status: http.StatusServiceUnavailable},
				{status: http.StatusServiceUnavailable, retryAfter: pastDate},
				{status: http.StatusOK, body: "ok"},
			},
			timeout:      250 * time.Millisecond,
			wantStatus:   http.StatusOK,
			wantRequests: 3,
		},
		{
			name: "seconds header is cancellable",
			responses: []scriptedResponse{
				{status: http.StatusServiceUnavailable, retryAfter: "60"},
			},
			timeout:      50 * time.Millisecond,
			wantErr:      true,
			wantRequests: 1,
		},
		{
			name: "date header is cancellable",
			responses: []scriptedResponse{
				{status: http.StatusServiceUnavailable, retryAfter: futureDate},
			},
			timeout:      50 * time.Millisecond,
			wantErr:      true,
			wantRequests: 1,
		},
		{
			name: "fallback is cancellable after second failure",
			responses: []scriptedResponse{
				{status: http.StatusServiceUnavailable},
				{status: http.StatusServiceUnavailable},
			},
			timeout:      50 * time.Millisecond,
			wantErr:      true,
			wantRequests: 2,
		},
		{
			name: "final retryable response is returned",
			responses: []scriptedResponse{
				{status: http.StatusServiceUnavailable, retryAfter: "0", body: "first"},
				{status: http.StatusServiceUnavailable, retryAfter: "0", body: "second"},
				{status: http.StatusServiceUnavailable, retryAfter: "0", body: "final"},
			},
			timeout:      250 * time.Millisecond,
			wantStatus:   http.StatusServiceUnavailable,
			wantRequests: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := int(requests.Add(1))
				response := scriptedResponse{status: http.StatusOK, body: "unexpected"}
				if n <= len(tt.responses) {
					response = tt.responses[n-1]
				}
				if response.retryAfter != "" {
					w.Header().Set("Retry-After", response.retryAfter)
				}
				w.WriteHeader(response.status)
				_, _ = io.WriteString(w, response.body)
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(t.Context(), tt.timeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
			require.NoError(t, err)

			resp, err := DoWithRetry(server.Client(), req)
			if tt.wantErr {
				require.Error(t, err, "DoWithRetry should return error")
				require.ErrorIs(
					t,
					err,
					context.DeadlineExceeded,
					"DoWithRetry error = %v, want context deadline exceeded",
					err,
				)
				if resp != nil {
					resp.Body.Close()
					require.Fail(t, "DoWithRetry returned a response with an error")
				}
			} else {
				require.NoError(t, err, "DoWithRetry returned error")
				require.NotNil(t, resp, "DoWithRetry returned nil response")
				require.Equal(t, tt.wantStatus, resp.StatusCode, "status mismatch")
				_ = resp.Body.Close()
			}

			require.Equal(t, tt.wantRequests, requests.Load(), "requests mismatch")
		})
	}
}

func TestIsStaleConnError(t *testing.T) {
	resetByPeer := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET},
	}
	brokenPipe := &net.OpError{
		Op:  "write",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE},
	}
	refused := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}
	// Client.Do wraps transport failures in *url.Error before DoWithRetry sees them.
	asHTTP := func(err error) error {
		return &url.Error{Op: "Get", URL: "http://example.test", Err: err}
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: false},
		{name: "closed connection", err: net.ErrClosed, want: true},
		{name: "wrapped closed connection", err: asHTTP(net.ErrClosed), want: true},
		{name: "connection reset", err: resetByPeer, want: true},
		{name: "broken pipe", err: brokenPipe, want: true},
		{name: "wrapped connection reset", err: asHTTP(resetByPeer), want: true},
		{name: "broken pipe message", err: errors.New("write: broken pipe"), want: true},
		{name: "connection reset message", err: errors.New("read: connection reset by peer"), want: true},
		{name: "closed connection message", err: errors.New("use of closed network connection"), want: true},
		{name: "unrelated error", err: errors.New("connection refused"), want: false},
		{name: "connection refused", err: refused, want: false},
		{name: "wrapped connection refused", err: asHTTP(refused), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isStaleConnError(tt.err), "isStaleConnError(%v)", tt.err)
		})
	}
}

func TestDoWithRetryStaleConn(t *testing.T) {
	stale := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET},
	}
	refused := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}

	tests := []struct {
		name      string
		errs      []error
		wantCalls int
		wantErr   error
	}{
		{
			name:      "retries a stale keep-alive once",
			errs:      []error{stale, nil},
			wantCalls: 2,
		},
		{
			name:      "returns the second stale failure",
			errs:      []error{stale, stale},
			wantCalls: 2,
			wantErr:   stale,
		},
		{
			name:      "does not retry a non-stale transport error",
			errs:      []error{refused},
			wantCalls: 1,
			wantErr:   refused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &scriptedTransport{errs: tt.errs}
			client := &http.Client{Transport: transport}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", http.NoBody)
			require.NoError(t, err)

			resp, err := DoWithRetry(client, req)
			require.Equal(t, tt.wantCalls, transport.calls, "round trips")
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				if resp != nil {
					_ = resp.Body.Close()
					require.Fail(t, "DoWithRetry returned a response with an error")
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()
		})
	}
}

// scriptedTransport replays one error per RoundTrip. A nil error is 200 OK.
// Calls past the script also succeed, so an unexpected extra retry is visible.
type scriptedTransport struct {
	errs  []error
	calls int
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	if s.calls <= len(s.errs) && s.errs[s.calls-1] != nil {
		return nil, s.errs[s.calls-1]
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}, nil
}
