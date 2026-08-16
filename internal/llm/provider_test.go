package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProvider(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Provider
	}{
		{name: "empty uses auto", want: ProviderAuto},
		{name: "auto", input: "auto", want: ProviderAuto},
		{name: "trim and normalize", input: " OpenAI ", want: ProviderOpenAI},
		{name: "anthropic", input: "anthropic", want: ProviderAnthropic},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseProvider(test.input)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}

	_, err := ParseProvider("llama")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown provider "llama"`)
}

func TestResolveProvider(t *testing.T) {
	tests := []struct {
		name string
		cfg  ModelConfig
		want Provider
	}{
		{
			name: "explicit anthropic overrides detection",
			cfg:  ModelConfig{Name: "gpt-5", BaseURL: "https://gateway.example.com/v1", Provider: ProviderAnthropic},
			want: ProviderAnthropic,
		},
		{
			name: "explicit openai overrides model name",
			cfg:  ModelConfig{Name: "claude-sonnet", BaseURL: "https://api.anthropic.com", Provider: ProviderOpenAI},
			want: ProviderOpenAI,
		},
		{
			name: "auto keeps anthropic model detection",
			cfg:  ModelConfig{Name: "claude-sonnet", BaseURL: "https://gateway.example.com/v1", Provider: ProviderAuto},
			want: ProviderAnthropic,
		},
		{
			name: "empty keeps anthropic URL detection",
			cfg:  ModelConfig{Name: "gpt-5", BaseURL: "https://api.anthropic.com", Provider: ""},
			want: ProviderAnthropic,
		},
		{
			name: "auto defaults to openai compatible",
			cfg:  ModelConfig{Name: "gpt-5", BaseURL: "https://gateway.example.com/v1", Provider: ProviderAuto},
			want: ProviderOpenAI,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, ResolveProvider(test.cfg))
		})
	}
}
