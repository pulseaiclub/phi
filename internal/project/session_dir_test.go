package project

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectDirName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cwd  string
		want string
	}{
		{"/Users/foo/bar", "--Users-foo-bar--"},
		{"relative/proj", "--relative-proj--"},
		{".", "--unknown--"},
		{`C:\Users\me\proj`, "--C--Users-me-proj--"},
		{`\\server\share\proj`, "---server-share-proj--"},
		// Extended-length paths come back from Getwd when the shell was
		// started in a \\?\-prefixed directory; the '?' must not leak into
		// the name or mkdir fails on Windows.
		{`\\?\C:\Users\me\proj`, "--C--Users-me-proj--"},
		{`\\?\UNC\server\share\proj`, "---server-share-proj--"},
	}
	for _, tt := range tests {
		t.Run(tt.cwd, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ProjectDirName(tt.cwd))
		})
	}
}

func TestProjectSessionDirWin32Namespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		plain    string
		extended string
		want     string
	}{
		{"drive", `C:\Users\me\proj`, `\\?\C:\Users\me\proj`, "--C--Users-me-proj--"},
		{"unc", `\\Server\Share\proj`, `\\?\UNC\Server\Share\proj`, "---Server-Share-proj--"},
		{"unc lowercase", `\\Server\Share\proj`, `\\?\unc\Server\Share\proj`, "---Server-Share-proj--"},
		{"unc mixed case", `\\Server\Share\proj`, `\\?\UnC\Server\Share\proj`, "---Server-Share-proj--"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := filepath.Join("sessions", "test")
			// Both spellings must locate the historical session directory.
			want := filepath.Join(base, tt.want)
			assert.Equal(t, want, ProjectSessionDir(base, tt.plain))
			assert.Equal(t, want, ProjectSessionDir(base, tt.extended))
		})
	}
}

func TestProjectSessionDir(t *testing.T) {
	t.Parallel()
	base := "/tmp/phi-sessions"
	cwd := "/home/dev/app"
	got := ProjectSessionDir(base, cwd)
	assert.Equal(t, filepath.Join(base, "--home-dev-app--"), got)
}

func TestProjectSessionDirMethod(t *testing.T) {
	p := &Project{
		root:   "/Users/foo/Phi",
		global: GlobalLayout{root: "/tmp/.phi"},
	}
	assert.Equal(t, filepath.Join("/tmp/.phi/session", "--Users-foo-Phi--"), p.SessionDir())
}
