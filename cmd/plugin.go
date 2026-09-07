package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/project"
)

var (
	pluginCommand = cli.Command{
		Name: "plugin",
		Desc: "manage extensions (install, list, update, remove)",
		Long: `Install a PXB extension from a GitHub repo into ~/.phi/extensions/<repo>/.

Prefers a GitHub Release archive for this OS/arch (same layout as phi update):

  {repo}_{version}_{goos}_{goarch}.tar.gz   # .zip on Windows

The archive must contain phi.yaml and the compiled exec binary. If no matching
release asset exists, falls back to a shallow git clone (repo must already ship
the binary).

Install records the GitHub source in .phi-install.json, so plugins can be
updated in place without manual removal:

Examples:
  phi plugin install alice/greet
  phi plugin install alice/greet@v1.2.3
  phi plugin install github.com/alice/greet@main
  phi plugin list
  phi plugin update              # update all installed plugins
  phi plugin update greet        # update one plugin
  phi plugin update greet@latest # switch to the newest release
  phi plugin remove greet        # uninstall (alias: rm)

Security: extension processes run with your full permissions.`,
	}

	pluginInstallCommand = cli.Command{
		Name:    "install",
		ArgsUse: "<github-repo[@tag]>",
		Desc:    "install an extension from a GitHub release (git clone fallback)",
	}

	pluginListCommand = cli.Command{
		Name: "list",
		Desc: "list installed plugins",
	}

	pluginUpdateCommand = cli.Command{
		Name:    "update",
		ArgsUse: "[repo[@ref]]",
		Desc:    "update plugins to their newest version",
		Flags: []cli.Flag{
			cli.Bool("check", "", "report available updates without installing"),
		},
	}

	pluginRemoveCommand = cli.Command{
		Name:    "remove",
		Aliases: []string{"rm"},
		ArgsUse: "<repo>",
		Desc:    "remove an installed plugin",
	}
)

func init() {
	pluginInstallCommand.Run = func(args []string, _ cli.Flags) error {
		return pluginInstall(args)
	}
	pluginListCommand.Run = func(_ []string, _ cli.Flags) error { return pluginList() }
	pluginUpdateCommand.Run = func(args []string, f cli.Flags) error {
		return pluginUpdate(args, f.Bool("check"))
	}
	pluginRemoveCommand.Run = func(args []string, _ cli.Flags) error { return pluginRemove(args) }
	pluginCommand.Add(
		&pluginInstallCommand,
		&pluginListCommand,
		&pluginUpdateCommand,
		&pluginRemoveCommand,
	)
}

// splitPluginTarget splits "repo[@ref]" or "owner/repo[@ref]" into the
// on-disk repo name and an optional ref override ("latest" = newest release).
func splitPluginTarget(raw string) (id, ref string, err error) {
	raw = strings.TrimSpace(raw)
	pathPart, ref := raw, ""
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		before, after := raw[:i], raw[i+1:]
		if after != "" && !strings.Contains(after, "/") {
			pathPart, ref = before, after
		}
	}
	if strings.Contains(pathPart, "/") {
		spec, perr := extension.ParseSpec(raw)
		if perr != nil {
			return "", "", perr
		}
		return spec.Repo, ref, nil
	}
	return pathPart, ref, nil
}

func pluginInstall(args []string) error {
	if len(args) != 1 {
		return pluginInstallCommand.Usagef("expected <github-repo[@tag]>")
	}
	spec, err := extension.ParseSpec(args[0])
	if err != nil {
		return err
	}
	proj := project.GetDefaultProject()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return extension.Install(ctx, extension.InstallOptions{
		Dir:    proj.Global().ExtensionsDir(),
		Spec:   spec,
		Stdout: os.Stdout,
	})
}

func pluginList() error {
	proj := project.GetDefaultProject()
	installed, err := extension.ListInstalled(proj.Global().ExtensionsDir())
	if err != nil {
		return err
	}
	if len(installed) == 0 {
		fmt.Println("(no plugins — try: phi plugin install alice/greet)")
		return nil
	}
	for _, in := range installed {
		version := in.Manifest.Version
		source := "manual"
		if in.Managed {
			source = in.Meta.Source
			if in.Meta.ReleaseTag != "" {
				version = in.Meta.ReleaseTag
			}
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", in.ID, version, source, in.Path)
	}
	return nil
}

func pluginUpdate(args []string, check bool) error {
	if len(args) > 1 {
		return pluginUpdateCommand.Usagef("expected at most one <repo[@ref]>")
	}
	proj := project.GetDefaultProject()
	opts := extension.UpdateOptions{
		Dir:    proj.Global().ExtensionsDir(),
		Check:  check,
		Stdout: os.Stdout,
	}
	if len(args) == 1 {
		id, ref, err := splitPluginTarget(args[0])
		if err != nil {
			return err
		}
		opts.ID, opts.Ref = id, ref
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return extension.Update(ctx, opts)
}

func pluginRemove(args []string) error {
	if len(args) != 1 {
		return pluginRemoveCommand.Usagef("expected <repo>")
	}
	id, _, err := splitPluginTarget(args[0])
	if err != nil {
		return err
	}
	proj := project.GetDefaultProject()
	if err := extension.Remove(proj.Global().ExtensionsDir(), id); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", id)
	return nil
}
