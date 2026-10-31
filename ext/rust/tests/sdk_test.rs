//! End-to-end SDK tests: a fake PXB host drives the example binaries over
//! piped stdin/stdout and asserts the full handshake + RPC lifecycle.

use std::io::{BufReader, BufWriter, Write};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc::{self, Receiver};
use std::time::Duration;

use phi_ext::pxb;

/// A minimal PXB host. Uses the crate's own codec, so byte-level fidelity to
/// the Go host is pinned separately in `tests/pxb_test.rs` (golden fixtures).
struct Host {
    child: Child,
    rd: Receiver<Result<pxb::Frame, pxb::Error>>,
    wr: BufWriter<ChildStdin>,
    /// Registration frames collected by [`handshake`](Self::handshake).
    registrations: Vec<pxb::Frame>,
}

impl Host {
    fn spawn(name: &str) -> Self {
        let mut child = Command::new(example_bin(name))
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .expect("spawn example binary");
        let mut stdout = BufReader::new(child.stdout.take().unwrap());
        let (tx, rd) = mpsc::channel();
        std::thread::spawn(move || loop {
            let frame = pxb::read_frame(&mut stdout);
            let failed = frame.is_err();
            if tx.send(frame).is_err() || failed {
                break;
            }
        });
        let wr = BufWriter::new(child.stdin.take().unwrap());
        Self {
            child,
            rd,
            wr,
            registrations: Vec::new(),
        }
    }

    fn read(&mut self) -> pxb::Frame {
        match self
            .rd
            .recv_timeout(Duration::from_secs(5))
            .expect("PXB response timeout")
        {
            Ok(f) => f,
            Err(e) => panic!("host read failed (extension crashed?): {e}"),
        }
    }

    fn write(&mut self, typ: u16, flags: u16, id: u32, body: &[u8]) {
        pxb::write_frame(&mut self.wr, typ, flags, id, body).unwrap();
        self.wr.flush().unwrap();
    }

    /// Completes the handshake for any extension: read `Hello`, reply
    /// `HelloAck`, then return the registration frames up to (and including)
    /// `Ready`.
    fn handshake(&mut self) -> pxb::Hello {
        let f = self.read();
        assert_eq!(f.header.typ, pxb::TYPE_HELLO);
        let hello = pxb::Hello::decode(&f.body).unwrap();
        self.write(
            pxb::TYPE_HELLO_ACK,
            0,
            0,
            &pxb::HelloAck {
                protocol: pxb::PROTOCOL_VERSION,
                phi_version: "v0.0.0-test".into(),
                cwd: "/tmp".into(),
                session_id: "s1".into(),
                extension_dir: "/ext".into(),
            }
            .encode(),
        );
        loop {
            let f = self.read();
            match pxb::FrameType::from_u16(f.header.typ) {
                pxb::FrameType::RegisterCommand
                | pxb::FrameType::RegisterTool
                | pxb::FrameType::RegisterModel
                | pxb::FrameType::Subscribe => self.registrations.push(f),
                pxb::FrameType::Ready => break,
                other => panic!("unexpected frame during registration: {other:?}"),
            }
        }
        hello
    }

