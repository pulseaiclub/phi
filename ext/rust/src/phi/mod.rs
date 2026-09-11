//! Author-facing SDK for a Phi PXB extension binary — a Rust port of
//! `ext/go/phi`.
//!
//! Build an [`Extension`], register tools / slash commands / event handlers,
//! then call [`Extension::run`] to speak PXB on stdin/stdout until shutdown:
//!
//! ```no_run
//! use phi_ext::{phi, pxb};
//!
//! fn main() -> Result<(), phi::Error> {
//!     let mut m = phi::Extension::new("hello", "0.1.0");
//!     m.register_command("hello", phi::Command::new("Say hi", |_args, ctx| {
//!         ctx.notify("info", "Hello!");
//!         Ok(())
//!     }));
//!     m.subscribe(pxb::Event::SessionStart, |_ev| {});
//!     m.run()
//! }
//! ```
//!
//! The run loop is single-threaded. Command handlers receive a [`Context`]
//! whose methods (`notify`, `confirm`, `submit`, …) forward to the host over
//! the same pipe — the borrow checker enforces at compile time what the Go
//! SDK's mutexes enforce at runtime. Tool `execute` handlers may be async
//! ([`Tool::new_async`]); the SDK drives them to completion on a
//! single-threaded tokio runtime, so network / IO calls just work.

use crate::pxb;
use std::collections::HashMap;
use std::future::Future;
use std::io;
use std::pin::Pin;

pub use crate::pxb::Error;

mod schema;
pub use schema::Schema;

type Rd = io::StdinLock<'static>;
type Wr = io::StdoutLock<'static>;
type EventHandlers = HashMap<pxb::Event, Box<dyn FnMut(pxb::EventNotify)>>;

/// Host metadata filled by the hello handshake (and refreshed by
/// `SessionMeta` pushes).
#[derive(Debug, Clone, Default)]
pub struct HostInfo {
    pub cwd: String,
    pub session_id: String,
    pub extension_dir: String,
    pub phi_version: String,
}

/// An LLM-callable tool. `schema` is a typed JSON Schema for parameters
/// (same role as Go's `Parameters` / Codex's schemars-generated input schema).
/// The `execute` handler returns a boxed future so sync ([`Tool::new`]) and
/// async ([`Tool::new_async`]) handlers share one storage type; it is run to
/// completion on a single-threaded tokio runtime, blocking the PXB loop the
/// same way a sync handler does (the host waits for the result anyway).
#[allow(clippy::type_complexity)] // execute / detail signatures mirror the Go SDK
pub struct Tool {
    pub name: String,
    pub description: String,
    pub schema: Schema,
    /// Side-effect-free: the host may run a batch of Readable calls
    /// concurrently. Set with [`Tool::readable`].
    pub readable: bool,
    /// Host RPC wait for `execute`, in seconds. `0` = host default (30s).
    pub timeout_sec: u32,
    /// Optional one-line TUI detail from raw JSON args (before execute).
    pub detail_from_args: Option<Box<dyn FnMut(&[u8]) -> String>>,
    /// Tool handler: takes raw JSON args, returns a future yielding the
    /// result. Not `Send` — the run loop is single-threaded, so handlers may
    /// capture non-`Send` state.
    pub execute: Box<dyn FnMut(&[u8]) -> Pin<Box<dyn Future<Output = Result<ToolResult, String>>>>>,
}

impl Tool {
    pub fn new(
        name: impl Into<String>,
        description: impl Into<String>,
        schema: impl Into<Schema>,
        mut execute: impl FnMut(&[u8]) -> Result<ToolResult, String> + 'static,
    ) -> Self {
        Self {
            name: name.into(),
            description: description.into(),
            schema: schema.into(),
            timeout_sec: 0,
            detail_from_args: None,
            readable: false,
            // Sync handler: call it eagerly, hand the host a ready future.
            execute: Box::new(move |args| {
                let result = execute(args);
                Box::pin(async move { result })
            }),
        }
    }

    /// Builds a tool with an async handler (network / IO friendly). The
    /// closure returns a future that the SDK drives to completion on its
    /// single-threaded runtime when the host invokes the tool. Args are
    /// owned (`Vec<u8>`) so `|args| async move { … }` can capture them
    /// directly in a `'static` future.
    pub fn new_async<F, Fut>(
        name: impl Into<String>,
        description: impl Into<String>,
        schema: impl Into<Schema>,
        mut execute: F,
    ) -> Self
    where
        F: FnMut(Vec<u8>) -> Fut + 'static,
        Fut: Future<Output = Result<ToolResult, String>> + 'static,
    {
        Self {
            name: name.into(),
            description: description.into(),
            schema: schema.into(),
            timeout_sec: 0,
            readable: false,
            detail_from_args: None,
            execute: Box::new(move |args| Box::pin(execute(args.to_vec()))),
        }
    }

