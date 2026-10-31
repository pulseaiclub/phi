//! Model example: a direct-connect gateway plus an extension-served echo
//! model (Conn "ext") that streams the last user message back.

use phi_ext::phi;

fn main() -> Result<(), phi::Error> {
    let mut m = phi::Extension::new("models", "0.1.0");

    // Direct-connect: the host dials base_url itself, like a models[] entry
    // in config.yaml; only the registration settings matter here.
    m.register_model(
        phi::Model::direct("gw")
            .api("Anthropic")
            .base_url("http://127.0.0.1:9")
            .api_key("k")
            .context_window(42),
    );

    // Served: each request arrives as a ModelInvoke; emit deltas as they
    // come, return Ok to end with a done frame, Err to fail the stream.
    m.register_model(
        phi::Model::served("echo", |req, emit| {
            let last = req
                .messages
                .last()
                .map(|msg| msg.content.as_str())
                .unwrap_or("empty");
            emit(phi::ModelEvent {
                content: "echo: ".into(),
                ..Default::default()
            })?;
            emit(phi::ModelEvent {
                content: format!("{last} [think:{}]", req.think),
                ..Default::default()
            })?;
            emit(phi::ModelEvent {
                usage: Some(phi::Usage {
                    completion_tokens: 1,
                    prompt_tokens: 2,
                    total_tokens: 3,
                    ..Default::default()
                }),
                ..Default::default()
            })?;
            Ok(())
        })
        .context_window(1000)
        .think_enabled(true)
        .think_level("high"),
    );

    m.run()
}
