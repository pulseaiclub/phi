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
		i, err := load(p, output, NewRegisterProvider(p.name, owners))
		if err != nil {
			diagnostics = append(diagnostics, err)
			continue
		}
		h.active = append(h.active, i)
	}
	return h, diagnostics
}

// RegisterProvider stages a single plugin's declarations until startup succeeds.
type RegisterProvider struct {
	name    string
	owners  map[string]string
	pending []lib.Provider
}

func NewRegisterProvider(name string, owners map[string]string) *RegisterProvider {
	return &RegisterProvider{name: name, owners: owners}
}

func (r *RegisterProvider) Register(provider lib.Provider) error {
	if r.owners == nil {
		return errors.New("providers can be registered only during plugin startup")
	}
	if source, exists := r.owners[provider.ID]; exists {
		return fmt.Errorf("provider %q already registered by plugin %q", provider.ID, source)
	}
	for _, previous := range r.pending {
		if previous.ID == provider.ID {
			return fmt.Errorf("duplicate provider %q in plugin %q", provider.ID, r.name)
		}
	}
	r.pending = append(r.pending, provider)
	return nil
}

func (r *RegisterProvider) commit() []lib.Provider {
	providers := r.pending
	for _, provider := range providers {
		r.owners[provider.ID] = r.name
	}
	r.owners, r.pending = nil, nil
	return providers
}

func load(p descriptor, output io.Writer, register *RegisterProvider) (instance, error) {
	env := newEnv(output, register)
	if _, err := lug.DoFile(env, p.entry); err != nil {
		return instance{}, fmt.Errorf("plugin %q: load %s: %w", p.name, p.entry, err)
	}
	i := instance{name: p.name, env: env}
	if err := i.hook("on_load"); err != nil {
		return instance{}, err
	}
	i.providers = register.commit()
	return i, nil
}

func newEnv(output io.Writer, register *RegisterProvider) *lug.Env {
	env := lug.New()
	for _, b := range builtins {
		b.install(env, output)
	}
	installProviderAPI(env, register)
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
			if p.Auth != nil {
				auth := *p.Auth
				auth.Scopes = slices.Clone(auth.Scopes)
				p.Auth = &auth
			}
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
