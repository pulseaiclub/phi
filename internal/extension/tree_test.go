package extension_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm"
)

func TestTreePXBExtensionDeniesSuppliesSummaryAndReceivesNavigation(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(t.TempDir(), "event")
	t.Setenv("PHI_TEST_TREE_EVENT", output)
	source := `package main
import (
  "os"
  "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)
func main() {
  m := phi.New("tree", "1.0.0")
  m.OnSessionBeforeTree(func(ev ext.SessionBeforeTreeEvent) *ext.SessionBeforeTreeResult {
    if ev.Instructions == "deny" { return &ext.SessionBeforeTreeResult{Cancel:true, Reason:"stay here"} }
    return &ext.SessionBeforeTreeResult{Summary:"extension branch summary", Instructions:"keep decisions"}
  })
  m.OnSessionTree(func(ev ext.SessionTreeEvent) { _ = os.WriteFile(os.Getenv("PHI_TEST_TREE_EVENT"), []byte(ev.FromID+"\n"+ev.TargetID+"\n"+ev.SummaryID+"\n"+ev.Summary), 0600) })
  _ = m.Run()
}`
	require.NoError(t, extension.Materialize(t.Context(), filepath.Join(root, "tree"), "tree", "1.0.0", source))
	r, warnings, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warnings)
	t.Cleanup(r.Close)
	s, err := agent.NewSession()
	require.NoError(t, err)
	require.NoError(t, s.AddUser("original"))
	u := s.LastID()
	require.NoError(t, s.Append(llm.Message{Role: llm.RoleAssistant, Content: "answer"}))
	a := s.LastID()
	engine, err := agent.NewEngine(
		llm.ModelConfig{Name: "test", BaseURL: "http://127.0.0.1:9"},
		s,
		agent.WithExtensions(r),
	)
	require.NoError(t, err)
	_, err = engine.NavigateTree(t.Context(), u, agent.TreeOptions{Instructions: "deny"})
	require.ErrorContains(t, err, "stay here")
	assert.Equal(t, a, s.LastID())
	_, err = engine.NavigateTree(t.Context(), u, agent.TreeOptions{Summarize: true})
	require.NoError(t, err)
	assert.Contains(t, s.BuildContext()[0].Content, "extension branch summary")
	require.Eventually(
		t,
		func() bool { _, err := os.Stat(output); return err == nil },
		3*time.Second,
		10*time.Millisecond,
	)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, a+"\n"+s.LastID()+"\n"+s.LastID()+"\nextension branch summary", string(data))
}
