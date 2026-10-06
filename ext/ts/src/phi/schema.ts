// Typed JSON Schema for tool parameters — a TS port of
// ext/rust/src/phi/schema.rs.
//
// Authors build a schema with the builders below; the wire still carries
// opaque JSON Schema bytes (same as Go's `Parameters map[string]any` after
// json.Marshal). Serialization emits keys in a fixed order (`type`,
// `description`, …), so the bytes are stable across runs.

/** JSON Schema body for an LLM tool's parameters
 * (`type` / `properties` / `required` / …). */
export class Schema {
  // Erasable syntax only: the SDK runs under Node's strip-only TS mode.
  readonly #built: Node | null;
  readonly #rawJson?: Uint8Array;

  private constructor(built: Node | null, rawJson?: Uint8Array) {
    this.#built = built;
    this.#rawJson = rawJson;
  }

  /** Object schema (`{"type":"object",…}`). Default for tool parameters. */
  static object(): Schema {
    return new Schema({ kind: "object", properties: new Map(), required: [], enumValues: [] });
  }

  static string(): Schema {
    return new Schema({ kind: "string", properties: new Map(), required: [], enumValues: [] });
  }

  static number(): Schema {
    return new Schema({ kind: "number", properties: new Map(), required: [], enumValues: [] });
  }

  static integer(): Schema {
    return new Schema({ kind: "integer", properties: new Map(), required: [], enumValues: [] });
  }

  static boolean(): Schema {
    return new Schema({ kind: "boolean", properties: new Map(), required: [], enumValues: [] });
  }

  /** Array schema. `items` must be a builder schema, not raw JSON. */
  static array(items: Schema): Schema {
    const inner = items.#built;
    if (!inner) {
      throw new TypeError("Schema.array requires a builder schema, not Schema.raw");
    }
    return new Schema({
      kind: "array",
      properties: new Map(),
      required: [],
      enumValues: [],
      items: inner,
    });
  }

  /** Opaque JSON Schema bytes (escape hatch for hand-written schemas). */
  static raw(json: Uint8Array | string): Schema {
    const bytes = typeof json === "string" ? Buffer.from(json, "utf8") : json;
    return new Schema(null, bytes);
  }

  description(d: string): Schema {
    return this.mutate((n) => {
      n.description = d;
    });
  }

  /** Add an object property. No-op on non-object / raw schemas. */
  property(name: string, schema: Schema): Schema {
    const child = schema.#built;
    return this.mutate((n) => {
      if (n.kind === "object" && child) {
        n.properties.set(name, child);
      }
    });
  }

  /** Mark object property names as required. */
  required(names: Iterable<string>): Schema {
    return this.mutate((n) => {
      if (n.kind === "object") {
        n.required.push(...names);
      }
    });
  }

  additionalProperties(allow: boolean): Schema {
    return this.mutate((n) => {
      if (n.kind === "object") {
        n.additionalProperties = allow;
      }
    });
  }

  /** Restrict a string schema to an enum (Codex-style compact enums). */
  enumValues(values: Iterable<string>): Schema {
    return this.mutate((n) => {
      if (n.kind === "string") {
        n.enumValues.push(...values);
      }
    });
  }

  /** Builders copy before mutating, so a base schema can be reused across
   * tools without one tool's properties bleeding into another's. */
  private mutate(fn: (n: Node) => void): Schema {
    if (!this.#built) {
      return this; // raw schemas are immutable
    }
    const clone = structuredClone(this.#built);
    fn(clone);
    return new Schema(clone);
  }

  /** Serialize to JSON Schema bytes for `RegisterTool`. */
  toJsonBytes(): Uint8Array {
    if (this.#rawJson) return this.#rawJson;
    return Buffer.from(JSON.stringify(toJson(this.#built!)), "utf8");
  }
}

/** Anything usable as a tool parameter schema: a builder or raw JSON bytes. */
export type SchemaInput = Schema | Uint8Array;

/** Serializes a tool schema to the bytes `RegisterTool` carries. */
export function schemaBytes(schema: SchemaInput): Uint8Array {
  return schema instanceof Schema ? schema.toJsonBytes() : schema;
}

interface Node {
  kind: "object" | "string" | "number" | "integer" | "boolean" | "array";
  description?: string;
  properties: Map<string, Node>;
  required: string[];
  additionalProperties?: boolean;
  enumValues: string[];
  items?: Node;
}

// Plain objects preserve insertion order for string keys, so building the
// object in wire order keeps the serialized bytes stable.
function toJson(n: Node): Record<string, unknown> {
  const out: Record<string, unknown> = { type: n.kind };
  if (n.description !== undefined) {
    out.description = n.description;
  }
  if (n.kind === "object") {
    const props: Record<string, unknown> = {};
    for (const [name, child] of n.properties) {
      props[name] = toJson(child);
    }
    out.properties = props;
    if (n.required.length > 0) {
      out.required = n.required;
    }
    if (n.additionalProperties !== undefined) {
      out.additionalProperties = n.additionalProperties;
    }
  }
  if (n.kind === "string" && n.enumValues.length > 0) {
    out.enum = n.enumValues;
  }
  if (n.kind === "array" && n.items) {
    out.items = toJson(n.items);
  }
  return out;
}
