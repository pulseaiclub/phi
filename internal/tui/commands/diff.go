package commands

// DiffCommands owns the /diff slash command.
type DiffCommands struct {
	Open func(args []string)
}

// Register wires /diff into r.
func (d *DiffCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name:        "diff",
		Description: "Review git diff — /diff, /diff staged, /diff HEAD, /diff @file",
		Slash:       true,
		Insert:      "/diff ",
		Run: func(_ Context, args []string) error {
			if d.Open != nil {
				d.Open(args)
			}
			return nil
		},
	})
}
