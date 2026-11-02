package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

// CompactServerList returns space-separated server names.
func CompactServerList(names []string) string {
	if len(names) == 0 {
		return "(no mcp servers configured)"
	}
	return strings.Join(names, " ")
}

// CompactToolNames returns space-separated tool names.
func CompactToolNames(tools []ToolDefinition) string {
	if len(tools) == 0 {
		return "(no tools)"
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return strings.Join(names, " ")
}

// SlimTool renders a compact one-line schema for inspect.
// Example: echo|env:s{dev|prod}*  — name|param:type[{enum}][*required].
func SlimTool(t ToolDefinition) string {
	var b strings.Builder
	b.WriteString(t.Name)
	if t.Description != "" {
		b.WriteString(" — ")
		b.WriteString(t.Description)
	}
	props, required := schemaProps(t.InputSchema)
	if len(props) == 0 {
		return b.String()
	}
	b.WriteByte('\n')
	b.WriteString(t.Name)
	b.WriteByte('|')
	b.WriteString(strings.Join(slimParams(props, required), "|"))
	return b.String()
}

func slimParams(props map[string]string, required map[string]bool) []string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, name := range keys {
		s := name + ":" + props[name]
		if required[name] {
			s += "*"
		}
		out = append(out, s)
	}
	return out
}

func schemaProps(schema json.RawMessage) (map[string]string, map[string]bool) {
	props := map[string]string{}
	required := map[string]bool{}
	if len(schema) == 0 {
		return props, required
	}
	var doc struct {
		Properties map[string]struct {
			Type string `json:"type"`
			Enum []any  `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		return props, required
	}
	for name, p := range doc.Properties {
		typ := p.Type
		if typ == "" {
			typ = "?"
		}
		// shorten common JSON schema types
		switch typ {
		case "string":
			typ = "s"
		case "number", "integer":
			typ = "n"
		case "boolean":
			typ = "b"
		case "object":
			typ = "o"
		case "array":
			typ = "a"
		}
		// Enum values are the contract the model must satisfy, so they ride
		// the compact summary even though descriptions do not.
		if len(p.Enum) > 0 {
			typ += enumHint(p.Enum)
		}
		props[name] = typ
	}
	for _, r := range doc.Required {
		required[r] = true
	}
	return props, required
}

// maxEnumValues caps enum rendering so one huge enum cannot blow up the
// compact summary; the tail is summarized as +N.
const maxEnumValues = 8

func enumHint(values []any) string {
	shown, more := values, 0
	if len(shown) > maxEnumValues {
		shown, more = shown[:maxEnumValues], len(shown)-maxEnumValues
	}
	parts := make([]string, 0, len(shown)+1)
	for _, v := range shown {
		if s, ok := v.(string); ok {
			parts = append(parts, s)
			continue
		}
		// Non-string values keep their JSON form (numbers, bools, nested).
		b, err := json.Marshal(v)
		if err != nil {
			parts = append(parts, fmt.Sprintf("%v", v))
			continue
		}
		parts = append(parts, string(b))
	}
	if more > 0 {
		parts = append(parts, fmt.Sprintf("+%d", more))
	}
	return "{" + strings.Join(parts, "|") + "}"
}

// FormatCallResult keeps call output bounded for the model.
func FormatCallResult(s string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 32_000
	}
	if len(s) <= maxChars {
		return s
	}
	return util.TruncateBytes(s, maxChars, fmt.Sprintf("\n… truncated (%d chars total)", len(s)))
}
