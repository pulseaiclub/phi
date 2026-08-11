package toolmanager

import (
	"reflect"
	"testing"
)

func TestAssetNameCandidates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tool     string
		version  string
		platform string
		arch     string
		want     []string
	}{
		{
			name:     "ripgrep linux prefers musl",
			tool:     "rg",
			version:  "15.2.0",
			platform: PlatformLinux,
			arch:     ArchAMD64,
			want: []string{
				"ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz",
				"ripgrep-15.2.0-x86_64-unknown-linux-gnu.tar.gz",
			},
		},
		{
			name:     "fd linux prefers gnu",
			tool:     "fd",
			version:  "10.4.2",
			platform: PlatformLinux,
			arch:     ArchAMD64,
			want: []string{
				"fd-v10.4.2-x86_64-unknown-linux-gnu.tar.gz",
				"fd-v10.4.2-x86_64-unknown-linux-musl.tar.gz",
			},
		},
		{
			name:     "ripgrep darwin arm64",
			tool:     "rg",
			version:  "15.2.0",
			platform: PlatformDarwin,
			arch:     ArchARM64,
			want: []string{
				"ripgrep-15.2.0-aarch64-apple-darwin.tar.gz",
			},
		},
		{
			name:     "unsupported architecture",
			tool:     "rg",
			version:  "15.2.0",
			platform: PlatformLinux,
			arch:     ArchX86,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Tools[tt.tool].AssetNames.getAssetNames(tt.version, tt.platform, tt.arch)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("getAssetNames() = %q, want %q", got, tt.want)
			}
		})
	}
}
