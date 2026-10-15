package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	cli "github.com/pulseaiclub/pli"
)

// toolsFlag is the parsed --tools value: the raw comma list. Names are
// validated at parse time; selection happens in runHeadless once the MCP pool
// is loaded, so the chosen tools are MCP-aware.
type toolsFlag struct {
	set bool
	raw string
}

// String keeps pli help from dumping the zero value as "{false []}".
func (toolsFlag) String() string { return "" }

var runCommand = cli.Command{
	Name: "run",
	Desc: "run one agent loop headlessly and exit",
	Long: `Human logs go to stderr; with --jsonl, machine-readable events go to
stdout (one JSON object per line).

exit codes:
  0 success   1 runtime/LLM error   2 max rounds   3 config/usage`,
	Flags: []cli.Flag{
		cli.String("prompt", "p", "prompt to run (required)", ""),
		cli.Bool("jsonl", "", "emit JSONL events to stdout"),
		cli.Bool("yolo", "", "skip all permission checks for this run (benchmarks / CI only)"),
		cli.Var("max-rounds", "", "N", "cap tool rounds (default 64)", 0, parsePositiveInt),
		cli.Var(
			"timeout",
			"",
			"DURATION",
			"stop after a wall-clock duration (e.g. 10m; default unlimited)",
			time.Duration(0),
			parsePositiveDuration,
		),
		cli.String("session", "", "resume a persisted session by id or unique prefix", ""),
		cli.Bool("continue-last", "", "resume the newest persisted session for this directory"),
		cli.String("session-dir", "", "override the session storage directory", ""),
		cli.Var("tools", "", "LIST", "enable only these built-in tools (comma-separated)", toolsFlag{}, parseToolsFlag),
	},
}

func init() {
	// Run is assigned here so runOptionsFromFlags can call runCommand.Usagef
	// without an initialization cycle.
	runCommand.Run = func(_ []string, f cli.Flags) error {
		opts, err := runOptionsFromFlags(f)
		if err != nil {
			return err
		}
		return runHeadless(opts)
	}
}

func runOptionsFromFlags(f cli.Flags) (runOptions, error) {
	opts := runOptions{
		prompt:       f.String("prompt"),
		jsonl:        f.Bool("jsonl"),
		yolo:         f.Bool("yolo"),
		maxRounds:    f.Int("max-rounds"),
		timeout:      f.Duration("timeout"),
		session:      f.String("session"),
		continueLast: f.Bool("continue-last"),
		sessionDir:   f.String("session-dir"),
	}
	if tf, ok := f["tools"].(toolsFlag); ok && tf.set {
		opts.builtinTools = tf.raw
	}
	if strings.TrimSpace(opts.prompt) == "" {
		return opts, runCommand.Usagef("prompt is required (-p \"...\")")
	}
	if opts.continueLast && opts.session != "" {
		return opts, runCommand.Usagef("--continue-last and --session are mutually exclusive")
	}
	return opts, nil
}

func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive integer, got %q", s)
	}
	return n, nil
}

func parsePositiveDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("must be a positive duration, got %q", s)
	}
	return d, nil
}

func parseToolsFlag(s string) (toolsFlag, error) {
	// Validate names now (fail at parse time); runHeadless re-selects with the
	// loaded MCP pool so the chosen tools can resolve mcp:// URLs.
	if _, err := selectBuiltinTools(s, nil); err != nil {
		return toolsFlag{}, err
	}
	return toolsFlag{set: true, raw: s}, nil
}
