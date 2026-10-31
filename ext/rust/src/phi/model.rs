//! Model registration and streaming — the Rust port of Go's `ext.Model`,
//! `ext.ModelRequest` and `ext.ModelEvent`.
//!
//! Two connection methods (see [`Model::direct`] / [`Model::served`]):
//!
//! - **Direct-connect** ([`CONN_DIRECT`](crate::pxb::CONN_DIRECT)): the host
//!   dials `base_url` itself with the registration settings, exactly like a
//!   `models[]` entry in config.yaml. No handler needed.
//! - **Extension-served** ([`CONN_EXT`](crate::pxb::CONN_EXT)): the extension
//!   *is* the connection — each chat completion arrives as a `ModelInvoke`,
//!   the handler emits deltas, and the host assembles the final assistant
//!   message. Use it to proxy an upstream, speak a custom wire protocol, or
//!   generate locally.

use crate::pxb;
use serde::{Deserialize, Serialize};

/// Outcome of a stream handler or one of its `emit` calls. An `Err` fails the
/// stream with an error frame; `Ok(())` ends it with a done frame.
pub type StreamResult = Result<(), String>;

/// Storage type for a served model's stream handler. Receives one request and
/// an emit sink for deltas; runs on the PXB loop thread (see
/// [`Extension::register_model`](super::Extension::register_model)).
pub type StreamFn =
    Box<dyn FnMut(ModelRequest, &mut dyn FnMut(ModelEvent) -> StreamResult) -> StreamResult>;

/// A model the host can switch to. Build with [`Model::direct`] or
/// [`Model::served`], then tune with the builder methods.
#[derive(Default)]
pub struct Model {
    pub name: String,
    /// Connection method: [`CONN_DIRECT`](crate::pxb::CONN_DIRECT) or
    /// [`CONN_EXT`](crate::pxb::CONN_EXT).
    pub conn: String,
    /// Wire format for direct-connect: "OpenAI" | "OpenAIResponses" |
    /// "Anthropic" | "Gemini"; empty is OpenAI-compatible chat completions.
    pub api: String,
    pub base_url: String,
    pub api_key: String,
    /// Binds a built-in model name for capabilities and request hooks
    /// (e.g. "glm-5.3-flash" for an opaque gateway route).
    pub preset: String,
    /// Context window in tokens (0 disables compaction).
    pub context_window: u32,
    pub image_enabled: bool,
    pub think_enabled: bool,
    /// off|minimal|low|medium|high|xhigh|max; empty keeps the preset default.
    pub think_level: String,
    /// Serves each request when `conn` is `CONN_EXT`; required there, ignored
    /// for direct-connect. Set via [`Model::served`].
    pub stream: Option<StreamFn>,
}

impl Model {
    /// A direct-connect model: the host dials `base_url` itself. Complete it
    /// with [`api`](Self::api), [`base_url`](Self::base_url),
    /// [`api_key`](Self::api_key), [`preset`](Self::preset), …
    pub fn direct(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            ..Default::default()
        }
    }

    /// An extension-served model (`CONN_EXT`): `stream` handles each
    /// `ModelInvoke`, emitting [`ModelEvent`] deltas through the sink. Return
    /// `Ok(())` to end with a done frame, `Err` to fail the stream. Handlers
    /// run on the PXB loop thread — the host's `ModelCancel` is best-effort
    /// and only observed between requests.
    pub fn served(
        name: impl Into<String>,
        mut stream: impl FnMut(ModelRequest, &mut dyn FnMut(ModelEvent) -> StreamResult) -> StreamResult
            + 'static,
    ) -> Self {
        Self {
            name: name.into(),
            conn: pxb::CONN_EXT.to_string(),
            stream: Some(Box::new(move |req, emit| stream(req, emit))),
            ..Default::default()
        }
    }

    /// Sets the direct-connect wire format ("OpenAI" | "OpenAIResponses" |
    /// "Anthropic" | "Gemini"); empty is OpenAI-compatible chat completions.
    pub fn api(mut self, api: impl Into<String>) -> Self {
        self.api = api.into();
        self
    }

    /// Sets the direct-connect endpoint the host dials.
    pub fn base_url(mut self, url: impl Into<String>) -> Self {
        self.base_url = url.into();
        self
    }

    /// Sets the direct-connect API key.
    pub fn api_key(mut self, key: impl Into<String>) -> Self {
        self.api_key = key.into();
        self
    }

    /// Binds a built-in preset name for capabilities and request hooks.
    pub fn preset(mut self, preset: impl Into<String>) -> Self {
        self.preset = preset.into();
        self
    }

    /// Context window in tokens (0 disables compaction).
    pub fn context_window(mut self, tokens: u32) -> Self {
        self.context_window = tokens;
        self
    }

    /// Opts the model into image attachments.
    pub fn image_enabled(mut self, enabled: bool) -> Self {
        self.image_enabled = enabled;
        self
    }

    /// Exposes thinking; pair with [`think_level`](Self::think_level).
    pub fn think_enabled(mut self, enabled: bool) -> Self {
        self.think_enabled = enabled;
        self
    }

    /// Default think level: off|minimal|low|medium|high|xhigh|max; empty
    /// keeps the preset default.
    pub fn think_level(mut self, level: impl Into<String>) -> Self {
        self.think_level = level.into();
        self
    }
}

