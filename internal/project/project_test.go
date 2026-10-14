package project

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discoverInTempHome runs Discover("") with HOME redirected to a temp dir so
// tests never touch the real ~/.phi.
func discoverInTempHome(t *testing.T) *Project {
	t.Helper()
	home := t.TempDir()
	// os.UserHomeDir uses HOME on Unix and USERPROFILE on Windows.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := Discover("")
	require.NoError(t, err)
	return p
}

func TestDiscoverCreatesGlobalDirs(t *testing.T) {
	p := discoverInTempHome(t)

	for _, dir := range []string{
		p.Global().Root(),
		p.Global().BinDir(),
		p.Global().SkillsDir(),
		p.Global().ExtensionsDir(),
		p.Global().SessionBase(),
		p.Global().JobsDir(),
	} {
		info, err := os.Stat(dir)
		assert.NoErrorf(t, err, "expected dir %q to exist", dir)
		if err == nil {
			assert.Truef(t, info.IsDir(), "expected %q to be a directory", dir)
		}
	}
}

func TestExtensionsDirPath(t *testing.T) {
	p := discoverInTempHome(t)
	assert.Equal(t, filepath.Join(p.Global().Root(), "extensions"), p.Global().ExtensionsDir())
}

func TestLookBinPrefersBinDir(t *testing.T) {
	p := discoverInTempHome(t)
	fake := filepath.Join(p.Global().BinDir(), "rg")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))

	got, err := p.Global().LookBin("rg")
	require.NoError(t, err)
	assert.Equal(t, fake, got)
}

func TestLookBinFindsWindowsExe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows binaries are installed with an .exe suffix")
	}
	p := discoverInTempHome(t)
	exe := filepath.Join(p.Global().BinDir(), "fd.exe")
	require.NoError(t, os.WriteFile(exe, []byte("MZ"), 0o755))

	got, err := p.Global().LookBin("fd")
	require.NoError(t, err)
	assert.Equal(t, exe, got)
}

func TestLookBinFallsBackToPATH(t *testing.T) {
	p := discoverInTempHome(t)
	got, err := p.Global().LookBin("sh")
	if err != nil {
		t.Skip(err.Error())
	}
	require.NotEmpty(t, got)
	assert.NotContains(t, got, p.Global().BinDir())
}

func TestProjectDirs(t *testing.T) {
	p := discoverInTempHome(t)
	assert.Equal(t, filepath.Join(p.Root(), ".phi", "extensions"), p.ExtensionsDir())
	assert.Equal(t, filepath.Join(p.Root(), ".phi", "mcp.json"), p.MCPConfigFile())
}

