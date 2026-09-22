package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Catalog bounds. A catalog response is untrusted input: it is fetched from a
// remote origin and must not be able to consume unbounded memory or advertise
// a route this client cannot speak.
const (
	catalogTimeout   = 15 * time.Second
	catalogBodyLimit = int64(4 << 20)
	catalogMaxItems  = 2000
)

// Endpoint type identifiers OrcaRouter reports per model. They are the
// authority on what wire format a model speaks; a model name is not.
const (
	EndpointOpenAI         = "openai"
	EndpointOpenAIResponse = "openai-response"
	EndpointAnthropic      = "anthropic"
	EndpointGemini         = "gemini"
	EndpointEmbeddings     = "embeddings"
	EndpointImageGen       = "image-generation"
	EndpointVideo          = "openai-video"
	EndpointRerank         = "jina-rerank"
)

// chatEndpointTypes are the wire formats this project can actually drive.
var chatEndpointTypes = []string{
	EndpointOpenAI, EndpointAnthropic, EndpointGemini, EndpointOpenAIResponse,
}

// nonChatEndpointTypes are routes that are never a chat model even when the
// model also advertises a chat-compatible endpoint type.
var nonChatEndpointTypes = []string{EndpointImageGen, EndpointVideo, EndpointRerank}

// Capability selects which models an entry point may offer. Each AI entry
// point filters independently; a model that does not declare a capability is
// excluded (fail closed) rather than guessed at from its name.
type Capability string

// Capability values, one per AI entry point this project exposes.
const (
	// CapabilityChat is the text agent loop and everything that feeds it.
	CapabilityChat Capability = "chat"
	// CapabilityImageInput is chat plus a declared image input modality; it
	// gates the composer's image attachments.
	CapabilityImageInput Capability = "image-input"
	// CapabilityEmbedding, CapabilityImageGen, CapabilityVideo, and
	// CapabilityRerank are filtered for completeness: this project has no
	// entry point for them, so they are never offered, and a model claiming
	// one is excluded from the chat list.
	CapabilityEmbedding Capability = "embedding"
	CapabilityImageGen  Capability = "image-generation"
	CapabilityVideo     Capability = "video"
	CapabilityRerank    Capability = "rerank"
)

// Model is one catalog entry after filtering.
type Model struct {
	// ID is the model identifier exactly as the catalog reports it. The
	// vendor/model namespace is preserved: it is what the API expects.
	ID string
	// Name is the human label when the catalog provides one.
	Name string
	// ContextWindow is the advertised context length in tokens, 0 when unknown.
	ContextWindow int
	// InputModalities are the declared input modalities (text, image, …).
	InputModalities []string
	// EndpointTypes are the declared wire formats.
	EndpointTypes []string
	// Source records where the entry came from: "live" for a catalog fetch or
	// "seed" for the verified cold-start fallback.
	Source string
}

// CatalogSourceLive and CatalogSourceSeed label a Model's provenance. A seed
// entry is only ever present when live discovery failed.
const (
	CatalogSourceLive = "live"
	CatalogSourceSeed = "seed"
)

// Catalog is a filtered model list plus its provenance.
type Catalog struct {
	Models []Model
	// Source is CatalogSourceLive when the list came from the origin, or
	// CatalogSourceSeed when it is the verified fallback.
	Source string
	// Degraded reports that live discovery failed and a fallback is in use,
	// so the UI can say so instead of presenting a stale list as current.
	Degraded bool
	// Warning explains a degraded result to the user.
	Warning string
}

// catalogItem is one record from GET /v1/models. Unknown fields are ignored;
// a record whose shape is not recognized is dropped rather than trusted.
type catalogItem struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	ContextLength         int      `json:"context_length"`
	SupportedEndpointType []string `json:"supported_endpoint_types"`
	Architecture          *struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
}

type catalogResponse struct {
	Data []catalogItem `json:"data"`
}