/// One chat completion on a served model. Mirrors Go's `ext.ModelRequest`;
/// `messages` / `tools` carry the session-serialization JSON shapes, so
/// forward `system_prompt` as the system role.
#[derive(Debug, Clone, Default)]
pub struct ModelRequest {
    pub model: String,
    /// Requested think mode: off|minimal|low|medium|high|xhigh|max; empty
    /// means unset.
    pub think: String,
    pub system_prompt: String,
    pub messages: Vec<ChatMessage>,
    pub tools: Vec<ModelTool>,
}

/// One streamed delta, mirroring Go's `ext.ModelEvent`. The host assembles
/// the assistant message from fragments, so emit increments — never the full
/// text. Report `usage` once (on a delta or just before returning) for
/// context-window accounting.
#[derive(Debug, Clone, Default)]
pub struct ModelEvent {
    pub content: String,
    pub reasoning: String,
    /// OpenAI-style fragments: the first fragment for an index carries
    /// id / type / name, later ones append `function.arguments` text.
    pub tool_calls: Vec<ToolCallSpec>,
    pub usage: Option<Usage>,
}

/// The neutral conversation message, mirroring Go's `ext.ChatMessage`. Role
/// is system|user|assistant|tool; `tool_call_id` pairs a tool result with its
/// call.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ChatMessage {
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub content: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reasoning_content: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tool_calls: Vec<ToolCallSpec>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub tool_call_id: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub images: Vec<MessageImage>,
    /// Provider-continuation state (e.g. Anthropic cache prefix). Forward it
    /// verbatim when the upstream protocol needs it.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub provider_state: Option<serde_json::Value>,
}

/// A model-requested tool invocation in a request message, or an emitted
/// delta fragment (index + argument chunks) in a [`ModelEvent`].
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ToolCallSpec {
    // Go marshals `index,omitempty`, so a zero index stays off the wire.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub index: usize,
    #[serde(default)]
    pub id: String,
    #[serde(rename = "type", default)]
    pub call_type: String,
    #[serde(default)]
    pub function: FunctionSpec,
}

/// Names a tool and carries its JSON arguments.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct FunctionSpec {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub arguments: String,
}

/// One base64 image on a user message.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct MessageImage {
    #[serde(default)]
    pub data: String,
    #[serde(rename = "mimeType", default)]
    pub mime_type: String,
}

/// One tool schema the model may call.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct ModelTool {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parameters: Option<serde_json::Value>,
}

/// Disjoint token buckets, mirroring Go's `ext.Usage`.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Usage {
    #[serde(default)]
    pub completion_tokens: i64,
    #[serde(default)]
    pub prompt_tokens: i64,
    #[serde(default)]
    pub total_tokens: i64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub prompt_tokens_details: Option<PromptTokensDetails>,
}

/// Splits cache traffic out of the prompt count.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PromptTokensDetails {
    #[serde(default)]
    pub cached_tokens: i64,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub cache_write_tokens: i64,
}

fn is_zero<T: PartialEq + Default>(v: &T) -> bool {
    *v == T::default()
}
