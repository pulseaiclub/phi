// Package extension is the host side of the PXB extension mechanism: it
// discovers extension directories, spawns each binary, and exposes what the
// extensions registered (tools, slash commands, event intercepts) to the rest
// of phi.
//
// # Layers
//
// The package is split into semantic subpackages with a one-way dependency
// graph, so an extension feature lands in exactly one place (Cordis-style):
//
//	extension/            this package: extension discovery + Load     — lifecycle entry point
//	├── core/             kernel: Proc (one subprocess) + Fiber (its  — spawn, handshake, RPC,
//	│                         lifecycle state machine) + Runner       — dispose, topology, read model
//	├── ui/               BusUI: extension UI effects → host callbacks
//	├── loader/           config: phi.yaml manifest + discovery       — which extensions exist
//	├── install/          distribution: install, update, remove, list — cmd is the only consumer
//	└── create/           scaffolding: Materialize (dev/test helper)
//
// Load (load.go) bootstraps: it discovers directories through the loader and
// starts every extension through core. Arrows point down only:
// this package → core/loader, install → loader; create stands alone. Nothing
// imports upward, and loader knows nothing about the processes above it.
//
// The split mirrors cordis packages/core, which keeps Context / Fiber /
// Registry as files of one package — here core is that package: proc.go
// (PXB transport), fiber.go (state machine), runner.go / view.go / manage.go
// (the Runner and its read model). The root package intentionally exposes only
// lifecycle discovery through Load; callers use core and ui for their concrete
// types.
//
// See doc/extensions.md for the protocol and the author-facing SDK.
package extension
