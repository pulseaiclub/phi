// Unit tests for tagged-field encode/decode — mirrors
// ext/rust/src/pxb/fields.rs #[cfg(test)].

import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FieldReader, FieldWriter, WIRE_BYTES, WIRE_U64, walkFields } from "../src/pxb/fields.ts";
import { PxbError } from "../src/pxb/codec.ts";

describe("FieldWriter/FieldReader", () => {
  it("roundtrips writer values", () => {
    const w = new FieldWriter();
    w.putU64(1, 42);
    w.putU16(2, 7);
    w.putBool(3, true);
    w.putBool(4, false);
    w.putString(5, "hello");
    w.putBytes(6, Buffer.from([1, 2, 3]));
    w.putU16s(7, [5, 10]);

    const seen: Array<[number, number]> = [];
    walkFields(w.bytes(), (tag, kind, fr) => {
      const v =
        kind === WIRE_U64
          ? fr.u64()
          : kind === WIRE_BYTES
            ? fr.bytes().length
            : assert.fail("unreachable");
      seen.push([tag, v]);
    });
    assert.deepEqual(seen, [[1, 42], [2, 7], [3, 1], [4, 0], [5, 5], [6, 3], [7, 6]]);
  });

  it("omits empty values", () => {
    const w = new FieldWriter();
    w.putString(1, "");
    w.putBytes(2, new Uint8Array(0));
    w.putU16s(3, []);
    assert.equal(w.bytes().length, 0);
  });

  it("always writes non-opt numeric fields, even zero", () => {
    const w = new FieldWriter();
    w.putU32(1, 0);
    const { kind } = new FieldReader(w.bytes()).nextField();
    assert.equal(kind, WIRE_U64);
  });

  it("skips unknown tags", () => {
    const w = new FieldWriter();
    w.putU64(1, 9);
    w.putString(128, "experimental");
    w.putString(2, "known");

    let name = "";
    walkFields(w.bytes(), (tag, kind, fr) => {
      if (tag === 2) {
        name = fr.bytes().toString("utf8");
      } else {
        fr.skip(kind);
      }
    });
    assert.equal(name, "known");
  });

  it("rejects bad wire kinds", () => {
    const raw = Buffer.from([1, 0, 7]); // tag 1, unknown kind 7, no value
    assert.throws(() => walkFields(raw, () => {}), (e: PxbError) => e.code === "badWire");
  });

  it("rejects truncated payloads", () => {
    const w = new FieldWriter();
    w.putU64(1, 42);
    const bytes = w.bytes();
    assert.throws(
      () => walkFields(bytes.subarray(0, 3), () => {}),
      (e: PxbError) => e.code === "truncated",
    );
  });

  it("rejects tag 0", () => {
    const raw = Buffer.from([0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0]);
    assert.throws(() => walkFields(raw, () => {}), (e: PxbError) => e.code === "badTag");
  });

  it("grows beyond the initial buffer", () => {
    const w = new FieldWriter();
    const big = Buffer.alloc(1000, 0xab);
    w.putBytes(1, big);
    const fr = new FieldReader(w.bytes());
    assert.deepEqual(fr.nextField(), { tag: 1, kind: WIRE_BYTES });
    assert.deepEqual(fr.bytes(), big);
  });
});
