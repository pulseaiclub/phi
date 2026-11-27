package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/gemini/interactions"
	"github.com/pulseaiclub/phi/internal/tui/configui"
	"github.com/pulseaiclub/phi/internal/util"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	anthropicAPIVersion  = "2023-06-01"
	modelListTimeout     = 15 * time.Second
	modelListBodyLimit   = int64(4 << 20)

	// remoteCommandTimeout bounds CLI commands that wait on external services
	// (MCP calls/doctor, extension install/update).
	remoteCommandTimeout = 2 * time.Minute
)

type modelListResponse struct {
	Data          []json.RawMessage `json:"data"`
	Models        []json.RawMessage `json:"models"`
	NextPageToken string            `json:"nextPageToken"`
}

// fetchModels asks a provider which models it advertises (and any capability
// limits it reports), so `phi config` can pick a model ID and prefill unset
// fields. It runs on a background goroutine owned by the config overlay.
func fetchModels(ctx context.Context, baseURL, apiKey, api, model string) ([]configui.ModelInfo, error) {
	baseURL = strings.TrimSpace(baseURL)
	anthropic := isAnthropicAPI(api, baseURL, model)
	gemini := !anthropic && isGeminiAPI(api, baseURL)
	if strings.TrimSpace(api) == string(llm.GeminiInteractions) {
		baseURL = interactions.NormalizeBaseURL(baseURL)
	} else if baseURL == "" {
		switch {
		case anthropic:
			baseURL = "https://api.anthropic.com"
		case gemini:
			baseURL = defaultGeminiBaseURL
		default:
			baseURL = defaultOpenAIBaseURL
		}
	}
	endpoint, err := modelListEndpoint(baseURL, anthropic, gemini)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build model list request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	setModelListAuth(request.Header, baseURL, apiKey, anthropic, gemini)

	client := modelListHTTPClient()
	var items []json.RawMessage
	seenTokens := make(map[string]bool)
	for {
		page, err := fetchModelPage(client, request)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Data...)
		items = append(items, page.Models...)
		if !gemini || page.NextPageToken == "" {
			break
		}
		if seenTokens[page.NextPageToken] {
			return nil, errors.New("model list returned a repeated page token")
		}
		seenTokens[page.NextPageToken] = true
		request = request.Clone(ctx)
		query := request.URL.Query()
		query.Set("pageToken", page.NextPageToken)
		request.URL.RawQuery = query.Encode()
	}
	models := collectModelInfos(items, gemini)
	slices.SortFunc(models, func(a, b configui.ModelInfo) int {
		return strings.Compare(a.ID, b.ID)
	})
	return models, nil
}

func fetchModelPage(client *http.Client, request *http.Request) (*modelListResponse, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch model list: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, modelListBodyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read model list: %w", err)
	}
	if int64(len(body)) > modelListBodyLimit {
		return nil, errors.New("model list response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := util.TruncateBytes(strings.TrimSpace(string(body)), 500, "")
		if message == "" {
			message = response.Status
		}
		return nil, fmt.Errorf("model list request failed: %s", message)
	}

	var payload modelListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	return &payload, nil
}

// modelListHTTPClient is the shared client with one restriction on top: a
// redirect must stay on the same origin, so a provider cannot bounce the API
// key to another host.
func modelListHTTPClient() *http.Client {
	client := *util.DefaultHTTPClient()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		origin := via[0].URL
		if !strings.EqualFold(req.URL.Scheme, origin.Scheme) ||
			!strings.EqualFold(req.URL.Host, origin.Host) {
			return errors.New("model list redirect changed origin")
		}
		return nil
	}
	return &client
}

func isAnthropicAPI(api, baseURL, model string) bool {
	switch strings.TrimSpace(api) {
	case "Anthropic":
		return true
	case "OpenAI", "OpenAIResponses", "Gemini", string(llm.GeminiInteractions):
		return false
	}
	// Empty api: keep legacy heuristics so an entry with only claude-ish values
	// still probes the Anthropic endpoint.
	return strings.Contains(strings.ToLower(baseURL), "anthropic") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude")
}

func setModelListAuth(header http.Header, baseURL, apiKey string, anthropic, gemini bool) {
	key := strings.TrimSpace(apiKey)
	switch {
	case anthropic:
		header.Set("X-Api-Key", key)
		header.Set("Anthropic-Version", anthropicAPIVersion)
	case gemini && !strings.Contains(strings.ToLower(baseURL), "aiplatform.googleapis.com"):
		if key != "" {
			header.Set("x-goog-api-key", key)
		}
	default:
		header.Set("Authorization", "Bearer "+key)
	}
}

func isGeminiAPI(api, baseURL string) bool {
	switch strings.TrimSpace(api) {
	case "Gemini", string(llm.GeminiInteractions):
		return true
	case "OpenAI", "OpenAIResponses", "Anthropic":
		return false
	}
	lower := strings.ToLower(baseURL)
	return strings.Contains(lower, "generativelanguage.googleapis.com") && !strings.Contains(lower, "/openai")
}

