package lib

import (
	"fmt"
	"io"
	"strings"

	"github.com/pulseaiclub/lug"
	"github.com/pulseaiclub/lug/runtime"
)

// LuaFunction is the Go implementation behind a built-in Lua function.
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

// install binds the function under name, creating the module table for dotted
// names ("io.write" lands in a fresh io table) so order never matters.
func (b builtin) install(env *lug.Env, output io.Writer) {
	fn := runtime.NewClosure(b.newFunction(output).Apply, b.name)
	if module, field, ok := strings.Cut(b.name, "."); ok {
		t := env.Get(module).Table()
		if t == nil {
			t = lug.NewTable()
			env.Set(module, lug.TableVal(t))
		}
		t.Set(lug.String(field), fn)
		return
	}
	env.Set(b.name, fn)
}

func init() {
	registerBuiltin("print", NewPrint)
	registerBuiltin("io.write", NewIO)
}

// Print implements print: one line of space-separated arguments.
type Print struct {
	output io.Writer
}

func NewPrint(output io.Writer) *Print {
	return &Print{output: output}
}

func (p *Print) Apply(e *lug.Env, args []lug.Value) []lug.Value {
	fmt.Fprintln(p.output, runtime.PrintLine(e, args))
	return nil
}

// IO implements io.write: arguments concatenated without separators or newline.
type IO struct {
	output io.Writer
}

func NewIO(output io.Writer) *IO {
	return &IO{output: output}
}

func (i *IO) Apply(e *lug.Env, args []lug.Value) []lug.Value {
	for _, arg := range args {
		fmt.Fprint(i.output, runtime.ToStringEnv(e, arg))
	}
	return nil
}
