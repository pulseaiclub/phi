package lib

import (
	"io"

	"github.com/pulseaiclub/lug"
)

// BuiltinBinding registers every built-in Lua function into env.
type BuiltinBinding struct {
	output io.Writer
}

func NewBuiltinBinding(output io.Writer) *BuiltinBinding {
	return &BuiltinBinding{output: output}
}

func (b *BuiltinBinding) Bind(env *lug.Env) {
	installAll(env, b.output)
}

func installAll(env *lug.Env, output io.Writer) {
	for _, b := range builtins {
		b.install(env, output)
	}
}
