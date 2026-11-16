// Package commands owns the TUI slash/palette command surface.
//
// Assembly:
//
//	registry := commands.NewCommandRegistry()
//	builtins := commands.NewBuiltinRegistry(
//		commands.WithBuiltinCommandRegistry(registry),
//		commands.WithBuiltinBus(bus),
//		commands.WithBuiltinController(ctrl),
//		commands.WithBuiltinComposer(composer),
//		commands.WithBuiltinSubmitter(submitter),
//		commands.WithBuiltinCommandContext(commandCtx),
//	)
//
// Domains (session, branch, settings, extensions, skills, diff) register themselves
// hold Ctrl/Bus/Composer directly — no *Deps / *Params bags.
package commands
