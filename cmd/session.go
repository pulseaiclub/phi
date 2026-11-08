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

	sessionsRemoveCommand = cli.Command{
		Name:    "remove",
		Aliases: []string{"rm"},
		ArgsUse: "<session-id>",
		Desc:    "delete a persisted session for this directory (id prefix ok)",
	}

	sessionsForkCommand = cli.Command{
		Name:    "fork",
		ArgsUse: "<session-id>",
		Desc:    "copy a session at its current point into a new session, print the new id",
	}
)

func init() {
	// Run is assigned here, not in the literal: removeSession refers back to
	// sessionsRemoveCommand for Usagef and Go rejects that initialization cycle.
	sessionsRemoveCommand.Run = func(args []string, _ cli.Flags) error { return removeSession(args) }
	sessionsCommand.Add(&sessionsRemoveCommand)
	sessionsForkCommand.Run = func(args []string, _ cli.Flags) error { return forkSession(args) }
	sessionsCommand.Add(&sessionsForkCommand)
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
		line := fmt.Sprintf("%s  %s  %s", s.ID, s.Mtime.Format("2006-01-02 15:04:05"), s.Preview)
		if s.Parent != "" {
			line += "  ↳ " + session.ShortID(s.Parent)
		}
		fmt.Println(line)
	}
	return nil
}

// forkSession copies one session at its current point and prints the new id, so
// `phi run --session <new-id>` continues the copy without touching the source.
func forkSession(args []string) error {
	if len(args) != 1 {
		return sessionsForkCommand.Usagef("expected <session-id>")
	}
	proj := project.GetDefaultProject()
	path, err := session.FindSessionFile(proj.SessionDir(), args[0])
	if err != nil {
		return err
	}
	src, err := session.OpenSession(path)
	if err != nil {
		return err
	}
	fork, err := src.Fork()
	if err != nil {
		return err
	}
	fmt.Println(fork.ID())
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
