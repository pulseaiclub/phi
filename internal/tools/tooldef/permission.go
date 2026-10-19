package tooldef

import (
	"encoding/json"
	"fmt"

	"github.com/pulseaiclub/phi/internal/permission"
)

// Permission maps a call's raw arguments to a permission Request. This is the
// only thing the gate knows about a tool: each tool answers for its own schema,
// so internal/permission never names a tool or decodes its arguments.
type Permission func(args json.RawMessage, cwd string) (permission.Request, error)

// Declaration is the declarative half of the wiring: which kind of operation a
// tool performs, and where its command / paths live in its arguments. A tool
// states what it does; policy (allow / ask / deny) stays outside the tool
// package.
type Declaration[Args any] struct {
	Kind permission.Kind
	// Command returns the shell command an exec-like tool is about to run.
	Command func(Args) string
	// Paths returns the file paths or internal URLs a call touches. Relative
	// entries resolve against the session cwd; the binder normalizes each one.
	Paths func(Args) ([]string, error)
}

// WithPermission binds a declaration to the tool: arguments are decoded into
// Args, paths are normalized (mcp:// stays verbatim, files become absolute),
// and the result is the Request the gate evaluates.
func WithPermission[Args any](decl Declaration[Args]) ToolOption {
	return func(tool *Tool) {
		tool.Permission = func(args json.RawMessage, cwd string) (permission.Request, error) {
			req := permission.Request{Kind: decl.Kind}
			var in Args
			if err := json.Unmarshal(args, &in); err != nil {
				return req, err
			}
			if decl.Command != nil {
				req.Command = decl.Command(in)
			}
			if decl.Paths == nil {
				return req, nil
			}
			paths, err := decl.Paths(in)
			if err != nil {
				return req, err
			}
			for _, path := range paths {
				abs, err := permission.NormalizePath(path, cwd)
				if err != nil {
					return req, err
				}
				req.Paths = append(req.Paths, abs)
			}
			return req, nil
		}
	}
}

// PermissionRequest builds the permission Request for a tool call. Undeclared
// tools — extension-provided ones, or a name that is not in the registry —
// yield an unknown action, so the gate asks instead of trusting the name.
func (r Registry) PermissionRequest(
	name string,
	args json.RawMessage,
	cwd string,
) (permission.Request, error) {
	tool, ok := r[name]
	if !ok || tool.Permission == nil {
		return permission.Request{Tool: name, Kind: permission.KindUnknown}, nil
	}
	req, err := tool.Permission(args, cwd)
	if err != nil {
		return permission.Request{Tool: name, Kind: permission.KindUnknown}, fmt.Errorf("%s: %w", name, err)
	}
	req.Tool = name
	return req, nil
}
