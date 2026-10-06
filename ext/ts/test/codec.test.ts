// Unit tests for the frame codec and byte source — mirrors
// ext/rust/src/pxb/codec.rs #[cfg(test)].

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { PassThrough } from "node:stream";
import {
  ByteSource,
  PxbError,
  decodeHeader,
  encodeHeader,
  readFrame,
  writeFrame,
} from "../src/pxb/codec.ts";
import { HEADER_SIZE, MAX_PAYLOAD } from "../src/pxb/types.ts";

describe("header", () => {
  it("roundtrips", () => {
    const h = { type: 42, flags: 2, id: 7, payload: 1024 };
    assert.deepEqual(decodeHeader(encodeHeader(h)), h);
  });

  it("rejects bad input", () => {
    assert.throws(() => decodeHeader(Buffer.alloc(4)), (e: PxbError) => e.code === "shortBuffer");
    assert.throws(() => decodeHeader(Buffer.alloc(HEADER_SIZE)), (e: PxbError) => e.code === "badMagic");

    const b = encodeHeader({ type: 1, flags: 0, id: 0, payload: 1 << 30 });
    b.writeUInt32LE(MAX_PAYLOAD + 1, 12);
    assert.throws(() => decodeHeader(b), (e: PxbError) => e.code === "payloadTooLarge");
  });
});

describe("frames", () => {
  it("roundtrip via stream", async () => {
    const body = Buffer.from("hello");
    const pipe = new PassThrough();
    const src = new ByteSource(pipe);
    const pending = readFrame(src);
    await writeFrame(pipe, 1, 0, 0, body);
    const f = await pending;
    assert.equal(f.header.type, 1);
    assert.deepEqual(f.body, body);
  });

  it("empty body roundtrip", async () => {
    const pipe = new PassThrough();
    const src = new ByteSource(pipe);
    const pending = readFrame(src);
    await writeFrame(pipe, 9, 0, 0, Buffer.alloc(0));
    const f = await pending;
    assert.equal(f.header.type, 9);
    assert.equal(f.header.payload, 0);
    assert.equal(f.body.length, 0);
  });

  it("rejects oversized body on write", async () => {
    const pipe = new PassThrough();
    await assert.rejects(
      writeFrame(pipe, 1, 0, 0, Buffer.alloc(MAX_PAYLOAD + 1)),
      (e: PxbError) => e.code === "payloadTooLarge",
    );
  });

  it("never interleaves concurrent frame writes", async () => {
    // Fire-and-forget notifications race the run loop's replies; each frame
    // must land whole or the stream desyncs.
    const pipe = new PassThrough();
    const src = new ByteSource(pipe);
    const a = Buffer.from("aaaa");
    const b = Buffer.from("bbbb");
    await Promise.all([writeFrame(pipe, 1, 0, 1, a), writeFrame(pipe, 2, 0, 2, b)]);
    const first = await readFrame(src);
    const second = await readFrame(src);
    assert.equal(first.header.type, 1);
    assert.deepEqual(first.body, a);
    assert.equal(second.header.type, 2);
    assert.deepEqual(second.body, b);
  });
});

describe("ByteSource", () => {
  it("reads exact spans across chunk boundaries", async () => {
    const pipe = new PassThrough();
    const src = new ByteSource(pipe);
    pipe.write(Buffer.from([1, 2]));
    const pending = src.readExact(5);
    pipe.write(Buffer.from([3, 4, 5, 6]));
    assert.deepEqual(await pending, Buffer.from([1, 2, 3, 4, 5]));
    assert.deepEqual(await src.readExact(1), Buffer.from([6]));
  });

  it("rejects on EOF with a pxb io error", async () => {
    const pipe = new PassThrough();
    const src = new ByteSource(pipe);
    pipe.end(Buffer.from([1]));
    await assert.rejects(src.readExact(2), (e: PxbError) => e.code === "io");
  });

  it("returns a zero-length read without touching the stream", async () => {
    const pipe = new PassThrough();
    const src = new ByteSource(pipe);
    assert.equal((await src.readExact(0)).length, 0);
    pipe.end();
  });
});