    /// Sets how long the host waits for this tool's result (1–3600; host clamps).
    pub fn timeout_sec(mut self, secs: u32) -> Self {
        self.timeout_sec = secs;
        self
    }

    /// Sets a one-line TUI detail formatter for raw JSON arguments.
    pub fn detail_from_args(mut self, f: impl FnMut(&[u8]) -> String + 'static) -> Self {
        self.detail_from_args = Some(Box::new(f));
        self
    }

    /// Marks this tool side-effect-free so the host may run a batch of
    /// Readable calls concurrently.
    pub fn readable(mut self) -> Self {
        self.readable = true;
        self
    }
}

/// Outcome of a [`Tool`] execution.
#[derive(Debug, Clone, Default)]
pub struct ToolResult {
    pub content: String,
    pub detail: String,
    pub output: String,
    /// Ask the TUI to start the tool row open (user toggle still wins).
    pub expanded: bool,
}

/// A slash command. The handler receives the raw argument string and a
/// [`Context`] for host interaction (notify / confirm / submit / …).
#[allow(clippy::type_complexity)] // handler signature mirrors the Go SDK contract
pub struct Command {
    pub description: String,
    /// Leave `/name ` in the composer on picker accept / bare submit.
    pub needs_args: bool,
    pub handler: Box<dyn FnMut(&str, &mut Context<'_>) -> Result<(), String>>,
}

impl Command {
    pub fn new(
        description: impl Into<String>,
        handler: impl FnMut(&str, &mut Context<'_>) -> Result<(), String> + 'static,
    ) -> Self {
        Self {
            description: description.into(),
            needs_args: false,
            handler: Box::new(handler),
        }
    }

    /// Mark this command as requiring arguments so the host fills the
    /// composer instead of auto-submitting an empty invocation.
    pub fn needs_args(mut self) -> Self {
        self.needs_args = true;
        self
    }
}

/// Modal yes/no dialog shown by the host.
#[derive(Debug, Clone, Default, serde::Serialize)]
#[serde(rename_all = "PascalCase")] // host unmarshals into Go's ext.ConfirmRequest
pub struct ConfirmRequest {
    pub title: String,
    pub message: String,
    pub yes: String, // host default: "Yes"
    pub no: String,  // host default: "No"
    pub danger: bool,
}

/// The user's choice for a [`ConfirmRequest`].
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ConfirmReply {
    pub ok: bool,
}

#[derive(Debug, Clone, Default)]
pub struct ToolCallEvent {
    pub tool_name: String,
    pub tool_call_id: String,
    pub input: Vec<u8>,
}

#[derive(Debug, Clone, Default)]
pub struct ToolCallResult {
    pub block: bool,
    pub reason: String,
    /// `Some` rewrites the tool input; `None` keeps it.
    pub input: Option<Vec<u8>>,
    pub context: String,
}

#[derive(Debug, Clone, Default)]
pub struct ToolResultEvent {
    pub tool_name: String,
    pub tool_call_id: String,
    pub input: Vec<u8>,
    pub content: String,
    pub is_error: bool,
    pub err: String,
}

#[derive(Debug, Clone, Default)]
pub struct ToolResultResult {
    /// `Some` rewrites the tool output; `None` keeps it.
    pub content: Option<String>,
    pub context: String,
    /// Ends the agent loop.
    pub stop: bool,
    pub reason: String,
}

#[derive(Debug, Clone, Default)]
pub struct BeforeAgentStartEvent {
    pub prompt: String,
}

#[derive(Debug, Clone, Default)]
pub struct BeforeAgentStartResult {
    /// `Some` replaces the user prompt.
    pub prompt: Option<String>,
    pub system_prompt_append: String,
}

#[derive(Debug, Clone, Default)]
pub struct SessionBeforeSwitchEvent {
    pub reason: String,
    pub target_session_id: String,
}

#[derive(Debug, Clone, Default)]
pub struct SessionBeforeSwitchResult {
    pub cancel: bool,
    pub reason: String,
    pub toast: String,
}

#[derive(Debug, Clone, Default)]
pub struct UserInputEvent {
    pub text: String,
}

#[derive(Debug, Clone, Default)]
pub struct UserInputResult {
    /// `true` swallows the prompt (no agent loop).
    pub handled: bool,
    /// `Some` replaces the prompt text.
    pub text: Option<String>,
    pub reason: String,
}

#[derive(Debug, Clone, Default)]
pub struct TurnStoppingEvent {
    pub turn_index: u32,
}

#[derive(Debug, Clone, Default)]
pub struct TurnStoppingResult {
    /// Forces another agent step.
    pub continue_: bool,
    /// Injected as a user message when continuing.
    pub message: String,
    pub reason: String,
}

/// Registered intercept / subscribe handlers. Kept as one struct so `run`
/// can destructure the [`Extension`] into independent fields (each handler
/// borrows only what it needs, no interior mutability).
#[derive(Default)]
struct Handlers {
    tool_call: Option<Box<dyn FnMut(ToolCallEvent) -> Option<ToolCallResult>>>,
    tool_result: Option<Box<dyn FnMut(ToolResultEvent) -> Option<ToolResultResult>>>,
    before_agent_start:
        Option<Box<dyn FnMut(BeforeAgentStartEvent) -> Option<BeforeAgentStartResult>>>,
    session_before_switch:
        Option<Box<dyn FnMut(SessionBeforeSwitchEvent) -> Option<SessionBeforeSwitchResult>>>,
    user_input: Option<Box<dyn FnMut(UserInputEvent) -> Option<UserInputResult>>>,
    turn_stopping: Option<Box<dyn FnMut(TurnStoppingEvent) -> Option<TurnStoppingResult>>>,
    events: EventHandlers,
}

/// Bundles all mutable state owned by the run loop, so `serve` and
/// `serve_command` can borrow individual fields without passing 7+
/// separate `&mut` parameters.
struct ServeState<'a> {
    rd: &'a mut Rd,
    wr: &'a mut Wr,
    host: HostInfo,
    tools: Vec<Tool>,
    commands: Vec<(String, Command)>,
    handlers: Handlers,
    pending_submit: Option<String>,
    next_host_id: u32,
    rt: &'a tokio::runtime::Runtime,
}

/// The author-facing registration surface for a PXB extension binary.
pub struct Extension {
    name: String,
    version: String,
    tools: Vec<Tool>,
    commands: Vec<(String, Command)>,
    events: Vec<pxb::Event>,
    intercept: Vec<pxb::Event>,
    handlers: Handlers,
}

impl Extension {
    /// Constructs a module; call [`run`](Self::run) once everything is
    /// registered.
    pub fn new(name: impl Into<String>, version: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            version: version.into(),
            tools: Vec::new(),
            commands: Vec::new(),
            events: Vec::new(),
            intercept: Vec::new(),
            handlers: Handlers::default(),
        }
    }

