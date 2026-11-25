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
}

type ProviderModel struct {
	ID            string
	ContextWindow int
	MaxTokens     int
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
	t, err := declarationTable(v, "provider", "id", "api", "base_url", "models")
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
	return p, nil
}

// OwnerLedger tracks which plugin claimed each provider ID so a later plugin
// cannot shadow an earlier one. All reads and writes go through its methods;
// the map never leaves the type. The zero value is an empty ledger.
type OwnerLedger struct {
	byID map[string]string
}

func NewOwnerLedger() *OwnerLedger {
	return &OwnerLedger{byID: make(map[string]string)}
}

// owner reports the plugin that claimed id.
func (o *OwnerLedger) owner(id string) (string, bool) {
	source, ok := o.byID[id]
	return source, ok
}

// record makes plugin the sole owner of id. Register rejects collisions up
// front, so recording at commit time cannot fail.
func (o *OwnerLedger) record(id, plugin string) {
	if o.byID == nil {
		o.byID = make(map[string]string)
	}
	o.byID[id] = plugin
}

// RegisterProvider stages a single plugin's declarations until startup succeeds.
type RegisterProvider struct {
	name    string
	owners  *OwnerLedger
	pending []Provider
}

func NewRegisterProvider(name string, owners *OwnerLedger) *RegisterProvider {
	return &RegisterProvider{name: name, owners: owners}
}

func (r *RegisterProvider) Register(provider Provider) error {
	if r.owners == nil {
		return errors.New("providers can be registered only during plugin startup")
	}
	if source, exists := r.owners.owner(provider.ID); exists {
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

// Commit hands out the staged providers and retires the stager: after it runs,
// further Register calls fail so plugins cannot register from on_unload.
func (r *RegisterProvider) Commit() []Provider {
	providers := r.pending
	for _, provider := range providers {
		r.owners.record(provider.ID, r.name)
	}
	r.owners, r.pending = nil, nil
	return providers
}
