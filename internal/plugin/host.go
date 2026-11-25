// Package plugin runs trusted local Lua plugins inside phi.
package plugin

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/pulseaiclub/lug"
	"github.com/pulseaiclub/lug/runtime"
)

type instance struct {
	name string
	env  *lug.Env
}

// Host owns the environments whose startup completed successfully.
type Host struct {
	active []instance
}

// Open loads plugins from the supplied roots. A broken plugin yields a diagnostic
// without preventing the other plugins from starting.
func Open(currentVersion string, output io.Writer, roots ...string) (*Host, []error) {
	plugins, diagnostics := discover(currentVersion, roots...)
	h := &Host{}
	for _, p := range plugins {
		env := newEnv(output)
		if _, err := lug.DoFile(env, p.entry); err != nil {
			diagnostics = append(diagnostics, fmt.Errorf("plugin %q: load %s: %w", p.name, p.entry, err))
			continue
		}
		i := instance{name: p.name, env: env}
		if err := i.hook("on_load"); err != nil {
			diagnostics = append(diagnostics, err)
			continue
		}
		h.active = append(h.active, i)
	}
	return h, diagnostics
}

// Lua's default print and io.write target stdout, which belongs to the JSONL
// stream in headless mode. Route both to the caller's diagnostic writer.
func newEnv(output io.Writer) *lug.Env {
	env := lug.New()
	env.Set("print", runtime.NewClosure(func(e *lug.Env, args []lug.Value) []lug.Value {
		fmt.Fprintln(output, runtime.PrintLine(e, args))
		return nil
	}, "print"))
	env.Get("io").Table().Set(lug.String("write"), runtime.NewClosure(func(e *lug.Env, args []lug.Value) []lug.Value {
		for _, arg := range args {
			fmt.Fprint(output, runtime.ToStringEnv(e, arg))
		}
		return nil
	}, "io.write"))
	return env
}

func (i instance) hook(name string) error {
	if fn := i.env.Get(name); !fn.IsNil() {
		if _, err := lug.CallErr(i.env, fn); err != nil {
			return fmt.Errorf("plugin %q %s: %w", i.name, name, err)
		}
	}
	return nil
}

// Close unloads in reverse startup order. Repeated calls are harmless.
func (h *Host) Close() error {
	var errs []error
	for _, v := range slices.Backward(h.active) {
		if err := v.hook("on_unload"); err != nil {
			errs = append(errs, err)
		}
	}
	h.active = nil
	return errors.Join(errs...)
}
