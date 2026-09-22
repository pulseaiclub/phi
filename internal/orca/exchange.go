package orca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// exchangeTimeout bounds the code-for-key call. A hung consent exchange must
// fail with a message the user can act on, not block the terminal.
const exchangeTimeout = 30 * time.Second

// exchangeBodyLimit caps how much of a response we will read. The endpoint
// returns a small JSON object; anything larger is a proxy error page.
const exchangeBodyLimit = 64 << 10

// ScopeAPI is the scope a tool integration asks for.
const ScopeAPI = "api"

// ScopeConnector is the wider grant a workspace role may or may not permit.
const ScopeConnector = "connector"

// ExchangeResult is the successful outcome of a code exchange.
type ExchangeResult struct {
	// APIKey is the durable OrcaRouter key. Treat it as a secret.
	APIKey string
	// UserID is the account the key belongs to.
	UserID string
	// Scope is the scope that was GRANTED, read back from the response — not
	// the scope that was requested. A client that asked for connector and
	// received api was approved by a role that cannot grant more.
	Scope string
}

// ExchangeError classifies a failed exchange so callers can react without
// parsing strings. The response body is never embedded verbatim: a proxy could
// echo the request, and the request carries the verifier.
type ExchangeError struct {
	// Status is the HTTP status, or 0 for a transport failure.
	Status int
	// Code is the OAuth error code when the endpoint sent one.
	Code string
	// Message is a short, credential-free explanation.
	Message string
}

func (e *ExchangeError) Error() string {
	switch {
	case e.Status == 0:
		return "OrcaRouter authorization failed: " + e.Message
	case e.Code != "":
		return fmt.Sprintf("OrcaRouter authorization failed (%d %s): %s", e.Status, e.Code, e.Message)
	default:
		return fmt.Sprintf("OrcaRouter authorization failed (%d): %s", e.Status, e.Message)
	}
}

// Terminal reports whether retrying the same code can never succeed, so the
// caller should discard the attempt rather than loop.
func (e *ExchangeError) Terminal() bool {
	switch e.Status {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusUnauthorized:
		return true
	}
	// A 429 is a rate limit, not a bad code: the same code stays valid until
	// its TTL expires, so the user may retry after waiting.
	return false
}

// RateLimited reports a 429 from the consent endpoint. OrcaRouter caps
// PKCE-issued keys per user per 24 hours; re-authorizing on every launch is
// what trips it.
func (e *ExchangeError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

// Exchange redeems an authorization code for a durable API key.
//
// The verifier is sent in the request body and nowhere else — not in a URL, a
// query string, a log line, or an error.
func (o Origins) Exchange(ctx context.Context, httpClient *http.Client, code, verifier string) (ExchangeResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return ExchangeResult{}, &ExchangeError{Message: "no authorization code was supplied"}
	}
	if verifier == "" {
		return ExchangeResult{}, &ExchangeError{Message: "internal error: the code verifier is missing"}
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	payload, err := json.Marshal(map[string]string{
		"code":                  code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})
	if err != nil {
		return ExchangeResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.ExchangeURL(), bytes.NewReader(payload))
	if err != nil {
		return ExchangeResult{}, &ExchangeError{Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ExchangeResult{}, &ExchangeError{Message: transportMessage(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, exchangeBodyLimit+1))
	if err != nil {
		return ExchangeResult{}, &ExchangeError{Status: resp.StatusCode, Message: "could not read the response"}
	}
	if int64(len(body)) > exchangeBodyLimit {
		return ExchangeResult{}, &ExchangeError{Status: resp.StatusCode, Message: "response was too large"}
	}

	if resp.StatusCode != http.StatusOK {
		return ExchangeResult{}, classifyExchangeFailure(resp.StatusCode, body)
	}

	var ok struct {
		Key    string `json:"key"`
		UserID string `json:"user_id"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(body, &ok); err != nil {
		return ExchangeResult{}, &ExchangeError{
			Status:  resp.StatusCode,
			Message: "the response was not the expected JSON object",
		}
	}
	if strings.TrimSpace(ok.Key) == "" {
		return ExchangeResult{}, &ExchangeError{
			Status:  resp.StatusCode,
			Message: "the response carried no key",
		}
	}
	return ExchangeResult{APIKey: ok.Key, UserID: ok.UserID, Scope: ok.Scope}, nil
}

// classifyExchangeFailure turns a non-200 into a classified error. Error
// bodies are {"error": "...", "error_description": "..."} — the OAuth shape,
// not this API's usual envelope — so standard clients parse them. The
// status-specific guidance is always present so a body with only a terse code
// still tells the user what to do.
func classifyExchangeFailure(status int, body []byte) *ExchangeError {
	var env struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &env)

	detail := strings.TrimSpace(env.Description)
	if detail == "" {
		detail = strings.TrimSpace(env.Error)
	}
	guidance := statusGuidance(status)
	switch {
	case guidance != "" && detail != "" && detail != guidance:
		detail = guidance + " (" + detail + ")"
	case guidance != "":
		detail = guidance
	case detail == "":
		detail = "unexpected response"
	}
	return &ExchangeError{Status: status, Code: strings.TrimSpace(env.Error), Message: sanitizeMessage(detail)}
}

func statusGuidance(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "the request was rejected; code_challenge_method must be S256"
	case http.StatusForbidden:
		return "the code is unknown, expired, already used, or the verifier did not match"
	case http.StatusTooManyRequests:
		return "too many authorizations for this account; wait before trying again"
	default:
		return ""
	}
}

// sanitizeMessage strips anything credential-shaped from an upstream message
// before it can reach a terminal, a log, or a session transcript.
func sanitizeMessage(msg string) string {
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	for _, prefix := range []string{"sk-orca-", "sk-"} {
		for {
			i := strings.Index(msg, prefix)
			if i < 0 {
				break
			}
			end := i + len(prefix)
			for end < len(msg) && isKeyRune(msg[end]) {
				end++
			}
			msg = msg[:i] + "[redacted]" + msg[end:]
		}
	}
	return msg
}

func isKeyRune(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_':
		return true
	}
	return false
}

// transportMessage keeps the useful part of a transport error (usually a
// timeout or a DNS failure) without leaking the full URL, which for a
// misconfigured self-hosted base could carry a token.
func transportMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "the request timed out; check your network and try again"
	}
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		if uerr.Timeout() {
			return "the request timed out; check your network and try again"
		}
		return "could not reach the OrcaRouter authorization service: " + uerr.Err.Error()
	}
	return "could not reach the OrcaRouter authorization service"
}
