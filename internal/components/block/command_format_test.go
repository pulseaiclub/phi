package block

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
)

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
