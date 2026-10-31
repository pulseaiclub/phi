# Phi Rust extension SDK (`ext/rust`)

Rust is a **first-class language for Phi extensions** — on par with the Go SDK
in [`ext/go`](../go). Same PXB wire protocol on stdin/stdout, same host
features (LLM tools, slash commands, model registration and serving, intercepts,
event subscriptions, confirm dialogs), byte-for-byte interop, and the same
install flow (`phi.yaml` + a binary under `~/.phi/extensions/<name>/`). The only
dependencies are `serde`/`serde_json` at the JSON edges (tool schemas, model
messages, confirm payloads) plus `tokio` (`rt` feature) to drive async tool
handlers; the
PXB wire codec stays hand-rolled — no reflection, no runtime protocol deps.

Wire compatibility with the Go SDK is pinned byte-for-byte by golden tests
against `ext/go/pxb/testdata/*.bin` (`tests/pxb_test.rs`).

## Layout

| Path | Role |
|------|------|
| `src/pxb/` | Wire protocol: frames (`codec`), tagged fields (`fields`), message codecs (`msg`), types/events (`types`) |
| `src/phi/` | Author SDK: `Extension`, `Tool`, `Command`, `Model`, `Context`, `Schema` |
| `examples/` | Runnable extensions: `hello` (commands, intercepts, subscribe), `full` (tool, confirm, submit), `models` (direct-connect + served models) |
| `tests/` | Golden byte-compat + end-to-end fake-host tests |

## Authoring

The crate publishes to crates.io; release tags are `ext/rust/vX.Y.Z`. Until
the first publish, depend on it from the repo (`main`); after a release, use
the published version:

```toml
[dependencies]
# pre-release: tracks the latest main
phi-ext = { git = "https://github.com/pulseaiclub/phi", branch = "main" }
# release: published crate version (tagged ext/rust/vX.Y.Z)
# phi-ext = "0.1.0"
```

```rust,no_run
use phi_ext::{phi, pxb};

fn main() -> Result<(), phi::Error> {
    let mut m = phi::Extension::new("hello", "0.1.0");
    m.register_command("hello", phi::Command::new("Say hi", |_args, ctx| {
        ctx.notify("info", "Hello!");
        // ctx.submit("follow-up");      // after /hello returns
        // ctx.send_user_message("…");   // enqueue a turn anytime
        Ok(())
    }));
    // .needs_args() → picker/bare "/plan" fills "/plan " for the user to finish
    m.register_command(
        "plan",
        phi::Command::new("plan mode — /plan on|off|status", |_args, _ctx| Ok(())).needs_args(),
    );
    m.on_user_input(|_ev| None);   // return Some(UserInputResult { handled: true, .. }) to swallow
    m.on_tool_call(|_ev| None);    // return Some(ToolCallResult { block: true, reason: "…", .. }) to deny
    m.on_tool_result(|_ev| None);  // return Some(ToolResultResult { stop: true, .. }) to end the loop
    m.on_turn_stopping(|_ev| None); // return Some(TurnStoppingResult { continue_: true, message: "…", .. }) to steer
    m.subscribe(pxb::Event::SessionStart, |_ev| {});
    m.run()
}
```

Tools are usually IO-bound, so handlers may be async — the SDK runs them on a
single-threaded tokio runtime that blocks the PXB loop the same way a sync
handler would (the host waits for the result anyway):

```rust,no_run
use phi_ext::phi;

m.register_tool(phi::Tool::new_async(
    "fetch",
    "GET a URL and return its length",
    phi::Schema::object().property("url", phi::Schema::string()),
    |args| async move {
        // args is owned (Vec<u8>), so `async move` can capture it directly.
        // Network / IO calls (reqwest, tokio, …) go here.
        Ok(phi::ToolResult {
            content: format!("got {} bytes of args", args.len()),
            ..Default::default()
        })
    },
));
```

Sync handlers keep working unchanged via [`phi::Tool::new`] — the two share
one storage type, so a tool can switch to async without touching its schema.

Command handlers get a [`phi::Context`](src/phi.rs) for host interaction:
`notify`, `set_status`, `submit`, `send_user_message`, `confirm`,
`confirm_opts`. Commands themselves stay synchronous: their `Context` reads
nested PXB frames off the same pipe, which only works on the loop thread.

Extensions can register models the host switches to — direct-connect (the host
dials `base_url` itself) or extension-served (each request arrives as a
`ModelInvoke` and your handler streams deltas back):

```rust,no_run
use phi_ext::phi;

m.register_model(phi::Model::direct("gw")
    .api("Anthropic").base_url("https://gateway.example/v1").api_key("k")
    .context_window(200_000));
m.register_model(phi::Model::served("proxy", |req, emit| {
    // Forward req.system_prompt / req.messages / req.think upstream; emit
    // fragments as they arrive. Ok(()) ends with a done frame, Err fails it.
    emit(phi::ModelEvent { content: "hi".into(), ..Default::default() })
})
.context_window(272_000)
.think_enabled(true));
```

Served handlers run on the loop thread — the host's `ModelCancel` is
best-effort and only observed between requests. See
[doc/extensions.md](../../doc/extensions.md) for the full model contract.

Build and install (a `phi.yaml` manifest must live next to the binary):

```bash
cargo build --release --example hello
mkdir -p ~/.phi/extensions/hello
cp target/release/examples/hello phi.yaml ~/.phi/extensions/hello/
```

Reload in the TUI: **Ctrl+K → extensions → reload**.

PXB codec numbers (vs Go / JSON lines) live in the root
[README](../../README.md#extensions). Re-run the probe with
`cargo run --release --example bench`.

## Development

```bash
cargo test            # unit + golden + end-to-end fake-host tests
cargo fmt --check
cargo clippy --all-targets -- -D warnings
```

The run loop is single-threaded; the borrow checker replaces the Go SDK's
mutexes (handlers get `&mut` state and cannot alias the registry).
