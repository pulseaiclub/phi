// Tagged-field payloads (protobuf-style, fixed-width) — a TS port of
// ext/rust/src/pxb/fields.rs.
//
// Layout per field: `tag u16 | kind u8 | value…`. Only two wire kinds exist,
// so unknown fields are always skippable without a schema. Decoders must skip
// unknown tags; omitting a tag means the zero value.
//
// Evolution rules (see doc/extensions.md):
// - new fields take a new tag number, never reuse a tag;
// - empty strings/blobs are omitted on encode;
// - experimental tags use 128+ and must remain skippable.

import { PxbError } from "./codec.ts";

/** 8-byte little-endian (u16/u32/bool/event codes share this kind). */
export const WIRE_U64 = 1;
/** u32 length + bytes. */
export const WIRE_BYTES = 2;

/** Builds a tagged-field payload. */
export class FieldWriter {
  private buf: Buffer;
  private len = 0;

  constructor() {
    this.buf = Buffer.allocUnsafe(32);
  }

  private reserve(n: number): void {
    if (this.buf.length - this.len >= n) return;
    let cap = this.buf.length * 2;
    while (cap - this.len < n) cap *= 2;
    const grown = Buffer.allocUnsafe(cap);
    this.buf.copy(grown, 0, 0, this.len);
    this.buf = grown;
  }

  private putHdr(tag: number, kind: number): void {
    this.reserve(3);
    this.buf.writeUInt16LE(tag, this.len);
    this.buf.writeUInt8(kind, this.len + 2);
    this.len += 3;
  }

  /** The encoded payload. */
  bytes(): Buffer {
    return this.buf.subarray(0, this.len);
  }

  /** Writes an 8-byte integer field. Always written, even when zero —
   * decoders rely on the tag being present for bool-ish fields. */
  putU64(tag: number, v: number): void {
    this.putHdr(tag, WIRE_U64);
    this.reserve(8);
    this.buf.writeUInt32LE(v >>> 0, this.len);
    this.buf.writeUInt32LE(Math.floor(v / 0x100000000), this.len + 4);
    this.len += 8;
  }

  putU16(tag: number, v: number): void {
    this.putU64(tag, v);
  }

  putU32(tag: number, v: number): void {
    this.putU64(tag, v);
  }

  putBool(tag: number, v: boolean): void {
    this.putU64(tag, v ? 1 : 0);
  }

  /** Writes a length-prefixed blob. Empty blobs are omitted so decoders stay
   * compact on the hot path. */
  putBytes(tag: number, p: Uint8Array): void {
    if (p.length === 0) return;
    this.putHdr(tag, WIRE_BYTES);
    this.reserve(4 + p.length);
    this.buf.writeUInt32LE(p.length, this.len);
    Buffer.from(p.buffer, p.byteOffset, p.byteLength).copy(this.buf, this.len + 4);
    this.len += 4 + p.length;
  }

  putString(tag: number, s: string): void {
    if (s.length === 0) return;
    this.putBytes(tag, Buffer.from(s, "utf8"));
  }

  /** Writes a packed list as `WIRE_BYTES`: `u16 count` + values. */
  putU16s(tag: number, vs: readonly number[]): void {
    if (vs.length === 0) return;
    if (vs.length > 0xffff) {
      throw new PxbError("payloadTooLarge", "u16 list too long");
    }
    const inner = Buffer.allocUnsafe(2 + vs.length * 2);
    inner.writeUInt16LE(vs.length, 0);
    for (let i = 0; i < vs.length; i++) {
      inner.writeUInt16LE(vs[i]!, 2 + i * 2);
    }
    this.putBytes(tag, inner);
  }
}

/** Walks a tagged-field payload. */
export class FieldReader {
  private b: Buffer;
  private i = 0;

  constructor(b: Buffer) {
    this.b = b;
  }

  done(): boolean {
    return this.i >= this.b.length;
  }

  private need(n: number): void {
    if (this.b.length - this.i < n) {
      throw new PxbError("truncated", "truncated payload");
    }
  }

  /** Returns the next field tag and wire kind. */
  nextField(): { tag: number; kind: number } {
    this.need(3);
    const tag = this.b.readUInt16LE(this.i);
    const kind = this.b.readUInt8(this.i + 2);
    this.i += 3;
    if (kind !== WIRE_U64 && kind !== WIRE_BYTES) {
      throw new PxbError("badWire", "bad wire kind");
    }
    if (tag === 0) {
      throw new PxbError("badTag", "bad field tag");
    }
    return { tag, kind };
  }

  position(): number {
    return this.i;
  }

  /** Reads a `WIRE_U64` value (call after nextField returned WIRE_U64). */
  u64(): number {
    this.need(8);
    const lo = this.b.readUInt32LE(this.i);
    const hi = this.b.readUInt32LE(this.i + 4);
    this.i += 8;
    // Exact for every value this protocol carries (u16/u32 fields); larger
    // u64s lose precision the same way Go's int would overflow.
    return lo + hi * 0x100000000;
  }

  /** Reads a `WIRE_BYTES` value (call after nextField returned WIRE_BYTES).
   * The returned slice aliases the payload; copy it to retain. */
  bytes(): Buffer {
    this.need(4);
    const n = this.b.readUInt32LE(this.i);
    this.i += 4;
    this.need(n);
    const out = this.b.subarray(this.i, this.i + n);
    this.i += n;
    return out;
  }

  /** Discards the value for `kind`. */
  skip(kind: number): void {
    switch (kind) {
      case WIRE_U64:
        this.u64();
        return;
      case WIRE_BYTES:
        this.bytes();
        return;
      default:
        throw new PxbError("badWire", "bad wire kind");
    }
  }
}

/** Calls `f` for each field; unknown tags should be skipped via the reader.
 * `f` may call u64/bytes exactly once for the current field, or skip. */
export function walkFields(
  b: Buffer,
  f: (tag: number, kind: number, fr: FieldReader) => void,
): void {
  const fr = new FieldReader(b);
  while (!fr.done()) {
    const { tag, kind } = fr.nextField();
    const before = fr.position();
    f(tag, kind, fr);
    // If the callback neither consumed nor skipped, skip for them.
    if (fr.position() === before) {
      fr.skip(kind);
    }
  }
}
