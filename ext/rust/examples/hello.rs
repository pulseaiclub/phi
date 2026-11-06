//! Minimal hello extension, mirroring the Go example in `doc/extensions.md`.

use phi_ext::{phi, pxb};

fn main() -> Result<(), phi::Error> {
    let mut m = phi::Extension::new("hello", "0.1.0");

    m.register_command(
        "hello",
        phi::Command::new("Say hi", |_args, ctx| {
            ctx.notify("info", "Hello!");
            // ctx.submit("follow-up"); // after /hello returns
            // ctx.send_user_message("…"); // enqueue a turn anytime
            Ok(())
        }),
    );

    m.on_user_input(|_ev| {
        // return Some(phi::UserInputResult { handled: true, ..Default::default() }) to swallow
        // return Some(phi::UserInputResult { text: Some("rewritten".into()), ..Default::default() }) to transform
        None
    });

    m.on_tool_call(|_ev| {
        // return Some(phi::ToolCallResult { block: true, reason: "...".into(), ..Default::default() }) to deny
        None
    });

    m.on_tool_result(|_ev| {
        // return Some(phi::ToolResultResult { stop: true, ..Default::default() }) to end the agent loop
        None
    });

    m.subscribe(pxb::Event::SessionStart, |ev| {
        let _ = ev; // Reason, PreviousSessionID, …
    });

    m.run()
}
