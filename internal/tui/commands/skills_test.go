package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/listrows"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// recordingSkillComposer is the skillComposer stub: it records attached skills
// and what ShowList would put on screen.
type recordingSkillComposer struct {
	names  []string
	items  []listpicker.Item
	cfg    listpicker.ShowConfig
	accept func(listpicker.Item)
}

func (r *recordingSkillComposer) AddPendingSkill(name string) {
	r.names = append(r.names, name)
}

func (r *recordingSkillComposer) ShowList(
	items []listpicker.Item,
	cfg listpicker.ShowConfig,
	onAccept func(listpicker.Item),
	_ func(listpicker.Item),
) {
	r.items = items
	r.cfg = cfg
	r.accept = onAccept
}

// writeSkillFixture creates one valid skill under root.
func writeSkillFixture(t *testing.T, root, name, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\nDo the work.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
}

// drainToastMsg returns the last ToastMsg queued on bus and empties it.
func drainToastMsg(t *testing.T, bus *controller.Bus) controller.ToastMsg {
	t.Helper()
	var got controller.ToastMsg
	for _, m := range bus.Drain() {
		if tm, ok := m.(controller.ToastMsg); ok {
			got = tm
		}
	}
	return got
}

func TestSkillsCommand_NoSkillsWarnsAndSkipsPicker(t *testing.T) {
	cases := []struct {
		name string
		root func(*testing.T) string
	}{
		{"empty directory", func(t *testing.T) string { return t.TempDir() }},
		{"missing directory", func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := controller.NewBus(nil)
			comp := &recordingSkillComposer{}
			s := &SkillsCommands{
				Bus:        bus,
				SkillPaths: []string{tc.root(t)},
				composer:   comp,
			}

			s.Show()

			got := drainToastMsg(t, bus)
			assert.Equal(t, "No skills found", got.Message)
			assert.Equal(t, toast.ToastWarning, got.Kind)
			assert.Equal(t, 3*time.Second, got.Duration)
			assert.Nil(t, comp.items, "picker must stay closed when no skills load")
		})
	}
}

func TestSkillsCommand_LoadsSkillsIntoPicker(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "extract-and-distill", "Distill ideas from source material")

	bus := controller.NewBus(nil)
	comp := &recordingSkillComposer{}
	s := &SkillsCommands{
		Bus:        bus,
		SkillPaths: []string{root},
		composer:   comp,
	}
	r := NewCommandRegistry()
	s.Register(r)

	assert.Equal(t, "/skill", r.LookupInsert("skill"))
	require.True(t, r.DispatchSlash("/skill", NewContext(bus, nil)))

	require.Len(t, comp.items, 1)
	assert.Equal(t, listpicker.Item{
		ID:       "extract-and-distill",
		Primary:  "extract-and-distill",
		Detail:   "Distill ideas from source material",
		Keywords: "extract-and-distill Distill ideas from source material",
	}, comp.items[0])
	assert.Equal(t, listrows.SkillConfig(), comp.cfg)
	require.NotNil(t, comp.accept)
	comp.accept(comp.items[0])
	assert.Equal(t, []string{"extract-and-distill"}, comp.names)
	assert.Equal(t, "Skill attached: extract-and-distill", drainToastMsg(t, bus).Message)
}

func TestSkillsCommand_AcceptForwardsSkillAndToasts(t *testing.T) {
	bus := controller.NewBus(nil)
	comp := &recordingSkillComposer{}
	s := &SkillsCommands{Bus: bus, composer: comp}

	s.accept(listpicker.Item{ID: "review-pr"})

	assert.Equal(t, []string{"review-pr"}, comp.names)
	got := drainToastMsg(t, bus)
	assert.Equal(t, "Skill attached: review-pr", got.Message)
	assert.Equal(t, toast.ToastSuccess, got.Kind)
	assert.Equal(t, 2*time.Second, got.Duration)
}

func TestSkillsCommand_AcceptWithNilComposerToasts(t *testing.T) {
	bus := controller.NewBus(nil)
	s := &SkillsCommands{Bus: bus}

	require.NotPanics(t, func() {
		s.accept(listpicker.Item{ID: "review-pr"})
	})

	assert.Equal(t, "Skill attached: review-pr", drainToastMsg(t, bus).Message)
}