func TestLoadConfigDefaults(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: deepseek-chat
    api_key: sk-test
`), 0o644))

	require.NoError(t, p.LoadConfig())
	cfg := p.Config()

	assert.Equal(t, "deepseek-chat", cfg.Model().Name)
	assert.Equal(t, "sk-test", cfg.Model().APIKey)
	assert.Equal(t, "https://api.openai.com/v1", cfg.Model().BaseURL)
	assert.Equal(t, p.Global().SkillsDir(), cfg.SkillPath)
	// SkillPaths starts at the configured root; ~/.agents/skills follows.
	require.NotEmpty(t, cfg.SkillPaths)
	assert.Equal(t, p.Global().SkillsDir(), cfg.SkillPaths[0])
	// Model() carries the roots for agent.NewEngine.
	assert.Equal(t, cfg.SkillPaths, cfg.Model().SkillPaths)
}

func TestConfigModelRoundBudget(t *testing.T) {
	for name, budget := range map[string]int{"unset": 0, "configured": 7} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(`
models:
  - name: first
    api_key: first-key
  - name: selected
    api_key: selected-key
    default: true
skill_path: /skills
max_rounds: 7
`), 0o600))
			cfg, err := parseConfigFile(path)
			require.NoError(t, err)
			assert.Equal(t, 7, cfg.MaxRounds)
			cfg.MaxRounds = budget

			selected := cfg.Model()
			assert.Equal(t, "selected", selected.Name)
			assert.Equal(t, budget, selected.MaxRounds)
			// parseConfigFile reads keys only; loadConfig resolves the roots.
			assert.Equal(t, "/skills", cfg.SkillPath)
			assert.Nil(t, selected.SkillPaths)
			for _, m := range cfg.AllModels() {
				assert.Equal(t, budget, m.MaxRounds)
				found, ok := cfg.FindModel(m.Name)
				require.True(t, ok)
				assert.Equal(t, m, found)
			}
			_, ok := cfg.FindModel("missing")
			assert.False(t, ok)

			selected.MaxRounds = 99
			all := cfg.AllModels()
			all[0].MaxRounds = 99
			assert.Equal(t, budget, cfg.Model().MaxRounds)
			for _, raw := range cfg.Models {
				assert.Zero(t, raw.MaxRounds)
				assert.Empty(t, raw.SkillPaths)
			}
		})
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: file-model
    api_key: file-key
skill_path: /from/file
`), 0o644))

	t.Setenv("PHI_MODEL", "env-model")
	t.Setenv("PHI_API_KEY", "env-key")
	t.Setenv("PHI_BASE_URL", "https://env.example/v1")
	t.Setenv("PHI_SKILL_PATH", "/from/env")

	require.NoError(t, p.LoadConfig())
	cfg := p.Config()
	assert.Equal(t, "/from/env", cfg.SkillPaths[0], "the env path is the highest-priority root")
	assert.Equal(t, "env-model", cfg.Model().Name)
	assert.Equal(t, "env-key", cfg.Model().APIKey)
	assert.Equal(t, "https://env.example/v1", cfg.Model().BaseURL)
	assert.Equal(t, "/from/env", cfg.SkillPath)
}

// TestSkillRootsOrder pins the root order and the ~ expansion: the configured
// directory first, then ~/.agents/skills, then .agents/skills from cwd up to
// the enclosing git root.
func TestSkillRootsOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	repo := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	nested := filepath.Join(repo, "pkg", "deep")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	p, err := Discover(nested)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: m
    api_key: k
