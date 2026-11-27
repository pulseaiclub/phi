package configui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/project"
)

// newTestEditor builds an editor over a fake home, so paths in the chrome stay
// short and "~" shortening is exercised.
func newTestEditor(t *testing.T, doc *project.ConfigDoc) *ConfigEditor {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	e := New(doc, filepath.Join(home, ".phi", "config.yaml"), filepath.Join(home, ".phi", "skills"),
		components.DarkTheme(), nil, nil)
	require.NotEmpty(t, e.rows)
	return e
}

// focus moves the cursor onto key, failing the test when the row is missing.
func focus(t *testing.T, e *ConfigEditor, key string) {
	t.Helper()
	for i := range e.rows {
		if e.rows[i].key == key {
			e.setCursor(i)
			require.Equal(t, key, e.rows[e.cursor].key)
			return
		}
	}
	t.Fatalf("no row with key %q", key)
}

func press(e *ConfigEditor, code xui.KeyCode, r rune) *components.EventContext {
	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Code: code, Rune: r, Press: true})
	return ctx
}

func typeRune(e *ConfigEditor, r rune) {
	press(e, xui.KeyRune, r)
}

func sampleDoc() *project.ConfigDoc {
	return &project.ConfigDoc{
		Models: []project.ModelDoc{
			{Name: "model-a", APIKey: "key-a", BaseURL: "https://a.example/v1", Default: true},
			{Name: "model-b", APIKey: "key-b", BaseURL: "https://b.example/v1"},
		},
	}
}

func TestBuildRowsCoverEverySection(t *testing.T) {
	e := newTestEditor(t, sampleDoc())

	var sections []string
	for _, r := range e.rows {
		if r.kind == rowSection {
			sections = append(sections, r.title)
		}
	}
	assert.Equal(t, []string{"Models", "Skills", "Sub-agents"}, sections)

	// Every row key must be addressable, and the first model's default badge
	// must follow the document.
	require.Equal(t, "default", e.rows[1].badge)
	assert.Equal(t, "model-a", e.rows[1].name)
	assert.Empty(t, e.rows[2].badge)
}

func TestThinkingChoicesFollowPresetAndEndpoint(t *testing.T) {
	for _, tc := range []struct {
		entry project.ModelDoc
		want  []string
	}{
		{project.ModelDoc{Name: "group/auto-kimi-k3"}, []string{"", "low", "high", "max"}},
		{project.ModelDoc{Name: "kimi-k3", RequestParameters: map[string]any{"messages": nil}}, []string{"", "low", "high", "max"}},
		{project.ModelDoc{Name: "opaque", Preset: "glm-5.3", ThinkLevel: "medium"}, []string{"", "low", "high", "max"}},
		{project.ModelDoc{Name: "vendor/deepseek-v4-pro", BaseURL: "https://opencode.ai/zen/go/v1"}, []string{"", "high", "max"}},
		{project.ModelDoc{Name: "custom"}, []string{"", "off", "minimal", "low", "medium", "high", "xhigh", "max"}},
	} {
		t.Run(tc.entry.Name, func(t *testing.T) {
			e := newTestEditor(t, &project.ConfigDoc{Models: []project.ModelDoc{tc.entry}})
			assertThinkingChoices(t, e, tc.want)
		})
	}
}

func TestMaxTokensFieldUsesPresetAndOverride(t *testing.T) {
	doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "deepseek-flash"}}}
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(0, fMax))
	r, _ := e.focused()
	assert.Equal(t, "393k", r.display, "the preset max_tokens should be visible as the placeholder")

	e.commit(modelKey(0, fMax), "64000")
	require.NotNil(t, doc.Models[0].MaxTokens)
	assert.Equal(t, 64000, *doc.Models[0].MaxTokens)
	r, _ = e.focused()
	assert.Equal(t, "64000", r.display)
	for _, value := range []string{"0", "-1", "invalid"} {
		require.Error(t, e.apply(modelKey(0, fMax), value))
		assert.Equal(t, 64000, *doc.Models[0].MaxTokens, "invalid input must preserve the stored limit")
	}

	e.commit(modelKey(0, fMax), "")
	assert.Nil(t, doc.Models[0].MaxTokens, "clearing the field should restore the preset")
	focus(t, e, modelKey(0, fMax))
	r, _ = e.focused()
	assert.Equal(t, "393k", r.display)
}

func assertThinkingChoices(t *testing.T, e *ConfigEditor, want []string) {
	t.Helper()
	focus(t, e, modelKey(0, fThink))
	var choices []string
	for _, choice := range e.rows[e.cursor].options {
		choices = append(choices, choice.value)
	}
	assert.Equal(t, want, choices)
}

