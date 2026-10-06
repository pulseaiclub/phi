package lstool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/tooldef"

	"github.com/pulseaiclub/phi/internal/llm"
)

const (
	lsDescription = `List files and directories as an ASCII tree.

Use limit and max_depth to control output size. Hidden files and common
cache directories are skipped.`
	truncatedMessage = "[Tree truncated after %d files. Use limit=<n> to see more.]\n\n"
)

const (
	defaultMaxFiles = 500
	defaultMaxDepth = 3
)

// LsTool returns the ls tool definition + handler.
func LsTool() tooldef.Tool {
	return tooldef.NewTool(
		tooldef.WithDefinition(llm.ToolDefinition{
			Name:        "ls",
			Description: lsDescription,
			Params: &llm.FunctionParameters{
				Type: "object",
				Properties: llm.Object{
					"path": llm.Object{
						"type":        "string",
						"description": "Directory to list. Example: . or src",
					},
					"limit": llm.Object{
						"type":        "integer",
						"description": "Max files to scan. Example: 100 (default: 500)",
					},
					"max_depth": llm.Object{
						"type":        "integer",
						"description": "Max directory depth to expand. Example: 2 (default: 3)",
					},
				},
				Required: []string{"path"},
			},
			Readable: true,
		}),
		tooldef.WithDetail(lsDetail),
		tooldef.WithHandler(runLs),
	)
}

func lsDetail(in lsInput) string {
	return strings.TrimSpace(in.Path)
}

type lsInput struct {
	Path     string `json:"path,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	MaxDepth int    `json:"max_depth,omitempty"`
}

// UnmarshalJSON also accepts a plain JSON string as the path.
func (in *lsInput) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil && strings.TrimSpace(s) != "" {
		in.Path = strings.TrimSpace(s)
		return nil
	}
	type plain lsInput // distinct type: sheds UnmarshalJSON, avoids recursion
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("failed to parse ls arguments: %w", err)
	}
	*in = lsInput(p)
	return nil
}

func normalizeOptions(limit, maxDepth int) (int, int) {
	if limit <= 0 {
		limit = defaultMaxFiles
	}
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	return limit, maxDepth
}

type treeNode struct {
	Name     string      `json:"name"`
	IsDir    bool        `json:"isDir"`
	Type     string      `json:"type"`
	Children []*treeNode `json:"children,omitempty"`
}

var skipDirs = map[string]bool{
	"__pycache__":    true,
	"node_modules":   true,
	"venv":           true,
	".venv":          true,
	"vendor":         true,
	".idea":          true,
	".vscode":        true,
	"target":         true,
	"dist":           true,
	"build":          true,
	".pytest_cache":  true,
	".mypy_cache":    true,
	".tox":           true,
	"__pypackages__": true,
	".git":           true,
	".svn":           true,
	".hg":            true,
}

func runLs(ctx context.Context, in lsInput) (tooldef.Result, error) {
	dir, err := tooldef.ResolveToCwd(ctx, in.Path)
	if err != nil {
		return tooldef.Result{}, err
	}
	dir = filepath.Clean(dir)

	info, err := os.Stat(dir)
	if err != nil {
		return tooldef.Result{}, fmt.Errorf("path not found or inaccessible: %s. Check the path and permissions", dir)
	}
	if !info.IsDir() {
		return tooldef.Result{}, fmt.Errorf("not a directory: %s (ls expects a directory path)", dir)
	}

	limit, maxDepth := normalizeOptions(in.Limit, in.MaxDepth)

	var fileCount int
	var stoppedEarly bool
	root := buildTree(ctx, dir, &fileCount, &stoppedEarly, limit, 0, maxDepth)
	if root == nil {
		return tooldef.Result{}, fmt.Errorf("failed to build tree for directory %s", dir)
	}

	display := tooldef.RelToCwd(ctx, dir)
	treeStr := renderTree(display, root.Children)

	// A directory holding exactly limit files is a complete listing. Only
	// claim truncation when the walk actually stopped early with more to show.
	if !stoppedEarly {
		return tooldef.Result{Content: treeStr, Detail: display, Output: treeStr}, nil
	}

	truncated := fmt.Sprintf(truncatedMessage, limit) + treeStr
	return tooldef.Result{Content: truncated, Detail: display, Output: truncated}, nil
}

func shouldSkip(name string) bool {
	return (name != "" && name[0] == '.') || skipDirs[name]
}

func buildTree(
	ctx context.Context,
	dir string,
	fileCount *int,
	stoppedEarly *bool,
	limit, currentDepth, maxDepth int,
) *treeNode {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	node := &treeNode{
		Name:     filepath.Base(dir),
		IsDir:    true,
		Type:     "directory",
		Children: []*treeNode{},
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil
		}

		name := entry.Name()
		if shouldSkip(name) {
			continue
		}

		if *fileCount >= limit {
			// Current entry (and any after it) did not fit — the listing is cut.
			*stoppedEarly = true
			break
		}

		childPath := filepath.Join(dir, name)
		if entry.IsDir() {
			if currentDepth+1 >= maxDepth {
				node.Children = append(node.Children, &treeNode{
					Name:  name,
					IsDir: true,
					Type:  "directory",
				})
				continue
			}
			child := buildTree(ctx, childPath, fileCount, stoppedEarly, limit, currentDepth+1, maxDepth)
			if child != nil {
				node.Children = append(node.Children, child)
			} else {
				// If child directory cannot be read, still show directory node.
				node.Children = append(node.Children, &treeNode{
					Name:  name,
					IsDir: true,
					Type:  "directory",
				})
			}
		} else {
			*fileCount++
			node.Children = append(node.Children, &treeNode{
				Name:  name,
				IsDir: false,
				Type:  "file",
			})
		}
	}

	return node
}

func renderTree(rootPath string, children []*treeNode) string {
	var b strings.Builder
	root := filepath.ToSlash(rootPath)
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	fmt.Fprintf(&b, "%s\n", root)
	for i, node := range children {
		renderTreeNode(&b, node, "", i == len(children)-1)
	}
	return b.String()
}

func renderTreeNode(b *strings.Builder, node *treeNode, prefix string, isLast bool) {
	connector := "├── "
	nextPrefix := prefix + "│   "
	if isLast {
		connector = "└── "
		nextPrefix = prefix + "    "
	}

	name := node.Name
	if node.IsDir || node.Type == "directory" {
		name += string(os.PathSeparator)
	}
	b.WriteString(prefix)
	b.WriteString(connector)
	b.WriteString(name)
	b.WriteString("\n")

	for i, child := range node.Children {
		renderTreeNode(b, child, nextPrefix, i == len(node.Children)-1)
	}
}