// modelListEndpoint resolves the /models URL for a base URL, tolerating a base
// that already ends in /models or /v1. Gemini defaults to pageSize=50, so set
// the API maximum (1000) to avoid silent truncation.
func modelListEndpoint(baseURL string, anthropic, gemini bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("base URL must be an absolute HTTP(S) URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("base URL must use http or https")
	}

	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/models") {
		if anthropic && !strings.HasSuffix(path, "/v1") {
			path += "/v1"
		}
		path += "/models"
	}
	u.Path = path
	u.RawPath = ""
	if gemini {
		u.RawQuery = "pageSize=1000"
	} else {
		u.RawQuery = ""
	}
	u.Fragment = ""
	return u.String(), nil
}

func collectModelInfos(items []json.RawMessage, stripGeminiPrefix bool) []configui.ModelInfo {
	seen := make(map[string]struct{}, len(items))
	models := make([]configui.ModelInfo, 0, len(items))
	for _, raw := range items {
		info, ok := parseModelListItem(raw, stripGeminiPrefix)
		if !ok {
			continue
		}
		if _, dup := seen[info.ID]; dup {
			continue
		}
		seen[info.ID] = struct{}{}
		models = append(models, info)
	}
	return models
}

// parseModelListItem extracts the model ID and any explicitly advertised limits
// from one /models entry. Optional fields are decoded individually so gateways
// using non-standard shapes (e.g. string "architecture" or array "capabilities")
// never break model listing.
func parseModelListItem(raw json.RawMessage, stripGeminiPrefix bool) (configui.ModelInfo, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return configui.ModelInfo{}, false
	}
	id := cmp.Or(
		jsonString(fields["id"]),
		jsonString(fields["name"]),
		jsonString(fields["display_name"]),
	)
	if stripGeminiPrefix {
		id = strings.TrimPrefix(id, "models/")
	}
	if id == "" {
		return configui.ModelInfo{}, false
	}

	var topProvider map[string]json.RawMessage
	if rawTP := fields["top_provider"]; len(rawTP) > 0 {
		_ = json.Unmarshal(rawTP, &topProvider)
	}

	return configui.ModelInfo{
		ID: id,
		ContextWindow: cmp.Or(
			jsonPositiveInt(fields["context_length"]),
			jsonPositiveInt(topProvider["context_length"]),
			jsonPositiveInt(fields["max_model_len"]),
			jsonPositiveInt(fields["context_window"]),
			jsonPositiveInt(fields["max_input_tokens"]),
			jsonPositiveInt(fields["inputTokenLimit"]),
		),
		MaxTokens: cmp.Or(
			jsonPositiveInt(fields["max_completion_tokens"]),
			jsonPositiveInt(topProvider["max_completion_tokens"]),
			jsonPositiveInt(fields["max_output_tokens"]),
			jsonPositiveInt(fields["max_tokens"]),
			jsonPositiveInt(fields["outputTokenLimit"]),
		),
		ImageEnabled: parseModelImageSupport(fields),
	}, true
}

func parseModelImageSupport(fields map[string]json.RawMessage) *bool {
	if rawCaps := fields["capabilities"]; len(rawCaps) > 0 {
		var caps struct {
			ImageInput *struct {
				Supported *bool `json:"supported"`
			} `json:"image_input"`
			SupportsVision *bool `json:"supports_vision"`
		}
		if json.Unmarshal(rawCaps, &caps) == nil {
			if caps.ImageInput != nil && caps.ImageInput.Supported != nil {
				return caps.ImageInput.Supported
			}
			if caps.SupportsVision != nil {
				return caps.SupportsVision
			}
		}
	}
	if rawArch := fields["architecture"]; len(rawArch) > 0 {
		var arch struct {
			InputModalities []string `json:"input_modalities"`
		}
		if json.Unmarshal(rawArch, &arch) == nil {
			if supported := hasImageModality(arch.InputModalities); supported != nil {
				return supported
			}
		}
	}
	if rawMod := fields["modalities"]; len(rawMod) > 0 {
		var mod struct {
			Input []string `json:"input"`
		}
		if json.Unmarshal(rawMod, &mod) == nil {
			if supported := hasImageModality(mod.Input); supported != nil {
				return supported
			}
		}
	}
	if rawIn := fields["input_modalities"]; len(rawIn) > 0 {
		var in []string
		if json.Unmarshal(rawIn, &in) == nil {
			if supported := hasImageModality(in); supported != nil {
				return supported
			}
		}
	}
	if rawVision := fields["supports_vision"]; len(rawVision) > 0 {
		var supported *bool
		if json.Unmarshal(rawVision, &supported) == nil && supported != nil {
			return supported
		}
	}
	return nil
}

func hasImageModality(modalities []string) *bool {
	if len(modalities) == 0 {
		return nil
	}
	hasImage := slices.ContainsFunc(modalities, func(mod string) bool {
		return strings.EqualFold(strings.TrimSpace(mod), "image")
	})
	return &hasImage
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func jsonPositiveInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		if n > 0 {
			return n
		}
		return 0
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil && f >= 1 && f < float64(math.MaxInt) && math.Trunc(f) == f {
		return int(f)
	}
	return 0
}
