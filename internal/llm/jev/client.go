package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

const (
	// DefaultBaseURL is the TypeSafe AI API root.
	DefaultBaseURL = "https://api.typesafe.ai"
	// DefaultModel is the model used when neither Config.Model nor the
	// TYPESAFE_DEFAULT_MODEL environment variable names one.
	DefaultModel = "jev-latest"

	systemOnePath = "/v1/systemone"

	// requestIDHeader carries the server's request id, kept for error messages
	// and Response.RequestID.
	requestIDHeader = "x-typesafe-request-id"
	// providerName labels errors from this endpoint.
	providerName = "typesafe"
)

// Config is one TypeSafe endpoint connection. Empty fields fall back to the
// TYPESAFE_API_KEY, TYPESAFE_BASE_URL, and TYPESAFE_DEFAULT_MODEL environment
// variables, then to DefaultBaseURL and DefaultModel.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	// HTTPClient is the transport to use; nil means util.DefaultHTTPClient.
	// Timeouts come from this client and the context of each call.
	HTTPClient *http.Client
}

// Client asks System One questions over one TypeSafe endpoint. It is safe for
// concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

// NewClient resolves cfg against the environment. It fails when no API key is
// available, rather than deferring the rejection to the first request.
func NewClient(cfg Config) (*Client, error) {
	apiKey := firstNonEmpty(cfg.APIKey, envValue("TYPESAFE_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("no TypeSafe API key: set Config.APIKey or the TYPESAFE_API_KEY environment variable")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = util.DefaultHTTPClient()
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(firstNonEmpty(cfg.BaseURL, envValue("TYPESAFE_BASE_URL"), DefaultBaseURL), "/"),
		apiKey:     apiKey,
		model:      firstNonEmpty(cfg.Model, envValue("TYPESAFE_DEFAULT_MODEL"), DefaultModel),
	}, nil
}

// Usage counts the tokens one request billed.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Answer is one answer, found under its question name in Response.Answers.
// Type says which of the remaining fields the server filled: a noul answer sets
// Noul; a choice answer sets Choice, Confidence, and Probabilities; a score
// answer sets Score, Legend, Confidence, and Probabilities. An answer of a type
// this package does not model keeps its Type and leaves the rest at zero.
//
// Legend and the score half of Probabilities are keyed by score level as a
// decimal string ("0", "1", …), because a JSON object key is a string.
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Legend        map[string]any     `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// wireAnswer mirrors Answer with pointers, so an answer that omits a field its
// own type requires is caught instead of decoding as a confident zero.
type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Legend        map[string]any     `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// answer converts a decoded answer, rejecting one that omits a field the wire
// schema marks required for its type.
func (w wireAnswer) answer(name string) (Answer, error) {
	missing := func(field string) (Answer, error) {
		return Answer{}, fmt.Errorf("typesafe answer for question %q has no %q", name, field)
	}
	answer := Answer{Type: w.Type}
	switch w.Type {
	case AnswerNoul:
		if w.Noul == nil {
			return missing("noul")
		}
		answer.Noul = *w.Noul
	case AnswerChoice:
		switch {
		case w.Choice == nil:
			return missing("choice")
		case w.Confidence == nil:
			return missing("confidence")
		case w.Probabilities == nil:
			return missing("probabilities")
		}
		answer.Choice, answer.Confidence = *w.Choice, *w.Confidence
		answer.Probabilities = w.Probabilities
	case AnswerScore:
		switch {
		case w.Score == nil:
			return missing("score")
		case w.Confidence == nil:
			return missing("confidence")
		case w.Legend == nil:
			return missing("legend")
		case w.Probabilities == nil:
			return missing("probabilities")
		}
		answer.Score, answer.Confidence = *w.Score, *w.Confidence
		answer.Legend, answer.Probabilities = w.Legend, w.Probabilities
	}
	return answer, nil
}

// responseWire is the wire form of a System One response: answers decode field
// by field so each one's required fields can be checked.
type responseWire struct {
	Model   string                `json:"model"`
	Usage   Usage                 `json:"usage"`
	Answers map[string]wireAnswer `json:"answers"`
}

func (w responseWire) response(raw []byte, requestID string) (Response, error) {
	resp := Response{
		Model:     w.Model,
		Usage:     w.Usage,
		Answers:   make(map[string]Answer, len(w.Answers)),
		RequestID: requestID,
		RawBody:   raw,
	}
	for name, answer := range w.Answers {
		converted, err := answer.answer(name)
		if err != nil {
			return Response{}, err
		}
		resp.Answers[name] = converted
	}
	return resp, nil
}

// Response is one System One answer set.
type Response struct {
	// Model is the model that answered, which may differ from the alias asked for.
	Model string `json:"model"`
	// Usage is the token cost of the request.
	Usage Usage `json:"usage"`
	// Answers holds one answer per requested question name. A question left
	// unanswered, or answered with a contradicting type, is an error rather
	// than an empty Answer.
	Answers map[string]Answer `json:"answers"`
	// RequestID is the x-typesafe-request-id response header, when sent.
	RequestID string `json:"-"`
	// RawBody is the body as received, so a field this package does not model
	// stays reachable.
	RawBody json.RawMessage `json:"-"`
}

// Nouls returns the yes/no answers, keyed by question name.
func (r Response) Nouls() map[string]Answer { return r.answersOfType(AnswerNoul) }

// Choices returns the choice answers, keyed by question name.
func (r Response) Choices() map[string]Answer { return r.answersOfType(AnswerChoice) }

// Scores returns the score answers, keyed by question name.
func (r Response) Scores() map[string]Answer { return r.answersOfType(AnswerScore) }

func (r Response) answersOfType(typ string) map[string]Answer {
	answers := make(map[string]Answer)
	for name, answer := range r.Answers {
		if answer.Type == typ {
			answers[name] = answer
		}
	}
	return answers
}

// Options overrides client defaults for one call.
type Options struct {
	// Model overrides the client's default model.
	Model string
	// Headers adds request headers. Authentication and the wire format stay the
	// client's to set, so those keys cannot be overridden here.
	Headers map[string]string
	// ExtraBody adds top-level request-body fields, shallow-merged over state,
	// model, and questions: a colliding key wins, and object values are
	// replaced rather than deep-merged.
	ExtraBody map[string]any
}

// SystemOne asks the named questions about state and returns their answers.
// state is the content the questions refer to: text, an object, or an array.
func (c *Client) SystemOne(ctx context.Context, state any, questions Questions) (Response, error) {
	return c.SystemOneWithOptions(ctx, state, questions, Options{})
}

// SystemOneWithOptions is SystemOne with per-call overrides. Questions are
// validated here, so a malformed question fails locally instead of as a 422
// whose detail names a field rather than the caller's mistake.
func (c *Client) SystemOneWithOptions(
	ctx context.Context,
	state any,
	questions Questions,
	opts Options,
) (Response, error) {
	encoded, err := encodeQuestions(questions)
	if err != nil {
		return Response{}, err
	}
	if state == nil {
		return Response{}, errors.New("system one requires a state value")
	}
	body := map[string]any{
		"state":     state,
		"model":     firstNonEmpty(opts.Model, c.model),
		"questions": encoded,
	}
	maps.Copy(body, opts.ExtraBody)
	// ExtraBody may have replaced the questions, so validate against what was
	// actually sent rather than what was asked.
	if sent, ok := body["questions"].(map[string]any); ok {
		encoded = sent
	}

	payload, requestID, err := c.call(ctx, http.MethodPost, systemOnePath, body, opts.Headers)
	if err != nil {
		return Response{}, err
	}
	var wire responseWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return Response{}, fmt.Errorf("typesafe response for %s could not be decoded: %w", systemOnePath, err)
	}
	resp, err := wire.response(payload, requestID)
	if err != nil {
		return Response{}, err
	}
	if err := resp.validate(encoded); err != nil {
		return Response{}, err
	}
	return resp, nil
}

