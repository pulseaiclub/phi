package permission

import (
	"context"
	"fmt"
	"strings"
)

// modeGate applies session mode to the decisions of the gate underneath it.
// Mode is how Ask is folded, not a policy rule, so it stays out of the rule set.
type modeGate struct {
	mode  Mode
	inner Gate
}

func (g *modeGate) Check(ctx context.Context, req Request) (Decision, string) {
	// Readonly cannot delegate: a sub-agent process is not held to the readonly
	// contract, so this denies before any rule can ask.
	if g.mode == ModeReadonly && req.Kind == KindDelegate {
		return Deny, "readonly mode denies agent_spawn"
	}
	dec, reason := g.inner.Check(ctx, req)
	return foldMode(g.mode, dec, reason, req)
}

// Mode reports the session mode the gate enforces.
func (g *modeGate) Mode() Mode { return g.mode }

// foldMode applies mode. Interactive reports the gate's decision unchanged;
// readonly denies mutations and unknown asks outright; autopilot and
// headless-strict have nowhere to prompt, so Ask becomes Deny.
func foldMode(mode Mode, dec Decision, reason string, req Request) (Decision, string) {
	switch mode {
	case "", ModeInteractive:
		return dec, reason

	case ModeReadonly:
		if mutates(req) {
			// Allowlisted bash already passed the deny list; readonly does not
			// forbid reading through a command.
			if req.Kind == KindExec && dec == Allow {
				return Allow, reason
			}
			if dec == Allow || dec == Ask {
				return Deny, readonlyReason(req, reason)
			}
		}
		if dec == Ask {
			return Deny, askFoldReason(reason, mode)
		}
		return dec, reason

	case ModeAutopilot, ModeHeadlessStrict:
		if dec == Ask {
			return Deny, askFoldReason(reason, mode)
		}
		return dec, reason

	default:
		return dec, reason
	}
}

// mutates reports whether the call can change state. Kind is the tool's own
// declaration, so a tool that writes must say KindWrite to be gated as one.
func mutates(req Request) bool {
	return req.Kind == KindWrite || req.Kind == KindExec
}

func readonlyReason(req Request, fallback string) string {
	if fallback != "" && !strings.Contains(fallback, "requires approval") {
		return fallback
	}
	subject := req.Tool
	if subject == "" {
		subject = string(req.Kind)
	}
	return "readonly mode denies " + subject
}

func askFoldReason(reason string, mode Mode) string {
	if reason == "" {
		return fmt.Sprintf("%s mode denies operations that would require approval", mode)
	}
	return fmt.Sprintf("%s mode: %s", mode, reason)
}
