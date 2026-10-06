package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// maxAPIErrorBodyChars caps the raw-body fallback in FormatAPIError so a
// non-JSON error page can never flood the terminal or the session history.
const maxAPIErrorBodyChars = 2000

// FormatAPIError builds a compact, human-readable error for a non-2xx
// provider response: "<provider> API error (<status>): <message>". Provider
// JSON error envelopes are unwrapped to their message so raw JSON never
// reaches the UI; the trimmed raw body (capped) is the fallback for bodies
// with no recognizable envelope.
func FormatAPIError(provider string, status int, body []byte) error {
	return fmt.Errorf("%s API error (%d): %s", provider, status, APIErrorMessage(body))
}

// APIErrorMessage extracts the human-readable message from a provider error
// body — the envelope unwrapping and capped raw-body fallback FormatAPIError
// uses, without inventing an HTTP status. In-stream error events carry no
// status; use this for them.
func APIErrorMessage(body []byte) string {
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
	return msg
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
