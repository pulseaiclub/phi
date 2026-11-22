package commands

// DiffCommands owns the /diff slash command. It opens the viewer through the
// shared HostUI surface instead of a bare func field.
type DiffCommands struct {
	ui HostUI
}

// Register wires /diff into r.
func (d *DiffCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name:        "diff",
		Description: "Browse changed files — /diff, /diff staged, /diff HEAD, /diff @file",
		Slash:       true,
		Insert:      "/diff ",
		Run: func(_ Context, args []string) {
			if d.ui != nil {
				d.ui.OpenDiff(args)
			}
		},
	})
}
