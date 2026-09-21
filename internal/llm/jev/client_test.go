package jev

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient points a client at srv with the environment cleared, so the
// developer's shell cannot leak credentials or a default model into a test.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	clearEnv(t)
	client, err := NewClient(Config{APIKey: "test-key", BaseURL: srv.URL, HTTPClient: srv.Client()})
	require.NoError(t, err)
	return client
}

func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("TYPESAFE_BASE_URL", "")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "")
}

func TestNewClientDefaults(t *testing.T) {
	clearEnv(t)

	client, err := NewClient(Config{APIKey: "key"})

	require.NoError(t, err)
	assert.Equal(t, DefaultBaseURL, client.baseURL)
	assert.Equal(t, DefaultModel, client.model)
	assert.NotNil(t, client.httpClient)
}

func TestNewClientResolvesConfigOverEnvironment(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "env-key")
	t.Setenv("TYPESAFE_BASE_URL", "https://env.test/")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "env-model")

	client, err := NewClient(Config{APIKey: "key", BaseURL: "https://cfg.test/v1/", Model: "cfg-model"})
	require.NoError(t, err)
	assert.Equal(t, "key", client.apiKey)
	assert.Equal(t, "https://cfg.test/v1", client.baseURL) // trailing slash dropped
	assert.Equal(t, "cfg-model", client.model)

	// Blank config fields fall through to trimmed environment values.
	client, err = NewClient(Config{APIKey: " ", BaseURL: " ", Model: ""})
	require.NoError(t, err)
	assert.Equal(t, "env-key", client.apiKey)
	assert.Equal(t, "https://env.test", client.baseURL)
	assert.Equal(t, "env-model", client.model)
}

func TestNewClientRequiresAPIKey(t *testing.T) {
	clearEnv(t)

	_, err := NewClient(Config{BaseURL: "https://api.test"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Config.APIKey")
	assert.Contains(t, err.Error(), "TYPESAFE_API_KEY")
}

const systemOneBody = `{
	"model": "jev-latest",
	"usage": {"input_tokens": 120, "output_tokens": 12},
	"answers": {
		"billing": {"type": "noul", "noul": 0.98},
		"tone": {
			"type": "choice", "choice": "angry", "confidence": 0.9,
			"probabilities": {"angry": 0.9, "calm": 0.1}
		},
		"urgency": {
			"type": "score", "score": 1.7, "confidence": 0.8,
			"legend": {"0": "Can wait", "1": "Needs attention this week"},
			"probabilities": {"0": 0.3, "1": 0.7}
		}
	}
}`

func TestSystemOne(t *testing.T) {
	var (
		gotPath        string
		gotAuth        string
		gotContentType string
		gotBody        map[string]any
		decodeErr      error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		decodeErr = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set(requestIDHeader, "req-123")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, systemOneBody)
	}))
	defer srv.Close()

	resp, err := newTestClient(t, srv).SystemOne(
		t.Context(),
		map[string]any{"subject": "Duplicate charge"},
		Questions{
			"billing": Noul{
				Instructions: "Is this about billing?",
				Criteria:     &NoulCriteria{True: "credit card charge", False: "anything else"},
			},
			"tone": Choice{
				Criteria: map[string]any{"calm": nil, "angry": "a hostile message"},
			},
			"urgency": Score{Criteria: []any{"Can wait", "Needs attention this week"}},
		},
	)

	require.NoError(t, err)
	require.NoError(t, decodeErr)
	assert.Equal(t, systemOnePath, gotPath)
	assert.Equal(t, "Bearer test-key", gotAuth)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, DefaultModel, gotBody["model"])
	assert.Equal(t, map[string]any{"subject": "Duplicate charge"}, gotBody["state"])
	assert.Equal(t, map[string]any{
		"billing": map[string]any{
			"type":         "noul",
			"instructions": "Is this about billing?",
			"criteria":     map[string]any{"true": "credit card charge", "false": "anything else"},
		},
		"tone": map[string]any{
			"type":     "choice",
			"criteria": map[string]any{"calm": nil, "angry": "a hostile message"},
		},
		"urgency": map[string]any{
			"type":     "score",
			"criteria": []any{"Can wait", "Needs attention this week"},
		},
	}, gotBody["questions"])

	assert.Equal(t, "req-123", resp.RequestID)
	assert.Equal(t, "jev-latest", resp.Model)
	assert.Equal(t, Usage{InputTokens: 120, OutputTokens: 12}, resp.Usage)
	assert.JSONEq(t, systemOneBody, string(resp.RawBody))
	assert.InDelta(t, 0.98, resp.Nouls()["billing"].Noul, 0.0001)

	choice := resp.Choices()["tone"]
	assert.Equal(t, "angry", choice.Choice)
	assert.InDelta(t, 0.9, choice.Confidence, 0.0001)
	assert.Equal(t, map[string]float64{"angry": 0.9, "calm": 0.1}, choice.Probabilities)

	score := resp.Scores()["urgency"]
	assert.InDelta(t, 1.7, score.Score, 0.0001)
	// Score levels stay string-keyed: they are JSON object keys on the wire.
	assert.Equal(t, "Needs attention this week", score.Legend["1"])
	assert.Equal(t, map[string]float64{"0": 0.3, "1": 0.7}, score.Probabilities)
}

