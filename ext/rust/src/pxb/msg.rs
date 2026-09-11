//! Typed message payloads, encoded as tagged fields (see `fields.rs`).
//!
//! Every struct mirrors `ext/go/pxb` one-to-one so the two SDKs interop at
//! the byte level. Encode field order matches Go's writers — golden tests in
//! `tests/pxb_test.rs` pin the bytes against `ext/go/pxb/testdata/*.bin`.

use super::codec::Error;
use super::fields::{walk_fields, FieldReader, FieldWriter, WIRE_BYTES, WIRE_U64};

// Field tags, namespaced per message type. Ranges: 1–63 message-defined,
// 64–127 reserved, 128+ experimental (must remain skippable).
const F_HELLO_NAME: u16 = 1;
const F_HELLO_VERSION: u16 = 2;
const F_HELLO_CAPS: u16 = 3;
const F_HELLO_PROTOCOL: u16 = 4;

const F_ACK_PROTOCOL: u16 = 1;
const F_ACK_PHI_VERSION: u16 = 2;
const F_ACK_CWD: u16 = 3;
const F_ACK_SESSION_ID: u16 = 4;
const F_ACK_EXT_DIR: u16 = 5;

const F_REG_CMD_NAME: u16 = 1;
const F_REG_CMD_DESC: u16 = 2;
const F_REG_CMD_NEEDS_ARGS: u16 = 3;

const F_REG_TOOL_NAME: u16 = 1;
const F_REG_TOOL_DESC: u16 = 2;
const F_REG_TOOL_SCHEMA: u16 = 3;
const F_REG_TOOL_TIMEOUT_SEC: u16 = 4;
const F_REG_TOOL_HAS_DETAIL: u16 = 5;
const F_REG_TOOL_READABLE: u16 = 6;

const F_SUB_EVENTS: u16 = 1;
const F_SUB_INTERCEPT: u16 = 2;

const F_TOOL_DETAIL_RESULT: u16 = 1;

const F_CMD_INV_NAME: u16 = 1;
const F_CMD_INV_ARGS: u16 = 2;

const F_CMD_RES_OK: u16 = 1;
const F_CMD_RES_ERROR: u16 = 2;
const F_CMD_RES_NOTIFY: u16 = 3;
const F_CMD_RES_SUBMIT: u16 = 4;

const F_TOOL_INV_NAME: u16 = 1;
const F_TOOL_INV_ARGS: u16 = 2;

const F_TOOL_RES_CONTENT: u16 = 1;
const F_TOOL_RES_DETAIL: u16 = 2;
const F_TOOL_RES_OUTPUT: u16 = 3;
const F_TOOL_RES_IS_ERROR: u16 = 4;
const F_TOOL_RES_ERROR: u16 = 5;
const F_TOOL_RES_EXPANDED: u16 = 6; // TUI tool row starts expanded

const F_IX_REQ_EVENT: u16 = 1;
const F_IX_REQ_TOOL_NAME: u16 = 2;
const F_IX_REQ_TOOL_CALL_ID: u16 = 3;
const F_IX_REQ_INPUT: u16 = 4;
const F_IX_REQ_CONTENT: u16 = 5;
const F_IX_REQ_IS_ERROR: u16 = 6;
const F_IX_REQ_ERR_TEXT: u16 = 7;
const F_IX_REQ_PROMPT: u16 = 8;
const F_IX_REQ_REASON: u16 = 9;
const F_IX_REQ_TARGET_ID: u16 = 10;
const F_IX_REQ_TURN_INDEX: u16 = 11;

const F_IX_RES_BLOCK: u16 = 1;
const F_IX_RES_STOP: u16 = 2;
const F_IX_RES_CANCEL: u16 = 3;
const F_IX_RES_REASON: u16 = 4;
const F_IX_RES_INPUT: u16 = 5;
const F_IX_RES_CONTENT: u16 = 6;
const F_IX_RES_CONTEXT: u16 = 7;
const F_IX_RES_SYS_APPEND: u16 = 8;
const F_IX_RES_TOAST: u16 = 9;
const F_IX_RES_HANDLED: u16 = 10;
const F_IX_RES_PROMPT: u16 = 11;
const F_IX_RES_CONTINUE: u16 = 12;