func TestInvalidModelRetainsDefaultsAndShowsError(t *testing.T) {
	for _, entry := range []project.ModelDoc{
		{Name: "kimi-k3", ThinkLevel: "medium"},
		{Name: "kimi-k3", RequestParameters: core.RequestParameters{"messages": nil}},
		{Name: "custom", Preset: "unknown"},
	} {
		t.Run(entry.Name+entry.ThinkLevel+entry.Preset, func(t *testing.T) {
			e := newTestEditor(t, &project.ConfigDoc{Models: []project.ModelDoc{entry}})
			_, err := project.ResolveModelDoc(entry)
			require.Error(t, err)
			text := components.SurfaceText(drawForm(t, e))
			assert.Contains(t, text, strings.Split(err.Error(), ";")[0])
			if entry.Preset != "" {
				focus(t, e, modelKey(0, fURL))
				assert.Equal(t, defaultBaseURL, e.rows[e.cursor].display)
				return
			}
			focus(t, e, modelKey(0, "header"))
			assert.Equal(t, "OpenAI · 1.0M context", e.rows[e.cursor].note)
			focus(t, e, modelKey(0, fURL))
			assert.Equal(t, "https://api.moonshot.cn/v1", e.rows[e.cursor].display)
			focus(t, e, modelKey(0, fCtx))
			assert.Equal(t, "1.0M", e.rows[e.cursor].display)
			focus(t, e, modelKey(0, fImage))
			assert.Equal(t, "loader default: on", e.rows[e.cursor].hint)
			if entry.ThinkLevel != "" {
				e.commit(modelKey(0, fThink), "high")
				assert.NotContains(t, components.SurfaceText(drawForm(t, e)), "unsupported")
			}
		})
	}
}

func TestEditsRefreshThinkingChoices(t *testing.T) {
	e := newTestEditor(t, &project.ConfigDoc{Models: []project.ModelDoc{{
		Name: "vendor/deepseek-v4-pro",
	}}})
	assertThinkingChoices(t, e, []string{"", "off", "low", "high", "max"})

	e.commit(modelKey(0, fURL), "https://opencode.ai/zen/go/v1")
	drawForm(t, e)
	assertThinkingChoices(t, e, []string{"", "high", "max"})

	e.commit(modelKey(0, fAPI), "Anthropic")
	drawForm(t, e)
	assertThinkingChoices(t, e, []string{"", "off", "low", "high", "max"})
}

func TestFetchedModelRefreshesCachedRows(t *testing.T) {
	e := newTestEditor(t, &project.ConfigDoc{Models: []project.ModelDoc{{Name: "custom"}}})
	e.fetch = fetchState{done: true, index: 0, name: "custom", models: []ModelInfo{{ID: "group/auto-kimi-k3"}}}
	drawForm(t, e)
	require.True(t, e.picker.Open)
	press(e, xui.KeyEnter, 0)
	assert.Equal(t, "group/auto-kimi-k3", e.doc.Models[0].Name)
	assert.Contains(t, components.SurfaceText(drawForm(t, e)), "group/auto-kimi-k3")
	assertThinkingChoices(t, e, []string{"", "low", "high", "max"})
}

