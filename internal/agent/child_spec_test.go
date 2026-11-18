package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestSpecForRoleExplore(t *testing.T) {
	spec := agent.SpecForRole(job.RoleExplore)
	assert.Equal(t, job.RoleExplore, spec.Role)
	assert.Contains(t, spec.Hint, "explore sub-agent")
	assert.Contains(t, spec.Hint, "cannot modify files")
	names := toolNameSet(spec.Tools)
	assert.True(t, names["read"])
	assert.True(t, names["bash"])
	assert.False(t, names["write"])
	assert.False(t, names["edit"])
	assert.False(t, names["agent_spawn"])
}

func TestSpecForRoleWorker(t *testing.T) {
	spec := agent.SpecForRole(job.RoleWorker)
	assert.Equal(t, job.RoleWorker, spec.Role)
	assert.Contains(t, spec.Hint, "worker sub-agent")
	assert.Contains(t, spec.Hint, "cannot spawn further agents")
	names := toolNameSet(spec.Tools)
	assert.True(t, names["write"])
	assert.True(t, names["edit"])
	assert.True(t, names["read"])
	assert.False(t, names["agent_spawn"])
}

func TestSpecForRoleReview(t *testing.T) {
	spec := agent.SpecForRole(job.RoleReview)
	assert.Equal(t, job.RoleReview, spec.Role)
	assert.Contains(t, spec.Hint, "review sub-agent")
	assert.NotContains(t, spec.Hint, "allowlisted")
	names := toolNameSet(spec.Tools)
	assert.True(t, names["bash"])
	assert.True(t, names["grep"])
	assert.False(t, names["write"])
}

func TestSpecForRoleEmptyDefaultsExplore(t *testing.T) {
	spec := agent.SpecForRole("")
	assert.Equal(t, job.RoleExplore, spec.Role)
}

func TestParseRole(t *testing.T) {
	r, err := job.ParseRole("")
	require.NoError(t, err)
	assert.Equal(t, job.RoleExplore, r)

	_, err = job.ParseRole("nope")
	require.Error(t, err)
}

func toolNameSet(list []tooldef.Tool) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, tool := range list {
		m[tool.Definition.Name] = true
	}
	return m
}
