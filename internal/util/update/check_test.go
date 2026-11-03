package update_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/update"
)

func TestCheckUsesCacheWhenAvailable(t *testing.T) {
	t.Setenv("PHI_SKIP_VERSION_CHECK", "")

	dir := t.TempDir()
	cache := filepath.Join(dir, "update-check.json")
	payload, _ := json.Marshal(map[string]any{
		"checked_at": time.Now().UTC(),
		"current_at": "v0.1.0",
		"latest":     "v0.2.0",
		"url":        "https://example.com/releases/tag/v0.2.0",
	})
	require.NoError(t, os.WriteFile(cache, payload, 0o600))

	info := update.Check(t.Context(), update.CheckOptions{
		Current:  "v0.1.0",
		CacheDir: dir,
	})
	require.True(t, info.Available, "expected cached update, got %+v", info)
	require.Equal(t, "v0.2.0", info.Latest)
}

func TestSkipCheckEnv(t *testing.T) {
	t.Setenv("PHI_SKIP_VERSION_CHECK", "1")
	info := update.Check(t.Context(), update.CheckOptions{Current: "v0.1.0"})
	require.False(t, info.Available, "expected skip, got %+v", info)
}