func TestFetchPrefillsUnsetFieldsWithoutOverwriting(t *testing.T) {
	imgOn, imgOff := true, false

	t.Run("unknown model prefills unset fields and shows metadata in picker and footer", func(t *testing.T) {
		doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "custom"}}}
		e := newTestEditor(t, doc)
		e.fetch = fetchState{
			done:  true,
			index: 0,
			name:  "custom",
			models: []ModelInfo{
				{ID: "qwen/qwen3-coder", ContextWindow: 262_144, MaxTokens: 32_768, ImageEnabled: &imgOn},
				// One unusually long model ID must not crush metadata details on other rows.
				{ID: "vendor/very-long-gateway-model-name-with-many-qualifiers-and-tags:free"},
			},
		}
		text := components.SurfaceText(drawForm(t, e))
		require.True(t, e.picker.Open)
		assert.Contains(t, text, "262k context · 32k output · image")

		press(e, xui.KeyEnter, 0)
		m := doc.Models[0]
		assert.Equal(t, "qwen/qwen3-coder", m.Name)
		require.NotNil(t, m.ContextWindow)
		assert.Equal(t, 262_144, *m.ContextWindow)
		require.NotNil(t, m.MaxTokens)
		assert.Equal(t, 32_768, *m.MaxTokens)
		require.NotNil(t, m.ImageEnabled)
		assert.True(t, *m.ImageEnabled)
		assert.Contains(t, components.SurfaceText(drawForm(t, e)), "prefilled context window, max tokens, image input")
	})

	t.Run("existing user values are never overwritten", func(t *testing.T) {
		existing := 64_000
		doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "custom", ContextWindow: &existing}}}
		e := newTestEditor(t, doc)
		e.fetch = fetchState{
			done:  true,
			index: 0,
			name:  "custom",
			models: []ModelInfo{
				{ID: "qwen/qwen3-coder", ContextWindow: 262_144, MaxTokens: 32_768, ImageEnabled: &imgOff},
			},
		}
		drawForm(t, e)
		press(e, xui.KeyEnter, 0)
		assert.Equal(t, 64_000, *doc.Models[0].ContextWindow)
		require.NotNil(t, doc.Models[0].MaxTokens)
		assert.Equal(t, 32_768, *doc.Models[0].MaxTokens)
		assert.Nil(t, doc.Models[0].ImageEnabled, "false matches the unknown-model default and stays unset")
	})

	t.Run("preset-matching limits stay unset while gateway caps are prefilled", func(t *testing.T) {
		doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "custom"}}}
		e := newTestEditor(t, doc)
		e.fetch = fetchState{
			done: true, index: 0, name: "custom",
			models: []ModelInfo{{
				ID: "openai/gpt-6.2-astra", ContextWindow: 1_050_000, MaxTokens: 128_000, ImageEnabled: &imgOn,
			}},
		}
		drawForm(t, e)
		press(e, xui.KeyEnter, 0)
		assert.Equal(t, "openai/gpt-6.2-astra", doc.Models[0].Name)
		assert.Nil(t, doc.Models[0].ContextWindow, "preset-identical context_window stays unset")
		assert.Nil(t, doc.Models[0].MaxTokens, "preset-identical max_tokens stays unset")
		assert.Nil(t, doc.Models[0].ImageEnabled, "preset-identical image_enabled stays unset")

		e.fetch = fetchState{
			done: true, index: 0, name: "openai/gpt-6.2-astra",
			models: []ModelInfo{{
				ID: "anthropic/claude-sonnet-4.6", ContextWindow: 200_000, MaxTokens: 32_000, ImageEnabled: &imgOff,
			}},
		}
		drawForm(t, e)
		press(e, xui.KeyEnter, 0)
		require.NotNil(t, doc.Models[0].ContextWindow)
		assert.Equal(t, 200_000, *doc.Models[0].ContextWindow)
		require.NotNil(t, doc.Models[0].MaxTokens)
		assert.Equal(t, 32_000, *doc.Models[0].MaxTokens)
		require.NotNil(t, doc.Models[0].ImageEnabled)
		assert.False(t, *doc.Models[0].ImageEnabled, "explicit false overrides a vision-enabled preset")
	})

	t.Run("tighter gateway window prefills total window while keeping preset input cap", func(t *testing.T) {
		for _, tc := range []struct {
			advertised     int
			wantCtx        int
			wantCompaction int
			wantSet        bool
		}{
			{advertised: 1_000_000, wantCtx: 1_000_000, wantCompaction: 922_000, wantSet: true},
			{advertised: 200_000, wantCtx: 200_000, wantCompaction: 200_000, wantSet: true},
			{advertised: 2_000_000, wantCtx: 1_050_000, wantCompaction: 922_000, wantSet: false},
		} {
			doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "custom"}}}
			e := newTestEditor(t, doc)
			e.openModelPicker(
				0,
				[]ModelInfo{{ID: "openai/gpt-6.2-astra", ContextWindow: tc.advertised, MaxTokens: 256_000}},
				"",
				"",
			)
			assert.Contains(
				t,
				components.SurfaceText(drawForm(t, e)),
				humanTokens(tc.advertised)+" context · 256k output",
			)
			press(e, xui.KeyEnter, 0)

			m := doc.Models[0]
			if tc.wantSet {
				require.NotNil(t, m.ContextWindow)
				assert.Equal(t, tc.wantCtx, *m.ContextWindow)
			} else {
				assert.Nil(t, m.ContextWindow)
			}
			assert.Nil(t, m.MaxTokens)
			cfg, err := project.ResolveModelDoc(m)
			require.NoError(t, err)
			assert.Equal(t, tc.wantCtx, cfg.ContextWindow)
			assert.Equal(t, tc.wantCompaction, cfg.CompactionWindow())
		}
	})

	t.Run("minimal preset entry preserves discovered endpoint for unrecognized model", func(t *testing.T) {
		doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "gemini-4-neon", APIKey: "k"}}}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		gotCh := make(chan [2]string, 1)
		redrawCh := make(chan struct{}, 1)
		list := ModelLister(func(_ context.Context, baseURL, _, api, _ string) ([]ModelInfo, error) {
			gotCh <- [2]string{baseURL, api}
			return []ModelInfo{{ID: "gemma-4-27b-it", ContextWindow: 131_072, MaxTokens: 8_192}}, nil
		})
		e := New(doc, filepath.Join(home, ".phi", "config.yaml"), filepath.Join(home, ".phi", "skills"),
			components.DarkTheme(), list, func() { redrawCh <- struct{}{} })
		focus(t, e, modelKey(0, fName))
		e.fetchModels()
		select {
		case got := <-gotCh:
			assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta", got[0])
			assert.Equal(t, "Gemini", got[1])
		case <-time.After(time.Second):
			require.FailNow(t, "fetch did not invoke ModelLister")
		}
		select {
		case <-redrawCh:
		case <-time.After(time.Second):
			require.FailNow(t, "fetch goroutine did not finish")
		}

		drawForm(t, e)
		require.True(t, e.picker.Open)
		press(e, xui.KeyEnter, 0)
		cfg, err := project.ResolveModelDoc(doc.Models[0])
		require.NoError(t, err)
		assert.Equal(t, "gemma-4-27b-it", cfg.Name)
		assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta", cfg.BaseURL)
		assert.Equal(t, core.Gemini, cfg.API)
		assert.Equal(t, 131_072, cfg.ContextWindow)
	})

	t.Run("stale fetch is discarded when connection or api key changes in flight", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			model  project.ModelDoc
			result fetchState
		}{
			{
				name:  "base_url changed",
				model: project.ModelDoc{Name: "custom", BaseURL: "https://new.example/v1"},
				result: fetchState{
					done: true, index: 0, name: "custom", baseURL: "https://old.example/v1",
					models: []ModelInfo{{ID: "custom", ContextWindow: 32_768}},
				},
			},
			{
				name:  "api_key changed",
				model: project.ModelDoc{Name: "custom", BaseURL: "https://gw.example/v1", APIKey: "new-key"},
				result: fetchState{
					done: true, index: 0, name: "custom", apiKey: "old-key", baseURL: "https://gw.example/v1",
					models: []ModelInfo{{ID: "custom", ContextWindow: 32_768}},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				doc := &project.ConfigDoc{Models: []project.ModelDoc{tc.model}}
				e := newTestEditor(t, doc)
				e.fetch = tc.result
				e.pollFetch()
				assert.False(t, e.picker.Open)
				assert.Contains(t, e.status, "changed while fetching")
				assert.Nil(t, doc.Models[0].ContextWindow)
			})
		}
	})
}

