package listrows

import (
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/llm/skills"
)

// skillColumn fits the longest builtin skill name; longer ones elide, and a
// few typed characters filter them back into view.
const skillColumn = 28

// SkillConfig returns chrome copy for the skills picker.
func SkillConfig() listpicker.ShowConfig {
	return listpicker.ShowConfig{
		Title:       "Skills",
		FilterHint:  "filter skills…",
		Empty:       "No skills found",
		EmptyFilter: "No skills match %q",
		Hint:        chrome.ListHint("attach"),
		// No delete path: the picker keeps Ctrl+X inert instead of arming a
		// confirm nobody answers.
		PrimaryWidth: skillColumn,
	}
}

// SkillItems maps loaded skills into generic list rows. The display name is the
// row's identity: accepting it attaches that skill to the composer.
func SkillItems(list []*skills.Skill) []listpicker.Item {
	out := make([]listpicker.Item, 0, len(list))
	for _, sk := range list {
		name := skills.DisplayName(sk)
		out = append(out, listpicker.Item{
			ID:       name,
			Primary:  name,
			Detail:   sk.Description,
			Keywords: name + " " + sk.Description,
		})
	}
	return out
}
