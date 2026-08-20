package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// jsonRPCError is the JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	if e == nil {
		return "mcp: nil error"
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// jsonRPCResponse is a JSON-RPC 2.0 response (id optional for parsing).
type jsonRPCResponse struct {
	ID     any             `json:"id"`
	Method string          `json:"method"` // set on notifications / server requests
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

func marshalRequest(id int64, method string, params map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
}

func marshalNotification(method string, params map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func nextID(counter *atomic.Int64) int64 {
	return counter.Add(1)
}

type toolsListPage struct {
	Tools      []ToolDef
	NextCursor *string
}

func decodeToolsList(raw json.RawMessage) (toolsListPage, error) {
	var parsed struct {
		Tools      []ToolDef `json:"tools"`
		NextCursor *string   `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return toolsListPage{}, fmt.Errorf("decode tools/list: %w", err)
	}
	return toolsListPage{Tools: parsed.Tools, NextCursor: parsed.NextCursor}, nil
}

// extractToolContent turns a tools/call result into model-facing text.
func extractToolContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw)
	}
	var b strings.Builder
	for _, part := range result.Content {
		if part.Type == "text" || part.Type == "" {
			b.WriteString(part.Text)
		}
	}
	out := b.String()
	if out == "" {
		return string(raw)
	}
	if result.IsError {
		return "error: " + out
	}
	return out
}

// parseHTTPOrSSEBody accepts plain JSON or SSE lines starting with "data: ".
func parseHTTPOrSSEBody(body []byte) (jsonRPCResponse, error) {
	text := string(body)
	for line := range strings.SplitSeq(text, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "data: ") {
			continue
		}
		var rpc jsonRPCResponse
		if err := json.Unmarshal([]byte(s[len("data: "):]), &rpc); err == nil {
			return rpc, nil
		}
	}
	var rpc jsonRPCResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return jsonRPCResponse{}, fmt.Errorf("parse http body: %w; raw=%q", err, truncate(text, 200))
	}
	return rpc, nil
}

func resultOrError(method string, rpc jsonRPCResponse) (json.RawMessage, error) {
	if rpc.Error != nil {
		return nil, fmt.Errorf("mcp %s: %w", method, rpc.Error)
	}
	return rpc.Result, nil
}
