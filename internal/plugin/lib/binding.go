package lib

import (
	"errors"
	"fmt"

	"github.com/pulseaiclub/lug"
	"github.com/pulseaiclub/lug/runtime"
)

type Binding interface {
	Bind(env *lug.Env)
}

// Registry collects Binding instances and installs them into an environment.
type Registry struct {
	bindings []Binding
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Add(b Binding) {
	r.bindings = append(r.bindings, b)
}

func (r *Registry) BindAll(env *lug.Env) {
	for _, b := range r.bindings {
		b.Bind(env)
	}
}

// ProviderRegistry receives the providers a plugin declares at startup. Keeps
// this package free of any dependency on the plugin host.
type ProviderRegistry interface {
	Register(provider Provider) error
}

// ProviderBinding installs phi.provider.register.
type ProviderBinding struct {
	register ProviderRegistry
}

func NewProviderBinding(register ProviderRegistry) *ProviderBinding {
	return &ProviderBinding{register: register}
}

func (b *ProviderBinding) Bind(env *lug.Env) {
	provider := lug.NewTable()
	provider.Set(lug.String("register"), runtime.WrapFunc(func(v lug.Value) error {
		if b.register == nil {
			return errors.New("phi.provider.register: no provider registry installed")
		}
		p, err := ParseProvider(v)
		if err == nil {
			err = b.register.Register(p)
		}
		if err != nil {
			return fmt.Errorf("phi.provider.register: %w", err)
		}
		return nil
	}))
	phiTable(env).Set(lug.String("provider"), lug.TableVal(provider))
}

// phiTable returns the shared phi namespace, creating it on first use so the
// bindings can be installed in any order.
func phiTable(env *lug.Env) *lug.Table {
	if t := env.Get("phi").Table(); t != nil {
		return t
	}
	t := lug.NewTable()
	env.Set("phi", lug.TableVal(t))
	return t
}
