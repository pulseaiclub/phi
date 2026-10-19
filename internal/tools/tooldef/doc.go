// Package tooldef defines the core tool types (Tool, Handler, Result, Registry)
// and the helpers used by every built-in tool.
//
// Each tool also declares its permission shape (WithPermission): which action
// it performs and where its command / paths live in the arguments. That keeps
// internal/permission free of tool names and argument schemas.
package tooldef
