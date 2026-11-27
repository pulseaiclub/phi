package util

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultHTTPClientResponseHeaderTimeout(t *testing.T) {
	transport, ok := DefaultHTTPClient().Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, 6*time.Minute, transport.ResponseHeaderTimeout)
}
