package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/util"
)

//go:embed config.html
var configHTML []byte

// configDoc is the document served to / accepted from the config editor.
// The yaml tags mirror internal/project's fileConfig so the page round-trips
// config.yaml losslessly for every key the parser understands; the json tags
// drive the editor API. Pointer fields preserve "key absent" across saves so
// untouched sections are never rewritten.
type configDoc struct {
	Path        string     `yaml:"-"                     json:"path,omitempty"`
	Models      []modelDoc `yaml:"models"                json:"models"`
	SkillPath   *string    `yaml:"skill_path,omitempty"  json:"skillPath,omitempty"`
	Permissions *permDoc   `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Agents      *agentsDoc `yaml:"agents,omitempty"      json:"agents,omitempty"`
}

type modelDoc struct {
	Name          string `yaml:"name"                     json:"name"`
	APIKey        string `yaml:"api_key"                  json:"apiKey"`
	BaseURL       string `yaml:"base_url"                 json:"baseUrl"`
	Provider      string `yaml:"provider,omitempty"       json:"provider,omitempty"`
	ContextWindow *int   `yaml:"context_window,omitempty" json:"contextWindow,omitempty"`
	Default       bool   `yaml:"default,omitempty"        json:"default"`
}

type permDoc struct {
	Mode                *string   `yaml:"mode,omitempty"                  json:"mode,omitempty"`
	WorkspaceOnlyWrites *bool     `yaml:"workspace_only_writes,omitempty" json:"workspaceOnlyWrites,omitempty"`
	AskTimeoutSec       *int      `yaml:"ask_timeout_sec,omitempty"       json:"askTimeoutSec,omitempty"`
	DangerouslyAllowAll *bool     `yaml:"dangerously_allow_all,omitempty" json:"dangerouslyAllowAll,omitempty"`
	Bash                *bashDoc  `yaml:"bash,omitempty"                  json:"bash,omitempty"`
	Fetch               *fetchDoc `yaml:"fetch,omitempty"                 json:"fetch,omitempty"`
}

type bashDoc struct {
	Default *string  `yaml:"default,omitempty" json:"default,omitempty"`
	Allow   []string `yaml:"allow"             json:"allow,omitempty"`
	Deny    []string `yaml:"deny"              json:"deny,omitempty"`
}

type fetchDoc struct {
	Default      *string  `yaml:"default,omitempty" json:"default,omitempty"`
	AllowedHosts []string `yaml:"allowed_hosts"     json:"allowedHosts,omitempty"`
}

type agentsDoc struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

type modelListRequest struct {
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

type modelListItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type modelListResponse struct {
	Data   []modelListItem `json:"data"`
	Models []modelListItem `json:"models"`
}

const (
	defaultOpenAIBaseURL  = "https://api.openai.com/v1"
	anthropicAPIVersion   = "2023-06-01"
	modelListRequestLimit = 15 * time.Second
	modelListBodyLimit    = int64(4 << 20)
)

// configCmd starts a local web server (loopback only) that edits config.yaml
// in the browser.
func configCmd(args []string) int {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(
				os.Stdout,
				"usage: phi config\n\nOpen the HTML config editor (starts a local web server on 127.0.0.1).",
			)
			return ExitOK
		}
	}
	proj := project.GetDefaultProject()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi config:", err)
		return ExitError
	}
	addr := ln.Addr().(*net.TCPAddr)
	pageURL := fmt.Sprintf("http://127.0.0.1:%d/", addr.Port)
	fmt.Fprintf(os.Stderr, "phi config: %s\n  config: %s\n  Ctrl-C to stop\n", pageURL, proj.Global().ConfigFile())
	openBrowser(ctx, pageURL)

	srv := &http.Server{
		Handler:           &configHandler{configPath: proj.Global().ConfigFile()},
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "phi config:", err)
			return ExitError
		}
	case <-ctx.Done():
		_ = srv.Close()
	}
	return ExitOK
}

// configHandler serves the embedded editor page and its /api/config endpoints.
type configHandler struct {
	configPath string
}

func (h *configHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (r.URL.Path == "/api/config" || r.URL.Path == "/api/models") && !isLoopbackHost(r.Host) {
		writeConfigErr(w, http.StatusForbidden, errors.New("request origin is not allowed"))
		return
	}

	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(configHTML)
	case "/api/config":
		h.handleConfig(w, r)
	case "/api/models":
		h.handleModels(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *configHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		doc, err := readConfigDoc(h.configPath)
		if err != nil {
			writeConfigErr(w, http.StatusInternalServerError, err)
			return
		}
		doc.Path = h.configPath
		writeConfigJSON(w, doc)
	case http.MethodPost:
		if status, err := validateLocalJSONRequest(r); err != nil {
			writeConfigErr(w, status, err)
			return
		}
		var doc configDoc
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			writeConfigErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
			return
		}
		if err := validateConfigDoc(&doc); err != nil {
			writeConfigErr(w, http.StatusBadRequest, err)
			return
		}
		if err := writeConfigDoc(h.configPath, &doc); err != nil {
			writeConfigErr(w, http.StatusInternalServerError, err)
			return
		}
		writeConfigJSON(w, map[string]string{"status": "saved"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleModels fetches model IDs through the local config server so the page
// does not need cross-origin access to a provider API.
func (*configHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if status, err := validateLocalJSONRequest(r); err != nil {
		writeConfigErr(w, status, err)
		return
	}

	var input modelListRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeConfigErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
		return
	}

	baseURL := strings.TrimSpace(input.BaseURL)
	apiKey := strings.TrimSpace(input.APIKey)
	provider, err := llm.ParseProvider(input.Provider)
	if err != nil {
		writeConfigErr(w, http.StatusBadRequest, err)
		return
	}
	resolved := llm.ResolveProvider(llm.ModelConfig{
		Name:     input.Model,
		BaseURL:  baseURL,
		Provider: provider,
	})
	anthropic := resolved == llm.ProviderAnthropic
	if baseURL == "" {
		if anthropic {
			baseURL = "https://api.anthropic.com"
		} else {
			baseURL = defaultOpenAIBaseURL
		}
	}
	endpoint, err := modelListEndpoint(baseURL, anthropic)
	if err != nil {
		writeConfigErr(w, http.StatusBadRequest, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), modelListRequestLimit)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		writeConfigErr(w, http.StatusBadRequest, fmt.Errorf("build model list request: %w", err))
		return
	}
	request.Header.Set("Accept", "application/json")
	if anthropic {
		request.Header.Set("X-Api-Key", apiKey)
		request.Header.Set("Anthropic-Version", anthropicAPIVersion)
	} else {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	response, err := modelListHTTPClient().Do(request)
	if err != nil {
		writeConfigErr(w, http.StatusBadGateway, fmt.Errorf("fetch model list: %w", err))
		return
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, modelListBodyLimit+1))
	if err != nil {
		writeConfigErr(w, http.StatusBadGateway, fmt.Errorf("read model list: %w", err))
		return
	}
	if int64(len(body)) > modelListBodyLimit {
		writeConfigErr(w, http.StatusBadGateway, errors.New("model list response is too large"))
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 500 {
			message = message[:500]
		}
		if message == "" {
			message = response.Status
		}
		writeConfigErr(w, http.StatusBadGateway, fmt.Errorf("model list request failed: %s", message))
		return
	}

	var payload modelListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		writeConfigErr(w, http.StatusBadGateway, fmt.Errorf("decode model list: %w", err))
		return
	}
	models := collectModelIDs(append(payload.Data, payload.Models...))
	sort.Strings(models)
	writeConfigJSON(w, struct {
		Models []string `json:"models"`
	}{Models: models})
}

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

// validateLocalJSONRequest requires browser POSTs to use a non-simple content
// type and come from the config page's own origin. ServeHTTP separately checks
// that every API request uses a loopback Host.
func validateLocalJSONRequest(r *http.Request) (int, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, errors.New("content type must be application/json")
	}
	origin := r.Header.Get("Origin")
	originURL, err := url.Parse(origin)
	if err != nil || origin == "" || originURL.Scheme == "" || originURL.Host == "" ||
		originURL.User != nil || originURL.Path != "" || originURL.RawQuery != "" || originURL.Fragment != "" {
		return http.StatusForbidden, errors.New("request origin is not allowed")
	}

	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	if !strings.EqualFold(originURL.Scheme, expectedScheme) || !strings.EqualFold(originURL.Host, r.Host) {
		return http.StatusForbidden, errors.New("request origin is not allowed")
	}
	return 0, nil
}

func isLoopbackHost(rawHost string) bool {
	u, err := url.Parse("http://" + rawHost)
	if err != nil || u.Host != rawHost || u.User != nil || u.Path != "" {
		return false
	}
	hostname := u.Hostname()
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// readConfigDoc loads the current config file into the editor document. A
// missing file yields an empty document so the page can bootstrap a config.
func readConfigDoc(path string) (*configDoc, error) {
	doc := &configDoc{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func validateConfigDoc(doc *configDoc) error {
	if len(doc.Models) == 0 {
		return errors.New("at least one model is required")
	}
	hasDefault := false
	for i := range doc.Models {
		m := &doc.Models[i]
		if m.Name == "" {
			return fmt.Errorf("model %d has no name", i+1)
		}
		provider, err := llm.ParseProvider(m.Provider)
		if err != nil {
			return fmt.Errorf("model %d %q: %w", i+1, m.Name, err)
		}
		if m.Provider != "" {
			m.Provider = string(provider)
		}
		if !m.Default {
			continue
		}
		if hasDefault {
			return errors.New("only one model may be marked default")
		}
		hasDefault = true
		if m.APIKey == "" {
			return fmt.Errorf("default model %q is missing api_key", m.Name)
		}
	}
	if !hasDefault {
		doc.Models[0].Default = true
		if doc.Models[0].APIKey == "" {
			return fmt.Errorf("default model %q is missing api_key", doc.Models[0].Name)
		}
	}
	return nil
}

// writeConfigDoc backs up the current file and writes the document as YAML.
func writeConfigDoc(path string, doc *configDoc) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if cur, err := os.ReadFile(path); err == nil {
		//nolint:gosec // G306: config backup stays user-readable
		if err := os.WriteFile(path+".bak", cur, 0o644); err != nil {
			return fmt.Errorf("backup config: %w", err)
		}
	}
	return os.WriteFile(path, data, 0o644) //nolint:gosec // G306: config.yaml is meant to be user-readable
}

func writeConfigJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeConfigErr(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// openBrowser best-effort opens the editor URL in the default browser.
func openBrowser(ctx context.Context, pageURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", pageURL)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", pageURL)
	default:
		return
	}
	_ = cmd.Start()
}
