package archive

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractUnknownFormat(t *testing.T) {
	t.Parallel()
	err := Extract(t.Context(), "whatever.rar", "rar", t.TempDir())
	require.ErrorContains(t, err, "unknown archive format: rar")
}