    fn shutdown(&mut self) {
        self.write(pxb::TYPE_SHUTDOWN, 0, 0, &[]);
        let f = self.read();
        assert_eq!(f.header.typ, pxb::TYPE_SHUTDOWN_ACK);
        let status = self.child.wait().expect("wait for extension exit");
        assert!(status.success(), "extension exited with {status:?}");
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

#[test]
fn hello_extension_lifecycle() {
    let mut h = Host::spawn("hello");

    let hello = h.handshake();
    assert_eq!(hello.name, "hello");
    assert_eq!(hello.version, "0.1.0");
    assert_eq!(hello.protocol, pxb::PROTOCOL_VERSION);
    assert_eq!(
        hello.caps,
        pxb::CAP_COMMANDS | pxb::CAP_INTERCEPT | pxb::CAP_EVENTS | pxb::CAP_LARGE_FRAMES
    );

    // Command invoke → Notify frame, then CommandResponse echoing id.
    h.write(
        pxb::TYPE_COMMAND_INVOKED,
        pxb::FLAG_HAS_ID,
        1,
        &pxb::CommandInvoked {
            name: "hello".into(),
            args: "world".into(),
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_NOTIFY);
    let n = pxb::NotifyMsg::decode(&f.body).unwrap();
    assert_eq!((n.level.as_str(), n.message.as_str()), ("info", "Hello!"));

    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_COMMAND_RESPONSE);
    assert_eq!(f.header.flags & pxb::FLAG_HAS_ID, pxb::FLAG_HAS_ID);
    assert_eq!(f.header.id, 1);
    let resp = pxb::CommandResponse::decode(&f.body).unwrap();
    assert!(resp.ok);
    assert!(resp.error.is_empty());
    assert!(resp.submit.is_empty());

    // Intercept with no handler decision → empty response, id echoed.
    h.write(
        pxb::TYPE_INTERCEPT,
        pxb::FLAG_HAS_ID,
        2,
        &pxb::InterceptReq {
            event: pxb::Event::UserInput.code(),
            prompt: "hi".into(),
            ..Default::default()
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_INTERCEPT_RESPONSE);
    assert_eq!(f.header.flags & pxb::FLAG_HAS_ID, pxb::FLAG_HAS_ID);
    assert_eq!(f.header.id, 2);
    assert_eq!(f.body, pxb::InterceptResp::default().encode());

    // Fire-and-forget Event + SessionMeta must not break the loop.
    h.write(
        pxb::TYPE_EVENT,
        0,
        0,
        &pxb::EventNotify {
            event: pxb::Event::SessionStart.code(),
            session_id: "s9".into(),
            ..Default::default()
        }
        .encode(),
    );
    h.write(
        pxb::TYPE_SESSION_META,
        0,
        0,
        &pxb::SessionMeta {
            session_id: "s9".into(),
            cwd: "/new".into(),
        }
        .encode(),
    );

    h.shutdown();
}

#[test]
fn full_extension_confirm_tool_and_submit() {
    let mut h = Host::spawn("full");

    let hello = h.handshake();
    assert_eq!(hello.name, "full");
    assert_eq!(
        hello.caps,
        pxb::CAP_COMMANDS | pxb::CAP_TOOLS | pxb::CAP_LARGE_FRAMES
    );

    // Command "ask" issues a confirm HostRequest (id 1), waits for the
    // HostResult, then notifies and replies with the queued submit.
    h.write(
        pxb::TYPE_COMMAND_INVOKED,
        pxb::FLAG_HAS_ID,
        7,
        &pxb::CommandInvoked {
            name: "ask".into(),
            args: String::new(),
        }
        .encode(),
    );

    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_HOST_REQUEST);
    assert_eq!(f.header.flags & pxb::FLAG_HAS_ID, pxb::FLAG_HAS_ID);
    assert_eq!(f.header.id, 1);
    let hr = pxb::HostRequest::decode(&f.body).unwrap();
    assert_eq!(hr.method, "confirm");
    assert!(
        hr.arg.contains(r#""Title":"Confirm?""#),
        "unexpected arg: {hr:?}"
    );
    assert!(
        hr.arg.contains(r#""Message":"Proceed with /tmp/x?""#),
        "unexpected arg: {hr:?}"
    );

    h.write(
        pxb::TYPE_HOST_RESULT,
        pxb::FLAG_HAS_ID,
        1,
        &pxb::HostResult {
            ok: true,
            ..Default::default()
        }
        .encode(),
    );

    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_NOTIFY);
    let n = pxb::NotifyMsg::decode(&f.body).unwrap();
    assert_eq!(
        (n.level.as_str(), n.message.as_str()),
        ("info", "Confirmed!")
    );

    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_COMMAND_RESPONSE);
    assert_eq!(f.header.id, 7);
    let resp = pxb::CommandResponse::decode(&f.body).unwrap();
    assert!(resp.ok);
    assert_eq!(resp.submit, "follow-up from ask");

    // Tool invoke echoes its own id.
    h.write(
        pxb::TYPE_TOOL_INVOKE,
        pxb::FLAG_HAS_ID,
        9,
        &pxb::ToolInvoke {
            name: "echo".into(),
            args: br#"{"text":"hi"}"#.to_vec(),
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_TOOL_RESULT);
    assert_eq!(f.header.id, 9);
    let tr = pxb::ToolResultMsg::decode(&f.body).unwrap();
    assert!(!tr.is_error);
    assert_eq!(tr.content, r#"echo: {"text":"hi"}"#);
    assert!(tr.error.is_empty());

    // DetailFromArgs RPC (same invoke body, lighter reply).
    h.write(
        pxb::TYPE_TOOL_DETAIL_INVOKE,
        pxb::FLAG_HAS_ID,
        10,
        &pxb::ToolInvoke {
            name: "echo".into(),
            args: br#"{"text":"hi"}"#.to_vec(),
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_TOOL_DETAIL_RESULT);
    assert_eq!(f.header.id, 10);
    let detail = pxb::ToolDetailResult::decode(&f.body).unwrap();
    assert_eq!(detail.detail, r#"{"text":"hi"}"#);

    // Async tool handler: the SDK drives the returned future to completion
    // on its single-threaded runtime (the handler yields once inside).
    h.write(
        pxb::TYPE_TOOL_INVOKE,
        pxb::FLAG_HAS_ID,
        11,
        &pxb::ToolInvoke {
            name: "async-echo".into(),
            args: br#"{"text":"yo"}"#.to_vec(),
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!(f.header.typ, pxb::TYPE_TOOL_RESULT);
    assert_eq!(f.header.id, 11);
    let tr = pxb::ToolResultMsg::decode(&f.body).unwrap();
    assert!(!tr.is_error);
    assert_eq!(tr.content, r#"async echo: {"text":"yo"}"#);
    assert!(tr.error.is_empty());

    h.shutdown();
}

#[test]
fn models_extension_registration_and_stream() {
    let mut h = Host::spawn("models");
    let hello = h.handshake();
    assert_eq!(hello.name, "models");
    assert_eq!(hello.caps, pxb::CAP_MODELS | pxb::CAP_LARGE_FRAMES);

    let regs: Vec<pxb::RegisterModel> = h
        .registrations
        .iter()
        .map(|f| pxb::RegisterModel::decode(&f.body).unwrap())
        .collect();
    assert_eq!(regs.len(), 2);
    let direct = &regs[0];
    assert_eq!(
        (direct.name.as_str(), direct.conn.as_str()),
        ("gw", pxb::CONN_DIRECT)
    );
    assert_eq!(
        (
            direct.api.as_str(),
            direct.base_url.as_str(),
            direct.api_key.as_str()
        ),
        ("Anthropic", "http://127.0.0.1:9", "k")
    );
    assert_eq!(direct.context_window, 42);
    let served = &regs[1];
    assert_eq!(
        (served.name.as_str(), served.conn.as_str()),
        ("echo", pxb::CONN_EXT)
    );
    assert_eq!(served.context_window, 1000);
    assert!(served.think_enabled);
    assert_eq!(served.think_level, "high");

    // One invoke: deltas echo the id, then a done frame.
    h.write(
        pxb::TYPE_MODEL_INVOKE,
        pxb::FLAG_HAS_ID,
        5,
        &pxb::ModelInvoke {
            model: "echo".into(),
            messages_json: br#"[{"role":"user","content":"hi"}]"#.to_vec(),
            system_prompt: "sys".into(),
            think: "high".into(),
            ..Default::default()
        }
        .encode(),
    );
    let mut assembled = String::new();
    for _ in 0..3 {
        let f = h.read();
        assert_eq!((f.header.typ, f.header.id), (pxb::TYPE_MODEL_STREAM, 5));
        assert_eq!(f.header.flags, pxb::FLAG_HAS_ID);
        let ev = pxb::ModelStreamEvent::decode(&f.body).unwrap();
        assert_eq!(ev.type_, "delta");
        assembled.push_str(&ev.content);
        if !ev.usage_json.is_empty() {
            let usage: serde_json::Value = serde_json::from_slice(&ev.usage_json).unwrap();
            assert_eq!(usage["total_tokens"], 3);
        }
    }
    assert_eq!(assembled, "echo: hi [think:high]");
    let f = h.read();
    assert_eq!((f.header.typ, f.header.id), (pxb::TYPE_MODEL_STREAM, 5));
    let ev = pxb::ModelStreamEvent::decode(&f.body).unwrap();
    assert_eq!(ev.type_, "done");

    // Unknown model → a single error frame; the loop survives.
    h.write(
        pxb::TYPE_MODEL_INVOKE,
        pxb::FLAG_HAS_ID,
        6,
        &pxb::ModelInvoke {
            model: "nope".into(),
            ..Default::default()
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!((f.header.typ, f.header.id), (pxb::TYPE_MODEL_STREAM, 6));
    let ev = pxb::ModelStreamEvent::decode(&f.body).unwrap();
    assert_eq!(ev.type_, "error");
    assert!(ev.error.contains("unknown model nope"), "got: {ev:?}");

    // ModelCancel is best-effort; a late one after the stream ends is inert.
    h.write(pxb::TYPE_MODEL_CANCEL, pxb::FLAG_HAS_ID, 5, &[]);
    h.shutdown();
}

#[test]
fn async_timer_and_tcp_then_another_rpc() {
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap().to_string();
    let mut h = Host::spawn("sdk-probe");
    h.handshake();
    h.write(
        pxb::TYPE_TOOL_INVOKE,
        pxb::FLAG_HAS_ID,
        41,
        &pxb::ToolInvoke {
            name: "io".into(),
            args: address.into_bytes(),
        }
        .encode(),
    );
    listener.set_nonblocking(true).unwrap();
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    let mut stream = loop {
        match listener.accept() {
            Ok((stream, _)) => break stream,
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                assert!(
                    std::time::Instant::now() < deadline,
                    "loopback connection timeout"
                );
                std::thread::sleep(Duration::from_millis(5));
            }
            Err(error) => panic!("loopback accept: {error}"),
        }
    };
    stream.write_all(&[42]).unwrap();
    let f = h.read();
    assert_eq!((f.header.typ, f.header.id), (pxb::TYPE_TOOL_RESULT, 41));
    let result = pxb::ToolResultMsg::decode(&f.body).unwrap();
    assert!(!result.is_error, "{}", result.error);
    assert_eq!(result.content, "42");
    h.write(
        pxb::TYPE_TOOL_DETAIL_INVOKE,
        pxb::FLAG_HAS_ID,
        42,
        &pxb::ToolInvoke {
            name: "io".into(),
            args: vec![],
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!(
        (f.header.typ, f.header.id),
        (pxb::TYPE_TOOL_DETAIL_RESULT, 42)
    );
    h.shutdown();
}

#[test]
fn confirm_replays_requests_in_order_after_command() {
    let mut h = Host::spawn("full");
    h.handshake();
    h.write(
        pxb::TYPE_COMMAND_INVOKED,
        pxb::FLAG_HAS_ID,
        10,
        &pxb::CommandInvoked {
            name: "ask".into(),
            args: String::new(),
        }
        .encode(),
    );
    let confirm = h.read();
    assert_eq!(confirm.header.typ, pxb::TYPE_HOST_REQUEST);
    for (typ, id) in [
        (pxb::TYPE_TOOL_INVOKE, 11),
        (pxb::TYPE_TOOL_DETAIL_INVOKE, 12),
    ] {
        h.write(
            typ,
            pxb::FLAG_HAS_ID,
            id,
            &pxb::ToolInvoke {
                name: "echo".into(),
                args: b"queued".to_vec(),
            }
            .encode(),
        );
    }
    h.write(
        pxb::TYPE_INTERCEPT,
        pxb::FLAG_HAS_ID,
        13,
        &pxb::InterceptReq::default().encode(),
    );
    h.write(
        pxb::TYPE_COMMAND_INVOKED,
        pxb::FLAG_HAS_ID,
        14,
        &pxb::CommandInvoked {
            name: "missing".into(),
            args: String::new(),
        }
        .encode(),
    );
    h.write(
        pxb::TYPE_HOST_RESULT,
        pxb::FLAG_HAS_ID,
        confirm.header.id + 100,
        &pxb::HostResult {
            ok: true,
            ..Default::default()
        }
        .encode(),
    );
    assert!(matches!(
        h.rd.recv_timeout(Duration::from_millis(50)),
        Err(mpsc::RecvTimeoutError::Timeout)
    ));
    h.write(
        pxb::TYPE_HOST_RESULT,
        pxb::FLAG_HAS_ID,
        confirm.header.id,
        &pxb::HostResult {
            ok: true,
            ..Default::default()
        }
        .encode(),
    );
    assert_eq!(h.read().header.typ, pxb::TYPE_NOTIFY);
    for (typ, id) in [
        (pxb::TYPE_COMMAND_RESPONSE, 10),
        (pxb::TYPE_TOOL_RESULT, 11),
        (pxb::TYPE_TOOL_DETAIL_RESULT, 12),
        (pxb::TYPE_INTERCEPT_RESPONSE, 13),
        (pxb::TYPE_COMMAND_RESPONSE, 14),
    ] {
        let f = h.read();
        assert_eq!((f.header.typ, f.header.id), (typ, id));
        assert_eq!(f.header.flags, pxb::FLAG_HAS_ID);
    }
    h.shutdown();
}

#[test]
fn shutdown_during_confirm_exits_without_another_read() {
    let mut h = Host::spawn("sdk-probe");
    h.handshake();
    h.write(
        pxb::TYPE_COMMAND_INVOKED,
        pxb::FLAG_HAS_ID,
        1,
        &pxb::CommandInvoked {
            name: "ask".into(),
            args: String::new(),
        }
        .encode(),
    );
    assert_eq!(h.read().header.typ, pxb::TYPE_HOST_REQUEST);
    h.write(pxb::TYPE_SHUTDOWN, 0, 0, &[]);
    assert_eq!(h.read().header.typ, pxb::TYPE_SHUTDOWN_ACK);
    assert!(h.rd.recv_timeout(Duration::from_secs(5)).unwrap().is_err());
    assert!(h.child.wait().unwrap().success());
}

#[test]
fn oversized_tool_response_is_an_rpc_error_and_loop_survives() {
    let mut h = Host::spawn("sdk-probe");
    h.handshake();
    h.write(
        pxb::TYPE_TOOL_INVOKE,
        pxb::FLAG_HAS_ID,
        9,
        &pxb::ToolInvoke {
            name: "large".into(),
            args: vec![],
        }
        .encode(),
    );
    let f = h.read();
    assert_eq!((f.header.typ, f.header.id), (pxb::TYPE_TOOL_RESULT, 9));
    let result = pxb::ToolResultMsg::decode(&f.body).unwrap();
    assert!(result.is_error);
    assert!(result.error.contains("exceeds PXB payload limit"));
    h.shutdown();
}

/// Resolves an example binary. `CARGO_BIN_EXE_<name>` is only set for `bin`
/// targets, so examples are located under the target dir at test runtime.
/// Scoped invocations (`cargo test --test sdk_test`, or a test runner that
/// executes the test binary directly) do not build examples. Build once per
/// test process so existing binaries cannot silently test stale SDK code.
fn example_bin(name: &str) -> std::path::PathBuf {
    let manifest = env!("CARGO_MANIFEST_DIR");
    let mut dir = if let Ok(t) = std::env::var("CARGO_TARGET_DIR") {
        std::path::PathBuf::from(t)
    } else {
        std::path::PathBuf::from(manifest).join("target")
    };
    let profile = if cfg!(debug_assertions) {
        "debug"
    } else {
        "release"
    };
    dir.push(profile);
    dir.push("examples");
    #[cfg(windows)]
    let file = format!("{name}.exe");
    #[cfg(not(windows))]
    let file = name.to_string();
    dir.push(file);
    static BUILD: std::sync::Once = std::sync::Once::new();
    BUILD.call_once(|| {
        let mut command = std::process::Command::new(env!("CARGO"));
        command
            .current_dir(manifest)
            .args(["build", "--locked", "--examples"]);
        if !cfg!(debug_assertions) {
            command.arg("--release");
        }
        let status = command.status().expect("failed to build SDK test examples");
        assert!(status.success(), "cargo build --examples failed");
    });
    dir
}
