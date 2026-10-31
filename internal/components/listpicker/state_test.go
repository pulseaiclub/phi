package listpicker

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestRestoreStateAfterRowsChange(t *testing.T) {
	p := &Picker{}
	p.Show([]Item{{ID: "a", Primary: "file-a"}, {ID: "b", Primary: "file-b"}}, ShowConfig{})
	typeQuery(p, &components.EventContext{}, "file")
	p.Selected = 1
	state := p.State()
	p.Hide()
	p.Show([]Item{{ID: "b", Primary: "file-b"}, {ID: "c", Primary: "file-c"}}, ShowConfig{})
	p.Restore(state)
	assert.Equal(t, state, p.State())
	assert.Equal(t, 0, p.Selected)
	assert.Equal(t, len(state.Query), p.Cursor)

	p.Show([]Item{{ID: "c", Primary: "file-c"}}, ShowConfig{})
	p.Restore(state)
	assert.Equal(t, "c", p.State().SelectedID)
	p.Restore(State{Query: "absent", SelectedID: "b"})
	assert.Empty(t, p.State().SelectedID)
}