skill_path: ~/my-skills
`), 0o644))
	require.NoError(t, p.LoadConfig())

	cfg := p.Config()
	assert.Equal(t, filepath.Join(home, "my-skills"), cfg.SkillPath)
	assert.Equal(t, []string{
		filepath.Join(home, "my-skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(nested, ".agents", "skills"),
		filepath.Join(repo, "pkg", ".agents", "skills"),
		filepath.Join(repo, ".agents", "skills"),
	}, cfg.SkillPaths)
}

// expandHome only rewrites a leading "~"; everything else passes through.
func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	assert.Equal(t, home, expandHome("~"))
	assert.Equal(t, filepath.Join(home, "a", "b"), expandHome("  ~/a/b  "))
	assert.Equal(t, "/abs/path", expandHome("/abs/path"))
	assert.Equal(t, "relative/path", expandHome("relative/path"))
	// ~user (another account's home) is a real path form; leave it alone.
	assert.Equal(t, "~other/skills", expandHome("~other/skills"))
	assert.Empty(t, expandHome(""))
}

// A .git file (worktree or submodule checkout) marks the root just like a
// .git directory.
func TestIsGitRoot(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, isGitRoot(dir))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	assert.True(t, isGitRoot(dir))

	require.NoError(t, os.Remove(filepath.Join(dir, ".git")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: ../real\n"), 0o644))
	assert.True(t, isGitRoot(dir))
}

// PHI_SKILL_PATH takes the same expansion as the config file value.
func TestLoadConfigEnvSkillPathExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "env-model")
	t.Setenv("PHI_API_KEY", "env-key")
	t.Setenv("PHI_SKILL_PATH", "~/env-skills")

	p, err := Discover("")
	require.NoError(t, err)
	require.NoError(t, p.LoadConfig())
	assert.Equal(t, filepath.Join(home, "env-skills"), p.Config().SkillPath)
}

func TestLoadConfigMissingAPIKey(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte("models:\n  - name: x\n"), 0o644))
	t.Setenv("PHI_API_KEY", "")

	err := p.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key")
}

func TestLoadConfigConfigFileMissing(t *testing.T) {
	// Env-only setup: no config file, all values from environment.
	p := discoverInTempHome(t)
	t.Setenv("PHI_MODEL", "env-model")
	t.Setenv("PHI_API_KEY", "env-key")

	require.NoError(t, p.LoadConfig())
	assert.Equal(t, "env-model", p.Config().Model().Name)
	assert.Equal(t, "interactive", string(p.Config().Permissions.Mode))
}

func TestLoadConfigPermissions(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: m
    api_key: k
permissions:
  mode: headless-strict
  ask_timeout_sec: 30
  workspace_only_writes: true
  bash:
    default: ask
    allow:
      - '^echo\b'
    deny:
      - '\bsudo\b'
`), 0o644))

	require.NoError(t, p.LoadConfig())
	perm := p.Config().Permissions
	assert.Equal(t, "headless-strict", string(perm.Mode))
	assert.Equal(t, 30, perm.AskTimeoutSec)
	assert.Equal(t, []string{`^echo\b`}, perm.BashAllow)
	assert.Equal(t, []string{`\bsudo\b`}, perm.BashDeny)
	assert.True(t, p.Config().Agents.Enabled) // default on when agents: absent
}

func TestLoadConfigAgentsEnabled(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: m
    api_key: k
agents:
  enabled: true
`), 0o644))

	require.NoError(t, p.LoadConfig())
	assert.True(t, p.Config().Agents.Enabled)
}

func TestLoadConfigAgentsDisabled(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: m
    api_key: k
agents:
  enabled: false
`), 0o644))

	require.NoError(t, p.LoadConfig())
	assert.False(t, p.Config().Agents.Enabled)
}

func TestLoadConfigAgentsModelsOmitsEnabledKeepsDefaultOn(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: m
    api_key: k
agents:
  models:
    explore: m
`), 0o644))

	require.NoError(t, p.LoadConfig())
	assert.True(t, p.Config().Agents.Enabled)
	assert.Equal(t, "m", p.Config().Agents.Models.Explore)
}

func TestLoadConfigAgentsRoleModels(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: parent
    api_key: k
    default: true
  - name: cheap
    api_key: k
  - name: strong
    api_key: k
agents:
  enabled: true
  models:
    explore: cheap
    review: strong
`), 0o644))

	require.NoError(t, p.LoadConfig())
	cfg := p.Config()
	assert.Equal(t, "cheap", cfg.Agents.Models.Explore)
	assert.Equal(t, "strong", cfg.Agents.Models.Review)
	assert.Empty(t, cfg.Agents.Models.Worker)
}

func TestLoadConfigAgentsRoleModelUnknownKept(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: parent
    api_key: k
agents:
  models:
    explore: missing-model
`), 0o644))

	require.NoError(t, p.LoadConfig())
	assert.Equal(t, "missing-model", p.Config().Agents.Models.Explore)
	_, ok := p.Config().FindModel("missing-model")
	assert.False(t, ok)
}

func TestLoadConfigScalarOrInlineListForms(t *testing.T) {
	// The old line scanner only understood block lists (and treated an inline
	// sequence as one literal string); real YAML handles scalar and flow forms.
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: m
    api_key: k
permissions:
  bash:
    allow: "go test ./..."
    deny: ['rm -rf', 'sudo']
`), 0o644))

	require.NoError(t, p.LoadConfig())
	perm := p.Config().Permissions
	assert.Equal(t, []string{"go test ./..."}, perm.BashAllow)
	assert.Equal(t, []string{"rm -rf", "sudo"}, perm.BashDeny)
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	// A malformed config must fail loudly instead of silently degrading to
	// defaults (the old line scanner ignored malformed lines).
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(
		"models:\n  - name: m\n    api_key: k\npermissions: [unclosed\n",
	), 0o644))

	require.Error(t, p.LoadConfig())
}

