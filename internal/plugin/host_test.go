package plugin

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/plugin/lib"
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
		writeLuaPlugin(t, root, name, source)
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

func writeLuaPlugin(t *testing.T, root, name, source string) {
	t.Helper()
	writePlugin(t, root, name, "name: "+name, "")
	require.NoError(t, os.WriteFile(filepath.Join(root, name, "init.lua"), []byte(source), 0o644))
}

func TestHostProviderCatalog(t *testing.T) {
	root := t.TempDir()
	const register = `local function register(id)
		phi.provider.register({id=id, api="OpenAI", base_url="https://api.example/v1",
			models={{id="first", context_window=4096, max_tokens=512},
			        {id="up/second", context_window=8192, max_tokens=1024}}})
	end
	`
	writeLuaPlugin(t, root, "a", register+`register("entry"); error("entry failed")`)
	writeLuaPlugin(t, root, "b", register+`function on_load() register("hook"); error("hook failed") end`)
	writeLuaPlugin(t, root, "c", register+`register("unused"); register("acme")`)
	writeLuaPlugin(t, root, "d", register+`register("good")
		function on_unload() register("late") end`)

	host, warnings := Open("v1.0.0", io.Discard, "../../examples/plugins", root)
	require.Len(t, warnings, 3)
	assert.ErrorContains(t, warnings[0], "entry failed")
	assert.ErrorContains(t, warnings[1], "hook failed")
	assert.ErrorContains(t, warnings[2], `plugin "c"`)
	assert.ErrorContains(t, warnings[2], `plugin "acme"`)

	models := host.Models()
	require.Len(t, models, 3)
	chat := models["acme/chat"]
	assert.Equal(t, "acme", chat.Plugin)
	assert.Equal(t, "OpenAI", string(chat.Provider.API))
	assert.Equal(t, "https://api.acme.example/v1", chat.Provider.BaseURL)
	assert.Equal(t, lib.ProviderModel{ID: "chat", ContextWindow: 128000, MaxTokens: 8192}, chat.Model)
	require.NotNil(t, chat.Provider.Auth)
	assert.Equal(t, lib.PKCE{
		ClientID: "phi-cli", AuthorizeURL: "https://auth.acme.example/authorize",
		TokenURL: "https://auth.acme.example/token", Scopes: []string{"chat"},
	}, *chat.Provider.Auth)
	assert.Contains(t, models, "good/first")
	assert.Equal(t, "up/second", models["good/up/second"].Model.ID)
	chat.Provider.Auth.Scopes[0] = "changed"
	assert.Equal(t, []string{"chat"}, host.Models()["acme/chat"].Provider.Auth.Scopes)

	assert.ErrorContains(t, host.Close(), "only during plugin startup")
	assert.Empty(t, host.Models())
}