    /// Adds an LLM-callable tool.
    pub fn register_tool(&mut self, tool: Tool) {
        if !tool.name.is_empty() {
            self.tools.push(tool);
        }
    }

    /// Adds a slash command (cannot override builtins at host level).
    pub fn register_command(&mut self, name: impl Into<String>, cmd: Command) {
        let name = name.into();
        if !name.is_empty() && !self.commands.iter().any(|(n, _)| *n == name) {
            self.commands.push((name, cmd));
        }
    }

    /// Registers a pre-gate tool_call intercept.
    pub fn on_tool_call(
        &mut self,
        f: impl FnMut(ToolCallEvent) -> Option<ToolCallResult> + 'static,
    ) {
        self.handlers.tool_call = Some(Box::new(f));
        push_unique(&mut self.intercept, pxb::Event::ToolCall);
    }

    /// Registers a post-tool tool_result intercept.
    pub fn on_tool_result(
        &mut self,
        f: impl FnMut(ToolResultEvent) -> Option<ToolResultResult> + 'static,
    ) {
        self.handlers.tool_result = Some(Box::new(f));
        push_unique(&mut self.intercept, pxb::Event::ToolResult);
    }

    /// May append system prompt text before the agent loop starts.
    pub fn on_before_agent_start(
        &mut self,
        f: impl FnMut(BeforeAgentStartEvent) -> Option<BeforeAgentStartResult> + 'static,
    ) {
        self.handlers.before_agent_start = Some(Box::new(f));
        push_unique(&mut self.intercept, pxb::Event::BeforeAgentStart);
    }