// validate rejects a response that does not answer every question with an
// answer of that question's own type.
func (r Response) validate(questions map[string]any) error {
	for _, name := range slices.Sorted(maps.Keys(questions)) {
		asked, _ := questions[name].(map[string]any)
		want, _ := asked["type"].(string)
		answer, ok := r.Answers[name]
		if !ok {
			return fmt.Errorf("typesafe response left question %q unanswered", name)
		}
		if want != "" && answer.Type != want {
			return fmt.Errorf("typesafe answer for question %q has type %q, want %q", name, answer.Type, want)
		}
	}
	return nil
}

// APIError is a non-2xx TypeSafe response.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Body is the raw response body. Error unwraps its message; keep it for
	// callers that want the detail a message cannot carry.
	Body []byte
	// RequestID is the x-typesafe-request-id response header, when sent.
	RequestID string
}

func (e *APIError) Error() string {
	msg := errorMessage(e.Body)
	if msg == "" {
		msg = truncateBody(string(e.Body))
		if msg == "" {
			msg = "empty error response"
		}
	}
	out := fmt.Sprintf("%s API error (%d)", providerName, e.Status)
	if msg != "" {
		out += ": " + msg
	}
	if e.RequestID != "" {
		out += " (request_id=" + e.RequestID + ")"
	}
	return out
}

