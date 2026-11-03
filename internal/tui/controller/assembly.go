package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
)

// Assembly owns the environment-derived engine collaborators: the extension
// runner, the MCP pool, the sub-agent job manager, and the role→model
// resolver. TUI (`NewController`) and headless (`phi run`) assemble the same
// way; the permission gate, ask hooks, and session options stay per
// entrypoint. This is the single source of engine wiring knowledge — no
// entrypoint re-derives these collaborators by hand.
type Assembly struct {
	proj *project.Project

	ext atomic.Pointer[extension.Runner]
	mcp *mcp.Pool

	jobs *job.Manager

	// overrides maps sub-agent role → session-only model name. Guarded by
	// overridesMu: the palette writes on the UI goroutine while job
	// goroutines read through modelForRole.
	overridesMu sync.RWMutex
	overrides   map[job.Role]string

	parentModel func() llm.ModelConfig // fallback for roles without an override
	warn        func(string)           // non-fatal problem reporter
}

// AssemblyOpts tunes NewAssembly.
type AssemblyOpts struct {
	// ParentModel supplies the fallback model and the session round budget
	// for roles without an override. It is called from job goroutines, so it
	// must be safe for concurrent use. Nil falls back to the config default
	// model.
	ParentModel func() llm.ModelConfig
	// Warn reports non-fatal assembly problems (load failures, unknown role
	// models). Nil logs to the debug log.
	Warn func(string)
	// BuildJobs controls whether the sub-agent job manager (and its jobs
	// directory) is created. The TUI always builds: SetAgentsEnabled can turn
	// sub-agents on mid-session. Headless passes config.Agents.Enabled so a
	// disabled run never touches the jobs directory.
	BuildJobs bool
}

// NewAssembly loads extensions and the MCP pool (fail-open: nil), seeds the
// role→model overrides from the config's agents.*.model entries, and — when
// opts.BuildJobs — builds the sub-agent job manager. proj must be non-nil
// with a loaded config. On error everything loaded so far is closed.
func NewAssembly(proj *project.Project, opts AssemblyOpts) (*Assembly, error) {
	a := &Assembly{
		proj:        proj,
		overrides:   roleModelsFromConfig(proj.Config()),
		parentModel: opts.ParentModel,
		warn:        opts.Warn,
	}
	if a.parentModel == nil {
		a.parentModel = func() llm.ModelConfig { return proj.Config().Model() }
	}
	if a.warn == nil {
		a.warn = func(msg string) { debuglog.Logf("%s", msg) }
	}

	a.ext.Store(a.loadExtensions())
	if pool, err := mcp.LoadPool(proj.MCPConfigFile()); err != nil {
		a.warnf("mcp: load: %v", err)
	} else {
		a.mcp = pool
	}

	if opts.BuildJobs {
		jobs, err := agent.NewJobManager(proj.JobsDir(), a.parentModel(), a.modelForRole, a.Ext)
		if err != nil {
			a.Close()
			return nil, err
		}
		a.jobs = jobs
	}
	return a, nil
}

// loadExtensions discovers ~/.phi/extensions and <cwd>/.phi/extensions.
// Load errors are non-fatal (fail-open: nil runner).
func (a *Assembly) loadExtensions() *extension.Runner {
	r, warns, err := extension.Load(a.proj.Global().ExtensionsDir(), a.proj.ExtensionsDir())
	if err != nil {
		a.warnf("extensions: load failed: %v", err)
		return nil
	}
	logWarnings(a.warn, "extensions", warns)
	return r
}

// LoadExtensions re-discovers extensions for a reload. The error surfaces to
// the caller; warnings are logged.
func (a *Assembly) LoadExtensions() (*extension.Runner, []loader.Warning, error) {
	r, warns, err := extension.Load(a.proj.Global().ExtensionsDir(), a.proj.ExtensionsDir())
	if err != nil {
		return nil, warns, err
	}
	logWarnings(a.warn, "extensions", warns)
	return r, warns, nil
}

// ReloadExtensions re-discovers extensions, logging warnings. Failures are
// non-fatal and yield a nil runner.
func (a *Assembly) ReloadExtensions() *extension.Runner {
	r, _, err := a.LoadExtensions()
	if err != nil {
		a.warnf("extensions: reload failed: %v", err)
		return nil
	}
	return r
}

// Ext returns the current extension runner (nil when none loaded). Safe from
// any goroutine: extension reader goroutines call back through it. The nil
// Assembly is tolerated so a partially-built EngineController behaves like
// the zero value did.
func (a *Assembly) Ext() *extension.Runner {
	if a == nil {
		return nil
	}
	return a.ext.Load()
}

