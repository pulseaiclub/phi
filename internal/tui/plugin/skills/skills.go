package skills

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/listrows"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/llm/skills"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// SkillsCommands owns the /skill slash command, which opens a listpicker.
// It is intentionally a slash command, not a Ctrl+K palette entry: picking a
// skill is part of composing a turn, not a global action.
type SkillsCommands struct {
	Bus        *controller.Bus
	SkillPaths []string
	// Add attaches a chosen skill to the composer.
	Add skillAdder
	// ShowList opens the composer's shared list picker (composer.ShowList).
	ShowList func(items []listpicker.Item, cfg listpicker.ShowConfig, onAccept, onDelete func(listpicker.Item))
	// OpenPicker opens the skills list overlay; New binds it to Show.
	OpenPicker func(items []listpicker.Item, cfg listpicker.ShowConfig, onAccept func(listpicker.Item))
}

// New builds the skills command domain and registers /skill. The picker
// closes without a delete path: skills have nothing to delete from a picker.
func New(r *commands.CommandRegistry, deps SkillsCommands) *SkillsCommands {
	s := &deps
	s.OpenPicker = s.openList
	s.Register(r)
	return s
}

func (s *SkillsCommands) openList(items []listpicker.Item, cfg listpicker.ShowConfig, onAccept func(listpicker.Item)) {
	if s.ShowList != nil {
		s.ShowList(items, cfg, onAccept, nil)
	}
}

// skillAdder adds a pending skill to the composer.
type skillAdder interface {
	AddPendingSkill(name string)
}

// Register wires /skill into r as a slash command.
func (s *SkillsCommands) Register(r *commands.CommandRegistry) {
	if r == nil {
		return
	}
	r.Register(commands.Command{
		Name:        "skill",
		Description: "Attach a skill to the next turn — /skill",
		Slash:       true,
		Insert:      "/skill",
		Run: func(_ commands.Context, _ []string) {
			s.Show()
		},
	})
}

// Show loads skills from disk and opens the picker. Loading is one walk of the
// skill roots, so it runs inline like /sessions does.
func (s *SkillsCommands) Show() {
	list := skills.LoadAll(s.SkillPaths)
	if len(list) == 0 {
		commands.PublishToast(s.Bus, "No skills found", toast.ToastWarning, 3*time.Second)
		return
	}
	if s.OpenPicker == nil {
		return
	}
	s.OpenPicker(listrows.SkillItems(list), listrows.SkillConfig(), s.accept)
}

// accept is the picker's onAccept: attach the chosen skill and toast it.
func (s *SkillsCommands) accept(item listpicker.Item) {
	if s.Add != nil {
		s.Add.AddPendingSkill(item.ID)
	}
	commands.PublishToast(s.Bus, "Skill attached: "+item.ID, toast.ToastSuccess, 2*time.Second)
}