// FetchCatalog performs live discovery against the inference origin. The key
// is sent as a bearer token so the workspace's real access list is returned;
// the catalog endpoint also answers anonymously, in which case the result is
// the public list and is still treated as live.
func (o Origins) FetchCatalog(
	ctx context.Context,
	client *http.Client,
	apiKey string,
	capability Capability,
) (Catalog, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := o.ModelsURL()
	if q := capabilityQuery(capability); q != "" {
		endpoint += "?" + q
	}

	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return Catalog{}, err
	}
	req.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Catalog{}, fmt.Errorf("fetch model catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, catalogBodyLimit+1))
	if err != nil {
		return Catalog{}, fmt.Errorf("read model catalog: %w", err)
	}
	if int64(len(body)) > catalogBodyLimit {
		return Catalog{}, errors.New("model catalog response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Catalog{}, fmt.Errorf("model catalog request failed: %s", resp.Status)
	}

	var payload catalogResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return Catalog{}, fmt.Errorf("decode model catalog: %w", err)
	}
	if len(payload.Data) > catalogMaxItems {
		return Catalog{}, errors.New("model catalog advertised too many models")
	}

	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		m, ok := item.model()
		if !ok {
			continue
		}
		if !Supports(m, capability) {
			continue
		}
		m.Source = CatalogSourceLive
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return Catalog{Models: models, Source: CatalogSourceLive}, nil
}

// capabilityQuery maps a capability onto the catalog's own filter parameter.
// The catalog's server-side filter is a hint, not the authority: every model
// is re-checked locally with Supports, so a server that ignores the parameter
// (or widens it) cannot leak an incompatible model into a dropdown.
func capabilityQuery(c Capability) string {
	switch c {
	case CapabilityEmbedding:
		return "capability=embedding"
	case CapabilityImageGen:
		return "capability=image"
	default:
		return "capability=chat"
	}
}

// model converts a raw record, rejecting records with no usable identifier.
func (c catalogItem) model() (Model, bool) {
	id := strings.TrimSpace(c.ID)
	if id == "" {
		return Model{}, false
	}
	m := Model{
		ID:            id,
		Name:          strings.TrimSpace(c.Name),
		ContextWindow: c.ContextLength,
		EndpointTypes: normalizeTypes(c.SupportedEndpointType),
	}
	if c.Architecture != nil {
		m.InputModalities = normalizeTypes(c.Architecture.InputModalities)
	}
	if m.Name == "" {
		m.Name = id
	}
	return m, true
}

func normalizeTypes(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// Supports reports whether a model may be offered for a capability. It is
// deliberately fail-closed: an absent or unrecognized declaration excludes the
// model rather than admitting it on a name match.
func Supports(m Model, capability Capability) bool {
	switch capability {
	case CapabilityChat:
		return supportsChat(m)
	case CapabilityImageInput:
		return supportsChat(m) && hasType(m.InputModalities, "image")
	case CapabilityEmbedding:
		return hasType(m.EndpointTypes, EndpointEmbeddings) && !supportsChat(m)
	case CapabilityImageGen:
		return hasType(m.EndpointTypes, EndpointImageGen)
	case CapabilityVideo:
		return hasType(m.EndpointTypes, EndpointVideo)
	case CapabilityRerank:
		return hasType(m.EndpointTypes, EndpointRerank)
	default:
		return false
	}
}

// supportsChat requires a declared chat-capable wire format and excludes the
// routes that are never text chat even when they share an endpoint type.
func supportsChat(m Model) bool {
	if len(m.EndpointTypes) == 0 {
		return false
	}
	for _, t := range nonChatEndpointTypes {
		if hasType(m.EndpointTypes, t) {
			return false
		}
	}
	for _, t := range chatEndpointTypes {
		if hasType(m.EndpointTypes, t) {
			return true
		}
	}
	return false
}

func hasType(haystack []string, needle string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}

// Filter returns the models supporting capability.
func Filter(models []Model, capability Capability) []Model {
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if Supports(m, capability) {
			out = append(out, m)
		}
	}
	return out
}

