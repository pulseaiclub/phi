// Package permission implements the tool-execution gate: policy modes,
// decisions, and the user approval flow.
//
// It knows nothing about individual tools. Callers hand it a Request built by
// the tool itself (see tooldef.WithPermission); an undeclared tool arrives as
// KindUnknown, which asks rather than trusting the tool's name.
//
// The gate is a stack of three pieces, each with one job:
//
//	Gate      NewGate's return value: what callers hold
//	 └ modeGate    folds Ask by session mode (readonly, autopilot, headless)
//	    └ staticGate   splits a Request into subjects and matches rules
//	       └ ruleSet   deny / ask / allow tiers plus a kind fallback
//
// Rules are data (see Rule): selectors pick subjects, Decision says what
// happens. buildRules translates a Policy into rules once at construction, so
// evaluating a call never switches on a tool name or a config field. Tiers are
// strictest-first, and no allow entry can outrank a deny or an ask.
package permission
