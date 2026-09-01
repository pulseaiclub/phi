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

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();
const emptyBytes = new Uint8Array(0);

/** Write u16 LE into buf at offset. */
function writeU16(buf: Uint8Array, off: number, v: number): void {
  buf[off] = v & 0xff;
  buf[off + 1] = (v >>> 8) & 0xff;
}

/** Write u32 LE into buf at offset. */
function writeU32(buf: Uint8Array, off: number, v: number): void {
  buf[off] = v & 0xff;
  buf[off + 1] = (v >>> 8) & 0xff;
  buf[off + 2] = (v >>> 16) & 0xff;
  buf[off + 3] = (v >>> 24) & 0xff;
}

/** Write u64 LE (value must fit in JS safe integer / protocol u32 range). */
function writeU64(buf: Uint8Array, off: number, v: number): void {
  // Protocol values are u16/u32; high dword is always 0 on the hot path.
  writeU32(buf, off, v >>> 0);
  buf[off + 4] = 0;
  buf[off + 5] = 0;
  buf[off + 6] = 0;
  buf[off + 7] = 0;
}

function readU16(buf: Uint8Array, off: number): number {
  return buf[off]! | (buf[off + 1]! << 8);
}

function readU32(buf: Uint8Array, off: number): number {
  return (
    buf[off]! |
    (buf[off + 1]! << 8) |
    (buf[off + 2]! << 16) |
    (buf[off + 3]! << 24)
  ) >>> 0;
}

export function encodeHeader(dst: Uint8Array, h: Header): void {
  dst[0] = Magic[0]!;
  dst[1] = Magic[1]!;
  dst[2] = Magic[2]!;
  dst[3] = Magic[3]!;
  writeU16(dst, 4, h.type);
  writeU16(dst, 6, h.flags);
  writeU32(dst, 8, h.id);
  writeU32(dst, 12, h.payload);
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
  const payload = readU32(src, 12);
  if (payload > MaxPayload) {
    throw new PxbError("pxb: payload too large");
  }
  return {
    type: readU16(src, 4),
    flags: readU16(src, 6),
    id: readU32(src, 8),
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
  encodeHeader(out, { type, flags, id, payload: body.length });
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
    const n = this.len;
    if (n === 0) return emptyBytes;
    const out = new Uint8Array(n);
    out.set(this.buf.subarray(0, n));
    return out;
  }

  private ensure(n: number): void {
    const need = this.len + n;
    if (need <= this.buf.length) return;
    let cap = this.buf.length || 256;
    while (cap < need) cap <<= 1;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.len));
    this.buf = next;
  }

  private putHdr(tag: number, kind: number): void {
    this.ensure(3);
    const i = this.len;
    const b = this.buf;
    b[i] = tag & 0xff;
    b[i + 1] = (tag >>> 8) & 0xff;
    b[i + 2] = kind;
    this.len = i + 3;
  }

  putU64(tag: number, v: number): void {
    this.putHdr(tag, WireU64);
    this.ensure(8);
    writeU64(this.buf, this.len, v);
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
    this.ensure(4 + p.length);
    writeU32(this.buf, this.len, p.length);
    this.len += 4;
    this.buf.set(p, this.len);
    this.len += p.length;
  }

  putString(tag: number, s: string): void {
    if (s === "") return;
    this.putHdr(tag, WireBytes);
    // UTF-8 worst case: 4 bytes per JS code unit (surrogate pairs).
    const max = s.length * 4;
    this.ensure(4 + max);
    const dest = this.buf.subarray(this.len + 4, this.len + 4 + max);
    const { written } = textEncoder.encodeInto(s, dest);
    writeU32(this.buf, this.len, written);
    this.len += 4 + written;
  }

  putU16s(tag: number, vs: number[]): void {
    if (vs.length === 0) return;
    const innerLen = 2 + vs.length * 2;
    this.putHdr(tag, WireBytes);
    this.ensure(4 + innerLen);
    writeU32(this.buf, this.len, innerLen);
    this.len += 4;
    writeU16(this.buf, this.len, vs.length);
    this.len += 2;
    for (let i = 0; i < vs.length; i++) {
      writeU16(this.buf, this.len, vs[i]!);
      this.len += 2;
    }
  }
}

/** Module-level scratch writer — safe because encode* is sync and single-threaded. */
const scratchWriter = new FieldWriter(512);

/** Encode a payload with the shared scratch FieldWriter. */
export function encodeFields(fn: (fw: FieldWriter) => void): Uint8Array {
  scratchWriter.reset();
  fn(scratchWriter);
  return scratchWriter.toUint8Array();
}

export class FieldReader {
  private b: Uint8Array = emptyBytes;
  private i = 0;

  reset(b: Uint8Array): void {
    this.b = b;
    this.i = 0;
  }

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
    const b = this.b;
    const i = this.i;
    const tag = b[i]! | (b[i + 1]! << 8);
    const kind = b[i + 2]!;
    this.i = i + 3;
    if (kind !== WireU64 && kind !== WireBytes) {
      throw new PxbError("pxb: bad wire kind");
    }
    if (tag === 0) {
      throw new PxbError("pxb: bad field tag");
    }
    return { tag, kind };
  }

  /** Read WireU64 as number (protocol values fit in u32). */
  u64(): number {
    this.need(8);
    const v = readU32(this.b, this.i); // high dword unused on wire for protocol fields
    this.i += 8;
    return v;
  }

  bytes(): Uint8Array {
    this.need(4);
    const n = readU32(this.b, this.i);
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

  get offset(): number {
    return this.i;
  }
}

const scratchReader = new FieldReader();

export function walk(
  b: Uint8Array,
  fn: (tag: number, kind: number, fr: FieldReader) => void,
): void {
  const fr = scratchReader;
  fr.reset(b);
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
  return fr.u64();
}

export function takeBytes(kind: number, fr: FieldReader): Uint8Array {
  if (kind !== WireBytes) {
    fr.skip(kind);
    throw new PxbError("pxb: bad wire kind");
  }
  return fr.bytes();
}

export function takeString(kind: number, fr: FieldReader): string {
  return textDecoder.decode(takeBytes(kind, fr));
}

export function decodeU16s(p: Uint8Array): number[] {
  if (p.length < 2) throw new PxbError("pxb: truncated payload");
  const n = readU16(p, 0);
  const out = new Array<number>(n);
  let off = 2;
  for (let i = 0; i < n; i++) {
    if (off + 2 > p.length) throw new PxbError("pxb: truncated payload");
    out[i] = readU16(p, off);
    off += 2;
  }
  return out;
}

export { textDecoder, textEncoder, emptyBytes };
