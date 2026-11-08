package listrows

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm/skills"
)

func TestSkillItemsMapsNameAndDescription(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "extract-and-distill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: extract-and-distill
description: Distill ideas from source material
---
Do the work.
`), 0o644))

	list := skills.LoadAll([]string{dir})
	require.Len(t, list, 1)

	items := SkillItems(list)
	require.Len(t, items, 1)
	assert.Equal(t, "extract-and-distill", items[0].ID)
	assert.Equal(t, "extract-and-distill", items[0].Primary)
	assert.Equal(t, "Distill ideas from source material", items[0].Detail)
	assert.Contains(t, items[0].Keywords, "extract-and-distill")
	assert.Empty(t, items[0].Badge, "no current-row concept for skills")
}

// A skill with no frontmatter name falls back to its directory basename; the
// row carries that name as both ID and Primary so acceptance attaches it.
func TestSkillItemsFallsBackToDirBasename(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "unnamed-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
description: no name field
---
body
`), 0o644))

	items := SkillItems(skills.LoadAll([]string{dir}))
	require.Len(t, items, 1)
	assert.Equal(t, "unnamed-skill", items[0].ID)
	assert.Equal(t, "unnamed-skill", items[0].Primary)
}

func TestSkillConfig(t *testing.T) {
	cfg := SkillConfig()
	assert.Equal(t, "Skills", cfg.Title)
	assert.Contains(t, cfg.Hint, "attach")
	assert.NotEmpty(t, cfg.Empty)
	assert.Empty(t, cfg.ConfirmMessage, "no delete path means no confirm copy")
	assert.GreaterOrEqual(t, cfg.PrimaryWidth, len("incremental-implementation"),
		"the name column fits the longest builtin skill name")
}