// errorMessage extracts the server's message from the TypeSafe error envelopes:
// {"error": …}, {"message": …}, or the FastAPI {"detail": …} whose validation
// entries become "path: reason". It returns "" when the body carries none.
func errorMessage(body []byte) string {
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if len(env.Error) > 0 && !bytes.Equal(bytes.TrimSpace(env.Error), []byte("null")) {
		if msg := errorObjectMessage(env.Error); msg != "" {
			return msg
		}
	}
	if env.Message != "" {
		return env.Message
	}
	return detailMessage(env.Detail)
}

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

func detailMessage(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}
	var entries []struct {
		Loc []any  `json:"loc"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Msg == "" {
			continue
		}
		if path := locPath(entry.Loc); path != "" {
			parts = append(parts, path+": "+entry.Msg)
			continue
		}
		parts = append(parts, entry.Msg)
	}
	return strings.Join(parts, "; ")
}

// locPath renders a validation error's location as a dotted path with bracketed
// indexes, dropping the leading "body" segment every request error carries.
func locPath(loc []any) string {
	var path strings.Builder
	for _, segment := range loc {
		switch value := segment.(type) {
		case string:
			if value == "body" {
				continue
			}
			if path.Len() > 0 {
				path.WriteByte('.')
			}
			path.WriteString(value)
		case float64:
			fmt.Fprintf(&path, "[%d]", int(value))
		}
	}
	return path.String()
}

// maxErrorBodyChars caps the raw-body fallback so a non-JSON error page can
// never flood an error message.
const maxErrorBodyChars = 2000

// truncateBody trims and caps a raw body for the Error fallback.
func truncateBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if r := []rune(trimmed); len(r) > maxErrorBodyChars {
		return string(r[:maxErrorBodyChars]) + "…"
	}
	return trimmed
}

// call sends one request and returns the response body and the server's request
// id. A non-2xx status becomes an *APIError; util.DoWithRetry handles retrying
// an attempt.
func (c *Client) call(
	ctx context.Context,
	method string,
	path string,
	body any,
	headers map[string]string,
) ([]byte, string, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("encode %s request: %w", path, err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, "", err
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	// Authentication and the request encoding are the client's, not the
	// caller's, so they are applied last.
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := util.DoWithRetry(c.httpClient, req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read %s response: %w", path, err)
	}
	requestID := resp.Header.Get(requestIDHeader)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, requestID, &APIError{Status: resp.StatusCode, Body: raw, RequestID: requestID}
	}
	return raw, requestID, nil
}

// firstNonEmpty returns the first value that is not blank, trimmed, or "".
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// envValue returns the trimmed value of an environment variable. Blank values
// fall through to the next default, so an exported-but-empty variable cannot
// shadow a real one.
func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