// ModelIDs returns just the identifiers, for a select control.
func ModelIDs(models []Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

// Find returns the model with the given ID.
func Find(models []Model, id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// LiveCatalogWithFallback returns the live catalog for a capability, falling
// back to the verified seed when discovery fails.
//
// A successful live fetch is authoritative: the seed is never merged into it,
// so a stale entry cannot reappear once the origin has answered.
func (o Origins) LiveCatalogWithFallback(
	ctx context.Context,
	client *http.Client,
	apiKey string,
	capability Capability,
) Catalog {
	live, err := o.FetchCatalog(ctx, client, apiKey, capability)
	if err == nil {
		return live
	}
	return Catalog{
		Models:   Filter(SeedModels(), capability),
		Source:   CatalogSourceSeed,
		Degraded: true,
		Warning:  "live model discovery failed; showing the verified fallback list (" + err.Error() + ")",
	}
}

// seedModel is a verified fallback entry. Every field here was observed on the
// live catalog or is documented by the provider; none of it is inferred from a
// model name.
type seedModel struct {
	Model
	// Reasoning records the documented reasoning-effort ladder, when the model
	// has one.
	Reasoning []string
}

// Verified cold-start seed. It exists so a fresh installation during a catalog
// outage still has a usable model list, and so `openai/gpt-5.5`'s reasoning
// ladder survives a discovery failure. The live catalog stays authoritative.
//
// Sources (verified 2026-09-22):
//   - https://api.orcarouter.ai/v1/models — live catalog, endpoint types and
//     context lengths
//   - https://www.orcarouter.ai — documented model list and reasoning levels
var seedCatalog = []seedModel{
	{
		Model: Model{
			ID:              "openai/gpt-5.5",
			Name:            "openai/gpt-5.5",
			ContextWindow:   272_000,
			InputModalities: []string{"text", "image"},
			EndpointTypes:   []string{EndpointOpenAI, EndpointOpenAIResponse},
			Source:          CatalogSourceSeed,
		},
		Reasoning: []string{"low", "medium", "high", "xhigh"},
	},
	{
		Model: Model{
			ID:              "anthropic/claude-opus-4.8",
			Name:            "anthropic/claude-opus-4.8",
			ContextWindow:   200_000,
			InputModalities: []string{"text", "image"},
			EndpointTypes:   []string{EndpointAnthropic, EndpointOpenAI},
			Source:          CatalogSourceSeed,
		},
	},
	{
		Model: Model{
			ID:              "google/gemini-3.5-flash",
			Name:            "google/gemini-3.5-flash",
			ContextWindow:   1_000_000,
			InputModalities: []string{"text", "image"},
			EndpointTypes:   []string{EndpointGemini, EndpointOpenAI},
			Source:          CatalogSourceSeed,
		},
	},
	{
		Model: Model{
			ID:              "deepseek/deepseek-v4-pro",
			Name:            "deepseek/deepseek-v4-pro",
			ContextWindow:   1_048_576,
			InputModalities: []string{"text"},
			EndpointTypes:   []string{EndpointOpenAI, EndpointOpenAIResponse},
			Source:          CatalogSourceSeed,
		},
	},
	{
		Model: Model{
			ID:              "orcarouter/auto",
			Name:            "orcarouter/auto",
			InputModalities: []string{"text"},
			EndpointTypes:   []string{EndpointOpenAI, EndpointOpenAIResponse},
			Source:          CatalogSourceSeed,
		},
	},
}

// SeedModels returns a copy of the verified fallback catalog.
func SeedModels() []Model {
	out := make([]Model, 0, len(seedCatalog))
	for _, s := range seedCatalog {
		out = append(out, s.Model)
	}
	return out
}

// SeedReasoningLevels returns the verified reasoning-effort ladder for a seed
// model, or nil when the model has none. It exists so restoring a fallback
// does not silently drop a model's reasoning metadata.
func SeedReasoningLevels(id string) []string {
	for _, s := range seedCatalog {
		if s.ID == id {
			return append([]string(nil), s.Reasoning...)
		}
	}
	return nil
}

// SeedIDs returns the verified fallback identifiers, in order.
func SeedIDs() []string { return ModelIDs(SeedModels()) }

// CatalogURLFor returns the authoritative catalog URL for a capability, for
// evidence and diagnostics.
func (o Origins) CatalogURLFor(c Capability) string {
	if q := capabilityQuery(c); q != "" {
		return o.ModelsURL() + "?" + q
	}
	return o.ModelsURL()
}

// ParseCatalogForTest parses a raw catalog body. It exists so tests can drive
// the real parser over a fixture without a network call.
func ParseCatalogForTest(body []byte, capability Capability) ([]Model, error) {
	var payload catalogResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Data) > catalogMaxItems {
		return nil, errors.New("model catalog advertised too many models")
	}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		m, ok := item.model()
		if !ok || !Supports(m, capability) {
			continue
		}
		m.Source = CatalogSourceLive
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// EnsureCatalogQueryIsURLSafe is a compile-time-ish guard used by tests to
// confirm the catalog URL carries no credential: the key travels in the
// Authorization header, never in the query string.
func (o Origins) EnsureCatalogQueryIsURLSafe() error {
	u, err := url.Parse(o.CatalogURLFor(CapabilityChat))
	if err != nil {
		return err
	}
	if u.RawQuery != "" && strings.Contains(strings.ToLower(u.RawQuery), "key") {
		return errors.New("catalog URL must not carry a key")
	}
	return nil
}