const F_EV_EVENT: u16 = 1;
const F_EV_TOOL_NAME: u16 = 2;
const F_EV_TOOL_CALL_ID: u16 = 3;
const F_EV_INPUT: u16 = 4;
const F_EV_IS_ERROR: u16 = 5;
const F_EV_PROMPT: u16 = 6;
const F_EV_REASON: u16 = 7;
const F_EV_TURN_INDEX: u16 = 8;
const F_EV_SESSION_ID: u16 = 9;
const F_EV_PREVIOUS_SESSION_ID: u16 = 10;
const F_EV_TARGET_SESSION_ID: u16 = 11;

const F_NOTIFY_LEVEL: u16 = 1;
const F_NOTIFY_MESSAGE: u16 = 2;
const F_NOTIFY_STATUS: u16 = 3;
const F_NOTIFY_STATUS_SET: u16 = 4;

const F_HOST_REQ_METHOD: u16 = 1;
const F_HOST_REQ_ARG: u16 = 2;

const F_HOST_RES_OK: u16 = 1;
const F_HOST_RES_ERROR: u16 = 2;
const F_HOST_RES_BODY: u16 = 3;

const F_META_SESSION_ID: u16 = 1;
const F_META_CWD: u16 = 2;

fn take_u64(kind: u8, fr: &mut FieldReader<'_>) -> Result<u64, Error> {
    if kind != WIRE_U64 {
        fr.skip(kind)?;
        return Err(Error::BadWire);
    }
    fr.u64()
}

fn take_bytes<'a>(kind: u8, fr: &mut FieldReader<'a>) -> Result<&'a [u8], Error> {
    if kind != WIRE_BYTES {
        fr.skip(kind)?;
        return Err(Error::BadWire);
    }
    fr.bytes()
}

/// Wire strings are byte blobs; text fields surface as UTF-8 with invalid
/// sequences replaced (Go keeps raw bytes, which is unusable for `String`).
fn take_string(kind: u8, fr: &mut FieldReader<'_>) -> Result<String, Error> {
    Ok(String::from_utf8_lossy(take_bytes(kind, fr)?).into_owned())
}

/// The first frame from an extension.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Hello {
    pub name: String,
    pub version: String,
    pub caps: u32,
    pub protocol: u16,
}

pub fn encode_hello(h: &Hello) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_HELLO_NAME, &h.name);
    fw.put_string(F_HELLO_VERSION, &h.version);
    fw.put_u32(F_HELLO_CAPS, h.caps);
    fw.put_u16(F_HELLO_PROTOCOL, h.protocol);
    fw.into_vec()
}

pub fn decode_hello(b: &[u8]) -> Result<Hello, Error> {
    let mut h = Hello::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_HELLO_NAME => h.name = take_string(kind, fr)?,
            F_HELLO_VERSION => h.version = take_string(kind, fr)?,
            F_HELLO_CAPS => h.caps = take_u64(kind, fr)? as u32,
            F_HELLO_PROTOCOL => h.protocol = take_u64(kind, fr)? as u16,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(h)
}

/// The host reply to `Hello`.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct HelloAck {
    pub protocol: u16,
    pub phi_version: String,
    pub cwd: String,
    pub session_id: String,
    pub extension_dir: String,
}

pub fn encode_hello_ack(h: &HelloAck) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_u16(F_ACK_PROTOCOL, h.protocol);
    fw.put_string(F_ACK_PHI_VERSION, &h.phi_version);
    fw.put_string(F_ACK_CWD, &h.cwd);
    fw.put_string(F_ACK_SESSION_ID, &h.session_id);
    fw.put_string(F_ACK_EXT_DIR, &h.extension_dir);
    fw.into_vec()
}

pub fn decode_hello_ack(b: &[u8]) -> Result<HelloAck, Error> {
    let mut h = HelloAck::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_ACK_PROTOCOL => h.protocol = take_u64(kind, fr)? as u16,
            F_ACK_PHI_VERSION => h.phi_version = take_string(kind, fr)?,
            F_ACK_CWD => h.cwd = take_string(kind, fr)?,
            F_ACK_SESSION_ID => h.session_id = take_string(kind, fr)?,
            F_ACK_EXT_DIR => h.extension_dir = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(h)
}

