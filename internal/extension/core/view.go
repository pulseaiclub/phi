package core

import (
	"sort"

	ext "github.com/pulseaiclub/phi/ext/go"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// model is an immutable snapshot of everything the host derives from the
// loaded extensions: per-event handler lists, the tool list the agent may see,
// extension tools and slash commands.
//
// The Runner rebuilds it whenever the extension topology changes (load,
// unload, hot swap, base-tool or active-tool change), so the hot paths become a
// single map lookup: event dispatch alone walks the fiber set four times per
// tool call. Rebuilding is O(fibers) and happens on user-driven events, not per
// dispatch.
//
// Order is extension-name order. Iterating Runner.fibers is randomized, so the
// old walk made "first handler wins" behavior (tool_call blocking, prompt
// rewrite, user_input intercept) depend on map order.
type model struct {
	names       []string         // sorted extension names
	handlers    map[string][]any // event → handlers, extension-name order
	tools       []ext.ToolInfo   // builtin first, then extension tools
	activeTools []ext.ToolInfo   // tools is filtered by the active set
	extTools    []tooldef.Tool   // extension tools as agent tools
	commands    []ext.CommandEntry
	routes      []ModelRoute // extension-registered models, extension-name order
}

// buildModelLocked snapshots the current topology. Caller holds r.mu for writing.
//
// Handler registration is write-once per extension: the SDK serializes tools,
// commands and subscriptions during the handshake, and Proc rejects register
// frames afterwards, so a fiber's API is immutable once it is visible here. A
// cached aggregate therefore cannot go stale on its own — only a topology
// change can, and every mutation path calls invalidateLocked.
func (r *Runner) buildModelLocked() *model {
	m := &model{
		names:    make([]string, 0, len(r.fibers)),
		handlers: make(map[string][]any, len(ext.EventNames)),
	}
	for name := range r.fibers {
		m.names = append(m.names, name)
	}
	sort.Strings(m.names)

	seenCmd := make(map[string]bool)
	seenModel := make(map[string]bool)
	for _, name := range m.names {
		api := r.fibers[name].API()
		if api == nil {
			continue
		}
		for _, reg := range r.fibers[name].Proc().Models() {
			// First extension to claim a model name wins, like commands.
			if seenModel[reg.Name] {
				continue
			}
			seenModel[reg.Name] = true
			m.routes = append(m.routes, ModelRoute{Ext: name, Reg: reg})
		}
		for _, event := range ext.EventNames {
			if hs := api.Handlers(event); len(hs) > 0 {
				m.handlers[event] = append(m.handlers[event], hs...)
			}
		}
		for _, def := range api.Tools() {
			m.extTools = append(m.extTools, toolFromDef(def))
			m.tools = append(m.tools, ext.ToolInfo{
				Name:        def.Name,
				Description: def.Description,
				Source:      "extension",
			})
		}
		for _, e := range api.CommandEntries() {
			// First extension to claim a name wins.
			if seenCmd[e.Name] {
				continue
			}
			seenCmd[e.Name] = true
			m.commands = append(m.commands, e)
		}
	}

	// Builtin tools keep their place at the front of the list.
	all := make([]ext.ToolInfo, 0, len(r.baseTools)+len(m.tools))
	for _, t := range r.baseTools {
		all = append(all, ext.ToolInfo{
			Name:        t.Definition.Name,
			Description: t.Definition.Description,
			Source:      "builtin",
		})
	}
	m.tools = append(all, m.tools...)
	if r.activeNames == nil {
		m.activeTools = m.tools
	} else {
		for _, info := range m.tools {
			if r.activeNames[info.Name] {
				m.activeTools = append(m.activeTools, info)
			}
		}
	}
	return m
}

// snapshot returns the cached read model, rebuilding it after a topology change.
func (r *Runner) snapshot() *model {
	r.mu.RLock()
	m := r.view
	r.mu.RUnlock()
	if m != nil {
		return m
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view == nil {
		r.view = r.buildModelLocked()
	}
	return r.view
}

// invalidateLocked drops the cached read model. Caller holds r.mu for writing.
func (r *Runner) invalidateLocked() { r.view = nil }

// topologyChanged drops the cached read model and tells the host that its
// derived state (tool schemas, palette, footer) is stale.
//
// Every path that adds, removes or replaces a fiber's process must call this:
// the model cannot notice on its own, and a swap keeps the same fiber object in
// the same map slot, so nothing else would invalidate it.
func (r *Runner) topologyChanged() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.invalidateLocked()
	r.mu.Unlock()
	r.notifyChange()
}

// fiberList returns a copy of the current fibers under the read lock.
func (r *Runner) fiberList() []*Fiber {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fiberListLocked()
}

// fiberListLocked copies the fiber set. Caller holds r.mu.
func (r *Runner) fiberListLocked() []*Fiber {
	out := make([]*Fiber, 0, len(r.fibers))
	for _, f := range r.fibers {
		out = append(out, f)
	}
	return out
}

// Names returns the loaded extension names, sorted.
func (r *Runner) Names() []string {
	if r == nil {
		return nil
	}
	return r.snapshot().names
}

// Fiber returns one fiber by name, or nil.
func (r *Runner) Fiber(name string) *Fiber {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fibers[name]
}
