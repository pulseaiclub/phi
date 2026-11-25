package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pulseaiclub/phi/internal/plugin"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/version"
)

func startPlugins(proj *project.Project) func() {
	roots := []string{filepath.Join(proj.Global().Root(), "plugins")}
	switch os.Getenv("PHI_PLUGINS") {
	case "off":
		return func() {}
	case "project":
		// Running in a checkout is not consent to execute its Lua code.
		roots = append(roots, filepath.Join(proj.Root(), ".phi", "plugins"))
	}
	host, warnings := plugin.Open(version.Version, os.Stderr, roots...)
	for _, err := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	return func() {
		if err := host.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}
}