func TestSystemOneOverrides(t *testing.T) {
	var (
		gotBody  map[string]any
		gotTrace string
		gotAuth  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTrace = r.Header.Get("X-Trace")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(
			w,
			`{"model":"jev-mini","usage":{"input_tokens":1,"output_tokens":2},"answers":{"risk":{"type":"noul","noul":0.1}}}`,
		)
	}))
	defer srv.Close()

	resp, err := newTestClient(t, srv).SystemOneWithOptions(t.Context(), "text", Questions{"risk": Noul{}}, Options{
		Model:     "jev-mini",
		Headers:   map[string]string{"X-Trace": "abc", "Authorization": "Bearer not-mine"},
		ExtraBody: map[string]any{"temperature": 0, "state": "overridden"},
	})

	require.NoError(t, err)
	assert.Equal(t, "jev-mini", resp.Model)
	assert.Equal(t, "abc", gotTrace)
	// Authentication is the client's to set, not the caller's.
	assert.Equal(t, "Bearer test-key", gotAuth)
	assert.Equal(t, "jev-mini", gotBody["model"])
	// extra_body merges last, so it replaces the state it collides with.
	assert.Equal(t, "overridden", gotBody["state"])
	assert.Equal(t, float64(0), gotBody["temperature"])
}

func TestSystemOnePassesRawQuestionsThrough(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(
			w,
			`{"model":"m","usage":{"input_tokens":1,"output_tokens":2},"answers":{"risk":{"type":"rank","order":["low","high"]}}}`,
		)
	}))
	defer srv.Close()

	resp, err := newTestClient(t, srv).SystemOne(t.Context(), "text", Questions{
		"risk": RawQuestion{"type": "rank", "criteria": []any{"low", "high"}},
	})

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "rank", "criteria": []any{"low", "high"}},
		gotBody["questions"].(map[string]any)["risk"])
	assert.Equal(t, "rank", resp.Answers["risk"].Type)
}

