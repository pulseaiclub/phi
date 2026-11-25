// Package plugin runs trusted local Lua plugins inside phi.
package plugin

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/pulseaiclub/lug"

	"github.com/pulseaiclub/phi/internal/plugin/lib"
)

type instance struct {
	name      string
	env       *lug.Env
	providers []lib.Provider
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
	owners := make(map[string]string)

	for _, p := range plugins {
		i, err := load(p, output, lib.NewRegisterProvider(p.name, owners))
		if err != nil {
			diagnostics = append(diagnostics, err)
			continue
		}
		h.active = append(h.active, i)
	}
	return h, diagnostics
}

func load(p descriptor, output io.Writer, register *lib.RegisterProvider) (instance, error) {
	env := newEnv(output, register)
	if _, err := lug.DoFile(env, p.entry); err != nil {
		return instance{}, fmt.Errorf("plugin %q: load %s: %w", p.name, p.entry, err)
	}
	i := instance{name: p.name, env: env}
	if err := i.hook("on_load"); err != nil {
		return instance{}, err
	}
	// Commit after on_load so hook-time registrations land; a failed startup
	// drops the plugin together with everything it declared.
	i.providers = register.Commit()
	return i, nil
}

func newEnv(output io.Writer, register lib.ProviderRegistry) *lug.Env {
	env := lug.New()
	reg := lib.NewRegistry()
	reg.Add(lib.NewBuiltinBinding(output))
	reg.Add(lib.NewProviderBinding(register))
	reg.BindAll(env)
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

// RegisteredModel keeps the upstream ID separate from the catalog key.
type RegisteredModel struct {
	Plugin   string
	Provider lib.Provider
	Model    lib.ProviderModel
}

// Models returns a snapshot keyed by the complete selection ID.
func (h *Host) Models() map[string]RegisteredModel {
	models := make(map[string]RegisteredModel)
	for _, i := range h.active {
		for _, p := range i.providers {
			p.Models = slices.Clone(p.Models)
			for _, m := range p.Models {
				models[p.ID+"/"+m.ID] = RegisteredModel{Plugin: i.name, Provider: p, Model: m}
			}
		}
	}
	return models
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