func TestCursorSkipsNonSelectableRows(t *testing.T) {
	doc := sampleDoc()
	doc.Models[0].Preset = "unknown"
	e := newTestEditor(t, doc)
	drawForm(t, e)
	for i, r := range e.rows {
		if !r.selectable() {
			assert.Equal(t, -1, e.rowAt(e.rowsTop+i-e.scroll))
		}
	}
	for range len(e.rows) * 2 {
		e.move(1)
		require.True(t, e.rows[e.cursor].selectable())
	}
	for range len(e.rows) * 2 {
		e.move(-1)
		require.True(t, e.rows[e.cursor].selectable())
	}

	e.jumpEdge(true)
	require.True(t, e.rows[e.cursor].selectable())
	e.jumpEdge(false)
	require.True(t, e.rows[e.cursor].selectable())
}

func TestEditModelNameRejectsEmpty(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(0, fName))
	press(e, xui.KeyEnter, 0)
	require.NotNil(t, e.edit, "Enter must open the inline editor")

	press(e, xui.KeyBackspace, 0)
	for range len("model-a") - 1 {
		press(e, xui.KeyBackspace, 0)
	}
	press(e, xui.KeyEnter, 0)
	require.NotNil(t, e.edit, "an empty name must keep the editor open")
	assert.Equal(t, "model-a", doc.Models[0].Name)
	assert.Equal(t, statusError, e.statusKind)

	typeRune(e, 'x')
	press(e, xui.KeyEnter, 0)
	assert.Nil(t, e.edit)
	assert.Equal(t, "x", doc.Models[0].Name)
	assert.True(t, e.Dirty())
}

func TestEditKeyPromptUsesNoCLIValues(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(0, fKey))
	press(e, xui.KeyEnter, 0)
	require.NotNil(t, e.edit)
	assert.Equal(t, "key-a", e.edit.line.Text(), "editing starts from the stored value")

	press(e, xui.KeyEscape, 0)
	assert.Nil(t, e.edit)
	assert.False(t, e.Dirty(), "canceling an edit must not dirty the document")
}

func TestCycleChoiceAndTriState(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(0, fAPI))
	press(e, xui.KeyRight, 0)
	assert.Equal(t, "OpenAI", doc.Models[0].API)
	press(e, xui.KeyLeft, 0)
	assert.Empty(t, doc.Models[0].API)

	focus(t, e, modelKey(0, fImage))
	press(e, xui.KeyRight, 0)
	require.NotNil(t, doc.Models[0].ImageEnabled)
	assert.True(t, *doc.Models[0].ImageEnabled)
	press(e, xui.KeyRight, 0)
	require.NotNil(t, doc.Models[0].ImageEnabled)
	assert.False(t, *doc.Models[0].ImageEnabled)
	press(e, xui.KeyRight, 0)
	assert.Nil(t, doc.Models[0].ImageEnabled, "the third step must return to auto (absent)")
}

func TestDefaultModelIsExclusive(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(1, fDeflt))
	press(e, xui.KeyEnter, 0)
	assert.False(t, doc.Models[0].Default)
	assert.True(t, doc.Models[1].Default)
}

func TestAddAndDeleteModel(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	typeRune(e, 'a')
	require.Len(t, doc.Models, 3)
	assert.Equal(t, "new model — type a model id", e.status)
	require.NotNil(t, e.edit, "adding a model opens the name editor")
	typeRune(e, 'c')
	press(e, xui.KeyEnter, 0)
	assert.Equal(t, "c", doc.Models[2].Name)

	focus(t, e, modelKey(2, fName))
	typeRune(e, 'd')
	require.NotNil(t, e.confirm, "deleting a model must confirm first")
	assert.False(t, e.confirm.yes, "a destructive confirm must default to No")
	assert.Len(t, doc.Models, 3)

	press(e, xui.KeyEnter, 0) // Enter takes the default: keep the model.
	assert.Len(t, doc.Models, 3)

	typeRune(e, 'd')
	typeRune(e, 'y')
	assert.Nil(t, e.confirm)
	assert.Len(t, doc.Models, 2)

	// Deleting the default promotes the next model, so the file never relies on
	// the loader's implicit first-entry rule.
	focus(t, e, modelKey(0, fName))
	typeRune(e, 'd')
	typeRune(e, 'y')
	require.Len(t, doc.Models, 1)
	assert.True(t, doc.Models[0].Default)
}

