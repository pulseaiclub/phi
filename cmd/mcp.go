package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
)

var (
	mcpCommand = cli.Command{
		Name: "mcp",
		Desc: "manage MCP servers (list, add, remove, call, doctor)",
		Long: `Set PHI_MCP=off to disable MCP tools in the agent.
Run 'phi mcp list' to see configured servers.`,
	}

	mcpListCommand = cli.Command{
		Name: "list",
		Desc: "list configured servers",
	}

	mcpAddCommand = cli.Command{
		Name:    "add",
		ArgsUse: "<name> -- <cmd> [args...]",
		Desc:    "add a stdio server to ~/.phi/mcp.json",
	}

	mcpRemoveCommand = cli.Command{
		Name:    "remove",
		Aliases: []string{"rm"},
		ArgsUse: "<name>",
		Desc:    "remove a server from user config",
	}

	mcpCallCommand = cli.Command{
		Name:    "call",
		ArgsUse: "<server> <tool> [json]",
		Desc:    "call a tool (optional JSON args object)",
	}

	mcpDoctorCommand = cli.Command{
		Name: "doctor",
		Desc: "check config + connectivity",
	}
)

func init() {
	mcpListCommand.Run = func(_ []string, _ cli.Flags) error { return mcpList() }
	mcpAddCommand.Run = func(args []string, _ cli.Flags) error { return mcpAdd(args) }
	mcpRemoveCommand.Run = func(args []string, _ cli.Flags) error { return mcpRemove(args) }
	mcpCallCommand.Run = func(args []string, _ cli.Flags) error { return mcpCall(args) }
	mcpDoctorCommand.Run = func(_ []string, _ cli.Flags) error { return mcpDoctor() }

	mcpCommand.Add(
		&mcpListCommand,
		&mcpAddCommand,
		&mcpRemoveCommand,
		&mcpCallCommand,
		&mcpDoctorCommand,
	)
}

func mcpList() error {
	servers, err := mcp.Load(project.GetDefaultProject().MCPConfigFile())
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("(no servers — try: phi mcp add fetch -- npx -y @modelcontextprotocol/server-fetch)")
		return nil
	}
	for _, name := range sortedKeys(servers) {
		cfg := servers[name]
		cmd, _ := cfg.CmdLine()
		fmt.Printf("%s\t%s\n", name, strings.Join(cmd, " "))
	}
	return nil
}

func sortedKeys(m map[string]mcp.ServerConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mcpAdd(args []string) error {
	if len(args) == 0 {
		return mcpAddCommand.Usagef("missing <name> -- <command> [args…]")
	}
	name := args[0]
	rest := args[1:]
	// allow optional "--" (Parse already strips a lone "--")
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return mcpAddCommand.Usagef("missing command after --")
	}
	cfg := mcp.ServerConfig{
		Transport: "stdio",
		Command:   rest[:1],
		Args:      rest[1:],
	}
	if err := mcp.AddServer(name, cfg); err != nil {
		return err
	}
	fmt.Printf("added %s\n", name)
	return nil
}

func mcpRemove(args []string) error {
	if len(args) != 1 {
		return mcpRemoveCommand.Usagef("expected <name>")
	}
	ok, err := mcp.RemoveServer(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%q is not a configured MCP server — see 'phi mcp list'", args[0])
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

func mcpCall(args []string) error {
	if len(args) < 2 {
		return mcpCallCommand.Usagef("expected <server> <tool> [json-args]")
	}
	server, tool := args[0], args[1]
	argMap := map[string]any{}
	if len(args) >= 3 {
		raw := strings.Join(args[2:], " ")
		if err := json.Unmarshal([]byte(raw), &argMap); err != nil {
			return mcpCallCommand.Usagef("args must be a JSON object: %v", err)
		}
	}
	pool, err := mcp.LoadPool(project.GetDefaultProject().MCPConfigFile())
	if err != nil {
		return err
	}
	if pool == nil {
		return errors.New("MCP disabled (PHI_MCP=off)")
	}
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := pool.Call(ctx, server, tool, argMap)
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

func mcpDoctor() error {
	if mcp.Disabled() {
		fmt.Println("PHI_MCP=off — MCP disabled")
		return nil
	}
	pool, err := mcp.LoadPool(project.GetDefaultProject().MCPConfigFile())
	if err != nil {
		return err
	}
	if pool == nil {
		return errors.New("MCP is disabled by PHI_MCP=off; unset it to run doctor")
	}
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	results := pool.Doctor(ctx)
	fail := 0
	for _, r := range results {
		status := "ok"
		if !r.OK {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s\t%s\t%s\n", status, r.Name, r.Detail)
	}
	if fail > 0 {
		return exitCode(ExitError)
	}
	return nil
}
