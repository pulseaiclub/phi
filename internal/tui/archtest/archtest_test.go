// Package archtest enforces the runtime+plugin dependency red lines as a CI
// test: the runtime never imports a plugin, and plugins never import each
// other. The compiler already enforces both as import cycles for direct
// imports; this test also catches indirect jumps through future glue
// packages.
package archtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const module = "github.com/pulseaiclub/phi/internal/tui"

func imports(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", "list", "-f", "{{join .Imports \"\\n\"}}", pkg).Output()
	require.NoError(t, err, "go list %s", pkg)
	var deps []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			deps = append(deps, line)
		}
	}
	return deps
}

func pluginDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "plugin"))
	require.NoError(t, err)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

func isPluginImport(imp string) bool {
	return strings.HasPrefix(imp, module+"/plugin/")
}

func TestRuntimeDoesNotImportPlugins(t *testing.T) {
	for _, imp := range imports(t, module+"/runtime") {
		assert.Falsef(t, isPluginImport(imp),
			"runtime imports plugin package %s — the dependency direction is plugin → runtime", imp)
	}
}

func TestPluginsDoNotImportEachOther(t *testing.T) {
	for _, dir := range pluginDirs(t) {
		self := module + "/plugin/" + dir
		for _, imp := range imports(t, self) {
			if !isPluginImport(imp) || imp == self {
				continue
			}
			assert.Failf(
				t,
				"plugin cross-import",
				"plugin/%s imports plugin package %s — plugins stay mutually independent; talk over bus msgs or a consumer-shaped port",
				dir,
				imp,
			)
		}
	}
}