// The confirm rows are painted Yes then No, so the keys must follow that order:
// up/left to the row above, down/right/tab to the row below. Reversed keys made
// the highlight jump at the same key the user pressed and stand still on a
// second press, which read as an unresponsive dialog.
func TestConfirmArrowsFollowRowOrder(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(0, fName))
	typeRune(e, 'd')
	require.NotNil(t, e.confirm)
	require.False(t, e.confirm.yes, "a destructive confirm starts on No, the second row")

	press(e, xui.KeyUp, 0)
	assert.True(t, e.confirm.yes, "↑ selects the first row (Yes)")
	press(e, xui.KeyDown, 0)
	assert.False(t, e.confirm.yes, "↓ selects the second row (No)")

	press(e, xui.KeyLeft, 0)
	assert.True(t, e.confirm.yes, "← is the same axis as ↑")
	press(e, xui.KeyRight, 0)
	assert.False(t, e.confirm.yes, "→ is the same axis as ↓")

	press(e, xui.KeyRune, 'k')
	assert.True(t, e.confirm.yes)
	press(e, xui.KeyRune, 'j')
	assert.False(t, e.confirm.yes)

	press(e, xui.KeyEscape, 0)
	assert.Nil(t, e.confirm)
	assert.Len(t, doc.Models, 2, "a cancel must not delete the model")
}

func TestDeleteModelCanBeCancelled(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, modelKey(0, fName))
	typeRune(e, 'd')
	press(e, xui.KeyEscape, 0)
	assert.Nil(t, e.confirm)
	assert.Len(t, doc.Models, 2)
}

func TestSaveWritesFileAndBackup(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".phi")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("models:\n  - name: old\n    api_key: k\n"), 0o600))

	doc, err := project.ReadConfigDoc(path)
	require.NoError(t, err)
	e := New(doc, path, "/skills", components.DarkTheme(), nil, nil)

	focus(t, e, modelKey(0, fName))
	press(e, xui.KeyEnter, 0)
	press(e, xui.KeyEnd, 0)
	for _, r := range "-2" {
		typeRune(e, r)
	}
	press(e, xui.KeyEnter, 0)

	focus(t, e, modelKey(0, fMax))
	press(e, xui.KeyEnter, 0)
	for _, r := range "64000" {
		typeRune(e, r)
	}
	press(e, xui.KeyEnter, 0)

	press(e, xui.KeyRune, 's')
	assert.Equal(t, statusSuccess, e.statusKind, e.status)
	assert.False(t, e.Dirty())

	saved, err := project.ReadConfigDoc(path)
	require.NoError(t, err)
	require.Len(t, saved.Models, 1)
	assert.Equal(t, "old-2", saved.Models[0].Name)
	require.NotNil(t, saved.Models[0].MaxTokens)
	assert.Equal(t, 64000, *saved.Models[0].MaxTokens)

	backup, err := os.ReadFile(path + ".bak")
	require.NoError(t, err)
	assert.Contains(t, string(backup), "name: old")

	// The written file must load through the runtime parser.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := project.Discover("")
	require.NoError(t, err)
	require.NoError(t, p.LoadConfig())
	assert.Equal(t, "old-2", p.Config().Model().Name)
	assert.Equal(t, 64000, p.Config().Model().MaxTokens)
}

func TestSaveRefusesInvalidDocument(t *testing.T) {
	doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "", APIKey: "k"}}}
	e := New(doc, filepath.Join(t.TempDir(), "config.yaml"), "/skills", components.DarkTheme(), nil, nil)

	press(e, xui.KeyRune, 's')
	assert.Equal(t, statusError, e.statusKind)
	assert.Contains(t, e.status, "no name")
}

func TestQuitGuardsUnsavedChanges(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	ctx := press(e, xui.KeyEscape, 0)
	assert.True(t, ctx.Quit, "a clean editor quits on Esc")

	focus(t, e, modelKey(0, fImage))
	typeRune(e, ' ') // toggle a bool through the space shortcut
	require.True(t, e.Dirty())

	ctx = press(e, xui.KeyEscape, 0)
	require.NotNil(t, e.confirm)
	assert.False(t, ctx.Quit)

	ctx = press(e, xui.KeyEscape, 0) // cancel the confirm
	assert.Nil(t, e.confirm)
	assert.False(t, ctx.Quit)

	press(e, xui.KeyEscape, 0)
	ctx = press(e, xui.KeyRune, 'y') // discard
	assert.True(t, ctx.Quit)
}

