// Frame codec: fixed 16-byte header + length-prefixed payload — a TS port of
// ext/rust/src/pxb/codec.rs. Readers never scan for delimiters; unknown frame
// types are skipped by reading payload_len bytes and ignoring the body.

import { Readable, Writable } from "node:stream";
import { HEADER_SIZE, MAGIC, MAX_PAYLOAD } from "./types.ts";

/** PXB protocol error codes. */
export type PxbErrorCode =
  | "io"
  | "badMagic"
  | "payloadTooLarge"
  | "shortBuffer"
  | "truncated"
  | "badWire"
  | "badTag"
  | "unexpectedFrame";

/** The error type thrown by every pxb decode / frame operation. */
export class PxbError extends Error {
  readonly code: PxbErrorCode;

  constructor(code: PxbErrorCode, message: string, options?: { cause?: unknown }) {
    super(`pxb: ${message}`, options);
    this.name = "PxbError";
    this.code = code;
  }
}

export function isPxbError(e: unknown): e is PxbError {
  return e instanceof PxbError;
}

/** Wraps an I/O failure so callers can branch on {@link PxbError.code}. */
export function ioError(cause: unknown): PxbError {
  if (isPxbError(cause)) return cause;
  const message = cause instanceof Error ? cause.message : String(cause);
  return new PxbError("io", `io: ${message}`, { cause });
}

/** The 16-byte frame prefix. */
export interface Header {
  type: number;
  flags: number;
  id: number;
  payload: number;
}

/** One complete message: header plus payload bytes. */
export interface Frame {
  header: Header;
  body: Buffer;
}

/** Encodes a header into a fresh 16-byte buffer. */
export function encodeHeader(h: Header): Buffer {
  const b = Buffer.allocUnsafe(HEADER_SIZE);
  MAGIC.copy(b, 0);
  b.writeUInt16LE(h.type, 4);
  b.writeUInt16LE(h.flags, 6);
  b.writeUInt32LE(h.id, 8);
  b.writeUInt32LE(h.payload, 12);
  return b;
}

/** Parses a 16-byte header. */
export function decodeHeader(src: Buffer): Header {
  if (src.length < HEADER_SIZE) {
    throw new PxbError("shortBuffer", "short buffer");
  }
  if (!src.subarray(0, 4).equals(MAGIC)) {
    throw new PxbError("badMagic", "bad magic");
  }
  const h: Header = {
    type: src.readUInt16LE(4),
    flags: src.readUInt16LE(6),
    id: src.readUInt32LE(8),
    payload: src.readUInt32LE(12),
  };
  if (h.payload > MAX_PAYLOAD) {
    throw new PxbError("payloadTooLarge", "payload too large");
  }
  return h;
}

/** Writes header + body to `w`, awaiting backpressure. The frame goes out as
 * one chunk so concurrent writers (fire-and-forget notifies, the run loop's
 * replies) can never interleave header and body. Resolves once the bytes are
 * handed to the OS. */
export async function writeFrame(
  w: Writable,
  type: number,
  flags: number,
  id: number,
  body: Buffer,
): Promise<void> {
  if (body.length > MAX_PAYLOAD) {
    throw new PxbError("payloadTooLarge", "payload too large");
  }
  const hdr = encodeHeader({ type, flags, id, payload: body.length });
  const frame = body.length > 0 ? Buffer.concat([hdr, body]) : hdr;
  await writeAll(w, frame);
}

function writeAll(w: Writable, chunk: Buffer): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const detach = (): void => {
      w.off("error", onError);
      w.off("drain", onDrain);
    };
    const onError = (e: Error): void => {
      detach();
      reject(ioError(e));
    };
    const onDrain = (): void => {
      detach();
      arm();
      pump();
    };
    const arm = (): void => {
      w.once("error", onError);
    };
    const pump = (): void => {
      if (w.write(chunk)) {
        detach();
        resolve();
      } // else: onDrain resumes; a stream failure fires onError
    };
    arm();
    pump();
  });
}

/** Pull-based exact-read source over a byte stream (extension stdin).
 * Concurrent readers queue: each `readExact` is fulfilled in call order. */
export class ByteSource {
  private chunks: Buffer[] = [];
  private total = 0;
  private eof = false;
  private error: Error | null = null;
  private waiters: Array<{ need: number; wake: () => void }> = [];

  constructor(stream: Readable) {
    stream.on("data", (chunk: Buffer) => {
      this.chunks.push(chunk);
      this.total += chunk.length;
      this.wake();
    });
    const close = (): void => {
      this.eof = true;
      this.wake();
    };
    stream.once("end", close);
    stream.once("error", (e: Error) => {
      this.error = ioError(e);
      this.wake();
    });
  }

  private wake(): void {
    for (;;) {
      const head = this.waiters[0];
      if (!head || !(this.total >= head.need || this.settled())) {
        return;
      }
      this.waiters.shift();
      head.wake();
    }
  }

  private settled(): boolean {
    return this.eof || this.error !== null;
  }

  /** Unconsumed bytes currently buffered. */
  get buffered(): number {
    return this.total;
  }

  private wait(need: number): Promise<void> {
    return new Promise((resolve, reject) => {
      this.waiters.push({
        need,
        wake: () => {
          if (this.error) {
            reject(this.error);
          } else {
            resolve();
          }
        },
      });
    });
  }

  /** Reads exactly `n` bytes; rejects with a PXB io error on EOF. */
  async readExact(n: number): Promise<Buffer> {
    if (n === 0) return Buffer.alloc(0);
    while (this.total < n && !this.settled()) {
      await this.wait(n);
    }
    if (this.total < n) {
      throw this.error ?? new PxbError("io", "unexpected EOF");
    }
    return this.take(n);
  }

  private take(n: number): Buffer {
    const first = this.chunks[0]!;
    if (first.length === n) {
      this.chunks.shift();
      this.total -= n;
      return first;
    }
    if (first.length > n) {
      this.chunks[0] = first.subarray(n);
      this.total -= n;
      return first.subarray(0, n);
    }
    const out = Buffer.allocUnsafe(n);
    let filled = 0;
    while (filled < n) {
      const chunk = this.chunks[0]!;
      const take = Math.min(chunk.length, n - filled);
      chunk.copy(out, filled, 0, take);
      filled += take;
      if (take === chunk.length) {
        this.chunks.shift();
      } else {
        this.chunks[0] = chunk.subarray(take);
      }
    }
    this.total -= n;
    return out;
  }
}

/** Reads one frame from `src` into an owned body buffer. */
export async function readFrame(src: ByteSource): Promise<Frame> {
  const hdr = await src.readExact(HEADER_SIZE);
  const header = decodeHeader(hdr);
  const body = header.payload > 0 ? await src.readExact(header.payload) : Buffer.alloc(0);
  return { header, body };
}
