package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageImageRoundTrip(t *testing.T) {
	orig := Message{
		Role:    RoleUser,
		Content: "what is this?",
		Images: []Image{
			{Data: "QUJD", MimeType: "image/png"},
		},
	}
	body, err := json.Marshal(orig)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"images"`)
	assert.Contains(t, string(body), `"mimeType"`)

	var got Message
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, orig, got)
}

func TestMessageWithoutImagesMarshalsPlain(t *testing.T) {
	body, err := json.Marshal(Message{Role: RoleUser, Content: "hi"})
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"images"`)
}

func TestModelConfigCompactionWindow(t *testing.T) {
	tests := []struct {
		name string
		cfg  ModelConfig
		want int
	}{
		{name: "zero context window disables compaction", cfg: ModelConfig{InputLimit: 922_000}, want: 0},
		{name: "unset input limit uses context window", cfg: ModelConfig{ContextWindow: 1_048_576}, want: 1_048_576},
		{
			name: "tighter input limit caps compaction window",
			cfg:  ModelConfig{ContextWindow: 1_050_000, InputLimit: 922_000},
			want: 922_000,
		},
		{
			name: "larger input limit cannot widen context window",
			cfg:  ModelConfig{ContextWindow: 200_000, InputLimit: 922_000},
			want: 200_000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.CompactionWindow())
		})
	}
}