func TestChoicePickerCommitsSelection(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	focus(t, e, keyAgentsExplore)
	press(e, xui.KeyEnter, 0)
	require.True(t, e.picker.Open)

	press(e, xui.KeyEnter, 0) // first row is "(inherit)".
	assert.False(t, e.picker.Open)
	require.NotNil(t, doc.Agents)
	require.NotNil(t, doc.Agents.Models)
	assert.Empty(t, doc.Agents.Models.Explore)

	focus(t, e, keyAgentsExplore)
	press(e, xui.KeyEnter, 0)
	require.True(t, e.picker.Open)
	typeRune(e, 'b') // filter to model-b
	press(e, xui.KeyEnter, 0)
	assert.Equal(t, "model-b", doc.Agents.Models.Explore)

	focus(t, e, keyAgentsExplore)
	press(e, xui.KeyEnter, 0)
	require.True(t, e.picker.Open)
	press(e, xui.KeyEnter, 0)
	assert.False(t, e.picker.Open, "Enter must close the reopened picker")
	assert.Equal(t, "model-b", doc.Agents.Models.Explore, "opening and accepting must keep the current value")
}

func TestDrawPaintsFormAndStatus(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)

	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 48}, Method: xui.WidthUnicode}
	surf := e.Draw(ctx)
	text := components.SurfaceText(surf)

	assert.Contains(t, text, "phi config")
	assert.Contains(t, text, "model-a")
	assert.Contains(t, text, "~/.phi/config.yaml")
	assert.Contains(t, text, "auto")
	assert.Contains(t, text, "MODELS")
	assert.Contains(t, text, "↑↓ move")

	// The caret follows the inline editor.
	focus(t, e, modelKey(0, fName))
	press(e, xui.KeyEnter, 0)
	surf = e.Draw(ctx)
	require.NotNil(t, surf.Cursor)
	assert.Positive(t, surf.Cursor.Y)

	// An unsaved document is called out in the title bar.
	typeRune(e, 'x')
	press(e, xui.KeyEnter, 0)
	surf = e.Draw(ctx)
	assert.Contains(t, components.SurfaceText(surf), "●")
}

// modelHeader returns the painted header line of one model, without the right
// border or trailing padding. Header rows start at xModel, field rows at
// xModelField, so the leading cell fixes which one this is.
func modelHeader(t *testing.T, text, name string) string {
	t.Helper()
	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(l, "│   "+name) || strings.HasPrefix(l, "│ > "+name) {
			return strings.TrimRight(l, " │")
		}
	}
	require.FailNow(t, "no model header painted", "name=%q", name)
	return ""
}

func TestDrawAlignsModelHeadersToValueColumn(t *testing.T) {
	window := 1_000_000
	doc := &project.ConfigDoc{Models: []project.ModelDoc{
		{Name: "deepseek-v4-flash", API: "OpenAIResponses", ContextWindow: &window, Default: true},
		{Name: "glm-5.2", API: "OpenAIResponses", ContextWindow: &window},
	}}
	e := newTestEditor(t, doc)

	surf := e.Draw(components.DrawContext{Max: components.Size{Width: 100, Height: 48}, Method: xui.WidthUnicode})
	col := e.valueCol()
	text := components.SurfaceText(surf)

	// The name owns the label column, and the default marker plus the summary
	// share the value column: a long name is truncated rather than allowed to
	// paint over the badge.
	name := "deepseek-v4-flash"
	pad := strings.Repeat(" ", col-xModel-len(name))
	assert.Equal(t, "│ > "+name+pad+"default · OpenAIResponses · 1M context", modelHeader(t, text, name))
	assert.Equal(t,
		"│   glm-5.2"+strings.Repeat(" ", col-xModel-len("glm-5.2"))+"OpenAIResponses · 1M context",
		modelHeader(t, text, "glm-5.2"))
}

func TestDrawIsStableOnTinyScreens(t *testing.T) {
	e := newTestEditor(t, sampleDoc())
	for _, size := range []components.Size{{Width: 1, Height: 1}, {Width: 20, Height: 4}, {Width: 40, Height: 6}} {
		surf := e.Draw(components.DrawContext{Max: size, Method: xui.WidthUnicode})
		assert.Equal(t, size, surf.Size)
	}
}

// The footer keeps the destructive shortcut visible while the model is focused.
func TestFooterHintsDeletion(t *testing.T) {
	e := newTestEditor(t, sampleDoc())
	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 32}, Method: xui.WidthUnicode}
	assert.Contains(t, components.SurfaceText(e.Draw(ctx)), "d delete")
}

// A footer too narrow for every hint drops whole ones from the end; it never
// paints half a word.
func TestFooterDropsHintsItCannotFit(t *testing.T) {
	e := newTestEditor(t, sampleDoc())
	segs := e.hintSegments()
	assert.Equal(t, segs, strings.Split(e.hintLine(200, xui.WidthUnicode), chrome.Sep))

	for _, w := range []int{30, 50, 80, 100} {
		budget := max(w-6, 1)
		line := e.hintLine(budget, xui.WidthUnicode)
		require.NotEmpty(t, line)
		assert.LessOrEqual(t, xui.StringWidth(line, xui.WidthUnicode), budget)
		assert.Equal(t, segs[:strings.Count(line, chrome.Sep)+1], strings.Split(line, chrome.Sep))
	}
}

