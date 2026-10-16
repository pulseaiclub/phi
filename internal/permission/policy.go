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

// ModeOf returns the permission Mode configured on g, if known.
// BypassGate unwraps to its Inner. Unknown gate types return "".
func ModeOf(g Gate) Mode {
	for g != nil {
		switch x := g.(type) {
		case *StaticGate:
			if x.Policy.Mode != "" {
				return x.Policy.Mode
			}
			return ModeInteractive
		case *BypassGate:
			g = x.Inner
		case AllowAll:
			return ""
		default:
			return ""
		}
	}
	return ""
}

// Action names the kind of tool operation being gated.
type Action string

// Action values name the kinds of tool operations being gated.
const (
	ActionUnknown Action = "unknown"
	ActionBash    Action = "bash"
	ActionRead    Action = "read"
	ActionWrite   Action = "write"
	ActionEdit    Action = "edit"
	ActionGrep    Action = "grep"
	ActionFind    Action = "find"
	ActionLs      Action = "ls"

	ActionAgentSpawn   Action = "agent_spawn"   // delegates execution in a workspace
	ActionAgentControl Action = "agent_control" // waits for or cancels existing work
)

// Request describes a tool invocation for permission evaluation.
type Request struct {
	Action  Action
	Tool    string
	Paths   []string // absolute, cleaned
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
