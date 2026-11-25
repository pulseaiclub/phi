// Command modelgen syncs built-in preset specs (ContextWindow, InputLimit,
// MaxTokens, ImageEnabled) from models.dev into specs_gen.go.
//
// It intentionally does not import internal/project/model so it can regenerate
// specs_gen.go even when the generated file is missing or stale.
//
// Usage from repo root:
//
//	make sync-models
//	go run ./internal/project/model/cmd/modelgen
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCatalogURL  = "https://models.dev/api.json"
	defaultSourcesPath = "internal/project/model/sources.json"
	defaultOutputPath  = "internal/project/model/specs_gen.go"
	fetchTimeout       = 30 * time.Second
	maxCatalogBytes    = 32 << 20
)

type syncedSpec struct {
	name          string
	contextWindow int
	inputLimit    int
	maxTokens     int
	imageEnabled  bool
}

type catalogProvider struct {
	Models map[string]catalogModel `json:"models"`
}

type catalogModel struct {
	Status string `json:"status"`
	Limit  struct {
		Context int `json:"context"`
		Input   int `json:"input"`
		Output  int `json:"output"`
	} `json:"limit"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

func main() {
	sources := flag.String(
		"sources",
		defaultSourcesPath,
		"JSON file mapping preset names to models.dev <provider>/<model> IDs",
	)
	out := flag.String("out", defaultOutputPath, "output Go file path")
	url := flag.String("url", defaultCatalogURL, "models.dev catalog JSON URL")
	flag.Parse()

	if err := runCLI(*sources, *out, *url); err != nil {
		log.Fatal(err)
	}
}

func runCLI(sourcesPath, outPath, catalogURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	return run(ctx, sourcesPath, catalogURL, outPath, os.Stderr)
}

func run(ctx context.Context, sourcesPath, catalogURL, outPath string, errOut io.Writer) error {
	//nolint:gosec // G304: sources path is an explicit local CLI flag.
	sourcesRaw, err := os.ReadFile(sourcesPath)
	if err != nil {
		return fmt.Errorf("read sources %s: %w", sourcesPath, err)
	}
	var sources map[string]string
	if err := json.Unmarshal(sourcesRaw, &sources); err != nil {
		return fmt.Errorf("parse sources %s: %w", sourcesPath, err)
	}

	raw, err := fetchCatalog(ctx, catalogURL)
	if err != nil {
		return err
	}

	var catalog map[string]catalogProvider
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return fmt.Errorf("parse catalog JSON: %w", err)
	}

	specs, err := resolveSpecs(sources, catalog, errOut)
	if err != nil {
		return err
	}

	src, err := renderSpecs(specs)
	if err != nil {
		return err
	}

	// Generated source is world-readable, matching the rest of the repository.
	return os.WriteFile(outPath, src, 0o644) //nolint:gosec // G306: not a secret
}

func fetchCatalog(ctx context.Context, catalogURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build catalog request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G107: catalog URL is an explicit CLI flag.
	if err != nil {
		return nil, fmt.Errorf("fetch catalog %s: %w", catalogURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog %s: unexpected HTTP status %s", catalogURL, resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read catalog response: %w", err)
	}
	if len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("catalog response exceeds %d bytes", maxCatalogBytes)
	}
	return data, nil
}

func resolveSpecs(
	sources map[string]string,
	catalog map[string]catalogProvider,
	errOut io.Writer,
) ([]syncedSpec, error) {
	names := slices.Sorted(maps.Keys(sources))
	specs := make([]syncedSpec, 0, len(names))
	for _, name := range names {
		catalogID := sources[name]
		providerID, modelID, ok := strings.Cut(catalogID, "/")
		if !ok || providerID == "" || modelID == "" {
			return nil, fmt.Errorf("preset %q has invalid catalogID %q; expected <provider>/<model>", name, catalogID)
		}
		provider, ok := catalog[providerID]
		if !ok {
			return nil, fmt.Errorf("preset %q: provider %q not found in catalog", name, providerID)
		}
		entry, ok := provider.Models[modelID]
		if !ok {
			return nil, fmt.Errorf("preset %q: model %q not found under provider %q", name, modelID, providerID)
		}
		if entry.Limit.Context <= 0 {
			return nil, fmt.Errorf("preset %q (%s): invalid context window %d", name, catalogID, entry.Limit.Context)
		}
		if entry.Limit.Input < 0 || entry.Limit.Input > entry.Limit.Context {
			return nil, fmt.Errorf(
				"preset %q (%s): invalid input limit %d (context %d)",
				name,
				catalogID,
				entry.Limit.Input,
				entry.Limit.Context,
			)
		}
		if entry.Limit.Output < 0 {
			return nil, fmt.Errorf("preset %q (%s): invalid output limit %d", name, catalogID, entry.Limit.Output)
		}
		if entry.Status == "deprecated" {
			if _, err := fmt.Fprintf(
				errOut,
				"warning: preset %q (%s) is marked deprecated in models.dev\n",
				name,
				catalogID,
			); err != nil {
				return nil, err
			}
		}
		inputLimit := entry.Limit.Input
		if inputLimit == entry.Limit.Context {
			inputLimit = 0
		}
		specs = append(specs, syncedSpec{
			name:          name,
			contextWindow: entry.Limit.Context,
			inputLimit:    inputLimit,
			maxTokens:     entry.Limit.Output,
			imageEnabled:  slices.Contains(entry.Modalities.Input, "image"),
		})
	}
	return specs, nil
}

func renderSpecs(specs []syncedSpec) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by internal/project/model/cmd/modelgen from models.dev. DO NOT EDIT.\n\n")
	buf.WriteString("package model\n\n")
	buf.WriteString("type modelSpec struct {\n")
	buf.WriteString("\tContextWindow int\n")
	buf.WriteString("\tInputLimit    int\n")
	buf.WriteString("\tMaxTokens     int\n")
	buf.WriteString("\tImageEnabled  bool\n")
	buf.WriteString("}\n\n")
	buf.WriteString("var syncedSpecs = map[string]modelSpec{\n")
	for _, s := range specs {
		if s.inputLimit > 0 {
			fmt.Fprintf(
				&buf,
				"\t%q: {ContextWindow: %s, InputLimit: %s, MaxTokens: %s, ImageEnabled: %t},\n",
				s.name,
				formatInt(s.contextWindow),
				formatInt(s.inputLimit),
				formatInt(s.maxTokens),
				s.imageEnabled,
			)
			continue
		}
		fmt.Fprintf(
			&buf,
			"\t%q: {ContextWindow: %s, MaxTokens: %s, ImageEnabled: %t},\n",
			s.name,
			formatInt(s.contextWindow),
			formatInt(s.maxTokens),
			s.imageEnabled,
		)
	}
	buf.WriteString("}\n")

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated specs: %w", err)
	}
	return src, nil
}

func formatInt(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append(parts, s[len(s)-3:])
		s = s[:len(s)-3]
	}
	parts = append(parts, s)
	slices.Reverse(parts)
	return strings.Join(parts, "_")
}
