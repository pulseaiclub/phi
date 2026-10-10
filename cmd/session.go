package main

import (
	"fmt"
	"os"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
)

var (
	sessionsCommand = cli.Command{
		Name: "sessions",
		Desc: "list persisted sessions for this directory",
		Run:  func(_ []string, _ cli.Flags) error { return listSessions() },
	}

	sessionsListCommand = cli.Command{
		Name: "list",
		Desc: "list persisted sessions for this directory",
		Run:  func(_ []string, _ cli.Flags) error { return listSessions() },
	}

	sessionsRemoveCommand = cli.Command{
		Name:    "remove",
		Aliases: []string{"rm"},
		ArgsUse: "<session-id>",
		Desc:    "delete a persisted session for this directory (id prefix ok)",
	}
)

func init() {
	// Run is assigned here, not in the literal: removeSession refers back to
	// sessionsRemoveCommand for Usagef and Go rejects that initialization cycle.
	sessionsRemoveCommand.Run = func(args []string, _ cli.Flags) error { return removeSession(args) }
	sessionsCommand.Add(&sessionsListCommand)
	sessionsCommand.Add(&sessionsRemoveCommand)
}

// listSessions prints persisted sessions for the current project, newest first.
func listSessions() error {
	proj := project.GetDefaultProject()
	dir := proj.SessionDir()
	list, err := session.ListSessions(dir)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "no sessions in %s\n", dir)
		return nil
	}
	for _, s := range list {
		fmt.Printf("%s  %s  %s\n", s.ID, s.Mtime.Format("2006-01-02 15:04:05"), s.Preview)
	}
	return nil
}

// removeSession deletes one session file by id (exact or unique prefix).
func removeSession(args []string) error {
	if len(args) != 1 {
		return sessionsRemoveCommand.Usagef("expected <session-id>")
	}
	proj := project.GetDefaultProject()
	if err := session.DeleteSession(proj.SessionDir(), args[0]); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", args[0])
	return nil
}
