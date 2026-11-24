package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleCatalogJSON = `{
  "provA": {
    "models": {
      "alpha": {
        "status": "",
        "limit": {"context": 1048576, "output": 131072},
        "modalities": {"input": ["text", "image"]}
      }
    }
  },
  "provB": {
    "models": {
      "beta": {
        "status": "deprecated",
        "limit": {"context": 128000, "output": 16384},
        "modalities": {"input": ["text"]}
      }
    }
  }
}`

func TestRunGeneratesSpecsAndWarnsDeprecated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleCatalogJSON))
	}))
	defer srv.Close()

	dir := t.TempDir()
	sourcesPath := filepath.Join(dir, "sources.json")
	require.NoError(t, os.WriteFile(sourcesPath, []byte(`{"beta":"provB/beta","alpha":"provA/alpha"}`), 0o644))
	outPath := filepath.Join(dir, "specs_gen.go")

	var errBuf bytes.Buffer
	require.NoError(t, run(t.Context(), sourcesPath, srv.URL, outPath, &errBuf))
	assert.Contains(t, errBuf.String(), `warning: preset "beta" (provB/beta) is marked deprecated`)

	generated, err := os.ReadFile(outPath)
	require.NoError(t, err)
	text := string(generated)
	assert.Contains(t, text, `"alpha": {ContextWindow: 1_048_576, MaxTokens: 131_072, ImageEnabled: true}`)
	assert.Contains(t, text, `"beta":  {ContextWindow: 128_000, MaxTokens: 16_384, ImageEnabled: false}`)
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name    string
		sources string
		status  int
		catalog string
		wantErr string
	}{
		{
			name:    "invalid catalogID",
			sources: `{"a":"noslash"}`,
			status:  http.StatusOK,
			catalog: `{}`,
			wantErr: `invalid catalogID "noslash"`,
		},
		{
			name:    "empty model in catalogID",
			sources: `{"a":"prov/"}`,
			status:  http.StatusOK,
			catalog: `{}`,
			wantErr: `invalid catalogID "prov/"`,
		},
		{
			name:    "missing provider",
			sources: `{"a":"nope/a"}`,
			status:  http.StatusOK,
			catalog: `{}`,
			wantErr: `provider "nope" not found`,
		},
		{
			name:    "missing model",
			sources: `{"a":"prov/missing"}`,
			status:  http.StatusOK,
			catalog: `{"prov": {"models": {}}}`,
			wantErr: `model "missing" not found`,
		},
		{
			name:    "zero context",
			sources: `{"a":"prov/a"}`,
			status:  http.StatusOK,
			catalog: `{"prov": {"models": {"a": {"limit": {"context": 0, "output": 100}}}}}`,
			wantErr: `invalid context window 0`,
		},
		{
			name:    "negative output limit",
			sources: `{"a":"prov/a"}`,
			status:  http.StatusOK,
			catalog: `{"prov": {"models": {"a": {"limit": {"context": 1000, "output": -100}}}}}`,
			wantErr: `invalid output limit -100`,
		},
		{
			name:    "http failure",
			sources: `{"a":"prov/a"}`,
			status:  http.StatusBadGateway,
			catalog: `bad gateway`,
			wantErr: `unexpected HTTP status`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.catalog))
			}))
			defer srv.Close()

			dir := t.TempDir()
			sourcesPath := filepath.Join(dir, "sources.json")
			require.NoError(t, os.WriteFile(sourcesPath, []byte(tt.sources), 0o644))
			outPath := filepath.Join(dir, "specs_gen.go")

			var errBuf bytes.Buffer
			err := run(t.Context(), sourcesPath, srv.URL, outPath, &errBuf)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
