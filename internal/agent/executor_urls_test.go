package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/skills"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/readtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/urls"
)

func urlSkill(t *testing.T) (string, string, []*skills.Skill) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "example")
	require.NoError(t, os.Mkdir(dir, 0o700))
	path := filepath.Join(dir, "SKILL.md")
	require.NoError(
		t,
		os.WriteFile(path, []byte("---\nname: example\ndescription: example skill\n---\nsecret skill body\n"), 0o600),
	)
	list, err := skills.LoadSkills(root)
	require.NoError(t, err)
	canonical, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return root, canonical, list
}

func runURLRead(t *testing.T, ex *Executor, path string) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{"path": path, "offset": 5, "limit": 1})
	require.NoError(t, err)
	msgs, _, _ := ex.Run(
		t.Context(),
		[]llm.ToolCall{{ID: "read-url", Function: llm.Function{Name: "read", Arguments: string(args)}}},
		func(session.ToolData) bool { return true },
	)
	require.Len(t, msgs, 1)
	return msgs[0].Content
}

func TestExecutorReadURLActualTarget(t *testing.T) {
	_, target, list := urlSkill(t)
	gate := &recordingGate{}
	ex := NewExecutor(tooldef.NewRegistry([]tooldef.Tool{readtool.ReadTool()}), gate, nil, nil)
	ex.readURLs = urls.NewSkillRouter(list)
	content := runURLRead(t, ex, "skill://example")
	assert.Equal(t, []string{target}, gate.last.Paths)
	assert.Contains(t, content, "5|secret skill body")
	assert.NotContains(t, content, "name: example")
	assert.Contains(t, content, "@file ")
	assert.NotContains(t, content, "@file skill://")
}

func TestExecutorReadURLDeniedAndUnknown(t *testing.T) {
	_, target, list := urlSkill(t)
	for _, path := range []string{"skill://example", "unknown://example", "skill://missing", "skill://example/../outside"} {
		t.Run(path, func(t *testing.T) {
			ran := false
			asked := false
			reg := tooldef.Registry{
				"read": {
					Definition: llm.ToolDefinition{Name: "read"},
					Run: func(context.Context, json.RawMessage) (tooldef.Result, error) {
						ran = true
						return tooldef.Result{Content: "unexpected"}, nil
					},
				},
			}
			ex := NewExecutor(
				reg,
				fixedGate{dec: permission.Ask},
				func(_ context.Context, req permission.Request, _ string) (permission.AskResult, error) {
					asked = true
					assert.Equal(t, []string{target}, req.Paths)
					return permission.AskResult{Approved: false}, nil
				},
				nil,
			)
			ex.readURLs = urls.NewSkillRouter(list)
			content := runURLRead(t, ex, path)
			assert.False(t, ran)
			assert.Equal(t, path == "skill://example", asked)
			assert.NotContains(t, content, "unexpected")
			assert.NotEmpty(t, content)
		})
	}
}

func TestExecutorReadURLRejectsTrailingWhitespace(t *testing.T) {
	_, target, list := urlSkill(t)
	asset := filepath.Join(filepath.Dir(target), "asset.txt")
	require.NoError(t, os.WriteFile(asset, []byte("wrong target body"), 0o600))
	require.NoError(t, os.WriteFile(asset+" ", []byte("intended target body"), 0o600))
	gate := &recordingGate{}
	read := readtool.ReadTool()
	run := read.Run
	ran := false
	read.Run = func(ctx context.Context, args json.RawMessage) (tooldef.Result, error) {
		ran = true
		return run(ctx, args)
	}
	ex := NewExecutor(tooldef.NewRegistry([]tooldef.Tool{read}), gate, nil, nil)
	ex.readURLs = urls.NewSkillRouter(list)
	content := runURLRead(t, ex, "skill://example/asset.txt%20")
	assert.Contains(t, content, "leading or trailing whitespace")
	assert.Contains(t, content, "rename the target")
	assert.Equal(t, permission.Request{}, gate.last, "resolution must fail before the gate")
	assert.False(t, ran, "read must not run against the trimmed target")
	assert.NotContains(t, content, "wrong target body")
}

func TestExecutorReadURLGateDeny(t *testing.T) {
	_, _, list := urlSkill(t)
	ex := NewExecutor(tooldef.NewRegistry([]tooldef.Tool{readtool.ReadTool()}),
		fixedGate{dec: permission.Deny, reason: "target denied"}, nil, nil)
	ex.readURLs = urls.NewSkillRouter(list)
	assert.Equal(t, "target denied", runURLRead(t, ex, "skill://example"))
}

func TestExecutorReadURLAfterExtensionRewrite(t *testing.T) {
	_, target, list := urlSkill(t)
	r := loadExt(t, `package main
import (
 "encoding/json"
 "github.com/pulseaiclub/phi/ext/go"
 "github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
 m := phi.New("test", "0.0.1")
 m.OnToolCall(func(ev ext.ToolCallEvent) *ext.ToolCallResult {
  return &ext.ToolCallResult{Input: json.RawMessage(`+"`"+`{"path":"skill://example","offset":5,"limit":1}`+"`"+`)}
 })
 _ = m.Run()
}`)
	gate := &recordingGate{}
	ex := NewExecutor(tooldef.NewRegistry([]tooldef.Tool{readtool.ReadTool()}), gate, nil, r)
	ex.readURLs = urls.NewSkillRouter(list)
	assert.Contains(t, runURLRead(t, ex, "unknown://original"), "secret skill body")
	assert.Equal(t, []string{target}, gate.last.Paths)
}

func TestEngineReadURLUsesConfiguredSkills(t *testing.T) {
	root, target, _ := urlSkill(t)
	sess, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	// Child engines use the same constructor with their own cwd and no jobs.
	eng, err := NewEngine(
		llm.ModelConfig{Name: "test", APIKey: "k", BaseURL: "http://example", SkillPaths: []string{root}},
		sess,
	)
	require.NoError(t, err)
	gate := &recordingGate{}
	eng.SetPermission(gate, nil)
	assert.Contains(t, eng.systemPrompt(), "skill://example")
	assert.Contains(t, runURLRead(t, eng.executor, "skill://example"), "secret skill body")
	assert.Equal(t, []string{target}, gate.last.Paths)
}

func TestExecutorReadOrdinaryPathUnchanged(t *testing.T) {
	ex := NewExecutor(nil, nil, nil, nil)
	for _, path := range []string{"relative/file", "notes:todo", "release:2026-final", "notes:todo://asset", "1invalid://asset"} {
		t.Run(path, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"path": path, "offset": 2, "limit": 3})
			require.NoError(t, err)
			got, err := ex.resolveReadURL(t.Context(), args)
			require.NoError(t, err)
			assert.JSONEq(t, string(args), string(got))
		})
	}
}

func TestExecutorReadOrdinaryColonFilename(t *testing.T) {
	for _, name := range []string{"notes:todo", "release:2026-final"} {
		t.Run(name, func(t *testing.T) {
			cwd := t.TempDir()
			target := filepath.Join(cwd, name)
			require.NoError(t, os.WriteFile(target, []byte("1\n2\n3\n4\ncolon filename body\n"), 0o600))
			gate := &recordingGate{}
			ex := NewExecutor(tooldef.NewRegistry([]tooldef.Tool{readtool.ReadTool()}), gate, nil, nil)
			ex.cwd = cwd
			assert.Contains(t, runURLRead(t, ex, name), "5|colon filename body")
			assert.Equal(t, []string{target}, gate.last.Paths)
		})
	}
}