    /// May cancel a session switch.
    pub fn on_session_before_switch(
        &mut self,
        f: impl FnMut(SessionBeforeSwitchEvent) -> Option<SessionBeforeSwitchResult> + 'static,
    ) {
        self.handlers.session_before_switch = Some(Box::new(f));
        push_unique(&mut self.intercept, pxb::Event::SessionBeforeSwitch);
    }

    /// May transform or swallow the user prompt before the agent loop.
    pub fn on_user_input(
        &mut self,
        f: impl FnMut(UserInputEvent) -> Option<UserInputResult> + 'static,
    ) {
        self.handlers.user_input = Some(Box::new(f));
        push_unique(&mut self.intercept, pxb::Event::UserInput);
    }

    /// May steer another agent step when the model stops with no tools.
    pub fn on_turn_stopping(
        &mut self,
        f: impl FnMut(TurnStoppingEvent) -> Option<TurnStoppingResult> + 'static,
    ) {
        self.handlers.turn_stopping = Some(Box::new(f));
        push_unique(&mut self.intercept, pxb::Event::TurnStopping);
    }

    /// Adds a fire-and-forget lifecycle listener; the payload is the wire
    /// `EventNotify`. Unknown events are ignored.
    pub fn subscribe(&mut self, event: pxb::Event, f: impl FnMut(pxb::EventNotify) + 'static) {
        if event == pxb::Event::Unknown(0) {
            return;
        }
        push_unique(&mut self.events, event);
        self.handlers.events.insert(event, Box::new(f));
    }

    /// Speaks PXB on stdin/stdout until the host shuts down.
    pub fn run(self) -> Result<(), Error> {
        let stdin = io::stdin();
        let stdout = io::stdout();
        let mut rd = stdin.lock();
        let mut wr = stdout.lock();

        // One single-threaded runtime drives every async tool handler; it
        // blocks the read loop exactly like a sync handler would.
        let rt = tokio::runtime::Builder::new_current_thread().build()?;

        let host = handshake(&mut rd, &mut wr, &self)?;
        register(&mut wr, &self)?;

        let Extension {
            tools,
            commands,
            handlers,
            ..
        } = self;
        let mut state = ServeState {
            rd: &mut rd,
            wr: &mut wr,
            host,
            tools,
            commands,
            handlers,
            pending_submit: None,
            next_host_id: 0,
            rt: &rt,
        };
        serve(&mut state)
    }
}

/// Exchanges the HELLO handshake and fills [`HostInfo`] from the host's ack.
fn handshake(rd: &mut Rd, wr: &mut Wr, ext: &Extension) -> Result<HostInfo, Error> {
    let mut caps = 0u32;
    if !ext.commands.is_empty() {
        caps |= pxb::CAP_COMMANDS;
    }
    if !ext.tools.is_empty() {
        caps |= pxb::CAP_TOOLS;
    }
    if !ext.events.is_empty() {
        caps |= pxb::CAP_EVENTS;
    }
    if !ext.intercept.is_empty() {
        caps |= pxb::CAP_INTERCEPT;
    }

    let hello = pxb::encode_hello(&pxb::Hello {
        name: ext.name.clone(),
        version: ext.version.clone(),
        caps,
        protocol: pxb::PROTOCOL_VERSION,
    });
    pxb::write_frame(wr, pxb::TYPE_HELLO, 0, 0, &hello)?;

    let f = pxb::read_frame(rd)?;
    if f.header.typ != pxb::TYPE_HELLO_ACK {
        return Err(Error::UnexpectedFrame {
            want: "hello_ack",
            got: f.header.typ,
        });
    }
    let ack = pxb::decode_hello_ack(&f.body)?;
    Ok(HostInfo {
        cwd: ack.cwd,
        session_id: ack.session_id,
        extension_dir: ack.extension_dir,
        phi_version: ack.phi_version,
    })
}

