package commands

// CodeCommands owns the /code slash command, which opens a file in the
// full-screen viewer through the shared HostUI surface.
type CodeCommands struct {
	ui HostUI
}

// Register wires /code into r.
func (c *CodeCommands) Register(r *CommandRegistry) {
	r.Register(Command{
		Name:        "code",
		Description: "Pick a file to view — /code [path[:line]]",
		Slash:       true,
		Insert:      "/code",
		Run: func(_ Context, args []string) {
			if c.ui != nil {
				c.ui.OpenCode(args)
			}
		},
	})
}
