package bashcomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommandWord(t *testing.T) {
	for _, tt := range []struct {
		name  string
		line  string
		index int
		ok    bool
	}{
		{name: "the first word names the command", line: "git status", index: 0, ok: true},
		{name: "an empty line names nothing", line: "", ok: false},
		{name: "a launcher passes the command through", line: "sudo git status", index: 1, ok: true},
		{name: "a launcher flag belongs to the launcher", line: "sudo -u root git status", index: 2, ok: true},
		{name: "env passes it through too", line: "env git status", index: 1, ok: true},
		{name: "xargs runs the command after it", line: "xargs git status", index: 1, ok: true},
		{name: "an assignment configures the command that follows", line: "GOOS=linux go build", index: 1, ok: true},
		{name: "several assignments stack", line: "A=1 B=2 go build", index: 2, ok: true},
		{name: "a lone launcher has no command yet", line: "sudo ", index: 1, ok: false},
		{name: "an assignment alone has no command yet", line: "GOOS=linux ", index: 1, ok: false},
		{name: "a quoted launcher word is not a launcher", line: "'sudo' git", index: 0, ok: true},
		{name: "an equals inside a value is not an assignment name", line: "a-b=1 go", index: 0, ok: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			index, ok := CommandWord(Tokenize(tt.line))
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.index, index)
		})
	}
}