// SwapExt replaces the runner and returns the previous one for the caller to
// close.
func (a *Assembly) SwapExt(r *extension.Runner) (prev *extension.Runner) {
	if a == nil {
		return nil
	}
	return a.ext.Swap(r)
}

// MCP returns the MCP pool (nil when none configured or loading failed).
func (a *Assembly) MCP() *mcp.Pool {
	if a == nil {
		return nil
	}
	return a.mcp
}

// Jobs returns the sub-agent job manager. It is always built; engines gate it
// by the agents-enabled switch.
func (a *Assembly) Jobs() *job.Manager {
	if a == nil {
		return nil
	}
	return a.jobs
}

// RoleModel returns the session-only model name for a sub-agent role; empty
// means the role inherits the parent model.
func (a *Assembly) RoleModel(role job.Role) string {
	if a == nil {
		return ""
	}
	a.overridesMu.RLock()
	defer a.overridesMu.RUnlock()
	return a.overrides[role]
}

// SetRoleModel sets the session-only model name for a sub-agent role. Empty
// name clears the override; the name must exist in config or extensions.
func (a *Assembly) SetRoleModel(role job.Role, name string) error {
	if a == nil {
		return errors.New("no controller assembly")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		a.overridesMu.Lock()
		defer a.overridesMu.Unlock()
		delete(a.overrides, role)
		return nil
	}
	if _, ok := a.proj.Config().FindModel(name); !ok {
		if _, ok := a.Ext().LookupModel(name); !ok {
			return fmt.Errorf("unknown model %q", name)
		}
	}
	a.overridesMu.Lock()
	defer a.overridesMu.Unlock()
	a.overrides[role] = name
	return nil
}

// Close shuts the assembled collaborators down: jobs first (they may still
// call into extensions), then the MCP pool, then extension subprocesses.
func (a *Assembly) Close() {
	if a == nil {
		return
	}
	if a.jobs != nil {
		_ = a.jobs.Close(context.Background())
	}
	if a.mcp != nil {
		_ = a.mcp.Close()
	}
	if prev := a.SwapExt(nil); prev != nil {
		prev.Close()
	}
}

// EngineOptions returns the agent.Engine options shared by every entrypoint:
// gate, extensions, MCP pool, and — when enabled — the job manager. Extra
// options append after the shared ones.
func (a *Assembly) EngineOptions(
	gate permission.Gate,
	agentsEnabled bool,
	extra ...agent.EngineOption,
) []agent.EngineOption {
	opts := []agent.EngineOption{
		agent.WithGate(gate),
		agent.WithExtensions(a.Ext()),
	}
	if a.mcp != nil {
		opts = append(opts, agent.WithMCP(a.mcp))
	}
	if agentsEnabled && a.jobs != nil {
		opts = append(opts, agent.WithJobs(a.jobs))
	}
	return append(opts, extra...)
}

// modelForRole resolves a sub-agent's model: session override first, then
// extension-routed models, then config, falling back to the live parent. The
// session round budget travels with the parent: whichever MaxRounds the
// current run carries governs every role model, in the TUI and headless
// alike.
func (a *Assembly) modelForRole(role job.Role) llm.ModelConfig {
	if a == nil {
		return llm.ModelConfig{}
	}
	role = job.NormalizeRole(string(role))
	parent := a.parentModel()
	name := a.roleOverride(role)

	if name == "" {
		return parent
	}
	if route, ok := a.Ext().LookupModel(name); ok {
		m, err := extensionModelConfig(route, a.proj.Config(), parent.MaxRounds)
		if err != nil {
			a.warnf("agents: extension role model %q: %v; using parent", name, err)
			return parent
		}
		return m
	}
	if m, ok := a.proj.Config().FindModel(name); ok {
		m.MaxRounds = parent.MaxRounds
		return m
	}
	a.warnf("agents: unknown role model %q for %s; using parent", name, role)
	return parent
}

// roleOverride snapshots one override under the lock; modelForRole runs on
// job goroutines while the palette writes.
func (a *Assembly) roleOverride(role job.Role) string {
	a.overridesMu.RLock()
	defer a.overridesMu.RUnlock()
	return a.overrides[role]
}

func (a *Assembly) warnf(format string, args ...any) {
	a.warn(fmt.Sprintf(format, args...))
}

func logWarnings(warn func(string), domain string, warns []loader.Warning) {
	for _, w := range warns {
		warn(domain + ": " + w.String())
	}
}
