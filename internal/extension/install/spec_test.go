package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		owner   string
		repo    string
		ref     string
		wantErr bool
	}{
		{in: "alice/greet", owner: "alice", repo: "greet"},
		{in: "alice/greet@v1.2.3", owner: "alice", repo: "greet", ref: "v1.2.3"},
		{in: "github.com/alice/greet", owner: "alice", repo: "greet"},
		{in: "github.com/alice/greet@main", owner: "alice", repo: "greet", ref: "main"},
		{in: "https://github.com/alice/greet", owner: "alice", repo: "greet"},
		{in: "https://github.com/alice/greet.git", owner: "alice", repo: "greet"},
		{in: "https://github.com/alice/greet@v1", owner: "alice", repo: "greet", ref: "v1"},
		{in: "https://github.com/alice/greet.git@v1", owner: "alice", repo: "greet", ref: "v1"},
		{in: "", wantErr: true},
		{in: "just-repo", wantErr: true},
		{in: "git@github.com:alice/greet.git", wantErr: true},
		{in: "https://gitlab.com/alice/greet", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSpec(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.owner, got.Owner)
			assert.Equal(t, tc.repo, got.Repo)
			assert.Equal(t, tc.ref, got.Ref)
			assert.Equal(t, "https://github.com/"+tc.owner+"/"+tc.repo+".git", got.CloneURL())
			assert.Equal(t, tc.repo, got.ID())
		})
	}
}
