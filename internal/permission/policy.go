package permission

// Mode controls how Ask decisions are folded.
type Mode string

// Mode values control how Ask decisions are folded.
const (
	ModeInteractive    Mode = "interactive"
	ModeReadonly       Mode = "readonly"
	ModeAutopilot      Mode = "autopilot"
	ModeHeadlessStrict Mode = "headless-strict"
)

// Decision is the gate outcome before optional Ask folding.
type Decision int

// Decision values are the gate outcomes before optional Ask folding.
const (
	Allow Decision = iota
	Deny
	Ask
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case Ask:
		return "ask"
	default:
		return "unknown"
	}
}

// ModeOf reports the permission Mode g enforces, if it exposes one. Gate
// decorators that hide the mode (BypassGate) are unwrapped; a gate with no mode
// of its own (AllowAll) reports "".
func ModeOf(g Gate) Mode {
	for g != nil {
		switch x := g.(type) {
		case interface{ Mode() Mode }:
			return x.Mode()
		case *BypassGate:
			g = x.Inner
		default:
			return ""
		}
	}
	return ""
}

// Request describes a tool invocation for permission evaluation. Tools build
// it (tooldef.WithPermission); the gate never inspects tool arguments itself.
type Request struct {
	Kind    Kind
	Tool    string
	Paths   []string // absolute and cleaned, or internal URLs verbatim
	Command string
}

// Policy is the configurable permission ruleset.
type Policy struct {
	Mode                Mode
	WorkspaceOnlyWrites bool
	AskTimeoutSec       int
	BashDefault         Decision // typically Ask
	BashAllow           []string // regex
	BashDeny            []string // regex
	SensitivePathDeny   []string // path prefixes
	WorkspaceOnlyReads  bool     // if true, out-of-workspace reads deny
	DangerouslyAllowAll bool     // skip all permission checks
	// Rules are extra rules from config (see RuleSpec.Compile). They are filed
	// by decision into the same tiers as the built-ins and run after them, so
	// they can tighten a policy but never soften a built-in deny.
	Rules []Rule
}

// DefaultPolicy returns the interactive defaults from task-002.
func DefaultPolicy() Policy {
	return Policy{
		Mode:                ModeInteractive,
		WorkspaceOnlyWrites: true,
		AskTimeoutSec:       120,
		BashDefault:         Ask,
		BashAllow:           defaultBashAllow,
		BashDeny:            defaultBashDeny,
		SensitivePathDeny:   defaultSensitivePaths(),
		WorkspaceOnlyReads:  false,
	}
}

// ChildPolicy is the default gate for sub-agents: no interactive Ask, bash
// allowed unless it matches the hard deny list. Mode still folds writes
// (Readonly) or Ask leftovers (HeadlessStrict).
func ChildPolicy(mode Mode) Policy {
	p := DefaultPolicy()
	p.Mode = mode
	p.BashDefault = Allow
	return p
}