/// Announces tools, commands, and subscription interest, then signals READY.
fn register(wr: &mut Wr, ext: &Extension) -> Result<(), Error> {
    for tool in &ext.tools {
        let body = pxb::encode_register_tool(&pxb::RegisterTool {
            name: tool.name.clone(),
            description: tool.description.clone(),
            schema_json: tool.schema.to_json_bytes(),
            timeout_sec: tool.timeout_sec,
            has_detail: tool.detail_from_args.is_some(),
            readable: tool.readable,
        });
        pxb::write_frame(wr, pxb::TYPE_REGISTER_TOOL, 0, 0, &body)?;
    }
    for (name, cmd) in &ext.commands {
        let body = pxb::encode_register_command(&pxb::RegisterCommand {
            name: name.clone(),
            description: cmd.description.clone(),
            needs_args: cmd.needs_args,
        });
        pxb::write_frame(wr, pxb::TYPE_REGISTER_COMMAND, 0, 0, &body)?;
    }
    if !ext.events.is_empty() || !ext.intercept.is_empty() {
        let body = pxb::encode_subscribe(&pxb::Subscribe {
            events: ext.events.iter().map(|e| e.code()).collect(),
            intercept: ext.intercept.iter().map(|e| e.code()).collect(),
        });
        pxb::write_frame(wr, pxb::TYPE_SUBSCRIBE, 0, 0, &body)?;
    }
    pxb::write_frame(wr, pxb::TYPE_READY, 0, 0, &[])
}

/// Dispatches frames until the host shuts down, handing each frame type to a
/// focused handler that borrows only the state it mutates.
fn serve(state: &mut ServeState<'_>) -> Result<(), Error> {
    loop {
        let f = pxb::read_frame(state.rd)?;
        match pxb::FrameType::from_u16(f.header.typ) {
            pxb::FrameType::Shutdown => {
                pxb::write_frame(state.wr, pxb::TYPE_SHUTDOWN_ACK, 0, 0, &[])?;
                return Ok(());
            }
            pxb::FrameType::CommandInvoked => serve_command(state, &f)?,
            pxb::FrameType::ToolInvoke => {
                serve_tool(state.wr, &f, &mut state.tools, state.rt)?
            }
            pxb::FrameType::ToolDetailInvoke => {
                serve_tool_detail(state.wr, &f, &mut state.tools)?
            }
            pxb::FrameType::Intercept => {
                serve_intercept(state.wr, &f, &mut state.handlers)?
            }
            pxb::FrameType::Event => {
                if let Ok(ev) = pxb::decode_event_notify(&f.body) {
                    dispatch_event(&mut state.handlers.events, ev);
                }
            }
            pxb::FrameType::SessionMeta => {
                if let Ok(meta) = pxb::decode_session_meta(&f.body) {
                    apply_session_meta(&mut state.host, meta);
                }
            }
            // Unknown frame types are already consumed by length; ignore.
            _ => {}
        }
    }
}

/// Invokes a registered slash-command handler and replies with its outcome.
/// An unknown command fails with "unknown command".
///
/// Commands stay synchronous: their [`Context`] reads nested PXB frames off
/// the same pipe, which only works on the loop thread. Use async *tool*
/// handlers for IO-heavy work.
fn serve_command(state: &mut ServeState<'_>, frame: &pxb::Frame) -> Result<(), Error> {
    let inv = pxb::decode_command_invoked(&frame.body)?;
    let mut resp = pxb::CommandResponse {
        ok: true,
        ..Default::default()
    };
    if let Some((_, cmd)) = state.commands.iter_mut().find(|(n, _)| *n == inv.name) {
        let mut ctx = Context {
            cwd: state.host.cwd.clone(),
            session_id: state.host.session_id.clone(),
            has_ui: true,
            rd: state.rd,
            wr: state.wr,
            host: &mut state.host,
            pending_submit: &mut state.pending_submit,
            next_host_id: &mut state.next_host_id,
            events: &mut state.handlers.events,
        };
        if let Err(e) = (cmd.handler)(&inv.args, &mut ctx) {
            resp.ok = false;
            resp.error = e;
        }
    } else {
        resp.ok = false;
        resp.error = "unknown command".into();
    }
    resp.submit = state.pending_submit.take().unwrap_or_default();
    let body = pxb::encode_command_response(&resp);
    pxb::write_frame(
        state.wr,
        pxb::TYPE_COMMAND_RESPONSE,
        frame.header.flags,
        frame.header.id,
        &body,
    )?;
    Ok(())
}

