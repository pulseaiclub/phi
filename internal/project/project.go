package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/pulseaiclub/phi/internal/orca"
)

// GlobalLayout describes the global phi home directory (~/.phi).
type GlobalLayout struct {
	root string
}

// Root returns the global phi home directory (~/.phi).
func (g GlobalLayout) Root() string { return g.root }

// ConfigFile returns the path to the global config file.
func (g GlobalLayout) ConfigFile() string { return filepath.Join(g.root, "config.yaml") }

// BinDir returns the directory for downloaded tool binaries.
func (g GlobalLayout) BinDir() string { return filepath.Join(g.root, "bin") }

// LookBin returns name from BinDir if present, otherwise PATH.
func (g GlobalLayout) LookBin(name string) (string, error) {
	for _, path := range binCandidates(filepath.Join(g.BinDir(), name)) {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is not available: install to ~/.phi/bin or PATH", name)
	}
	return p, nil
}

// binCandidates returns the paths to probe inside a single directory.
// Downloads save Windows binaries as "<name>.exe", so an extensionless stat
// misses an fd/rg that is sitting right there in ~/.phi/bin. exec.LookPath
// appends PATHEXT on its own, so only the bin dir needs the extra variant.
func binCandidates(path string) []string {
	if runtime.GOOS == "windows" && filepath.Ext(path) == "" {
		return []string{path + ".exe", path}
	}
	return []string{path}
}

// SkillsDir returns the directory for SKILL.md files.
func (g GlobalLayout) SkillsDir() string { return filepath.Join(g.root, "skills") }

// ExtensionsDir returns the directory for Go extension plugins (~/.phi/extensions).
func (g GlobalLayout) ExtensionsDir() string { return filepath.Join(g.root, "extensions") }

// SessionBase returns the root directory for persisted sessions.
func (g GlobalLayout) SessionBase() string { return filepath.Join(g.root, "session") }

// JobsDir returns the directory for sub-agent job artifacts.
func (g GlobalLayout) JobsDir() string { return filepath.Join(g.root, "jobs") }

// SessionDir returns the per-cwd session storage directory
// (~/.phi/session/<encoded-cwd>/).
func (p *Project) SessionDir() string {
	return ProjectSessionDir(p.global.SessionBase(), p.root)
}

// JobsDir returns ~/.phi/jobs for sub-agent job artifacts.
func (p *Project) JobsDir() string {
	return p.global.JobsDir()
}

// ExtensionsDir returns <root>/.phi/extensions, the per-project extensions
// directory (user extensions live under Global().ExtensionsDir()).
func (p *Project) ExtensionsDir() string {
	return filepath.Join(p.root, ".phi", "extensions")
}

// MCPConfigFile returns <root>/.phi/mcp.json, the per-project MCP config
// file (the user config is ~/.phi/mcp.json).
func (p *Project) MCPConfigFile() string {
	return filepath.Join(p.root, ".phi", "mcp.json")
}

// Project is the resolved phi workspace: the current working directory plus
// the global layout and its loaded configuration.
type Project struct {
	root   string
	global GlobalLayout
	config *Config
	// orca is the OrcaRouter credential store, created on first use. It is the
	// one place a provider secret is read from and written to, shared by the
	// API-key path, the PKCE path, the catalog fetch, and the GUI.
	orca     *orca.Store
	orcaOnce sync.Once
}

// Root returns the working directory the project was resolved from.
func (p *Project) Root() string { return p.root }

// Global returns the global phi layout (~/.phi).
func (p *Project) Global() GlobalLayout { return p.global }

// Config returns the loaded configuration, or nil before LoadConfig.
func (p *Project) Config() *Config { return p.config }

// OrcaStore returns the OrcaRouter credential store for this workspace. The
// store persists to the phi home directory that already holds config,
// sessions, and jobs — no new secret store is introduced.
func (p *Project) OrcaStore() *orca.Store {
	p.orcaOnce.Do(func() {
		p.orca = orca.NewStore(orca.DefaultStorePath(p.global.Root()))
	})
	return p.orca
}

// LoadConfig reads, env-overrides and finalizes the global configuration.
// The result is cached on the Project until the next LoadConfig call.
func (p *Project) LoadConfig() error {
	cfg, err := loadConfig(p.global, p.OrcaStore())
	if err != nil {
		return err
	}
	p.config = cfg
	return nil
}

// ensureGlobalDirs creates the global phi home directories. It is what makes
// ~/.phi/{bin,skills,extensions,session,jobs} exist from the very first startup.
func ensureGlobalDirs(global GlobalLayout) error {
	dirs := []string{
		global.Root(),
		global.BinDir(),
		global.SkillsDir(),
		global.ExtensionsDir(),
		global.SessionBase(),
		global.JobsDir(),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

// Discover resolves the phi workspace starting from startDir ("" = cwd) and
// ensures the global directory layout exists.
func Discover(startDir string) (*Project, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(startDir)
	if err != nil {
		return nil, err
	}
	global := GlobalLayout{root: filepath.Join(home, ".phi")}
	if err := ensureGlobalDirs(global); err != nil {
		return nil, err
	}
	return &Project{root: absRoot, global: global}, nil
}
