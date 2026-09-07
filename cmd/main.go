package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	cli "github.com/pulseaiclub/pli"
)

func main() {
	if err := root().Dispatch(os.Args[1:]); err != nil {
		if ue, ok := errors.AsType[*cli.UsageError](err); ok {
			fmt.Fprintf(os.Stderr, "%s\n\n%s", ue.Error(), ue.Help())
			os.Exit(ExitUsage)
		}
		if he, ok := errors.AsType[*cli.HelpError](err); ok {
			fmt.Print(he.Help)
			return
		}
		if ee, ok := errors.AsType[*exitError](err); ok {
			os.Exit(ee.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(ExitError)
	}
}

// root is the process command tree. Built once: pli Add wires parent pointers
// and ignores a second Add on the same Command value.
var root = sync.OnceValue(buildRoot)

func buildRoot() *cli.Command {
	r := &cli.Command{
		Name: "phi",
		Desc: "terminal coding agent",
		Long: `phi                start the interactive TUI
phi tui            start the interactive TUI
phi config         open the HTML config editor (local web server)
phi update         install the latest release (see 'phi update --help')
phi run -p "..."   run one agent loop headlessly (see 'phi run --help')
phi sessions list  list persisted sessions for this directory
phi mcp …          manage MCP servers (see 'phi mcp --help')
phi plugin …          install/list/update/remove extensions (see 'phi plugin --help')`,
	}
	r.Run = func(args []string, _ cli.Flags) error {
		if len(args) > 0 {
			return r.Usagef("unknown command %q", args[0])
		}
		return runTUI()
	}
	r.Add(
		&tuiCommand,
		&runCommand,
		&sessionsCommand,
		&mcpCommand,
		&pluginCommand,
		&configCommand,
		&updateCommand,
	)
	return r
}
