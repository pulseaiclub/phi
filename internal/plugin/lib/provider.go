package lib

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"

	"github.com/pulseaiclub/lug"

	"github.com/pulseaiclub/phi/internal/llm"
)

type Provider struct {
	ID      string
	API     llm.RouterType
	BaseURL string
	Models  []ProviderModel
	Auth    *PKCE
}

type ProviderModel struct {
	ID            string
	ContextWindow int
	MaxTokens     int
}

type PKCE struct {
	ClientID     string
	AuthorizeURL string
	TokenURL     string
	Scopes       []string
}

func declarationTable(v lug.Value, name string, fields ...string) (*lug.Table, error) {
	t := v.Table()
	if t == nil {
		return nil, fmt.Errorf("%s: expected a table, got %s", name, v.Type())
	}
	for k, _, ok := t.Next(lug.Nil); ok; k, _, ok = t.Next(k) {
		if !k.IsString() {
			return nil, fmt.Errorf("%s: expected string field names, got %s", name, k.Type())
		}
		if !slices.Contains(fields, k.Str()) {
			return nil, fmt.Errorf("%s: unknown field %q", name, k.Str())
		}
	}
	return t, nil
}

func requiredText(v lug.Value, name string) (string, error) {
	if !v.IsString() || strings.TrimSpace(v.Str()) == "" {
		return "", fmt.Errorf("%s: expected a non-empty string", name)
	}
	return v.Str(), nil
}

func checkEndpoint(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		strings.Contains(raw, "#") {
		return fmt.Errorf("%s: expected an absolute HTTP(S) URL without user info or fragment", field)
	}
	return nil
}

// Len only measures the dense part of a Lua table. Count actual entries before
// reading 1..n so a stray hash key or a hole cannot disappear during decoding.
func declarationArray(v lug.Value, field string) ([]lug.Value, error) {
	t := v.Table()
	if t == nil {
		return nil, fmt.Errorf("%s: expected an array", field)
	}
	seen := make(map[int64]bool)
	for k, _, ok := t.Next(lug.Nil); ok; k, _, ok = t.Next(k) {
		i, integral := lug.AsInt(k)
		if !k.IsNumber() || !integral || i < 1 || seen[i] {
			return nil, fmt.Errorf("%s: expected consecutive integer keys starting at 1", field)
		}
		seen[i] = true
	}
	items := make([]lug.Value, len(seen))
	for i := range items {
		items[i] = t.Get(lug.Int(int64(i + 1)))
		if items[i].IsNil() {
			return nil, fmt.Errorf("%s: missing array element %d", field, i+1)
		}
	}
	return items, nil
}

func (p PKCE) validate() error {
	if strings.TrimSpace(p.ClientID) == "" {
		return errors.New("client_id: expected a non-empty string")
	}
	if err := checkEndpoint(p.AuthorizeURL, "authorize_url"); err != nil {
		return err
	}
	if err := checkEndpoint(p.TokenURL, "token_url"); err != nil {
		return err
	}
	for i, s := range p.Scopes {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("scopes[%d]: expected a non-empty string", i+1)
		}
	}
	return nil
}

func ParsePKCE(v lug.Value) (PKCE, error) {
	t, err := declarationTable(v, "pkce", "client_id", "authorize_url", "token_url", "scopes")
	if err != nil {
		return PKCE{}, err
	}
	// Str returns the empty string for non-strings; validate rejects those too.
	p := PKCE{
		ClientID:     t.Get(lug.String("client_id")).Str(),
		AuthorizeURL: t.Get(lug.String("authorize_url")).Str(),
		TokenURL:     t.Get(lug.String("token_url")).Str(),
	}
	if scopes := t.Get(lug.String("scopes")); !scopes.IsNil() {
		items, err := declarationArray(scopes, "scopes")
		if err != nil {
			return PKCE{}, err
		}
		for _, item := range items {
			p.Scopes = append(p.Scopes, item.Str())
		}
	}
	if err := p.validate(); err != nil {
		return PKCE{}, err
	}
	return p, nil
}

func positiveInt(v lug.Value, field string) (int, error) {
	n := v.Num()
	// MaxInt can round up as a float64; the exclusive bound is exactly representable.
	const intLimit = math.MaxInt + 1
	if !v.IsNumber() || n < 1 || n >= intLimit || math.Trunc(n) != n {
		return 0, fmt.Errorf("%s: expected a positive integer that fits in int", field)
	}
	return int(n), nil
}

func parseProviderModel(v lug.Value) (ProviderModel, error) {
	t, err := declarationTable(v, "model", "id", "context_window", "max_tokens")
	if err != nil {
		return ProviderModel{}, err
	}
	id, err := requiredText(t.Get(lug.String("id")), "id")
	if err != nil {
		return ProviderModel{}, err
	}
	window, err := positiveInt(t.Get(lug.String("context_window")), "context_window")
	if err != nil {
		return ProviderModel{}, err
	}
	tokens, err := positiveInt(t.Get(lug.String("max_tokens")), "max_tokens")
	if err != nil {
		return ProviderModel{}, err
	}
	if tokens > window {
		return ProviderModel{}, errors.New("max_tokens cannot exceed context_window")
	}
	return ProviderModel{ID: id, ContextWindow: window, MaxTokens: tokens}, nil
}

func ParseProvider(v lug.Value) (Provider, error) {
	t, err := declarationTable(v, "provider", "id", "api", "base_url", "models", "auth")
	if err != nil {
		return Provider{}, err
	}
	id, err := requiredText(t.Get(lug.String("id")), "id")
	if err != nil {
		return Provider{}, err
	}
	if strings.Contains(id, "/") {
		return Provider{}, errors.New("provider id must not contain '/'")
	}
	p := Provider{
		ID:      id,
		API:     llm.RouterType(t.Get(lug.String("api")).Str()),
		BaseURL: t.Get(lug.String("base_url")).Str(),
	}
	switch p.API {
	case llm.OpenAI, llm.OpenAIResponses, llm.Anthropic, llm.Gemini:
	default:
		return Provider{}, fmt.Errorf("api: expected OpenAI, OpenAIResponses, Anthropic or Gemini, got %q", p.API)
	}
	if err := checkEndpoint(p.BaseURL, "base_url"); err != nil {
		return Provider{}, err
	}
	items, err := declarationArray(t.Get(lug.String("models")), "models")
	if err != nil {
		return Provider{}, err
	}
	if len(items) == 0 {
		return Provider{}, errors.New("models: at least one model is required")
	}
	ids := make(map[string]bool, len(items))
	for i, item := range items {
		m, err := parseProviderModel(item)
		if err != nil {
			return Provider{}, fmt.Errorf("models[%d]: %w", i+1, err)
		}
		if ids[m.ID] {
			return Provider{}, fmt.Errorf("models[%d]: duplicate model id %q", i+1, m.ID)
		}
		ids[m.ID] = true
		p.Models = append(p.Models, m)
	}
	if auth := t.Get(lug.String("auth")); !auth.IsNil() {
		var descriptor *PKCE
		if u := auth.Userdata(); u != nil {
			descriptor, _ = u.Data.(*PKCE)
		}
		if descriptor == nil {
			return Provider{}, errors.New("auth: expected a phi.oauth2.pkce descriptor")
		}
		// Userdata fields are writable from Lua. Validate and snapshot at registration.
		if err := descriptor.validate(); err != nil {
			return Provider{}, fmt.Errorf("auth: %w", err)
		}
		copy := *descriptor
		copy.Scopes = slices.Clone(copy.Scopes)
		p.Auth = &copy
	}
	return p, nil
}
