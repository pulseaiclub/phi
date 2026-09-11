//! Typed JSON Schema for tool parameters.
//!
//! Codex generates these via `schemars` from Rust argument structs. Authors
//! build an equivalent schema with the builders below, serialized with
//! `serde_json`; the wire still carries opaque JSON Schema bytes (same as
//! Go's `Parameters map[string]any` after `json.Marshal`).

use std::collections::BTreeMap;

/// JSON Schema body for an LLM tool's parameters
/// (`type` / `properties` / `required` / …).
#[derive(Debug, Clone)]
pub struct Schema {
    inner: SchemaInner,
}

#[derive(Debug, Clone)]
enum SchemaInner {
    Built(Node),
    /// Escape hatch for hand-written JSON Schema bytes.
    Raw(Vec<u8>),
}

#[derive(Debug, Clone)]
struct Node {
    kind: Kind,
    description: Option<String>,
    properties: BTreeMap<String, Node>,
    required: Vec<String>,
    additional_properties: Option<bool>,
    enum_values: Vec<String>,
    items: Option<Box<Node>>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Kind {
    Object,
    String,
    Number,
    Integer,
    Boolean,
    Array,
}

impl Default for Node {
    fn default() -> Self {
        Self {
            kind: Kind::Object,
            description: None,
            properties: BTreeMap::new(),
            required: Vec::new(),
            additional_properties: None,
            enum_values: Vec::new(),
            items: None,
        }
    }
}

/// Serializes a schema node as JSON Schema. Keys are emitted in a fixed order
/// (`type`, `description`, …) and `serde_json`'s `preserve_order` keeps that
/// order on the wire, so the bytes are stable across runs.
impl serde::Serialize for Node {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        use serde::ser::SerializeMap;
        let mut m = s.serialize_map(Some(3))?;
        m.serialize_entry("type", kind_str(self.kind))?;
        if let Some(d) = &self.description {
            m.serialize_entry("description", d)?;
        }
        if self.kind == Kind::Object {
            m.serialize_entry("properties", &self.properties)?;
            if !self.required.is_empty() {
                m.serialize_entry("required", &self.required)?;
            }
            if let Some(allow) = self.additional_properties {
                m.serialize_entry("additionalProperties", &allow)?;
            }
        }
        if self.kind == Kind::String && !self.enum_values.is_empty() {
            m.serialize_entry("enum", &self.enum_values)?;
        }
        if self.kind == Kind::Array {
            if let Some(items) = &self.items {
                m.serialize_entry("items", items.as_ref())?;
            }
        }
        m.end()
    }
}

impl Schema {
    fn from_node(node: Node) -> Self {
        Self {
            inner: SchemaInner::Built(node),
        }
    }

    /// Object schema (`{"type":"object",…}`). Default for tool parameters.
    pub fn object() -> Self {
        Self::from_node(Node {
            kind: Kind::Object,
            ..Node::default()
        })
    }

    pub fn string() -> Self {
        Self::from_node(Node {
            kind: Kind::String,
            ..Node::default()
        })
    }

    pub fn number() -> Self {
        Self::from_node(Node {
            kind: Kind::Number,
            ..Node::default()
        })
    }

    pub fn integer() -> Self {
        Self::from_node(Node {
            kind: Kind::Integer,
            ..Node::default()
        })
    }

    pub fn boolean() -> Self {
        Self::from_node(Node {
            kind: Kind::Boolean,
            ..Node::default()
        })
    }

    /// Array schema. `items` must be a builder schema (not [`Schema::raw`]).
    pub fn array(items: Schema) -> Self {
        let SchemaInner::Built(items_node) = items.inner else {
            panic!("Schema::array requires a builder schema, not Schema::raw");
        };
        Self::from_node(Node {
            kind: Kind::Array,
            items: Some(Box::new(items_node)),
            ..Node::default()
        })
    }

    /// Opaque JSON Schema bytes (the previous `Vec<u8>` API).
    pub fn raw(json: impl Into<Vec<u8>>) -> Self {
        Self {
            inner: SchemaInner::Raw(json.into()),
        }
    }

    pub fn description(mut self, d: impl Into<String>) -> Self {
        if let SchemaInner::Built(n) = &mut self.inner {
            n.description = Some(d.into());
        }
        self
    }

    /// Add an object property. No-op on non-object / raw schemas.
    pub fn property(mut self, name: impl Into<String>, schema: Schema) -> Self {
        if let (SchemaInner::Built(n), SchemaInner::Built(child)) = (&mut self.inner, schema.inner) {
            if n.kind == Kind::Object {
                n.properties.insert(name.into(), child);
            }
        }
        self
    }

    /// Mark object property names as required.
    pub fn required<I, S>(mut self, names: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        if let SchemaInner::Built(n) = &mut self.inner {
            if n.kind == Kind::Object {
                n.required.extend(names.into_iter().map(Into::into));
            }
        }
        self
    }

    pub fn additional_properties(mut self, allow: bool) -> Self {
        if let SchemaInner::Built(n) = &mut self.inner {
            if n.kind == Kind::Object {
                n.additional_properties = Some(allow);
            }
        }
        self
    }

    /// Restrict a string schema to an enum (Codex-style compact enums).
    pub fn enum_values<I, S>(mut self, values: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        if let SchemaInner::Built(n) = &mut self.inner {
            if n.kind == Kind::String {
                n.enum_values.extend(values.into_iter().map(Into::into));
            }
        }
        self
    }

    /// Serialize to JSON Schema bytes for `RegisterTool`.
    pub fn to_json_bytes(&self) -> Vec<u8> {
        match &self.inner {
            SchemaInner::Raw(b) => b.clone(),
            SchemaInner::Built(n) => {
                serde_json::to_vec(n).expect("schema serialization cannot fail")
            }
        }
    }
}

impl From<Vec<u8>> for Schema {
    fn from(json: Vec<u8>) -> Self {
        Schema::raw(json)
    }
}

impl From<&[u8]> for Schema {
    fn from(json: &[u8]) -> Self {
        Schema::raw(json.to_vec())
    }
}

fn kind_str(k: Kind) -> &'static str {
    match k {
        Kind::Object => "object",
        Kind::String => "string",
        Kind::Number => "number",
        Kind::Integer => "integer",
        Kind::Boolean => "boolean",
        Kind::Array => "array",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn object_with_required_string_prop() {
        let s = Schema::object()
            .property("text", Schema::string().description("input text"))
            .required(["text"]);
        assert_eq!(
            String::from_utf8(s.to_json_bytes()).unwrap(),
            r#"{"type":"object","properties":{"text":{"type":"string","description":"input text"}},"required":["text"]}"#
        );
    }

    #[test]
    fn string_enum_and_additional_properties() {
        let s = Schema::object()
            .property(
                "mode",
                Schema::string().enum_values(["read-only", "workspace-write"]),
            )
            .additional_properties(false);
        let json = String::from_utf8(s.to_json_bytes()).unwrap();
        assert!(json.contains(r#""enum":["read-only","workspace-write"]"#));
        assert!(json.contains(r#""additionalProperties":false"#));
    }

    #[test]
    fn array_of_strings() {
        let s = Schema::object().property("tags", Schema::array(Schema::string()));
        assert_eq!(
            String::from_utf8(s.to_json_bytes()).unwrap(),
            r#"{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}"#
        );
    }

    #[test]
    fn raw_passthrough() {
        let raw = br#"{"type":"object"}"#;
        assert_eq!(Schema::raw(raw.to_vec()).to_json_bytes(), raw);
    }
}
