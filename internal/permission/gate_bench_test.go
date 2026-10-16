package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkGatePaths(b *testing.B) {
	for _, sensitive := range []struct {
		name   string
		exists bool
	}{
		{name: "sensitive_existing", exists: true},
		{name: "sensitive_missing", exists: false},
	} {
		b.Run(sensitive.name, func(b *testing.B) {
			// Resolve the fixture root so OS temp-directory aliases do not add path components.
			root, err := filepath.EvalSymlinks(b.TempDir())
			require.NoError(b, err)
			requestDir := filepath.Join(root, "requests")
			require.NoError(b, os.Mkdir(requestDir, 0o700))

			policy := DefaultPolicy()
			// Synthetic siblings keep every prefix outside the requests and independent of HOME.
			policy.SensitivePathDeny = make([]string, 5)
			for i := range policy.SensitivePathDeny {
				prefix := filepath.Join(root, fmt.Sprintf("sensitive-%d", i))
				policy.SensitivePathDeny[i] = prefix
				if sensitive.exists {
					require.NoError(b, os.Mkdir(prefix, 0o700))
				}
			}
			gate, err := NewGate(policy, root)
			require.NoError(b, err)

			editPaths := make([]string, 10)
			for i := range editPaths {
				editPaths[i] = filepath.Join(requestDir, fmt.Sprintf("file-%d.txt", i))
				require.NoError(b, os.WriteFile(editPaths[i], []byte("fixture\n"), 0o600))
			}
			deepDir := requestDir
			for i := range 12 {
				deepDir = filepath.Join(deepDir, fmt.Sprintf("missing-%d", i))
			}

			for _, tc := range []struct {
				name string
				req  Request
			}{
				{
					name: "existing_file",
					req:  Request{Action: ActionWrite, Tool: "write", Paths: editPaths[:1]},
				},
				{
					name: "new_leaf",
					req: Request{Action: ActionWrite, Tool: "write", Paths: []string{
						filepath.Join(requestDir, "new.txt"),
					}},
				},
				{
					name: "missing_dirs12",
					req: Request{Action: ActionWrite, Tool: "write", Paths: []string{
						filepath.Join(deepDir, "new.txt"),
					}},
				},
				{
					name: "edit10",
					req:  Request{Action: ActionEdit, Tool: "edit", Paths: editPaths},
				},
			} {
				b.Run(tc.name, func(b *testing.B) {
					ctx := b.Context()
					decision, reason := gate.Check(ctx, tc.req)
					require.Equal(b, Allow, decision, reason)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						gate.Check(ctx, tc.req)
					}
				})
			}
		})
	}
}
