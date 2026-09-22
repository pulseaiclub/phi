package controller

import (
	"time"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// orcaAuthFailure returns the terminal-401 callback for an engine, or nil when
// the model is not routed through OrcaRouter.
//
// An OrcaRouter key is a durable credential with no refresh grant, so a 401 is
// terminal: the account has to re-authenticate. The callback marks the exact
// account and credential generation the engine was bound to — never the
// current one — so a late failure from a request made before a newer login
// cannot mark the fresh key as broken. The stored secret is kept: the user may
// be offline and unable to obtain a replacement, so a misclassified failure
// must not destroy the only credential they have.
func (c *EngineController) orcaAuthFailure(cfg llm.ModelConfig) agent.AuthFailureFunc {
	if c.proj == nil {
		return nil
	}
	store := c.proj.OrcaStore()
	if !routesToOrcaRouter(cfg) {
		return nil
	}
	// Bind the generation now: this engine's requests will carry the credential
	// that is current at open time, and the 401 that comes back belongs to it.
	generation := store.Generation()
	return func(rejected llm.ModelConfig) {
		if rejected.API != llm.OrcaRouter {
			return
		}
		if !store.MarkUnauthorized(generation) {
			// A newer login replaced the credential, or this generation was
			// already flagged; either way the fresh key is left alone.
			return
		}
		c.bus.Publish(ToastMsg{
			Message: "OrcaRouter rejected this API key (401). " +
				"Reconnect in phi config, or run 'phi auth login --orcarouter'.",
			Kind:     toast.ToastError,
			Duration: 8 * time.Second,
		})
	}
}

// routesToOrcaRouter reports whether an engine's model config will be sent to
// OrcaRouter, by an explicit provider or by an OrcaRouter model name whose base
// URL is the OrcaRouter one. Anything else keeps the generic provider path and
// gets no reauthentication callback.
func routesToOrcaRouter(cfg llm.ModelConfig) bool {
	if cfg.API == llm.OrcaRouter {
		return true
	}
	if cfg.API != "" || cfg.Name == "" {
		return false
	}
	if _, ok := model.Lookup(cfg.Name); !ok {
		return false
	}
	return cfg.BaseURL == "" || cfg.BaseURL == model.OrcaRouterBaseURL
}
