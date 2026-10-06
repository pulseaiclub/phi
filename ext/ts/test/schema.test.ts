// Schema builder tests — mirrors ext/rust/src/phi/schema.rs #[cfg(test)].

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { Schema } from "../src/phi/schema.ts";

describe("Schema", () => {
  it("object with required string property", () => {
    const s = Schema.object()
      .property("text", Schema.string().description("input text"))
      .required(["text"]);
    assert.equal(
      Buffer.from(s.toJsonBytes()).toString("utf8"),
      '{"type":"object","properties":{"text":{"type":"string","description":"input text"}},"required":["text"]}',
    );
  });

  it("string enum and additionalProperties", () => {
    const s = Schema.object()
      .property("mode", Schema.string().enumValues(["read-only", "workspace-write"]))
      .additionalProperties(false);
    const json = Buffer.from(s.toJsonBytes()).toString("utf8");
    assert.ok(json.includes('"enum":["read-only","workspace-write"]'), json);
    assert.ok(json.includes('"additionalProperties":false'), json);
  });

  it("array of strings", () => {
    const s = Schema.object().property("tags", Schema.array(Schema.string()));
    assert.equal(
      Buffer.from(s.toJsonBytes()).toString("utf8"),
      '{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}',
    );
  });

  it("raw passthrough", () => {
    const raw = '{"type":"object"}';
    assert.deepEqual(Schema.raw(raw).toJsonBytes(), Buffer.from(raw));
  });

  it("builders copy, so a base schema is reusable", () => {
    const base = Schema.object().property("a", Schema.string());
    const extended = base.property("b", Schema.string()).required(["b"]);
    const json = (s: Schema) => Buffer.from(s.toJsonBytes()).toString("utf8");
    assert.equal(json(base), '{"type":"object","properties":{"a":{"type":"string"}}}');
    assert.equal(
      json(extended),
      '{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["b"]}',
    );
  });

  it("array rejects raw items", () => {
    assert.throws(() => Schema.array(Schema.raw("{}")), TypeError);
  });

  it("builders no-op on non-object parents", () => {
    const s = Schema.string().property("x", Schema.string()).required(["x"]);
    assert.equal(
      Buffer.from(s.toJsonBytes()).toString("utf8"),
      '{"type":"string"}',
    );
  });
});
