//! Byte-level compatibility tests against the Go SDK's golden fixtures
//! (`ext/go/pxb/testdata/*.bin`, regenerated with `UPDATE_GOLDEN=1 go test`).
//! Skipped when the repo layout is absent (e.g. crate published standalone).

use std::io::Cursor;
use std::path::PathBuf;

use phi_ext::pxb;

fn testdata(name: &str) -> Option<Vec<u8>> {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../go/pxb/testdata")
        .join(name);
    std::fs::read(path).ok()
}

/// A decode-then-re-encode pass must reproduce the Go bytes exactly.
#[test]
fn golden_hello() {
    let Some(raw) = testdata("hello.bin") else {
        return;
    };
    let h = pxb::Hello::decode(&raw).unwrap();
    assert_eq!(h.name, "greet");
    assert_eq!(h.version, "1.0.0");
    assert_eq!(h.caps, pxb::CAP_TOOLS | pxb::CAP_COMMANDS);
    assert_eq!(h.protocol, 1);
    assert_eq!(h.encode(), raw);
}

#[test]
fn golden_hello_ack() {
    let Some(raw) = testdata("hello_ack.bin") else {
        return;
    };
    let h = pxb::HelloAck::decode(&raw).unwrap();
    assert_eq!(h.protocol, 1);
    assert_eq!(h.phi_version, "v0.19.0");
    assert_eq!(h.cwd, "/tmp");
    assert_eq!(h.session_id, "s1");
    assert_eq!(h.extension_dir, "/ext");
    assert_eq!(h.encode(), raw);
}

#[test]
fn golden_subscribe() {
    let Some(raw) = testdata("subscribe.bin") else {
        return;
    };
    let s = pxb::Subscribe::decode(&raw).unwrap();
    assert_eq!(
        s.events,
        vec![pxb::Event::SessionStart.code(), pxb::Event::AgentEnd.code()]
    );
    assert_eq!(s.intercept, vec![pxb::Event::ToolCall.code()]);
    assert_eq!(s.encode(), raw);
}

#[test]
fn golden_intercept_req() {
    let Some(raw) = testdata("intercept_req.bin") else {
        return;
    };
    let r = pxb::InterceptReq::decode(&raw).unwrap();
    assert_eq!(r.event, pxb::Event::ToolCall.code());
    assert_eq!(r.tool_name, "bash");
    assert_eq!(r.tool_call_id, "c1");
    assert_eq!(r.input, br#"{"command":"ls"}"#);
    assert!(!r.is_error);
    assert_eq!(r.encode(), raw);
}

#[test]
fn golden_register_model() {
    let Some(raw) = testdata("register_model.bin") else {
        return;
    };
    let m = pxb::RegisterModel::decode(&raw).unwrap();
    assert_eq!(m.name, "echo");
    assert_eq!(m.api, "Anthropic");
    assert_eq!(m.base_url, "http://127.0.0.1:9");
    assert_eq!(m.api_key, "k");
    assert_eq!(m.preset, "glm-5.3-flash");
    assert_eq!(m.context_window, 1000);
    assert!(m.image_enabled);
    assert!(m.think_enabled);
    assert_eq!(m.think_level, "high");
    assert_eq!(m.conn, pxb::CONN_EXT);
    assert_eq!(m.encode(), raw);
}

#[test]
fn golden_model_invoke() {
    let Some(raw) = testdata("model_invoke.bin") else {
        return;
    };
    let inv = pxb::ModelInvoke::decode(&raw).unwrap();
    assert_eq!(inv.model, "echo");
    assert_eq!(inv.messages_json, br#"[{"role":"user","content":"hi"}]"#);
    assert_eq!(inv.tools_json, br#"[{"name":"t"}]"#);
    assert_eq!(inv.system_prompt, "sys");
    assert_eq!(inv.think, "high");
    assert_eq!(inv.encode(), raw);
}

#[test]
fn golden_model_stream() {
    let Some(raw) = testdata("model_stream.bin") else {
        return;
    };
    let ev = pxb::ModelStreamEvent::decode(&raw).unwrap();
    assert_eq!(ev.type_, "delta");
    assert_eq!(ev.content, "c");
    assert_eq!(ev.reasoning, "r");
    assert_eq!(ev.tool_calls, br#"[{"index":0,"id":"c1"}]"#);
    assert_eq!(ev.usage_json, br#"{"total_tokens":3}"#);
    assert!(ev.error.is_empty());
    assert_eq!(ev.encode(), raw);
}

/// The framed fixture is a full `TypeHello` frame; the payload must equal
/// `hello.bin` and the header must parse.
#[test]
fn golden_hello_frame() {
    let Some(raw) = testdata("hello_frame.bin") else {
        return;
    };
    let f = pxb::read_frame(&mut Cursor::new(raw)).unwrap();
    assert_eq!(f.header.typ, pxb::TYPE_HELLO);
    assert_eq!(f.header.flags, 0);
    assert_eq!(f.header.id, 0);
    assert_eq!(f.body, testdata("hello.bin").unwrap());
}
