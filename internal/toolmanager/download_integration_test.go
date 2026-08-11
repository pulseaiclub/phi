package toolmanager

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

func TestDownloadToolFromGitHub(t *testing.T) {
	if os.Getenv("PHI_RUN_NETWORK_TESTS") != "1" {
		t.Skip("set PHI_RUN_NETWORK_TESTS=1 to test a real GitHub release download")
	}

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", homeDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path, err := DownloadTool(ctx, "rg")
	if err != nil {
		t.Fatal(err)
	}

	commandCtx, commandCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer commandCancel()
	out, err := exec.CommandContext(commandCtx, path, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("run downloaded ripgrep: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "ripgrep") {
		t.Fatalf("unexpected ripgrep version output: %s", out)
	}
}

func TestSelectCompatibleFdReleaseFromGitHub(t *testing.T) {
	if os.Getenv("PHI_RUN_NETWORK_TESTS") != "1" {
		t.Skip("set PHI_RUN_NETWORK_TESTS=1 to query real GitHub releases")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	releases, err := githubrelease.FetchRecent(ctx, Tools["fd"].Repo, compatibleReleaseLookback)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := selectCompatibleAsset(
		Tools["fd"],
		releases,
		PlatformDarwin,
		ArchAMD64,
		"darwin",
		"amd64",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asset.Name, "x86_64-apple-darwin") {
		t.Fatalf("unexpected Intel macOS fd asset: %s", asset.Name)
	}
}
