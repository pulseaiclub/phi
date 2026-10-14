package main

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

	"github.com/pulseaiclub/phi/internal/util"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	anthropicAPIVersion  = "2023-06-01"
	modelListTimeout     = 15 * time.Second
	modelListBodyLimit   = int64(4 << 20)
)

type modelListResponse struct {
	Data   []modelListItem `json:"data"`
	Models []modelListItem `json:"models"`
}

type modelListItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// fetchModelIDs asks a provider which models it advertises, so `phi config` can
// fill a model name from a list instead of a typed-in guess. It runs on a
// background goroutine owned by the config overlay.
func fetchModelIDs(ctx context.Context, baseURL, apiKey, api, model string) ([]string, error) {
	baseURL = strings.TrimSpace(baseURL)
	anthropic := isAnthropicAPI(api, baseURL, model)
	if baseURL == "" {
		if anthropic {
			baseURL = "https://api.anthropic.com"
		} else {
			baseURL = defaultOpenAIBaseURL
		}
	}
	endpoint, err := modelListEndpoint(baseURL, anthropic)
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
	if anthropic {
		request.Header.Set("X-Api-Key", strings.TrimSpace(apiKey))
		request.Header.Set("Anthropic-Version", anthropicAPIVersion)
	} else {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}

	response, err := modelListHTTPClient().Do(request)
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
	models := collectModelIDs(append(payload.Data, payload.Models...))
	sort.Strings(models)
	return models, nil
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
	case "OpenAI", "OpenAIResponses", "Gemini":
		return false
	}
	// Empty api: keep legacy heuristics so an entry with only claude-ish values
	// still probes the Anthropic endpoint.
	return strings.Contains(strings.ToLower(baseURL), "anthropic") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude")
}

// modelListEndpoint resolves the /models URL for a base URL, tolerating a base
// that already ends in /models or /v1.
func modelListEndpoint(baseURL string, anthropic bool) (string, error) {
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
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func collectModelIDs(items []modelListItem) []string {
	seen := make(map[string]struct{}, len(items))
	models := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.Name)
		}
		if id == "" {
			id = strings.TrimSpace(item.DisplayName)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	return models
}
