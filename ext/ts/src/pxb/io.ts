import { Readable, Writable } from "node:stream";
import { decodeHeader, encodeHeader, type Frame } from "./fields.js";
import { HeaderSize, MaxPayload } from "./types.js";

/** Async frame reader over a Node readable stream (stdin). */
export class FrameReader {
  private buf = Buffer.alloc(0);
  private readonly pending: Buffer[] = [];
  private wait: (() => void) | null = null;
  private ended = false;
  private err: Error | null = null;

  constructor(private readonly stream: Readable) {
    stream.on("data", (chunk: Buffer) => {
      this.pending.push(chunk);
      this.wait?.();
      this.wait = null;
    });
    stream.on("end", () => {
      this.ended = true;
      this.wait?.();
      this.wait = null;
    });
    stream.on("error", (e: Error) => {
      this.err = e;
      this.wait?.();
      this.wait = null;
    });
  }

  private async fill(need: number): Promise<void> {
    while (this.buf.length < need) {
      if (this.err) throw this.err;
      if (this.pending.length > 0) {
        this.buf = Buffer.concat([this.buf, ...this.pending]);
        this.pending.length = 0;
        continue;
      }
      if (this.ended) {
        throw Object.assign(new Error("pxb: unexpected EOF"), { code: "EOF" });
      }
      await new Promise<void>((resolve) => {
        this.wait = resolve;
      });
    }
  }

  async read(): Promise<Frame> {
    await this.fill(HeaderSize);
    const hdr = decodeHeader(this.buf.subarray(0, HeaderSize));
    const total = HeaderSize + hdr.payload;
    if (hdr.payload > MaxPayload) {
      throw new Error("pxb: payload too large");
    }
    await this.fill(total);
    // Copy body so it survives the next read (matches Go CloneBody semantics).
    const body = Buffer.from(this.buf.subarray(HeaderSize, total));
    this.buf = this.buf.subarray(total);
    return { type: hdr.type, flags: hdr.flags, id: hdr.id, body };
  }
}

/** Frame writer over a Node writable stream (stdout). */
export class FrameWriter {
  private readonly hdr = Buffer.allocUnsafe(HeaderSize);

  constructor(private readonly stream: Writable) {}

  write(type: number, flags: number, id: number, body: Uint8Array): void {
    if (body.length > MaxPayload) {
      throw new Error("pxb: payload too large");
    }
    encodeHeader(this.hdr, { type, flags, id, payload: body.length });
    // Two writes: reuse header scratch, avoid allocating a combined frame.
    this.stream.write(this.hdr);
    if (body.length > 0) {
      this.stream.write(
        Buffer.from(body.buffer, body.byteOffset, body.byteLength),
      );
    }
  }
}