/// Registers a slash command.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct RegisterCommand {
    pub name: String,
    pub description: String,
    /// Host leaves `/name ` in the composer on picker accept / bare submit.
    /// `false` omits the wire field (backward compatible).
    pub needs_args: bool,
}

pub fn encode_register_command(r: &RegisterCommand) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_REG_CMD_NAME, &r.name);
    fw.put_string(F_REG_CMD_DESC, &r.description);
    if r.needs_args {
        fw.put_bool(F_REG_CMD_NEEDS_ARGS, true);
    }
    fw.into_vec()
}

pub fn decode_register_command(b: &[u8]) -> Result<RegisterCommand, Error> {
    let mut r = RegisterCommand::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_REG_CMD_NAME => r.name = take_string(kind, fr)?,
            F_REG_CMD_DESC => r.description = take_string(kind, fr)?,
            F_REG_CMD_NEEDS_ARGS => r.needs_args = take_u64(kind, fr)? != 0,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Registers an LLM tool; `schema_json` is opaque JSON Schema bytes.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct RegisterTool {
    pub name: String,
    pub description: String,
    pub schema_json: Vec<u8>,
    /// Host RPC wait for this tool's result, in seconds. `0` omits the field
    /// (host default). Host clamps to a maximum.
    pub timeout_sec: u32,
    /// Extension can answer [`TYPE_TOOL_DETAIL_INVOKE`](crate::pxb::TYPE_TOOL_DETAIL_INVOKE).
    /// `false` omits the wire field (backward compatible).
    pub has_detail: bool,
    /// Side-effect-free; the host may batch read-only calls concurrently.
    /// `false` omits the wire field (old hosts stay sequential).
    pub readable: bool,
}

pub fn encode_register_tool(r: &RegisterTool) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_REG_TOOL_NAME, &r.name);
    fw.put_string(F_REG_TOOL_DESC, &r.description);
    fw.put_bytes(F_REG_TOOL_SCHEMA, &r.schema_json);
    if r.timeout_sec > 0 {
        fw.put_u32(F_REG_TOOL_TIMEOUT_SEC, r.timeout_sec);
    }
    if r.has_detail {
        fw.put_bool(F_REG_TOOL_HAS_DETAIL, true);
    }
    if r.readable {
        fw.put_bool(F_REG_TOOL_READABLE, true);
    }
    fw.into_vec()
}

pub fn decode_register_tool(b: &[u8]) -> Result<RegisterTool, Error> {
    let mut r = RegisterTool::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_REG_TOOL_NAME => r.name = take_string(kind, fr)?,
            F_REG_TOOL_DESC => r.description = take_string(kind, fr)?,
            F_REG_TOOL_SCHEMA => r.schema_json = take_bytes(kind, fr)?.to_vec(),
            F_REG_TOOL_TIMEOUT_SEC => r.timeout_sec = take_u64(kind, fr)? as u32,
            F_REG_TOOL_HAS_DETAIL => r.has_detail = take_u64(kind, fr)? != 0,
            F_REG_TOOL_READABLE => r.readable = take_u64(kind, fr)? != 0,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Ext→host reply for a detail-from-args request (body of tool invoke is reused).
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ToolDetailResult {
    pub detail: String,
}

pub fn encode_tool_detail_result(r: &ToolDetailResult) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_TOOL_DETAIL_RESULT, &r.detail);
    fw.into_vec()
}

pub fn decode_tool_detail_result(b: &[u8]) -> Result<ToolDetailResult, Error> {
    let mut r = ToolDetailResult::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_TOOL_DETAIL_RESULT => r.detail = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Declares event / intercept interests.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Subscribe {
    pub events: Vec<u16>,
    pub intercept: Vec<u16>,
}

pub fn encode_subscribe(s: &Subscribe) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_u16s(F_SUB_EVENTS, &s.events);
    fw.put_u16s(F_SUB_INTERCEPT, &s.intercept);
    fw.into_vec()
}

pub fn decode_subscribe(b: &[u8]) -> Result<Subscribe, Error> {
    let mut s = Subscribe::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_SUB_EVENTS => s.events = decode_u16s(take_bytes(kind, fr)?)?,
            F_SUB_INTERCEPT => s.intercept = decode_u16s(take_bytes(kind, fr)?)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(s)
}

