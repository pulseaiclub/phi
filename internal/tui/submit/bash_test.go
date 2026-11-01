package submit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBashLiveOutputPublishesTrailingUpdate(t *testing.T) {
	updates := make(chan string, 2)
	live := newBashLiveOutput(50*time.Millisecond, func(output string) {
		updates <- output
	})
	t.Cleanup(live.Close)

	live.Append("first")
	require.Equal(t, "first", <-updates)
	live.Append("-second")

	select {
	case got := <-updates:
		require.Equal(t, "first-second", got)
	case <-time.After(2 * time.Second):
		require.Fail(t, "timed out waiting for trailing update")
	}
}

func TestParseBashInput(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		command   string
		inContext bool
		ok        bool
	}{
		{"local shell", "!ls -la", "ls -la", false, true},
		{"context shell", "!!git status", "git status", true, true},
		{"trims surrounding space", "!  ls  ", "ls", false, true},
		{"bare bang", "!", "", false, false},
		{"bare context bang", "!!", "", true, false},
		{"plain text", "ls", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, inContext, ok := parseBashInput(tt.text)

			assert.Equal(t, tt.command, command)
			assert.Equal(t, tt.inContext, inContext)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func TestBashLiveOutputCloseCancelsTrailingUpdate(t *testing.T) {
	updates := make(chan string, 2)
	live := newBashLiveOutput(time.Hour, func(output string) {
		updates <- output
	})

	live.Append("first")
	<-updates
	live.Append("-second")
	live.Close()

	live.mu.Lock()
	defer live.mu.Unlock()
	require.True(t, live.stopped, "expected stopped=true")
	assert.Nil(t, live.timer, "expected timer=nil")
}
