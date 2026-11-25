package block

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
)

func TestBashBlockRendersOutput(t *testing.T) {
	var lines []string
	for range 20 {
		lines = append(lines, "file.go")
	}
	b := &BashBlock{
		Command:  "ls",
		Output:   strings.Join(lines, "\n"),
		Status:   status.ToolDone,
		Expanded: true,
		Theme:    components.DefaultTheme(),
	}
	s := b.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 40}})
	joined := components.SurfaceText(s)
	require.Contains(t, joined, "$")
	require.Contains(t, joined, "ls")
	require.NotContains(t, joined, "Show more")
	require.NotContains(t, joined, "lines truncated")
	require.Contains(t, joined, "file.go")
}

func TestCommandChainLayoutAndResize(t *testing.T) {
	command := "go fmt ./... && go test ./... && go build ./cmd"
	bash := &BashBlock{Command: command, Status: status.ToolDone}
	agent := &AgentBlock{
		Name: "agent_spawn", Expanded: true,
		Children: []ChildTool{{Name: "bash", Detail: command, Status: status.ToolDone}},
	}
	for _, tc := range []struct {
		widget components.Widget
		prefix string
		indent string
		skip   int
	}{
		{bash, "$ ", "  ", 0},
		{agent, "╰── ✓ bash ", "           ", 1},
	} {
		for _, width := range []int{40, 100, 40} {
			s := tc.widget.Draw(components.DrawContext{Max: components.Size{Width: width}, Method: xui.WidthUnicode})
			lines := strings.Split(strings.TrimSuffix(components.SurfaceText(s), "\n"), "\n")[tc.skip:]
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " ")
			}
			want := []string{tc.prefix + command}
			if width == 40 && tc.widget == bash {
				want = []string{
					tc.prefix + "go fmt ./... && go test ./... && go bu",
					tc.indent + "ild ./cmd",
				}
			} else if width == 40 {
				want = []string{
					tc.prefix + "go fmt ./... && go test ./...",
					"            && go build ./cmd",
				}
			}
			require.Equal(t, want, lines)
		}
	}
	require.Equal(t, command, bash.Command)
	require.Equal(t, "$ "+command, bash.CopyText())
	require.Equal(t, command, agent.Children[0].Detail)
	require.Contains(t, agent.CopyText(), command)
}

func TestBashCommandContinuation(t *testing.T) {
	b := &BashBlock{Command: "printf 'abcdefghij'\n  echo done", Status: status.ToolDone}
	s := b.Draw(components.DrawContext{Max: components.Size{Width: 14}, Method: xui.WidthUnicode})
	require.Equal(t, "$ printf 'abcd\n  efghij'     \n    echo done \n", components.SurfaceText(s))
	require.Equal(t, "$ "+b.Command, b.CopyText())
}

func TestAgentBashCommandContinuation(t *testing.T) {
	th := components.DefaultTheme()
	a := &AgentBlock{
		Name: "agent_spawn", Status: status.ToolDone, Expanded: true, Theme: th,
		Children: []ChildTool{
			{Name: "bash", Detail: "abcdefghijklmno", Status: status.ToolDone},
			{Name: "bash", Detail: "ABCDEFGHIJKLMNO", Status: status.ToolDone},
		},
	}
	s := a.Draw(components.DrawContext{Max: components.Size{Width: 20}, Method: xui.WidthUnicode})
	lines := strings.Split(strings.TrimSuffix(components.SurfaceText(s), "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	require.Equal(t, []string{
		"├── ✓ bash abcdefghi",
		"│          jklmno",
		"╰── ✓ bash ABCDEFGHI",
		"           JKLMNO",
	}, lines[1:])
	require.True(t, th.Foreground.Equal(s.Buffer[20+11].Style), "command must be readable")
	require.True(t, th.Muted.Equal(s.Buffer[40].Style), "tree guide stays muted")
}

func TestWrappedBashTitleKeepsOutputToggle(t *testing.T) {
	b := &BashBlock{
		Command: strings.Repeat("x", 30), Output: "output", Status: status.ToolDone,
	}
	ctx := components.DrawContext{Max: components.Size{Width: 14}, Method: xui.WidthUnicode}
	s := b.Draw(ctx)
	require.Greater(t, s.Size.Height, 1)
	require.True(t, b.ClickAt(3, s.Size.Height-1), "last command row toggles output")
	expanded := b.Draw(ctx)
	require.Contains(t, components.SurfaceText(expanded), "output")
	require.False(t, b.ClickAt(3, s.Size.Height), "output row stays selectable")
}

func TestBashCommandsRetainAllTextOnResize(t *testing.T) {
	command := "printf '%s\\n' '中文 👩‍💻 é'; " + strings.Repeat("/long/path-without-breaks", 20) + " END"
	for _, width := range []int{2, 8, 12, 13, 20, 80, 120} {
		for _, nested := range []bool{false, true} {
			var widget components.Widget
			prefix := "$ "
			if nested {
				widget = &AgentBlock{
					Name: "agent_spawn", Expanded: true,
					Children: []ChildTool{{Name: "bash", Detail: command, Status: status.ToolDone}},
				}
				prefix = "╰── ✓ bash "
			} else {
				widget = &BashBlock{Command: command, Status: status.ToolDone}
			}
			s := widget.Draw(components.DrawContext{Max: components.Size{Width: width}, Method: xui.WidthUnicode})
			var painted strings.Builder
			for _, cell := range s.Buffer {
				if !cell.Default && !cell.Trail {
					painted.WriteString(cell.Char)
				}
			}
			// Ignore only layout whitespace; every command grapheme, quote and
			// operator must survive wrapping, including a long unbroken path.
			nonspace := func(s string) string { return strings.ReplaceAll(s, " ", "") }
			require.True(t, strings.HasSuffix(nonspace(painted.String()), nonspace(prefix+command)),
				"width=%d nested=%t: %s", width, nested, painted.String())
		}
	}
}
