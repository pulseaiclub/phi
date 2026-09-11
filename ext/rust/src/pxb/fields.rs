//! Tagged-field payloads (protobuf-style, fixed-width).
//!
//! Layout per field: `tag u16 | kind u8 | value…`. Only two wire kinds exist,
//! so unknown fields are always skippable without a schema. Decoders must skip
//! unknown tags; omitting a tag means the zero value.
//!
//! Evolution rules (see `doc/extensions.md`):
//! - new fields take a new tag number, never reuse a tag;
//! - empty strings/blobs are omitted on encode;
//! - experimental tags use 128+ and must remain skippable.

use super::codec::{Error, Error::*};

/// 8-byte little-endian (u16/u32/bool/event codes share this kind).
pub const WIRE_U64: u8 = 1;
/// u32 length + bytes.
pub const WIRE_BYTES: u8 = 2;

/// Builds a tagged-field payload.
pub struct FieldWriter {
    buf: Vec<u8>,
}

impl Default for FieldWriter {
    fn default() -> Self {
        Self::new()
    }
}

impl FieldWriter {
    pub fn new() -> Self {
        Self {
            buf: Vec::with_capacity(32),
        }
    }

    /// Returns the encoded payload.
    pub fn bytes(&self) -> &[u8] {
        &self.buf
    }

    pub fn into_vec(self) -> Vec<u8> {
        self.buf
    }

    fn put_hdr(&mut self, tag: u16, kind: u8) {
        self.buf.extend_from_slice(&tag.to_le_bytes());
        self.buf.push(kind);
    }

    /// Writes an 8-byte integer field. Always written, even when zero —
    /// decoders rely on the tag being present for bool-ish fields.
    pub fn put_u64(&mut self, tag: u16, v: u64) {
        self.put_hdr(tag, WIRE_U64);
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn put_u16(&mut self, tag: u16, v: u16) {
        self.put_u64(tag, u64::from(v));
    }

    pub fn put_u32(&mut self, tag: u16, v: u32) {
        self.put_u64(tag, u64::from(v));
    }

    pub fn put_bool(&mut self, tag: u16, v: bool) {
        self.put_u64(tag, u64::from(v));
    }

    /// Writes a length-prefixed blob. Empty blobs are omitted so decoders stay
    /// compact on the hot path.
    pub fn put_bytes(&mut self, tag: u16, p: &[u8]) {
        if p.is_empty() {
            return;
        }
        self.put_hdr(tag, WIRE_BYTES);
        self.buf.extend_from_slice(&(p.len() as u32).to_le_bytes());
        self.buf.extend_from_slice(p);
    }

    pub fn put_string(&mut self, tag: u16, s: &str) {
        if s.is_empty() {
            return;
        }
        self.put_bytes(tag, s.as_bytes());
    }

    /// Writes a packed list as `WireBytes`: `u16 count` + values.
    pub fn put_u16s(&mut self, tag: u16, vs: &[u16]) {
        if vs.is_empty() {
            return;
        }
        let mut inner = Vec::with_capacity(2 + vs.len() * 2);
        inner.extend_from_slice(&(vs.len() as u16).to_le_bytes());
        inner.extend(vs.iter().flat_map(|v| v.to_le_bytes()));
        self.put_bytes(tag, &inner);
    }
}

/// Walks a tagged-field payload.
pub struct FieldReader<'a> {
    b: &'a [u8],
    i: usize,
}

impl<'a> FieldReader<'a> {
    pub fn new(b: &'a [u8]) -> Self {
        Self { b, i: 0 }
    }

    pub fn done(&self) -> bool {
        self.i >= self.b.len()
    }

    fn need(&self, n: usize) -> Result<(), Error> {
        if self.b.len() - self.i < n {
            return Err(Truncated);
        }
        Ok(())
    }

    /// Returns the next field tag and wire kind.
    pub fn next_field(&mut self) -> Result<(u16, u8), Error> {
        if self.done() {
            return Err(Truncated);
        }
        self.need(3)?;
        let tag = u16::from_le_bytes([self.b[self.i], self.b[self.i + 1]]);
        let kind = self.b[self.i + 2];
        self.i += 3;
        if kind != WIRE_U64 && kind != WIRE_BYTES {
            return Err(BadWire);
        }
        if tag == 0 {
            return Err(BadTag);
        }
        Ok((tag, kind))
    }

