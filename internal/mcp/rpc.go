package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/util"
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

func decodeToolsList(raw json.RawMessage) ([]ToolDefinition, error) {
	var parsed struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	return parsed.Tools, nil
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
// Servers may interleave log notifications (notifications/message, no id)
// before the response frame, so the first frame that is an actual response
// (id set, no method) wins. If none matches, the first parseable frame is
// returned (previous behavior).
func parseHTTPOrSSEBody(body []byte) (jsonRPCResponse, error) {
	text := string(body)
	var first jsonRPCResponse
	found := false
	for line := range strings.SplitSeq(text, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "data: ") {
			continue
		}
		var rpc jsonRPCResponse
		if err := json.Unmarshal([]byte(s[len("data: "):]), &rpc); err != nil {
			continue
		}
		if !found {
			first, found = rpc, true
		}
		if rpc.Method == "" && rpc.ID != nil {
			return rpc, nil
		}
	}
	if found {
		return first, nil
	}
	var rpc jsonRPCResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return jsonRPCResponse{}, fmt.Errorf("parse http body: %w; raw=%q", err, util.TruncateBytes(text, 200, "…"))
	}
	return rpc, nil
}

func resultOrError(method string, rpc jsonRPCResponse) (json.RawMessage, error) {
	if rpc.Error != nil {
		return nil, fmt.Errorf("mcp %s: %w", method, rpc.Error)
	}
	return rpc.Result, nil
}