/// Executes a tool and replies with its result, or an error result when the
/// tool is unknown or its handler failed. Async handlers are driven to
/// completion on the extension's single-threaded runtime, so the loop blocks
/// for the handler the same way it does for a sync one.
fn serve_tool(
    wr: &mut Wr,
    frame: &pxb::Frame,
    tools: &mut [Tool],
    rt: &tokio::runtime::Runtime,
) -> Result<(), Error> {
    let inv = pxb::decode_tool_invoke(&frame.body)?;
    let tr = match tools.iter_mut().find(|t| t.name == inv.name) {
        Some(tool) => match rt.block_on((tool.execute)(&inv.args)) {
            Ok(res) => pxb::ToolResultMsg {
                content: res.content,
                detail: res.detail,
                output: res.output,
                expanded: res.expanded,
                ..Default::default()
            },
            Err(e) => tool_error(e),
        },
        None => tool_error("unknown tool"),
    };
    let body = pxb::encode_tool_result(&tr);
    pxb::write_frame(
        wr,
        pxb::TYPE_TOOL_RESULT,
        frame.header.flags,
        frame.header.id,
        &body,
    )?;
    Ok(())
}

/// Returns a one-line TUI detail for raw tool args (or empty when unset/unknown).
fn serve_tool_detail(wr: &mut Wr, frame: &pxb::Frame, tools: &mut [Tool]) -> Result<(), Error> {
    let inv = pxb::decode_tool_invoke(&frame.body)?;
    let detail = tools
        .iter_mut()
        .find(|t| t.name == inv.name)
        .and_then(|t| t.detail_from_args.as_mut())
        .map(|f| f(&inv.args))
        .unwrap_or_default();
    let body = pxb::encode_tool_detail_result(&pxb::ToolDetailResult { detail });
    pxb::write_frame(
        wr,
        pxb::TYPE_TOOL_DETAIL_RESULT,
        frame.header.flags,
        frame.header.id,
        &body,
    )?;
    Ok(())
}

/// Replies to one intercept request with the registered handler's result.
fn serve_intercept(wr: &mut Wr, frame: &pxb::Frame, handlers: &mut Handlers) -> Result<(), Error> {
    let req = pxb::decode_intercept_req(&frame.body)?;
    let resp = handle_intercept(req, handlers);
    let body = pxb::encode_intercept_resp(&resp);
    pxb::write_frame(
        wr,
        pxb::TYPE_INTERCEPT_RESPONSE,
        frame.header.flags,
        frame.header.id,
        &body,
    )?;
    Ok(())
}

/// Dispatches one intercept request to the registered handler. A missing
/// handler (or a handler returning `None`) yields an empty response — the
/// host treats that as "no change".
fn handle_intercept(req: pxb::InterceptReq, handlers: &mut Handlers) -> pxb::InterceptResp {
    let mut resp = pxb::InterceptResp::default();
    match pxb::Event::from_code(req.event) {
        pxb::Event::ToolCall => {
            let Some(f) = handlers.tool_call.as_mut() else {
                return resp;
            };
            let Some(r) = f(ToolCallEvent {
                tool_name: req.tool_name,
                tool_call_id: req.tool_call_id,
                input: req.input,
            }) else {
                return resp;
            };
            resp.block = r.block;
            resp.reason = r.reason;
            resp.context = r.context;
            if let Some(v) = r.input {
                resp.input = v;
            }
        }
        pxb::Event::ToolResult => {
            let Some(f) = handlers.tool_result.as_mut() else {
                return resp;
            };
            let Some(r) = f(ToolResultEvent {
                tool_name: req.tool_name,
                tool_call_id: req.tool_call_id,
                input: req.input,
                content: req.content,
                is_error: req.is_error,
                err: req.err_text,
            }) else {
                return resp;
            };
            resp.context = r.context;
            resp.stop = r.stop;
            resp.reason = r.reason;
            if let Some(v) = r.content {
                resp.content = v;
            }
        }
        pxb::Event::BeforeAgentStart => {
            let Some(f) = handlers.before_agent_start.as_mut() else {
                return resp;
            };
            let Some(r) = f(BeforeAgentStartEvent { prompt: req.prompt }) else {
                return resp;
            };
            resp.system_prompt_append = r.system_prompt_append;
            if let Some(v) = r.prompt {
                resp.prompt = v;
            }
        }
        pxb::Event::SessionBeforeSwitch => {
            let Some(f) = handlers.session_before_switch.as_mut() else {
                return resp;
            };
            let Some(r) = f(SessionBeforeSwitchEvent {
                reason: req.reason,
                target_session_id: req.target_id,
            }) else {
                return resp;
            };
            resp.cancel = r.cancel;
            resp.reason = r.reason;
            resp.toast = r.toast;
        }
        pxb::Event::UserInput => {
            let Some(f) = handlers.user_input.as_mut() else {
                return resp;
            };
            let Some(r) = f(UserInputEvent { text: req.prompt }) else {
                return resp;
            };
            resp.handled = r.handled;
            resp.reason = r.reason;
            if let Some(v) = r.text {
                resp.prompt = v;
            }
        }
        pxb::Event::TurnStopping => {
            let Some(f) = handlers.turn_stopping.as_mut() else {
                return resp;
            };
            let Some(r) = f(TurnStoppingEvent {
                turn_index: req.turn_index,
            }) else {
                return resp;
            };
            resp.continue_ = r.continue_;
            resp.prompt = r.message;
            resp.reason = r.reason;
        }
        _ => {}
    }
    resp
}

