package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupErrorKeepsTheUserFacingMessage(t *testing.T) {
	cause := errors.New("missing models (add at least one model in ~/.phi/config.yaml)")
	err := fmt.Errorf("phi run: %w", setupError{err: cause})

	require.True(t, isSetupError(err), "entrypoints must recognize a setup failure through wrapping")
	assert.Contains(t, err.Error(), "missing models")
	assert.NotContains(t, err.Error(), "setup error", "the marker must stay invisible to the user")
}

func TestOtherErrorsAreNotSetupFailures(t *testing.T) {
	assert.False(t, isSetupError(errors.New("permissions: bad rule")))
	assert.False(t, isSetupError(nil))
}

func TestPrintSetupHintNamesBothPaths(t *testing.T) {
	var buf bytes.Buffer
	printSetupHint(&buf)
	out := buf.String()
	assert.Contains(t, out, "phi config")
	assert.Contains(t, out, "PHI_MODEL")
	assert.Contains(t, out, "PHI_API_KEY")
}
