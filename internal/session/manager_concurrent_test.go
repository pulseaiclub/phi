package session

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

// TestManagerConcurrentAccess guards the mutex: concurrent append/read
// must not race or corrupt the tree. Run with -race.
func TestManagerConcurrentAccess(t *testing.T) {
	m, err := NewSessionManager(t.TempDir(), WithShouldFlush(false))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range 50 {
				msg := llm.Message{
					Role:    llm.RoleUser,
					Content: fmt.Sprintf("w%d-%d", w, i),
				}
				id, err := m.Append(msg)
				assert.NoError(t, err)
				if err != nil {
					return
				}
				_ = m.BuildContext()
				_ = m.GetBranch(id)
				_ = m.Len()
			}
		}(w)
	}
	wg.Wait()

	require.GreaterOrEqual(t, m.Len(), 8*50)
}

// TestRenameSessionKeepsConcurrentAppends covers what the manager mutex cannot:
// `phi sessions rename` stages a copy of the file and replaces it, so an append
// that lands between the copy and the rename must wait for the session lock
// instead of being dropped. Two managers share the file and never share a mutex,
// which is exactly the shape of two phi processes.
func TestRenameSessionKeepsConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	writer, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = writer.Append(llm.Message{Role: llm.RoleAssistant, Content: "start"})
	require.NoError(t, err)

	const appends = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range appends {
			_, err := writer.Append(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("m%d", i)})
			assert.NoError(t, err)
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 100 {
			assert.NoError(t, RenameSession(dir, writer.ID(), fmt.Sprintf("name %d", i)))
		}
	}()
	wg.Wait()

	loaded, err := OpenSession(writer.File())
	require.NoError(t, err)
	// header + the opening assistant message + every accepted append.
	assert.Equal(t, 2+appends, loaded.Len(), "a rename must not drop a concurrent append")
	assert.NotEmpty(t, loaded.Title())
}

// TestSetTitleDuringAppend covers the rename path rewriting the same file
// appends write to: the mutex must keep the header rewrite from clobbering a
// message line. Run with -race.
func TestSetTitleDuringAppend(t *testing.T) {
	dir := t.TempDir()
	m, err := NewSessionManager(dir, WithSessionDir(dir), WithShouldFlush(true))
	require.NoError(t, err)
	_, err = m.Append(llm.Message{Role: llm.RoleAssistant, Content: "start"})
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 40 {
			_, err := m.Append(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("m%d", i)})
			assert.NoError(t, err)
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 40 {
			assert.NoError(t, m.SetTitle(fmt.Sprintf("name %d", i)))
		}
	}()
	wg.Wait()

	loaded, err := OpenSession(m.File())
	require.NoError(t, err)
	assert.Equal(t, m.Title(), loaded.Title())
	// 1 header + 1 assistant + 40 user messages; a lost rewrite would drop lines.
	assert.Equal(t, 42, loaded.Len())
}
