package core

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/pulseaiclub/phi/ext/go/pxb"
)

// ModelRoute is one extension-registered model and the extension serving it.
type ModelRoute struct {
	// Ext is the extension name that registered the model; invocations go to
	// that extension's process.
	Ext string
	Reg pxb.RegisterModel
}

// ExtConn reports whether the extension serves the connection itself
// (ModelInvoke / ModelStreamEvent) instead of the host dialing BaseURL.
func (r ModelRoute) ExtConn() bool { return r.Reg.Conn == pxb.ConnExt }

// ExtensionModels returns every extension-registered model, extension-name
// order. The result is a cached read-model slice: treat it as read-only.
func (r *Runner) ExtensionModels() []ModelRoute {
	if r == nil {
		return nil
	}
	return r.snapshot().routes
}

// LookupModel resolves a model name to the extension that registered it.
// First registration wins; extension models shadow same-name config.yaml
// entries everywhere this is consulted.
func (r *Runner) LookupModel(name string) (ModelRoute, bool) {
	if r == nil {
		return ModelRoute{}, false
	}
	for _, route := range r.snapshot().routes {
		if route.Reg.Name == name {
			return route, true
		}
	}
	return ModelRoute{}, false
}

// ModelStream invokes an ext-served model on the extension that registered it.
func (r *Runner) ModelStream(
	ctx context.Context,
	route ModelRoute,
	req pxb.ModelInvoke,
) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
	if r == nil {
		return nil, errors.New("extension: no runner")
	}
	if !route.ExtConn() {
		return nil, fmt.Errorf("model %q is not extension-served", route.Reg.Name)
	}
	f := r.Fiber(route.Ext)
	if f == nil {
		return nil, fmt.Errorf("extension %q is not loaded", route.Ext)
	}
	proc := f.Proc()
	if proc == nil || proc.Closed() {
		return nil, fmt.Errorf("extension %q is not running", route.Ext)
	}
	return proc.StreamModel(ctx, req), nil
}