fn decode_u16s(p: &[u8]) -> Result<Vec<u16>, Error> {
    // Inner format is plain ByteReader: u16 count + u16 values (no tags).
    if p.len() < 2 {
        return Err(Error::Truncated);
    }
    let n = u16::from_le_bytes([p[0], p[1]]) as usize;
    let data = p.get(2..2 + n * 2).ok_or(Error::Truncated)?;
    Ok(data
        .chunks_exact(2)
        .map(|c| u16::from_le_bytes([c[0], c[1]]))
        .collect())
}

/// Host→ext when the user runs a slash command.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct CommandInvoked {
    pub name: String,
    pub args: String,
}

pub fn encode_command_invoked(c: &CommandInvoked) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_CMD_INV_NAME, &c.name);
    fw.put_string(F_CMD_INV_ARGS, &c.args);
    fw.into_vec()
}

pub fn decode_command_invoked(b: &[u8]) -> Result<CommandInvoked, Error> {
    let mut c = CommandInvoked::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_CMD_INV_NAME => c.name = take_string(kind, fr)?,
            F_CMD_INV_ARGS => c.args = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(c)
}

/// Ext→host slash command outcome.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct CommandResponse {
    pub ok: bool,
    pub error: String,
    pub notify: String,
    pub submit: String,
}

pub fn encode_command_response(c: &CommandResponse) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_bool(F_CMD_RES_OK, c.ok);
    fw.put_string(F_CMD_RES_ERROR, &c.error);
    fw.put_string(F_CMD_RES_NOTIFY, &c.notify);
    fw.put_string(F_CMD_RES_SUBMIT, &c.submit);
    fw.into_vec()
}

pub fn decode_command_response(b: &[u8]) -> Result<CommandResponse, Error> {
    let mut c = CommandResponse::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_CMD_RES_OK => c.ok = take_u64(kind, fr)? != 0,
            F_CMD_RES_ERROR => c.error = take_string(kind, fr)?,
            F_CMD_RES_NOTIFY => c.notify = take_string(kind, fr)?,
            F_CMD_RES_SUBMIT => c.submit = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(c)
}

/// Host→ext for a registered tool.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ToolInvoke {
    pub name: String,
    pub args: Vec<u8>,
}

pub fn encode_tool_invoke(t: &ToolInvoke) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_TOOL_INV_NAME, &t.name);
    fw.put_bytes(F_TOOL_INV_ARGS, &t.args);
    fw.into_vec()
}

pub fn decode_tool_invoke(b: &[u8]) -> Result<ToolInvoke, Error> {
    let mut t = ToolInvoke::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_TOOL_INV_NAME => t.name = take_string(kind, fr)?,
            F_TOOL_INV_ARGS => t.args = take_bytes(kind, fr)?.to_vec(),
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(t)
}

/// Ext→host tool outcome.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ToolResultMsg {
    pub content: String,
    pub detail: String,
    pub output: String,
    pub is_error: bool,
    pub error: String,
    /// TUI tool row starts expanded (user toggle still wins).
    pub expanded: bool,
}

pub fn encode_tool_result(t: &ToolResultMsg) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_TOOL_RES_CONTENT, &t.content);
    fw.put_string(F_TOOL_RES_DETAIL, &t.detail);
    fw.put_string(F_TOOL_RES_OUTPUT, &t.output);
    fw.put_bool(F_TOOL_RES_IS_ERROR, t.is_error);
    fw.put_string(F_TOOL_RES_ERROR, &t.error);
    if t.expanded {
        fw.put_bool(F_TOOL_RES_EXPANDED, true);
    }
    fw.into_vec()
}

pub fn decode_tool_result(b: &[u8]) -> Result<ToolResultMsg, Error> {
    let mut t = ToolResultMsg::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_TOOL_RES_CONTENT => t.content = take_string(kind, fr)?,
            F_TOOL_RES_DETAIL => t.detail = take_string(kind, fr)?,
            F_TOOL_RES_OUTPUT => t.output = take_string(kind, fr)?,
            F_TOOL_RES_IS_ERROR => t.is_error = take_u64(kind, fr)? != 0,
            F_TOOL_RES_ERROR => t.error = take_string(kind, fr)?,
            F_TOOL_RES_EXPANDED => t.expanded = take_u64(kind, fr)? != 0,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(t)
}

