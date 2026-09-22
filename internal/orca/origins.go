// Package orca implements OrcaRouter as a first-class provider: the credential
// seam shared by both authentication choices (a pasted API key and OAuth 2.0 +
// PKCE), the authorization-code exchange, and the capability-filtered model
// catalog.
//
// Two origins are involved and they are never derived from one another:
// authentication lives on https://www.orcarouter.ai (authorize path /auth,
// exchange path /api/v1/auth/keys) and inference lives on
// https://api.orcarouter.ai/v1. Replacing a hostname or appending /v1 to the
// auth origin produces the /v1/auth/keys path that 404s.
package orca

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Public OrcaRouter endpoints. Auth and inference are deliberately separate
// constants so no code path can derive one from the other.
const (
	DefaultAuthBase = "https://www.orcarouter.ai"
	DefaultAPIBase  = "https://api.orcarouter.ai"

	// AuthorizePath is the consent screen; it is a page, not an API.
	AuthorizePath = "/auth"
	// ExchangePath is the code-for-key endpoint. It lives under the auth
	// origin's /api/v1 prefix — NOT under the inference origin's /v1.
	ExchangePath = "/api/v1/auth/keys"
	// ModelsPath is the catalog endpoint on the inference origin.
	ModelsPath = "/v1/models"
	// ChatCompletionsPath is the OpenAI-compatible inference endpoint.
	ChatCompletionsPath = "/v1/chat/completions"

	// ConsoleKeysURL is where a user reads or revokes keys.
	ConsoleKeysURL = "https://www.orcarouter.ai/console/authorized-apps"
)

// Environment variables recognized for origin overrides. A shared self-hosted
// base is the fallback; the explicit per-origin variables win over it.
const (
	EnvBaseURL     = "ORCA_BASE_URL"
	EnvAuthBaseURL = "ORCA_AUTH_BASE_URL"
	EnvAPIBaseURL  = "ORCA_API_BASE_URL"
	// EnvAPIKey is the project's existing secret mechanism for a hand-supplied
	// OrcaRouter key, mirroring PHI_API_KEY.
	EnvAPIKey = "ORCA_API_KEY" //nolint:gosec // ORCA_API_KEY is an environment variable name, not a credential.
)

// Origins is the resolved pair of OrcaRouter origins for one process.
type Origins struct {
	AuthBase string
	APIBase  string
}

// ResolveOrigins reads the shared base plus the explicit overrides, with the
// explicit values taking precedence. Every origin is validated: a non-loopback
// origin must be HTTPS, and an unparsable value is rejected rather than
// silently falling back to the public default.
func ResolveOrigins(getenv func(string) string) (Origins, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	shared := strings.TrimSpace(getenv(EnvBaseURL))
	auth := strings.TrimSpace(getenv(EnvAuthBaseURL))
	api := strings.TrimSpace(getenv(EnvAPIBaseURL))
	if auth == "" {
		auth = shared
	}
	if api == "" {
		api = shared
	}
	if auth == "" {
		auth = DefaultAuthBase
	}
	if api == "" {
		api = DefaultAPIBase
	}
	authURL, err := NormalizeOrigin(auth)
	if err != nil {
		return Origins{}, fmt.Errorf("%s: %w", EnvAuthBaseURL, err)
	}
	apiURL, err := NormalizeOrigin(api)
	if err != nil {
		return Origins{}, fmt.Errorf("%s: %w", EnvAPIBaseURL, err)
	}
	return Origins{AuthBase: authURL, APIBase: apiURL}, nil
}

// NormalizeOrigin validates a base URL and returns it without a trailing
// slash. HTTP is accepted only for loopback hosts, so a plaintext credential
// exchange cannot be pointed at a remote host by a stray config value.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("origin is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("origin %q is not a URL: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("origin %q needs a scheme and host", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("origin %q must not carry userinfo", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin %q must not carry a query or fragment", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", fmt.Errorf("origin %q must use https unless it is loopback", raw)
		}
	default:
		return "", fmt.Errorf("origin %q must use http or https", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

// authorizeURL builds the consent URL. path is fixed by the caller to
// AuthorizePath; params are the PKCE parameters.
func (o Origins) authorizeURL(params url.Values) string {
	return o.AuthBase + AuthorizePath + "?" + params.Encode()
}

// ExchangeURL is the code-for-key endpoint on the auth origin.
func (o Origins) ExchangeURL() string { return o.AuthBase + ExchangePath }

// ModelsURL is the catalog endpoint on the inference origin.
func (o Origins) ModelsURL() string { return o.APIBase + ModelsPath }

// InferenceBaseURL is what a ModelConfig.base_url should carry.
func (o Origins) InferenceBaseURL() string { return o.APIBase + "/v1" }