func TestSystemOneRejectsBadQuestionsBeforeSending(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	client := newTestClient(t, srv)

	tests := []struct {
		name      string
		questions Questions
		want      string
	}{
		{
			name:      "no questions",
			questions: Questions{},
			want:      "at least one question is required",
		},
		{
			name:      "nil question",
			questions: Questions{"tone": nil},
			want:      `question "tone" is nil`,
		},
		{
			name:      "typed nil question",
			questions: Questions{"tone": (*Noul)(nil)},
			want:      `question "tone" is nil`,
		},
		{
			name:      "choice without criteria",
			questions: Questions{"tone": Choice{}},
			want:      `question "tone": choice question requires "criteria"`,
		},
		{
			name:      "score without criteria",
			questions: Questions{"urgency": Score{}},
			want:      `question "urgency": score question has no criteria`,
		},
		{
			name:      "raw question without a type",
			questions: Questions{"x": RawQuestion{}},
			want:      `question "x": raw question requires a nonempty string "type"`,
		},
		{
			name:      "raw question with a non-string type",
			questions: Questions{"x": RawQuestion{"type": 42}},
			want:      "nonempty string",
		},
		{
			name:      "raw choice without criteria",
			questions: Questions{"x": RawQuestion{"type": "choice"}},
			want:      `question "x": choice question requires "criteria"`,
		},
		{
			name:      "raw score with empty criteria",
			questions: Questions{"x": RawQuestion{"type": "score", "criteria": []any{}}},
			want:      `question "x": score question has no criteria`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.SystemOne(t.Context(), "text", tt.questions)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}

	_, err := client.SystemOne(t.Context(), nil, Questions{"risk": Noul{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a state value")

	// Every rejection is local: no request may reach the server.
	assert.Zero(t, hits)
}

func TestEncodeQuestionsOmitsUnsetOptionals(t *testing.T) {
	encoded, err := encodeQuestions(Questions{
		"billing": Noul{Instructions: []any{"Is this about billing?"}},
		"tone":    Choice{Criteria: map[string]any{"calm": nil, "angry": "a hostile message"}},
		"urgency": Score{Instructions: "How urgent is this?", Criteria: []any{"Can wait", "Today"}},
	})

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"billing": map[string]any{"type": "noul", "instructions": []any{"Is this about billing?"}},
		"tone": map[string]any{
			"type":     "choice",
			"criteria": map[string]any{"calm": nil, "angry": "a hostile message"},
		},
		"urgency": map[string]any{
			"type":         "score",
			"instructions": "How urgent is this?",
			"criteria":     []any{"Can wait", "Today"},
		},
	}, encoded)
}

func TestSystemOneRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unanswered question",
			body: `{"model":"m","usage":{},"answers":{}}`,
			want: `typesafe response left question "risk" unanswered`,
		},
		{
			name: "answer contradicts the question",
			body: `{"model":"m","usage":{},"answers":{"risk":{"type":"score","score":1,"confidence":0.9,` +
				`"legend":{"0":"Can wait"},"probabilities":{"0":1}}}}`,
			want: `typesafe answer for question "risk" has type "score", want "noul"`,
		},
		{
			name: "answer omits a required field",
			body: `{"model":"m","usage":{},"answers":{"risk":{"type":"noul"}}}`,
			want: `typesafe answer for question "risk" has no "noul"`,
		},
		{
			name: "body is not JSON",
			body: "not json",
			want: "could not be decoded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv).SystemOne(t.Context(), "text", Questions{"risk": Noul{}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestSystemOneValidatesTheQuestionsExtraBodySends(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Answers the question the caller asked, not the one extra_body replaced
		// it with: validation has to follow the body that was sent.
		fmt.Fprint(w, `{"model":"m","usage":{},"answers":{"risk":{"type":"noul","noul":1}}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).SystemOneWithOptions(t.Context(), "text", Questions{"risk": Noul{}}, Options{
		ExtraBody: map[string]any{
			"questions": map[string]any{"other": map[string]any{"type": "noul"}},
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `left question "other" unanswered`)
}

func TestAPIError(t *testing.T) {
	const detail = `{"detail":[{"loc":["body","questions","urgency","score","criteria"],"msg":"Field required","type":"missing"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(requestIDHeader, "req-422")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, detail)
	}))
	defer srv.Close()

	_, err := newTestClient(
		t,
		srv,
	).SystemOne(t.Context(), "text", Questions{"urgency": Score{Criteria: []any{"Can wait"}}})

	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnprocessableEntity, apiErr.Status)
	assert.Equal(t, "req-422", apiErr.RequestID)
	assert.JSONEq(t, detail, string(apiErr.Body))
	assert.Equal(
		t,
		"typesafe API error (422): questions.urgency.score.criteria: Field required (request_id=req-422)",
		err.Error(),
	)
}

func TestSystemOneRetriesServerErrors(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(
			w,
			`{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{"risk":{"type":"noul","noul":0.5}}}`,
		)
	}))
	defer srv.Close()

	resp, err := newTestClient(t, srv).SystemOne(t.Context(), "text", Questions{"risk": Noul{}})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	assert.InDelta(t, 0.5, resp.Nouls()["risk"].Noul, 0.0001)
}
