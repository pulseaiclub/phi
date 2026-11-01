package session

import (
	"fmt"
	"strings"
)

// BashContextText formats a user-run shell command as the user message the
// model sees. Live recording and replay share it so a resumed session reads the
// same way as the turn that produced it.
func BashContextText(command, output string, exitCode int, cancelled bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ran `%s`\n", command)
	if output == "" {
		b.WriteString("(no output)")
	} else {
		b.WriteString("```\n")
		b.WriteString(strings.TrimRight(output, "\n"))
		b.WriteString("\n```")
	}
	if cancelled {
		b.WriteString("\n\n(command cancelled)")
	} else if exitCode != 0 {
		fmt.Fprintf(&b, "\n\nCommand exited with code %d", exitCode)
	}
	return b.String()
}
