package tree_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/tree"
)

func TestPrefixForSiblings(t *testing.T) {
	st := tree.DefaultStyle()
	require.Equal(t, "├── ", tree.PrefixForSiblings(3, 0, st), "first")
	require.Equal(t, "├── ", tree.PrefixForSiblings(3, 1, st), "mid")
	require.Equal(t, "╰── ", tree.PrefixForSiblings(3, 2, st), "last")
}

func TestPrefixNested(t *testing.T) {
	child := tree.Flat[string]{
		Item:             "a2",
		Depth:            1,
		IsLast:           true,
		AncestorsAreLast: []bool{false},
	}
	p := tree.Prefix(child, tree.DefaultStyle())
	require.Equal(t, "│   ╰── ", p, "nested last under non-last parent")
	pLast := tree.Prefix(tree.Flat[string]{Item: "b", IsLast: true}, tree.DefaultStyle())
	require.Equal(t, "╰── ", pLast, "root last")
}
