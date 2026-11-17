package text

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// HighlightShell tokenises a shell command with chroma's bash lexer and maps
// token types onto theme colors. Tokens that chromaStyle would leave at plain
// foreground keep base verbatim, so dim / strikethrough states survive; colored
// tokens adopt the token color but keep base's emphasis flags. Returns nil when
// tokenising fails — callers fall back to a single base-styled span.
func HighlightShell(cmd string, base xui.Style, th components.Theme) []components.Span {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	lexer := chroma.Coalesce(lexers.Get("bash"))
	it, err := lexer.Tokenise(nil, cmd)
	if err != nil {
		return nil
	}
	var out []components.Span
	for _, tok := range it.Tokens() {
		st := chromaStyle(tok.Type, th)
		if st == th.Foreground {
			st = base
		} else {
			st.Bold = base.Bold
			st.Italic = base.Italic
			st.Underline = base.Underline
			st.Strikethrough = base.Strikethrough
			st.Dim = base.Dim
		}
		out = append(out, components.Span{Text: tok.Value, Style: st})
	}
	return out
}