func TestMouseWheelMovesAndClickSelects(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)
	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 32}, Method: xui.WidthUnicode}
	e.Draw(ctx)

	start := e.cursor
	ev := &components.EventContext{}
	e.Handle(ev, xui.MouseEvent{Action: xui.MousePress, Button: xui.MouseWheelDown, Wheel: 2})
	assert.Equal(t, min(start+2, len(e.rows)-1), e.cursor)

	// A click on a selectable row selects it.
	y := e.rowsTop + (1 - e.scroll)
	require.GreaterOrEqual(t, y, e.rowsTop)
	ev = &components.EventContext{}
	e.Handle(ev, xui.MouseEvent{X: 6, Y: y, Action: xui.MousePress, Button: xui.MouseLeft})
	assert.Equal(t, 1, e.cursor)
}

func TestMouseDropsInlineEditOnClickAway(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)
	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 32}, Method: xui.WidthUnicode}
	e.Draw(ctx)

	// Open the inline editor on the first model's name.
	focus(t, e, modelKey(0, fName))
	e.activate()
	require.NotNil(t, e.edit)
	typeRune(e, 'X')

	// Click a different row: the edit is dropped (no silent commit), cursor
	// moves to the clicked row, and the buffer is gone.
	ev := &components.EventContext{}
	e.Handle(ev, xui.MouseEvent{X: 6, Y: e.rowsTop + (3 - e.scroll), Action: xui.MousePress, Button: xui.MouseLeft})
	assert.Nil(t, e.edit)
	assert.NotEqual(t, modelKey(0, fName), e.rows[e.cursor].key)
}

func TestMouseDismissesPickerAndIgnoresConfirm(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)
	ctx := components.DrawContext{Max: components.Size{Width: 100, Height: 32}, Method: xui.WidthUnicode}
	e.Draw(ctx)

	// A press while the picker is open closes it without touching the form.
	focus(t, e, modelKey(0, fAPI))
	e.activate() // opens the api picker
	require.True(t, e.picker.Open)
	before := e.cursor
	ev := &components.EventContext{}
	e.Handle(ev, xui.MouseEvent{X: 5, Y: 5, Action: xui.MousePress, Button: xui.MouseLeft})
	assert.False(t, e.picker.Open)
	assert.Equal(t, before, e.cursor)

	// A press while the confirm modal is up is ignored entirely.
	e.dirty = true // requestQuit only shows the modal when dirty
	e.requestQuit(&components.EventContext{})
	require.NotNil(t, e.confirm)
	cur := e.cursor
	ev = &components.EventContext{}
	e.Handle(ev, xui.MouseEvent{X: 5, Y: 5, Action: xui.MousePress, Button: xui.MouseLeft})
	require.NotNil(t, e.confirm)
	assert.Equal(t, cur, e.cursor)
}

func TestSkillsFallbackIsShownWhenUnset(t *testing.T) {
	doc := sampleDoc()
	e := newTestEditor(t, doc)
	focus(t, e, keySkillPath)
	r, _ := e.focused()
	assert.Equal(t, "~/.phi/skills", r.display)
	assert.Equal(t, styleMuted, r.style)
	// Empty skill_path stays absent from the document.
	press(e, xui.KeyEnter, 0)
	press(e, xui.KeyEnter, 0)
	assert.Nil(t, doc.SkillPath)
}