/// Host→ext for a blocking decision point.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct InterceptReq {
    pub event: u16,
    pub tool_name: String,
    pub tool_call_id: String,
    pub input: Vec<u8>,
    pub content: String,
    pub is_error: bool,
    pub err_text: String,
    pub prompt: String,
    pub reason: String,
    pub target_id: String,
    pub turn_index: u32,
}

pub fn encode_intercept_req(r: &InterceptReq) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_u16(F_IX_REQ_EVENT, r.event);
    fw.put_string(F_IX_REQ_TOOL_NAME, &r.tool_name);
    fw.put_string(F_IX_REQ_TOOL_CALL_ID, &r.tool_call_id);
    fw.put_bytes(F_IX_REQ_INPUT, &r.input);
    fw.put_string(F_IX_REQ_CONTENT, &r.content);
    fw.put_bool(F_IX_REQ_IS_ERROR, r.is_error);
    fw.put_string(F_IX_REQ_ERR_TEXT, &r.err_text);
    fw.put_string(F_IX_REQ_PROMPT, &r.prompt);
    fw.put_string(F_IX_REQ_REASON, &r.reason);
    fw.put_string(F_IX_REQ_TARGET_ID, &r.target_id);
    fw.put_u32(F_IX_REQ_TURN_INDEX, r.turn_index);
    fw.into_vec()
}

pub fn decode_intercept_req(b: &[u8]) -> Result<InterceptReq, Error> {
    let mut r = InterceptReq::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_IX_REQ_EVENT => r.event = take_u64(kind, fr)? as u16,
            F_IX_REQ_TOOL_NAME => r.tool_name = take_string(kind, fr)?,
            F_IX_REQ_TOOL_CALL_ID => r.tool_call_id = take_string(kind, fr)?,
            F_IX_REQ_INPUT => r.input = take_bytes(kind, fr)?.to_vec(),
            F_IX_REQ_CONTENT => r.content = take_string(kind, fr)?,
            F_IX_REQ_IS_ERROR => r.is_error = take_u64(kind, fr)? != 0,
            F_IX_REQ_ERR_TEXT => r.err_text = take_string(kind, fr)?,
            F_IX_REQ_PROMPT => r.prompt = take_string(kind, fr)?,
            F_IX_REQ_REASON => r.reason = take_string(kind, fr)?,
            F_IX_REQ_TARGET_ID => r.target_id = take_string(kind, fr)?,
            F_IX_REQ_TURN_INDEX => r.turn_index = take_u64(kind, fr)? as u32,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Ext→host intercept reply.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct InterceptResp {
    pub block: bool,
    pub stop: bool,
    pub cancel: bool,
    pub handled: bool,
    /// Written as `continue_` to dodge the keyword; wire tag is
    /// `F_IX_RES_CONTINUE`.
    pub continue_: bool,
    pub reason: String,
    pub input: Vec<u8>,
    pub content: String,
    pub context: String,
    pub system_prompt_append: String,
    pub toast: String,
    pub prompt: String,
}

pub fn encode_intercept_resp(r: &InterceptResp) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_bool(F_IX_RES_BLOCK, r.block);
    fw.put_bool(F_IX_RES_STOP, r.stop);
    fw.put_bool(F_IX_RES_CANCEL, r.cancel);
    fw.put_string(F_IX_RES_REASON, &r.reason);
    fw.put_bytes(F_IX_RES_INPUT, &r.input);
    fw.put_string(F_IX_RES_CONTENT, &r.content);
    fw.put_string(F_IX_RES_CONTEXT, &r.context);
    fw.put_string(F_IX_RES_SYS_APPEND, &r.system_prompt_append);
    fw.put_string(F_IX_RES_TOAST, &r.toast);
    fw.put_bool(F_IX_RES_HANDLED, r.handled);
    fw.put_string(F_IX_RES_PROMPT, &r.prompt);
    fw.put_bool(F_IX_RES_CONTINUE, r.continue_);
    fw.into_vec()
}

