package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// maxAPIErrorBodyChars caps the raw-body fallback in FormatAPIError so a
// non-JSON error page can never flood the terminal or the session history.
const maxAPIErrorBodyChars = 2000

// APIError is a non-2xx provider response, carrying the status so callers can
// distinguish a terminal authentication failure from a retryable one without
// parsing the message.
type APIError struct {
	Provider string
	Status   int
	Message  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s API error (%d): %s", e.Provider, e.Status, e.Message)
}

// IsUnauthorized reports whether err is a provider 401. A durable credential
// such as an OrcaRouter key has no refresh grant, so a 401 is terminal: the
// account must re-authenticate rather than retry.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}

// FormatAPIError builds a compact, human-readable error for a non-2xx
// provider response: "<provider> API error (<status>): <message>". Provider
// JSON error envelopes are unwrapped to their message so raw JSON never
// reaches the UI; the trimmed raw body (capped) is the fallback for bodies
// with no recognizable envelope.
func FormatAPIError(provider string, status int, body []byte) error {
	msg := apiErrorMessage(body)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if r := []rune(msg); len(r) > maxAPIErrorBodyChars {
			msg = string(r[:maxAPIErrorBodyChars]) + "…"
		}
	}
	if msg == "" {
		msg = "empty error response"
	}
	return &APIError{Provider: provider, Status: status, Message: msg}
}

// apiErrorMessage extracts the human-readable message from a provider error
// JSON body. OpenAI-compatible, Anthropic, and Gemini APIs all nest the
// reason under "error" as either an object with a "message" field or a plain
// string; some proxies use a flat top-level {"message": …}. It returns ""
// when the body has no recognizable message.
func apiErrorMessage(body []byte) string {
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if len(env.Error) > 0 && !bytes.Equal(bytes.TrimSpace(env.Error), []byte("null")) {
		if msg := errorObjectMessage(env.Error); msg != "" {
			return msg
		}
	}
	return env.Message
}

// errorObjectMessage reads a nested "error" value that is either an object
// with a "message" field ({"error":{"message": …}}) or a bare string
// ({"error":"…"}).
func errorObjectMessage(raw json.RawMessage) string {
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}
