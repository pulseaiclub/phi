package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatAPIError(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		status   int
		body     string
		want     string
	}{
		{
			name:     "openai envelope",
			provider: "LLM",
			status:   401,
			body:     `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`,
			want:     "LLM API error (401): Incorrect API key provided",
		},
		{
			name:     "anthropic envelope",
			provider: "anthropic",
			status:   400,
			body:     `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 > 200000 tokens"}}`,
			want:     "anthropic API error (400): prompt is too long: 210000 > 200000 tokens",
		},
		{
			name:     "gemini envelope",
			provider: "gemini",
			status:   400,
			body:     `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`,
			want:     "gemini API error (400): API key not valid. Please pass a valid API key.",
		},
		{
			name:     "error is a bare string",
			provider: "gemini",
			status:   404,
			body:     `{"error":"model not found"}`,
			want:     "gemini API error (404): model not found",
		},
		{
			name:     "flat message field",
			provider: "LLM",
			status:   413,
			body:     `{"message":"request body too large"}`,
			want:     "LLM API error (413): request body too large",
		},
		{
			name:     "non-json body falls back to raw text",
			provider: "LLM",
			status:   502,
			body:     "gateway unavailable\nplease retry",
			want:     "LLM API error (502): gateway unavailable\nplease retry",
		},
		{
			name:     "json without envelope falls back to raw body",
			provider: "LLM",
			status:   404,
			body:     `{"detail":"not found"}`,
			want:     "LLM API error (404): {\"detail\":\"not found\"}",
		},
		{
			name:     "empty error object falls back to raw body",
			provider: "LLM",
			status:   400,
			body:     `{"error":{}}`,
			want:     "LLM API error (400): {\"error\":{}}",
		},
		{
			name:     "empty body",
			provider: "LLM",
			status:   502,
			body:     "",
			want:     "LLM API error (502): empty error response",
		},
		{
			name:     "whitespace-only body",
			provider: "LLM",
			status:   502,
			body:     "  \n ",
			want:     "LLM API error (502): empty error response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FormatAPIError(tt.provider, tt.status, []byte(tt.body))
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
		})
	}
}

func TestFormatAPIErrorCapsRawFallback(t *testing.T) {
	big := strings.Repeat("x", maxAPIErrorBodyChars+500)
	err := FormatAPIError("LLM", 502, []byte(big))

	msg := err.Error()
	assert.Equal(t, "LLM API error (502): "+strings.Repeat("x", maxAPIErrorBodyChars)+"…", msg)
}

func TestAPIErrorMessage(t *testing.T) {
	assert.Equal(
		t,
		"Overloaded",
		APIErrorMessage([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)),
	)
	assert.Equal(t, "empty error response", APIErrorMessage(nil))
	assert.Equal(t, strings.Repeat("界", maxAPIErrorBodyChars)+"…",
		APIErrorMessage([]byte(strings.Repeat("界", maxAPIErrorBodyChars+1))))
}