pub fn decode_intercept_resp(b: &[u8]) -> Result<InterceptResp, Error> {
    let mut r = InterceptResp::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_IX_RES_BLOCK => r.block = take_u64(kind, fr)? != 0,
            F_IX_RES_STOP => r.stop = take_u64(kind, fr)? != 0,
            F_IX_RES_CANCEL => r.cancel = take_u64(kind, fr)? != 0,
            F_IX_RES_HANDLED => r.handled = take_u64(kind, fr)? != 0,
            F_IX_RES_CONTINUE => r.continue_ = take_u64(kind, fr)? != 0,
            F_IX_RES_REASON => r.reason = take_string(kind, fr)?,
            F_IX_RES_INPUT => r.input = take_bytes(kind, fr)?.to_vec(),
            F_IX_RES_CONTENT => r.content = take_string(kind, fr)?,
            F_IX_RES_CONTEXT => r.context = take_string(kind, fr)?,
            F_IX_RES_SYS_APPEND => r.system_prompt_append = take_string(kind, fr)?,
            F_IX_RES_TOAST => r.toast = take_string(kind, fr)?,
            F_IX_RES_PROMPT => r.prompt = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Fire-and-forget host→ext lifecycle event.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct EventNotify {
    pub event: u16,
    pub tool_name: String,
    pub tool_call_id: String,
    pub input: Vec<u8>,
    pub is_error: bool,
    pub prompt: String,
    pub reason: String,
    pub turn_index: u32,
    pub session_id: String,
    pub previous_session_id: String,
    pub target_session_id: String,
}

pub fn encode_event_notify(e: &EventNotify) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_u16(F_EV_EVENT, e.event);
    fw.put_string(F_EV_TOOL_NAME, &e.tool_name);
    fw.put_string(F_EV_TOOL_CALL_ID, &e.tool_call_id);
    fw.put_bytes(F_EV_INPUT, &e.input);
    fw.put_bool(F_EV_IS_ERROR, e.is_error);
    fw.put_string(F_EV_PROMPT, &e.prompt);
    fw.put_string(F_EV_REASON, &e.reason);
    fw.put_u32(F_EV_TURN_INDEX, e.turn_index);
    fw.put_string(F_EV_SESSION_ID, &e.session_id);
    fw.put_string(F_EV_PREVIOUS_SESSION_ID, &e.previous_session_id);
    fw.put_string(F_EV_TARGET_SESSION_ID, &e.target_session_id);
    fw.into_vec()
}

pub fn decode_event_notify(b: &[u8]) -> Result<EventNotify, Error> {
    let mut e = EventNotify::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_EV_EVENT => e.event = take_u64(kind, fr)? as u16,
            F_EV_TOOL_NAME => e.tool_name = take_string(kind, fr)?,
            F_EV_TOOL_CALL_ID => e.tool_call_id = take_string(kind, fr)?,
            F_EV_INPUT => e.input = take_bytes(kind, fr)?.to_vec(),
            F_EV_IS_ERROR => e.is_error = take_u64(kind, fr)? != 0,
            F_EV_PROMPT => e.prompt = take_string(kind, fr)?,
            F_EV_REASON => e.reason = take_string(kind, fr)?,
            F_EV_TURN_INDEX => e.turn_index = take_u64(kind, fr)? as u32,
            F_EV_SESSION_ID => e.session_id = take_string(kind, fr)?,
            F_EV_PREVIOUS_SESSION_ID => e.previous_session_id = take_string(kind, fr)?,
            F_EV_TARGET_SESSION_ID => e.target_session_id = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(e)
}

/// Ext→host UI toast / footer status.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct NotifyMsg {
    pub level: String,
    pub message: String,
    pub status: String,
    pub status_set: bool,
}

pub fn encode_notify(n: &NotifyMsg) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_NOTIFY_LEVEL, &n.level);
    fw.put_string(F_NOTIFY_MESSAGE, &n.message);
    fw.put_string(F_NOTIFY_STATUS, &n.status);
    fw.put_bool(F_NOTIFY_STATUS_SET, n.status_set);
    fw.into_vec()
}