/// Applies a session-meta push to host info; empty fields mean "no change".
fn apply_session_meta(host: &mut HostInfo, meta: pxb::SessionMeta) {
    if !meta.session_id.is_empty() {
        host.session_id = meta.session_id;
    }
    if !meta.cwd.is_empty() {
        host.cwd = meta.cwd;
    }
}

/// Dispatches an event push to its subscriber, if one is registered.
fn dispatch_event(handlers: &mut EventHandlers, ev: pxb::EventNotify) {
    let event = pxb::Event::from_code(ev.event);
    if let Some(handler) = handlers.get_mut(&event) {
        handler(ev);
    }
}

/// An error tool result: the message goes to both `error` and `content` so
/// the host surfaces it whichever field it renders.
fn tool_error(message: impl Into<String>) -> pxb::ToolResultMsg {
    let message = message.into();
    pxb::ToolResultMsg {
        is_error: true,
        error: message.clone(),
        content: message,
        ..Default::default()
    }
}

/// Interaction surface handed to command handlers. All host traffic goes
/// over the same PXB pipe the run loop owns — a handler may block the loop
/// (e.g. [`confirm`](Context::confirm) reads nested frames).
pub struct Context<'a> {
    pub cwd: String,
    pub session_id: String,
    pub has_ui: bool,
    rd: &'a mut Rd,
    wr: &'a mut Wr,
    host: &'a mut HostInfo,
    pending_submit: &'a mut Option<String>,
    next_host_id: &'a mut u32,
    events: &'a mut EventHandlers,
}