func TestLoadConfigModelsFlat(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: p
    api_key: pk
    base_url: https://primary.example/v1
    context_window: 1000
    default: true
  - name: a1
    api_key: ak1
    base_url: https://a1.example/v1
  - name: a2
    api_key: ak2
    base_url: https://a2.example/v1
    context_window: 2000
`), 0o644))

	require.NoError(t, p.LoadConfig())
	cfg := p.Config()
	require.Len(t, cfg.Models, 3)
	assert.Equal(t, "p", cfg.Models[0].Name)
	assert.Equal(t, "ak1", cfg.Models[1].APIKey)
	assert.Equal(t, 2000, cfg.Models[2].ContextWindow)
	assert.Equal(t, "p", cfg.DefaultModel)

	// Model() returns the entry marked default, with the skill roots applied.
	m := cfg.Model()
	assert.Equal(t, "p", m.Name)
	assert.Contains(t, m.SkillPaths, p.Global().SkillsDir())

	// Models() lists every entry with the skill roots applied.
	models := cfg.AllModels()
	require.Len(t, models, 3)
	for _, mm := range models {
		assert.Contains(t, mm.SkillPaths, p.Global().SkillsDir())
	}

	// FindModel returns the full per-model connection config.
	a2, ok := cfg.FindModel("a2")
	require.True(t, ok)
	assert.Equal(t, "https://a2.example/v1", a2.BaseURL)
	_, ok = cfg.FindModel("nope")
	assert.False(t, ok)
}

func TestLoadConfigDefaultFallsBackToFirst(t *testing.T) {
	// No entry marked default → the first entry wins.
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: first
    api_key: k1
  - name: second
    api_key: k2
`), 0o644))

	require.NoError(t, p.LoadConfig())
	cfg := p.Config()
	assert.Empty(t, cfg.DefaultModel)
	assert.Equal(t, "first", cfg.Model().Name)
}

func TestLoadConfigAppliesDeepSeekPresets(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`
models:
  - name: deepseek-flash
    api_key: sk-flash
  - name: deepseek-v4-pro
    api_key: sk-pro
    context_window: 50000
  - name: custom-model
    api_key: sk-custom
    base_url: https://custom.example/v1
`), 0o644))

	require.NoError(t, p.LoadConfig())
	cfg := p.Config()

	// deepseek-flash: only name + api_key → preset fills the rest.
	flash, ok := cfg.FindModel("deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "https://api.deepseek.com", flash.BaseURL)
	assert.Equal(t, 1_000_000, flash.ContextWindow)
	assert.True(t, flash.ImageEnabled, "flash supports image input")

	// deepseek-v4-pro: explicit context_window overrides the preset; image
	// stays off because v4-pro has no image understanding.
	pro, ok := cfg.FindModel("deepseek-v4-pro")
	require.True(t, ok)
	assert.Equal(t, "https://api.deepseek.com", pro.BaseURL)
	assert.Equal(t, 50_000, pro.ContextWindow, "explicit context_window overrides preset")
	assert.False(t, pro.ImageEnabled, "v4-pro has no image understanding")

	// Unknown names keep the generic OpenAI fallback and no preset.
	custom, ok := cfg.FindModel("custom-model")
	require.True(t, ok)
	assert.Equal(t, "https://custom.example/v1", custom.BaseURL)
	assert.Zero(t, custom.ContextWindow)
	assert.False(t, custom.ImageEnabled)
}
