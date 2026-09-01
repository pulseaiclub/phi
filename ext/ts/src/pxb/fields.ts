import {
  HeaderSize,
  Magic,
  MaxPayload,
  WireBytes,
  WireU64,
} from "./types.js";

export class PxbError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PxbError";
  }
}

export interface Header {
  type: number;
  flags: number;
  id: number;
  payload: number;
}

export interface Frame {
  type: number;
  flags: number;
  id: number;
  body: Uint8Array;
}

export function encodeHeader(dst: Uint8Array, h: Header): void {
  dst.set(Magic, 0);
  const view = new DataView(dst.buffer, dst.byteOffset, HeaderSize);
  view.setUint16(4, h.type, true);
  view.setUint16(6, h.flags, true);
  view.setUint32(8, h.id, true);
  view.setUint32(12, h.payload, true);
}

export function decodeHeader(src: Uint8Array): Header {
  if (src.length < HeaderSize) {
    throw new PxbError("pxb: short buffer");
  }
  if (
    src[0] !== Magic[0] ||
    src[1] !== Magic[1] ||
    src[2] !== Magic[2] ||
    src[3] !== Magic[3]
  ) {
    throw new PxbError("pxb: bad magic");
  }
  const view = new DataView(src.buffer, src.byteOffset, HeaderSize);
  const payload = view.getUint32(12, true);
  if (payload > MaxPayload) {
    throw new PxbError("pxb: payload too large");
  }
  return {
    type: view.getUint16(4, true),
    flags: view.getUint16(6, true),
    id: view.getUint32(8, true),
    payload,
  };
}

export function encodeFrame(
  type: number,
  flags: number,
  id: number,
  body: Uint8Array,
): Uint8Array {
  if (body.length > MaxPayload) {
    throw new PxbError("pxb: payload too large");
  }
  const out = new Uint8Array(HeaderSize + body.length);
  encodeHeader(out.subarray(0, HeaderSize), {
    type,
    flags,
    id,
    payload: body.length,
  });
  out.set(body, HeaderSize);
  return out;
}

/** Builds tagged-field payloads with buffer reuse. */
export class FieldWriter {
  private buf: Uint8Array;
  private len = 0;

  constructor(capacity = 256) {
    this.buf = new Uint8Array(capacity);
  }

  reset(): void {
    this.len = 0;
  }

  bytes(): Uint8Array {
    return this.buf.subarray(0, this.len);
  }

  /** Returns an owned copy of the current payload. */
  toUint8Array(): Uint8Array {
    return this.buf.slice(0, this.len);
  }

  private grow(n: number): void {
    if (this.len + n <= this.buf.length) return;
    const next = new Uint8Array(Math.max(this.buf.length * 2, this.len + n + 256));
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  private putHdr(tag: number, kind: number): void {
    this.grow(3);
    this.buf[this.len] = tag & 0xff;
    this.buf[this.len + 1] = (tag >> 8) & 0xff;
    this.buf[this.len + 2] = kind;
    this.len += 3;
  }

  putU64(tag: number, v: number | bigint): void {
    this.putHdr(tag, WireU64);
    this.grow(8);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 8);
    view.setBigUint64(0, BigInt(v), true);
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

  putBytes(tag: number, p: Uint8Array): void {
    if (p.length === 0) return;
    this.putHdr(tag, WireBytes);
    this.grow(4 + p.length);
    const view = new DataView(this.buf.buffer, this.buf.byteOffset + this.len, 4);
    view.setUint32(0, p.length, true);
    this.len += 4;
    this.buf.set(p, this.len);
    this.len += p.length;
  }

  putString(tag: number, s: string): void {
    if (s === "") return;
    this.putBytes(tag, new TextEncoder().encode(s));
  }

  putU16s(tag: number, vs: number[]): void {
    if (vs.length === 0) return;
    const inner = new Uint8Array(2 + vs.length * 2);
    const view = new DataView(inner.buffer);
    view.setUint16(0, vs.length, true);
    for (let i = 0; i < vs.length; i++) {
      view.setUint16(2 + i * 2, vs[i]!, true);
    }
    this.putBytes(tag, inner);
  }
}

export class FieldReader {
  private i = 0;

  constructor(private readonly b: Uint8Array) {}

  done(): boolean {
    return this.i >= this.b.length;
  }

  private need(n: number): void {
    if (this.b.length - this.i < n) {
      throw new PxbError("pxb: truncated payload");
    }
  }

  next(): { tag: number; kind: number } {
    if (this.done()) {
      throw new PxbError("pxb: truncated payload");
    }
    this.need(3);
    const tag = this.b[this.i]! | (this.b[this.i + 1]! << 8);
    const kind = this.b[this.i + 2]!;
    this.i += 3;
    if (kind !== WireU64 && kind !== WireBytes) {
      throw new PxbError("pxb: bad wire kind");
    }
    if (tag === 0) {
      throw new PxbError("pxb: bad field tag");
    }
    return { tag, kind };
  }

  u64(): bigint {
    this.need(8);
    const view = new DataView(this.b.buffer, this.b.byteOffset + this.i, 8);
    const v = view.getBigUint64(0, true);
    this.i += 8;
    return v;
  }

  bytes(): Uint8Array {
    this.need(4);
    const n =
      this.b[this.i]! |
      (this.b[this.i + 1]! << 8) |
      (this.b[this.i + 2]! << 16) |
      (this.b[this.i + 3]! << 24);
    this.i += 4;
    this.need(n);
    const out = this.b.subarray(this.i, this.i + n);
    this.i += n;
    return out;
  }

  skip(kind: number): void {
    if (kind === WireU64) {
      this.u64();
    } else if (kind === WireBytes) {
      this.bytes();
    } else {
      throw new PxbError("pxb: bad wire kind");
    }
  }

  /** Current read offset (for Walk auto-skip). */
  get offset(): number {
    return this.i;
  }
}

export function walk(
  b: Uint8Array,
  fn: (tag: number, kind: number, fr: FieldReader) => void,
): void {
  const fr = new FieldReader(b);
  while (!fr.done()) {
    const { tag, kind } = fr.next();
    const before = fr.offset;
    fn(tag, kind, fr);
    if (fr.offset === before) {
      fr.skip(kind);
    }
  }
}

export function takeU64(kind: number, fr: FieldReader): number {
  if (kind !== WireU64) {
    fr.skip(kind);
    throw new PxbError("pxb: bad wire kind");
  }
  return Number(fr.u64());
}

export function takeBytes(kind: number, fr: FieldReader): Uint8Array {
  if (kind !== WireBytes) {
    fr.skip(kind);
    throw new PxbError("pxb: bad wire kind");
  }
  return fr.bytes();
}

export function takeString(kind: number, fr: FieldReader): string {
  return new TextDecoder().decode(takeBytes(kind, fr));
}

const textDecoder = new TextDecoder();
const textEncoder = new TextEncoder();

export function decodeU16s(p: Uint8Array): number[] {
  if (p.length < 2) throw new PxbError("pxb: truncated payload");
  const view = new DataView(p.buffer, p.byteOffset, p.byteLength);
  const n = view.getUint16(0, true);
  const out: number[] = [];
  let off = 2;
  for (let i = 0; i < n; i++) {
    if (off + 2 > p.length) throw new PxbError("pxb: truncated payload");
    out.push(view.getUint16(off, true));
    off += 2;
  }
  return out;
}

export { textDecoder, textEncoder };
