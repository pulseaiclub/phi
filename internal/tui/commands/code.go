package commands

// CodeCommands owns the /code slash command, which opens a file in the
// full-screen viewer.
type CodeCommands struct {
	Open func(args []string)
}

// Register wires /code into r.
func (c *CodeCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name:        "code",
		Description: "Pick a file to view — /code [path[:line]]",
		Slash:       true,
		Insert:      "/code",
		Run: func(_ Context, args []string) {
			if c.Open != nil {
				c.Open(args)
			}
		},
	})
}
