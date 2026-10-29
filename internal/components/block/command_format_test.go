package block

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
)

func TestFormatCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		width   int
		want    string
	}{
		{"short", "a && b", 20, "a && b"},
		{"exact fit", "a && b", 6, "a && b"},
		{
			"chain", "go fmt ./... && go test ./... && go build ./cmd", 30,
			"go fmt ./... &&\ngo test ./... &&\ngo build ./cmd",
		},
		{"no spaces", "first&&second||third|fourth", 12, "first&&\nsecond||\nthird|\nfourth"},
		{"stderr pipe", "first |& second", 10, "first |&\nsecond"},
		{
			"quoted operators", `echo 'a && b' "c || d" 'e | f' && done`, 12,
			"echo 'a && b' \"c || d\" 'e | f' &&\ndone",
		},
		{"escaped operators", `echo a\&\&b c\|d && done`, 12, "echo a\\&\\&b c\\|d &&\ndone"},
		{"escaped quote", `echo "a\" && b" && done`, 12, "echo \"a\\\" && b\" &&\ndone"},
		{"redirection", "first 2>&1 >|out && second", 12, "first 2>&1 >|out &&\nsecond"},
		{"comment", "first && second # keep && || | here", 12, "first &&\nsecond # keep && || | here"},
		{"embedded hash", "echo foo#bar && second", 12, "echo foo#bar &&\nsecond"},
		{"tabs", "first &&\t\tsecond", 12, "first &&\nsecond"},
		{"unicode width", "中文 && 回家", 10, "中文 &&\n回家"},
		{"long argument", "echo aaaaaaaaaaaaaaaaaaaa", 10, "echo aaaaaaaaaaaaaaaaaaaa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, formatCommand(tc.command, tc.width, xui.WidthUnicode))
		})
	}
}

func TestFormatCommandPreservesComplexShell(t *testing.T) {
	for _, command := range []string{
		"first &&\n  second && third",
		"cat <<EOF && echo done\na && b\nEOF",
		"cat <<<'a && b' && echo done",
		`echo "$(echo "a && b")" && second`,
		"echo `echo a && b` && second",
		"echo $((a && b)) && second",
		"[[ a && b ]] && second",
		"array[a&&b]=x && second",
		"(first && second) && third",
		"{ first && second; } && third",
		`echo $'a\' && b' && second`,
		`echo "unfinished && second`,
	} {
		require.Equal(t, command, formatCommand(command, 8, xui.WidthUnicode))
	}
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
			if width == 40 {
				want = []string{
					tc.prefix + "go fmt ./... &&",
					tc.indent + "go test ./... &&",
					tc.indent + "go build ./cmd",
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
