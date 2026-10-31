package controller

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/extmodel"
	"github.com/pulseaiclub/phi/internal/project/model"
)

const (
	maxCommitMessageBytes   = 2 * 1024
	maxCommitReasoningBytes = 16 * 1024
	commitEndMarker         = "\n<phi-commit-end>"
)

// GenerateCommitMessage requests one tool-free completion using the controller's
// current model and extension topology. The request does not touch the agent
// engine, session, or executor.
func (c *EngineController) GenerateCommitMessage(
	ctx context.Context,
	system, user string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg := c.modelSnapshot()
	runner := c.Extensions()
	system = strings.TrimSpace(system + "\n\n" + prompt.ProjectContext(c.cwd))
	// Provider streams do not consistently preserve truncation reasons. Require
	// an explicit terminator so a valid-looking partial subject is not offered.
	system += "\nFor this completion apply only repository commit-message rules. After the complete subject, append " +
		"a newline and <phi-commit-end>. This required transport marker is not part of the message."
	client := newCompletionClient(cfg, runner, system)
	text, err := collectCompletion(ctx, client.Stream(ctx, []llm.Message{{Role: llm.RoleUser, Content: user}}))
	if err != nil {
		return "", err
	}
	text = strings.TrimRight(text, " \t\r\n")
	if !strings.HasSuffix(text, commitEndMarker) {
		return "", errors.New("commit message completion is missing its end marker; try again")
	}
	return strings.TrimSuffix(text, commitEndMarker), nil
}

func newCompletionClient(
	cfg llm.ModelConfig,
	runner *extension.Runner,
	system string,
) llm.ChatClient {
	if route, ok := runner.LookupModel(cfg.Name); ok && route.ExtConn() {
		stream := func(
			ctx context.Context,
			req pxb.ModelInvoke,
		) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
			return runner.ModelStream(ctx, route, req)
		}
		return extmodel.New(route.Reg.Name, stream, cfg, nil, system)
	}
	return llmclient.NewClient(cfg, model.HooksForConfig(cfg), nil, system)
}

func collectCompletion(ctx context.Context, events iter.Seq2[llm.StreamEvent, error]) (string, error) {
	var content strings.Builder
	var final string
	var finalSeen bool
	var reasoningBytes int
	var done bool

	for event, err := range events {
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if event.Err != "" || event.Type == llm.StreamEventTypeError {
			if event.Err == "" {
				event.Err = "model completion failed"
			}
			return "", errors.New(event.Err)
		}
		if len(event.Delta.ToolCalls) > 0 || (event.Final != nil && len(event.Final.ToolCalls) > 0) {
			return "", errors.New("commit message completion requested a tool")
		}
		if len(event.Delta.Content) > maxCommitMessageBytes-content.Len() {
			return "", fmt.Errorf("commit message completion exceeds %d bytes", maxCommitMessageBytes)
		}
		reasoningBytes += len(event.Delta.ReasoningContent)
		if reasoningBytes > maxCommitReasoningBytes ||
			(event.Final != nil && len(event.Final.ReasoningContent) > maxCommitReasoningBytes) {
			return "", errors.New("commit message completion exceeded its reasoning budget")
		}
		content.WriteString(event.Delta.Content)
		if event.Final != nil {
			finalSeen = true
			final = event.Final.Content
			if len(final) > maxCommitMessageBytes {
				return "", fmt.Errorf("commit message completion exceeds %d bytes", maxCommitMessageBytes)
			}
		}
		if event.Type == llm.StreamEventTypeDone {
			done = true
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !done {
		return "", errors.New("commit message completion ended without a final response")
	}
	// Final is an authoritative snapshot, not another content delta.
	if !finalSeen {
		final = content.String()
	}
	if strings.TrimSpace(final) == "" {
		return "", errors.New("commit message completion returned no content")
	}
	return final, nil
}
