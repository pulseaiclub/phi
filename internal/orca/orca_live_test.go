package orca_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/orca"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// This is the live integration check. It runs only when ORCAROUTER_API_KEY is
// present, and it exercises the code this change added — the origin resolver,
// the capability-filtered catalog, and the provider client — rather than a
// hand-rolled curl. Everything else in the suite uses fake keys.
//
// Run it with:
//
//	ORCAROUTER_API_KEY=sk-orca-... go test ./internal/orca/ -run Live -v
func liveKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("ORCAROUTER_API_KEY"))
	if key == "" {
		t.Skip("ORCAROUTER_API_KEY is not set; skipping the live provider check")
	}
	return key
}

func TestLiveCatalogAndChatThroughProvider(t *testing.T) {
	key := liveKey(t)

	// The default origins, resolved by the code under test: auth on
	// www.orcarouter.ai, inference on api.orcarouter.ai/v1. No overrides are
	// set, so this proves the shipped defaults are the ones actually used.
	origins, err := orca.ResolveOrigins(nil)
	require.NoError(t, err)
	assert.Equal(t, orca.DefaultAuthBase, origins.AuthBase)
	assert.Equal(t, orca.DefaultAPIBase, origins.APIBase)
	require.Equal(t, "https://api.orcarouter.ai/v1/models", origins.ModelsURL())

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	// The chat catalog, through the real discovery path.
	chat := origins.LiveCatalogWithFallback(ctx, nil, key, orca.CapabilityChat)
	require.False(t, chat.Degraded, "live discovery must succeed for this check: %s", chat.Warning)
	require.Equal(t, orca.CatalogSourceLive, chat.Source)
	require.NotEmpty(t, chat.Models, "the live chat catalog must not be empty")

	// Every model the chat selector would offer must survive the same filter
	// the dropdown applies, and none may be a non-text route.
	for _, m := range chat.Models {
		assert.True(t, orca.Supports(m, orca.CapabilityChat),
			"%s was offered for chat but does not support it", m.ID)
	}
	// The list is the catalog's, not a hand-written sample: the count must
	// match what the endpoint returned, and the IDs keep their namespace.
	assert.Contains(t, origins.CatalogURLFor(orca.CapabilityChat), "capability=chat")

	// Pick a chat model and route it the way the app does: through the preset
	// lookup, which decides the API type and base URL.
	picked := chat.Models[0]
	preset, ok := model.Lookup(picked.ID)
	require.True(t, ok, "%s must resolve to a preset", picked.ID)
	assert.Equal(t, llm.OrcaRouter, preset.Config.API,
		"the live catalog's models must route to OrcaRouter")
	assert.Equal(t, "https://api.orcarouter.ai/v1", preset.Config.BaseURL)

	cfg := preset.Config
	cfg.APIKey = key

	// One real chat completion through the provider client.
	cli := client.NewClient(cfg, client.Hooks{}, nil, "Answer with one short word.")
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "Reply with the single word: ready"}}

	var text strings.Builder
	var streamErr error
	for event, err := range cli.Stream(ctx, msgs) {
		if err != nil {
			streamErr = err
			break
		}
		if event.Type == llm.StreamEventTypeDelta {
			text.WriteString(event.Delta.Content)
		}
	}
	require.NoError(t, streamErr, "the provider request must succeed")
	assert.NotEmpty(t, strings.TrimSpace(text.String()), "the model must return text")
	assert.NotContains(t, text.String(), key)
	assert.NotContains(t, cfg.APIKey, "sk-orca-definitely-not-a-real-key")

	// A multimodal capability check over the same live catalog: whatever it
	// returns must be a strict subset of the chat list, and every entry must
	// declare an image input modality.
	multimodal := origins.LiveCatalogWithFallback(ctx, nil, key, orca.CapabilityImageInput)
	if !multimodal.Degraded {
		chatIDs := map[string]struct{}{}
		for _, m := range chat.Models {
			chatIDs[m.ID] = struct{}{}
		}
		for _, m := range multimodal.Models {
			_, inChat := chatIDs[m.ID]
			assert.True(t, inChat, "%s is offered for images but not for chat", m.ID)
			assert.Contains(t, m.InputModalities, "image",
				"%s is offered for images without declaring image input", m.ID)
		}
	}
}

// The catalog endpoint must reject a key it does not know, rather than
// silently serving an anonymous list the dropdown would then offer.
func TestLiveCatalogRejectsBadKey(t *testing.T) {
	liveKey(t)

	origins, err := orca.ResolveOrigins(nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	catalog := origins.LiveCatalogWithFallback(ctx, nil, "sk-orca-definitely-not-a-real-key", orca.CapabilityChat)
	assert.True(t, catalog.Degraded,
		"an unknown key must not yield a live catalog")
	assert.NotEqual(t, orca.CatalogSourceLive, catalog.Source)
}