pub fn decode_notify(b: &[u8]) -> Result<NotifyMsg, Error> {
    let mut n = NotifyMsg::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_NOTIFY_LEVEL => n.level = take_string(kind, fr)?,
            F_NOTIFY_MESSAGE => n.message = take_string(kind, fr)?,
            F_NOTIFY_STATUS => n.status = take_string(kind, fr)?,
            F_NOTIFY_STATUS_SET => n.status_set = take_u64(kind, fr)? != 0,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(n)
}

/// Ext→host capability RPC.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct HostRequest {
    pub method: String, // send_user_message | confirm
    pub arg: String,
}

pub fn encode_host_request(r: &HostRequest) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_HOST_REQ_METHOD, &r.method);
    fw.put_string(F_HOST_REQ_ARG, &r.arg);
    fw.into_vec()
}

pub fn decode_host_request(b: &[u8]) -> Result<HostRequest, Error> {
    let mut r = HostRequest::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_HOST_REQ_METHOD => r.method = take_string(kind, fr)?,
            F_HOST_REQ_ARG => r.arg = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Host→ext reply to a `HostRequest`.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct HostResult {
    pub ok: bool,
    pub error: String,
    pub body: String,
}

pub fn encode_host_result(r: &HostResult) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_bool(F_HOST_RES_OK, r.ok);
    fw.put_string(F_HOST_RES_ERROR, &r.error);
    fw.put_string(F_HOST_RES_BODY, &r.body);
    fw.into_vec()
}

pub fn decode_host_result(b: &[u8]) -> Result<HostResult, Error> {
    let mut r = HostResult::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_HOST_RES_OK => r.ok = take_u64(kind, fr)? != 0,
            F_HOST_RES_ERROR => r.error = take_string(kind, fr)?,
            F_HOST_RES_BODY => r.body = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(r)
}

/// Host→ext session identity push.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct SessionMeta {
    pub session_id: String,
    pub cwd: String,
}

pub fn encode_session_meta(m: &SessionMeta) -> Vec<u8> {
    let mut fw = FieldWriter::new();
    fw.put_string(F_META_SESSION_ID, &m.session_id);
    fw.put_string(F_META_CWD, &m.cwd);
    fw.into_vec()
}