impl Context<'_> {
    /// The working directory reported by the host.
    pub fn cwd(&self) -> &str {
        &self.cwd
    }

    /// The current session identifier.
    pub fn session_id(&self) -> &str {
        &self.session_id
    }

    /// Whether the host has a UI available.
    pub fn has_ui(&self) -> bool {
        self.has_ui
    }

    /// Pushes a toast to the host (`level`: `info` | `warning` | `error`).
    pub fn notify(&mut self, level: &str, message: &str) {
        let body = pxb::encode_notify(&pxb::NotifyMsg {
            level: level.into(),
            message: message.into(),
            ..Default::default()
        });
        let _ = pxb::write_frame(self.wr, pxb::TYPE_NOTIFY, 0, 0, &body);
    }

    /// Updates the host footer extension status (empty text clears).
    pub fn set_status(&mut self, text: &str) {
        let body = pxb::encode_notify(&pxb::NotifyMsg {
            status: text.into(),
            status_set: true,
            ..Default::default()
        });
        let _ = pxb::write_frame(self.wr, pxb::TYPE_NOTIFY, 0, 0, &body);
    }

    /// Queues a prompt for the host to send after the current slash command
    /// returns.
    pub fn submit(&mut self, text: &str) {
        *self.pending_submit = Some(text.to_string());
    }

    /// Asks the host to enqueue a user turn (fire-and-forget). Safe to call
    /// from command handlers on the PXB read loop.
    pub fn send_user_message(&mut self, text: &str) {
        if text.is_empty() {
            return;
        }
        let body = pxb::encode_host_request(&pxb::HostRequest {
            method: "send_user_message".into(),
            arg: text.into(),
        });
        let _ = pxb::write_frame(self.wr, pxb::TYPE_HOST_REQUEST, 0, 0, &body);
    }

    /// Shows a yes/no dialog on the host and waits for the answer.
    pub fn confirm(&mut self, title: &str, message: &str) -> ConfirmReply {
        self.confirm_opts(ConfirmRequest {
            title: title.into(),
            message: message.into(),
            ..Default::default()
        })
    }

    /// [`confirm`](Self::confirm) with labels / danger styling.
    pub fn confirm_opts(&mut self, req: ConfirmRequest) -> ConfirmReply {
        let Some(id) = self.send_host_request("confirm", &confirm_request_json(&req)) else {
            return ConfirmReply::default();
        };
        // Nested read: keep servicing SessionMeta pushes and subscribed
        // events while waiting for the HostResult that matches our id.
        loop {
            let Ok(f) = pxb::read_frame(self.rd) else {
                return ConfirmReply::default();
            };
            if let Some(reply) = self.nested_reply(f, id) {
                return reply;
            }
        }
    }

    /// Writes a host request carrying a fresh id; returns that id. `None`
    /// means the write failed — treat the host as gone.
    fn send_host_request(&mut self, method: &str, arg: &str) -> Option<u32> {
        *self.next_host_id = self.next_host_id.wrapping_add(1);
        let id = *self.next_host_id;
        let body = pxb::encode_host_request(&pxb::HostRequest {
            method: method.into(),
            arg: arg.into(),
        });
        if pxb::write_frame(self.wr, pxb::TYPE_HOST_REQUEST, pxb::FLAG_HAS_ID, id, &body).is_err() {
            return None;
        }
        Some(id)
    }

    /// Handles one frame read while waiting on a host result. Returns the
    /// [`ConfirmReply`] that ends the wait (a matching HostResult, Shutdown,
    /// or a broken pipe); `None` means the frame was consumed internally.
    fn nested_reply(&mut self, f: pxb::Frame, want_id: u32) -> Option<ConfirmReply> {
        match pxb::FrameType::from_u16(f.header.typ) {
            pxb::FrameType::HostResult => {
                if f.header.flags & pxb::FLAG_HAS_ID == 0 || f.header.id != want_id {
                    return None;
                }
                let Ok(res) = pxb::decode_host_result(&f.body) else {
                    return Some(ConfirmReply::default());
                };
                Some(ConfirmReply { ok: res.ok })
            }
            pxb::FrameType::SessionMeta => {
                if let Ok(meta) = pxb::decode_session_meta(&f.body) {
                    apply_session_meta(self.host, meta);
                }
                None
            }
            pxb::FrameType::Event => {
                if let Ok(ev) = pxb::decode_event_notify(&f.body) {
                    dispatch_event(self.events, ev);
                }
                None
            }
            pxb::FrameType::Shutdown => {
                let _ = pxb::write_frame(self.wr, pxb::TYPE_SHUTDOWN_ACK, 0, 0, &[]);
                Some(ConfirmReply::default())
            }
            _ => None,
        }
    }
}

/// Serializes a [`ConfirmRequest`] as the JSON the host parses. The host
/// unmarshals into Go's `ext.ConfirmRequest` (fields `Title`/`Message`/
/// `Yes`/`No`/`Danger`); `serde`'s `rename_all = "PascalCase"` pins the key
/// names to that contract, and `serde_json` handles escaping.
fn confirm_request_json(req: &ConfirmRequest) -> String {
    serde_json::to_string(req)
        .expect("ConfirmRequest holds only strings/bool; serialization cannot fail")
}

fn push_unique(xs: &mut Vec<pxb::Event>, v: pxb::Event) {
    if !xs.contains(&v) {
        xs.push(v);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn confirm_json_matches_go_field_names() {
        let req = ConfirmRequest {
            title: "Delete?".into(),
            message: "Remove /tmp/x".into(),
            yes: "Delete".into(),
            no: "Cancel".into(),
            danger: true,
        };
        assert_eq!(
            confirm_request_json(&req),
            r#"{"Title":"Delete?","Message":"Remove /tmp/x","Yes":"Delete","No":"Cancel","Danger":true}"#
        );
    }

    #[test]
    fn confirm_json_escapes_quotes_and_controls() {
        let req = ConfirmRequest {
            title: "say \"hi\"\n".into(),
            ..Default::default()
        };
        assert_eq!(
            confirm_request_json(&req),
            r#"{"Title":"say \"hi\"\n","Message":"","Yes":"","No":"","Danger":false}"#
        );
    }

    #[test]
    fn push_unique_keeps_first() {
        let mut xs = Vec::new();
        push_unique(&mut xs, pxb::Event::ToolCall);
        push_unique(&mut xs, pxb::Event::ToolCall);
        push_unique(&mut xs, pxb::Event::AgentEnd);
        assert_eq!(xs, vec![pxb::Event::ToolCall, pxb::Event::AgentEnd]);
    }
}
