package plugin

import (
	"fmt"
	"io"
	"strings"

	"github.com/pulseaiclub/lug"
	"github.com/pulseaiclub/lug/runtime"
)

type LuaFunction interface {
	Apply(e *lug.Env, args []lug.Value) []lug.Value
}

type builtin struct {
	name        string
	newFunction func(io.Writer) LuaFunction
}

// Populated only during package initialization; environments never share instances.
var builtins []builtin

func registerBuiltin[T LuaFunction](name string, factory func(io.Writer) T) {
	for _, b := range builtins {
		if b.name == name {
			panic("plugin: duplicate builtin " + name)
		}
	}
	builtins = append(builtins, builtin{name, func(output io.Writer) LuaFunction { return factory(output) }})
}

func (b builtin) install(env *lug.Env, output io.Writer) {
	fn := runtime.NewClosure(b.newFunction(output).Apply, b.name)
	if module, field, ok := strings.Cut(b.name, "."); ok {
		env.Get(module).Table().Set(lug.String(field), fn)
		return
	}
	env.Set(b.name, fn)
}

func init() { registerBuiltin("print", NewPrint) }

type Print struct {
	Output io.Writer
}

func NewPrint(output io.Writer) *Print {
	return &Print{
		Output: output,
	}
}

func (p Print) Apply(e *lug.Env, args []lug.Value) []lug.Value {
	fmt.Fprintln(p.Output, runtime.PrintLine(e, args))
	return nil
}

func init() { registerBuiltin("io.write", NewIO) }

type IO struct {
	Output io.Writer
}

func NewIO(output io.Writer) *IO {
	return &IO{Output: output}
}

func (i IO) Apply(e *lug.Env, args []lug.Value) []lug.Value {
	for _, arg := range args {
		fmt.Fprint(i.Output, runtime.ToStringEnv(e, arg))
	}
	return nil
}
