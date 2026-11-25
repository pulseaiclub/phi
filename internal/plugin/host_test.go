package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostLifecycle(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"a": `shared = true
			function on_load() print("load", 1) end
			function on_unload() print("close a") end`,
		"b": `function on_load() error("load failed") end
			function on_unload() print("unexpected") end`,
		"c": `assert(shared == nil)
			io.write("load c\n")
			function on_unload() print("close c"); error("close failed") end`,
		"d": `function`,
	} {
		dir := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("name: "+name), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "init.lua"), []byte(source), 0o644))
	}
	var out bytes.Buffer
	host, warnings := Open("v1.0.0", &out, root)
	require.Len(t, warnings, 2)
	assert.ErrorContains(t, warnings[0], "load failed")
	assert.ErrorContains(t, warnings[1], "init.lua")
	assert.Equal(t, "load\t1\nload c\n", out.String())
	assert.ErrorContains(t, host.Close(), "close failed")
	require.NoError(t, host.Close())
	assert.Equal(t, "load\t1\nload c\nclose c\nclose a\n", out.String())
}