    /// Reads a `WireU64` value (call after `next` returned `WireU64`).
    pub fn u64(&mut self) -> Result<u64, Error> {
        self.need(8)?;
        let v = u64::from_le_bytes(self.b[self.i..self.i + 8].try_into().unwrap());
        self.i += 8;
        Ok(v)
    }

    /// Reads a `WireBytes` value (call after `next` returned `WireBytes`).
    /// The returned slice aliases the payload.
    pub fn bytes(&mut self) -> Result<&'a [u8], Error> {
        self.need(4)?;
        let n = u32::from_le_bytes(self.b[self.i..self.i + 4].try_into().unwrap()) as usize;
        self.i += 4;
        self.need(n)?;
        let out = &self.b[self.i..self.i + n];
        self.i += n;
        Ok(out)
    }

    /// Discards the value for `kind`.
    pub fn skip(&mut self, kind: u8) -> Result<(), Error> {
        match kind {
            WIRE_U64 => {
                self.u64()?;
                Ok(())
            }
            WIRE_BYTES => {
                self.bytes()?;
                Ok(())
            }
            _ => Err(BadWire),
        }
    }
}

/// Calls `f` for each field; unknown tags should be skipped via the reader.
/// `f` may call `u64`/`bytes` exactly once for the current field, or `skip`.
pub fn walk_fields(
    b: &[u8],
    mut f: impl FnMut(u16, u8, &mut FieldReader<'_>) -> Result<(), Error>,
) -> Result<(), Error> {
    let mut fr = FieldReader::new(b);
    while !fr.done() {
        let (tag, kind) = fr.next_field()?;
        let before = fr.i;
        f(tag, kind, &mut fr)?;
        // If the callback neither consumed nor skipped, skip for them.
        if fr.i == before {
            fr.skip(kind)?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn writer_reader_roundtrip() {
        let mut w = FieldWriter::new();
        w.put_u64(1, 42);
        w.put_u16(2, 7);
        w.put_bool(3, true);
        w.put_bool(4, false);
        w.put_string(5, "hello");
        w.put_bytes(6, &[1, 2, 3]);
        w.put_u16s(7, &[5, 10]);

        let mut seen = Vec::new();
        walk_fields(w.bytes(), |tag, kind, fr| {
            let v = match kind {
                WIRE_U64 => fr.u64().unwrap(),
                WIRE_BYTES => {
                    let b = fr.bytes().unwrap();
                    b.len() as u64
                }
                _ => unreachable!(),
            };
            seen.push((tag, v));
            Ok(())
        })
        .unwrap();
        assert_eq!(
            seen,
            vec![(1, 42), (2, 7), (3, 1), (4, 0), (5, 5), (6, 3), (7, 6)]
        );
    }

    #[test]
    fn empty_values_are_omitted() {
        let mut w = FieldWriter::new();
        w.put_string(1, "");
        w.put_bytes(2, &[]);
        w.put_u16s(3, &[]);
        assert!(w.bytes().is_empty());
    }

    #[test]
    fn unknown_tags_are_skippable() {
        let mut w = FieldWriter::new();
        w.put_u64(1, 9);
        w.put_string(128, "experimental");
        w.put_string(2, "known");

        let mut name = String::new();
        walk_fields(w.bytes(), |tag, kind, fr| {
            match tag {
                2 => name = String::from_utf8_lossy(fr.bytes().unwrap()).into_owned(),
                _ => fr.skip(kind)?,
            }
            Ok(())
        })
        .unwrap();
        assert_eq!(name, "known");
    }

    #[test]
    fn rejects_bad_wire_kinds() {
        let mut w = FieldWriter::new();
        w.buf = vec![1, 0, 7]; // tag 1, unknown kind 7, no value
        let err = walk_fields(w.bytes(), |_t, _k, _fr| Ok(())).unwrap_err();
        assert!(matches!(err, Error::BadWire));
    }

    #[test]
    fn truncated_payload_errors() {
        let mut w = FieldWriter::new();
        w.put_u64(1, 42);
        let bytes = w.into_vec();
        let err = walk_fields(&bytes[..3], |_t, _k, _fr| Ok(())).unwrap_err();
        assert!(matches!(err, Error::Truncated));
    }
}
