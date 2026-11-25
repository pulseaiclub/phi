package lib

import (
	"bytes"
	"testing"

	"github.com/pulseaiclub/lug"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintBuiltin(t *testing.T) {
	var out bytes.Buffer
	env := lug.New()
	NewBuiltinBinding(&out).Bind(env)

	_, err := lug.DoString(env, `print("a", 1, true)`)
	require.NoError(t, err)
	assert.Equal(t, "a\t1\ttrue\n", out.String())
}

func TestIOWriteBuiltin(t *testing.T) {
	var out bytes.Buffer
	env := lug.New()
	NewBuiltinBinding(&out).Bind(env)

	_, err := lug.DoString(env, `io.write("x", 42)`)
	require.NoError(t, err)
	assert.Equal(t, "x42", out.String())
}

// Each Bind call must produce fresh function instances so plugins cannot see
// each other's output writer.
func TestBuiltinBindingIsolatesEnvs(t *testing.T) {
	var a, b bytes.Buffer
	envA, envB := lug.New(), lug.New()
	NewBuiltinBinding(&a).Bind(envA)
	NewBuiltinBinding(&b).Bind(envB)

	_, err := lug.DoString(envA, `print("from a")`)
	require.NoError(t, err)
	_, err = lug.DoString(envB, `io.write("from b\n")`)
	require.NoError(t, err)

	assert.Equal(t, "from a\n", a.String())
	assert.Equal(t, "from b\n", b.String())
}
