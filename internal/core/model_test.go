package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
