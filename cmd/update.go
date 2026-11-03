package main

import (
	"context"
	"os"
	"time"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/util/update"
	"github.com/pulseaiclub/phi/internal/version"
)

var updateCommand = cli.Command{
	Name: "update",
	Desc: "install the latest release",
	Long: `Environment:
  PHI_SKIP_VERSION_CHECK  skip startup version checks in the TUI
  GITHUB_TOKEN            optional; raises GitHub API rate limits`,
	Flags: []cli.Flag{
		cli.Bool("check", "", "query the latest release without installing"),
	},
	Run: func(_ []string, f cli.Flags) error {
		if f.Bool("check") {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return update.CheckOnly(ctx, version.Version)
		}
		ctx, cancel := context.WithTimeout(context.Background(), update.DefaultInstallTimeout)
		defer cancel()
		return update.Install(ctx, update.InstallOptions{
			Current: version.Version,
			Stdout:  os.Stdout,
		})
	},
}
