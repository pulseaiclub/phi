// Package configui is the full-screen editor behind `phi config`: one keyboard
// form over ~/.phi/config.yaml.
//
// The document (project.ConfigDoc) is the single source of truth. Rows are
// rebuilt from it after every edit, the cursor is anchored to a row key instead
// of an index, and nothing is written to disk until the user saves.
package configui

import "context"

// ModelInfo describes one model advertised by a provider's /models endpoint,
// along with any capability limits the endpoint explicitly reported.
type ModelInfo struct {
	ID            string
	ContextWindow int
	MaxTokens     int
	ImageEnabled  *bool
}

// ModelLister fetches the models a provider advertises for one connection.
// It runs off the UI goroutine, so implementations must be safe for concurrent
// use and must honor ctx.
type ModelLister func(ctx context.Context, baseURL, apiKey, api, model string) ([]ModelInfo, error)
