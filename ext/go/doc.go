// Package ext is the public API surface for Phi extensions.
//
// Extensions are native binaries that speak the PXB protocol (package ext/pxb)
// over stdin/stdout. Prefer the author SDK in package ext/phi:
//
//	m := phi.New("greet", "1.0.0")
//	m.RegisterTool(...)
//	m.Run()
//
// Ship a phi.yaml next to the binary under ~/.phi/extensions/<name>/.
// See doc/extensions.md.
package ext