func TestFetchDiscardedWhenModelMoves(t *testing.T) {
	doc := sampleDoc()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	list := ModelLister(func(_ context.Context, _, _, _, _ string) ([]ModelInfo, error) {
		close(fetchStarted)
		<-releaseFetch
		return []ModelInfo{{ID: "alpha"}, {ID: "beta"}}, nil
	})
	e := New(doc, filepath.Join(home, ".phi", "config.yaml"), filepath.Join(home, ".phi", "skills"),
		components.DarkTheme(), list, func() {})

	// Start a fetch on model 0, then delete model 0 before the result lands so
	// model 1 shifts into its slot. The fetch's captured index/name pair must no
	// longer match, and the picker must not open for the wrong model.
	focus(t, e, modelKey(0, fName))
	e.fetchModels()
	require.Eventually(t, func() bool {
		select {
		case <-fetchStarted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	e.fetchMu.Lock()
	active := e.fetch.active
	e.fetchMu.Unlock()
	require.True(t, active)

	// Delete model 0 (default promotion keeps model 1 as the new default).
	e.deleteModel(0)
	require.Len(t, doc.Models, 1)
	assert.Equal(t, "model-b", doc.Models[0].Name)

	close(releaseFetch)
	// Drain the background fetch synchronously: it only touches fetchMu state.
	require.Eventually(t, func() bool {
		e.fetchMu.Lock()
		done := e.fetch.done
		e.fetchMu.Unlock()
		return done
	}, time.Second, time.Millisecond)

	e.pollFetch()
	assert.False(t, e.picker.Open, "picker must not open for a model that moved")
	assert.Contains(t, e.status, "changed while fetching")
}

// drawForm paints the editor at a fixed frame size and returns the surface.
func drawForm(t *testing.T, e *ConfigEditor) components.Surface {
	t.Helper()
	const width, height = 100, 40
	return e.Draw(components.DrawContext{
		Max:    components.Size{Width: width, Height: height},
		Method: xui.WidthUnicode,
	})
}

// formRowY is the screen row a form key landed on in the last Draw.
func formRowY(t *testing.T, e *ConfigEditor, key string) int {
	t.Helper()
	for i := range e.rows {
		if e.rows[i].key == key {
			return 1 + i - e.scroll
		}
	}
	t.Fatalf("no row with key %q", key)
	return 0
}

func paintedStyle(t *testing.T, out components.Surface, x, y int) xui.Style {
	t.Helper()
	require.Less(t, x, out.Size.Width)
	require.Less(t, y, out.Size.Height)
	return out.Buffer[y*out.Size.Width+x].Style
}

// TestFormColumnsKeepOneQuietTone locks the two-column color grammar: the label
// column is one quiet tone, the value column carries the data, and a value the
// file does not set drops back to the label tone instead of inventing a third
// shade of gray.
func TestFormColumnsKeepOneQuietTone(t *testing.T) {
	// Only the name and the masked api key are set; every other field shows
	// what the loader would supply instead.
	doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "model-a", APIKey: "key-a"}}}
	e := newTestEditor(t, doc)
	th := components.DarkTheme()
	require.NotEqual(t, th.Muted.Fg, th.Foreground.Fg, "fixture needs a theme with two distinct tones")
	// Park the cursor on the last row so the asserted rows are not selected.
	focus(t, e, modelKey(0, fDeflt))
	out := drawForm(t, e)
	col := e.valueCol()
	quiet := secondaryStyle(th)

	nameY := formRowY(t, e, modelKey(0, fName))
	label := paintedStyle(t, out, xModelField, nameY)
	assert.Equal(t, quiet.Fg, label.Fg, "the label column keeps the quiet tone")
	assert.False(t, label.Dim, "Dim ranges from soft to invisible; labels must not rely on it")
	assert.Equal(t, th.Foreground.Fg, paintedStyle(t, out, col, nameY).Fg, "a set value is body text")

	keyY := formRowY(t, e, modelKey(0, fKey))
	assert.Equal(t, th.Foreground.Fg, paintedStyle(t, out, col, keyY).Fg,
		"a masked key is set, so it reads as data rather than as an empty field")

	// base_url and context_window are absent from the document.
	for _, key := range []string{modelKey(0, fURL), modelKey(0, fCtx)} {
		assert.Equal(t, quiet.Fg, paintedStyle(t, out, col, formRowY(t, e, key)).Fg,
			"%s must not look set", key)
	}
}

// Empty fields retain their placeholder tone while the active label and the
// entire row carry focus, including the trailing hint.
func TestSelectedRowIsOneStripe(t *testing.T) {
	doc := &project.ConfigDoc{Models: []project.ModelDoc{{Name: "model-a"}}}
	e := newTestEditor(t, doc)
	th := components.DarkTheme()

	// base_url is unset, so the row paints a placeholder plus a trailing hint.
	key := modelKey(0, fURL)
	focus(t, e, key)
	out := drawForm(t, e)
	y := formRowY(t, e, key)

	for x := 1; x < out.Size.Width-1; x++ {
		assert.Equal(t, th.SelectionBg.Bg, paintedStyle(t, out, x, y).Bg,
			"cell %d of the selected row must keep the stripe background", x)
	}

	label := paintedStyle(t, out, xModelField, y)
	empty := paintedStyle(t, out, e.valueCol(), y)
	assert.Equal(t, th.SelectionFg.Fg, label.Fg, "the selected label stays readable")
	assert.True(t, label.Bold, "the active label identifies focus even when the value is unset")
	assert.Equal(t, secondaryStyle(th).Fg, empty.Fg, "a selected empty field still reads as empty")
	assert.False(t, empty.Dim, "the quiet tone stays flat, on the stripe too")

	// Both the label and a filled value carry focus.
	focus(t, e, modelKey(0, fName))
	out = drawForm(t, e)
	y = formRowY(t, e, modelKey(0, fName))
	filledLabel := paintedStyle(t, out, xModelField, y)
	filled := paintedStyle(t, out, e.valueCol(), y)
	assert.Equal(t, th.SelectionFg.Fg, filled.Fg)
	assert.True(t, filled.Bold, "the value column carries the emphasis")
	assert.True(t, filledLabel.Bold)
}

func TestHumanTokens(t *testing.T) {
	cases := map[int]string{
		0:       "",
		999:     "999",
		128000:  "128k",
		1000000: "1M",
		1280000: "1.3M",
		2000000: "2M",
	}
	for n, want := range cases {
		assert.Equal(t, want, humanTokens(n), "n=%d", n)
	}
}