pub fn decode_session_meta(b: &[u8]) -> Result<SessionMeta, Error> {
    let mut m = SessionMeta::default();
    walk_fields(b, |tag, kind, fr| {
        match tag {
            F_META_SESSION_ID => m.session_id = take_string(kind, fr)?,
            F_META_CWD => m.cwd = take_string(kind, fr)?,
            _ => fr.skip(kind)?,
        }
        Ok(())
    })?;
    Ok(m)
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Round-trip every message through encode/decode and check that the
    /// encode is deterministic (same struct, same bytes twice).
    #[test]
    fn all_messages_roundtrip() {
        type Reencode = fn(&[u8]) -> Result<Vec<u8>, Error>;
        let cases: Vec<(Vec<u8>, Reencode)> = vec![
            (
                encode_hello(&Hello {
                    name: "greet".into(),
                    version: "1.0.0".into(),
                    caps: 3,
                    protocol: 1,
                }),
                |b| Ok(encode_hello(&decode_hello(b)?)),
            ),
            (
                encode_hello_ack(&HelloAck {
                    protocol: 1,
                    phi_version: "v0.19.0".into(),
                    cwd: "/tmp".into(),
                    session_id: "s1".into(),
                    extension_dir: "/ext".into(),
                }),
                |b| Ok(encode_hello_ack(&decode_hello_ack(b)?)),
            ),
            (
                encode_register_command(&RegisterCommand {
                    name: "hi".into(),
                    description: "Say hi".into(),
                    needs_args: true,
                }),
                |b| Ok(encode_register_command(&decode_register_command(b)?)),
            ),
            (
                encode_register_tool(&RegisterTool {
                    name: "t".into(),
                    description: "d".into(),
                    schema_json: br#"{"type":"object"}"#.to_vec(),
                    timeout_sec: 120,
                    has_detail: true,
                    readable: true,
                }),
                |b| Ok(encode_register_tool(&decode_register_tool(b)?)),
            ),
            (
                encode_tool_detail_result(&ToolDetailResult {
                    detail: "path/to/file".into(),
                }),
                |b| Ok(encode_tool_detail_result(&decode_tool_detail_result(b)?)),
            ),
            (
                encode_subscribe(&Subscribe {
                    events: vec![5, 10],
                    intercept: vec![1, 2],
                }),
                |b| Ok(encode_subscribe(&decode_subscribe(b)?)),
            ),
            (
                encode_command_invoked(&CommandInvoked {
                    name: "hi".into(),
                    args: "a b".into(),
                }),
                |b| Ok(encode_command_invoked(&decode_command_invoked(b)?)),
            ),
            (
                encode_command_response(&CommandResponse {
                    ok: false,
                    error: "boom".into(),
                    notify: String::new(),
                    submit: "next".into(),
                }),
                |b| Ok(encode_command_response(&decode_command_response(b)?)),
            ),
            (
                encode_tool_invoke(&ToolInvoke {
                    name: "t".into(),
                    args: br#"{"k":1}"#.to_vec(),
                }),
                |b| Ok(encode_tool_invoke(&decode_tool_invoke(b)?)),
            ),
            (
                encode_tool_result(&ToolResultMsg {
                    content: "c".into(),
                    detail: "d".into(),
                    output: "o".into(),
                    is_error: true,
                    error: "e".into(),
                    expanded: true,
                }),
                |b| Ok(encode_tool_result(&decode_tool_result(b)?)),
            ),
            (
                encode_intercept_req(&InterceptReq {
                    event: 1,
                    tool_name: "bash".into(),
                    tool_call_id: "c1".into(),
                    input: br#"{"command":"ls"}"#.to_vec(),
                    content: "out".into(),
                    is_error: false,
                    err_text: String::new(),
                    prompt: "p".into(),
                    reason: "r".into(),
                    target_id: "t2".into(),
                    turn_index: 3,
                }),
                |b| Ok(encode_intercept_req(&decode_intercept_req(b)?)),
            ),
            (
                encode_intercept_resp(&InterceptResp {
                    block: true,
                    stop: false,
                    cancel: false,
                    handled: true,
                    continue_: true,
                    reason: "r".into(),
                    input: b"in".to_vec(),
                    content: "c".into(),
                    context: "ctx".into(),
                    system_prompt_append: "sys".into(),
                    toast: "t".into(),
                    prompt: "p".into(),
                }),
                |b| Ok(encode_intercept_resp(&decode_intercept_resp(b)?)),
            ),
            (
                encode_event_notify(&EventNotify {
                    event: 5,
                    tool_name: "t".into(),
                    tool_call_id: "c".into(),
                    input: b"i".to_vec(),
                    is_error: true,
                    prompt: "p".into(),
                    reason: "r".into(),
                    turn_index: 2,
                    session_id: "s".into(),
                    previous_session_id: "ps".into(),
                    target_session_id: "ts".into(),
                }),
                |b| Ok(encode_event_notify(&decode_event_notify(b)?)),
            ),
            (
                encode_notify(&NotifyMsg {
                    level: "info".into(),
                    message: "Hello".into(),
                    status: "st".into(),
                    status_set: true,
                }),
                |b| Ok(encode_notify(&decode_notify(b)?)),
            ),
            (
                encode_host_request(&HostRequest {
                    method: "confirm".into(),
                    arg: r#"{"Title":"t"}"#.into(),
                }),
                |b| Ok(encode_host_request(&decode_host_request(b)?)),
            ),
            (
                encode_host_result(&HostResult {
                    ok: true,
                    error: String::new(),
                    body: "b".into(),
                }),
                |b| Ok(encode_host_result(&decode_host_result(b)?)),
            ),
            (
                encode_session_meta(&SessionMeta {
                    session_id: "s2".into(),
                    cwd: "/x".into(),
                }),
                |b| Ok(encode_session_meta(&decode_session_meta(b)?)),
            ),
        ];

        for (bytes, reencode) in cases {
            assert_eq!(reencode(&bytes).unwrap(), bytes);
        }
    }

    #[test]
    fn decode_skips_unknown_tags() {
        let mut w = FieldWriter::new();
        w.put_string(1, "name");
        w.put_string(200, "future field"); // experimental, must be skippable
        w.put_string(2, "1.0.0");
        let h = decode_hello(&w.into_vec()).unwrap();
        assert_eq!(h.name, "name");
        assert_eq!(h.version, "1.0.0");
    }
}
