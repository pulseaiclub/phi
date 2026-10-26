//! Frame codec: fixed 16-byte header + length-prefixed payload. Readers never
//! scan for delimiters; unknown frame types are skipped by reading
//! `payload_len` bytes and ignoring the body.

use std::fmt;
use std::io::{self, Read, Write};

use super::types::{HEADER_SIZE, MAGIC, MAX_PAYLOAD, MAX_STREAM_PAYLOAD};

/// PXB protocol errors.
#[derive(Debug)]
pub enum Error {
    Io(io::Error),
    BadMagic,
    PayloadTooLarge,
    ShortBuffer,
    Truncated,
    BadWire,
    BadTag,
    /// Received a frame of an unexpected type (e.g. not the expected
    /// handshake reply).
    UnexpectedFrame {
        want: &'static str,
        got: u16,
    },
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Io(e) => write!(f, "pxb: io: {e}"),
            Error::BadMagic => f.write_str("pxb: bad magic"),
            Error::PayloadTooLarge => f.write_str("pxb: payload too large"),
            Error::ShortBuffer => f.write_str("pxb: short buffer"),
            Error::Truncated => f.write_str("pxb: truncated payload"),
            Error::BadWire => f.write_str("pxb: bad wire kind"),
            Error::BadTag => f.write_str("pxb: bad field tag"),
            Error::UnexpectedFrame { want, got } => {
                write!(f, "pxb: expected {want} frame, got {got}")
            }
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Io(e) => Some(e),
            _ => None,
        }
    }
}

impl From<io::Error> for Error {
    fn from(e: io::Error) -> Self {
        Error::Io(e)
    }
}

/// The 16-byte frame prefix.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Header {
    pub typ: u16,
    pub flags: u16,
    pub id: u32,
    pub payload: u32,
}

/// Encodes a header into a fixed-size array.
pub fn encode_header(h: Header) -> [u8; HEADER_SIZE] {
    let mut b = [0u8; HEADER_SIZE];
    b[0..4].copy_from_slice(&MAGIC);
    b[4..6].copy_from_slice(&h.typ.to_le_bytes());
    b[6..8].copy_from_slice(&h.flags.to_le_bytes());
    b[8..12].copy_from_slice(&h.id.to_le_bytes());
    b[12..16].copy_from_slice(&h.payload.to_le_bytes());
    b
}

/// Parses a 16-byte header.
pub fn decode_header(src: &[u8]) -> Result<Header, Error> {
    if src.len() < HEADER_SIZE {
        return Err(Error::ShortBuffer);
    }
    if src[0..4] != MAGIC {
        return Err(Error::BadMagic);
    }
    let h = Header {
        typ: u16::from_le_bytes([src[4], src[5]]),
        flags: u16::from_le_bytes([src[6], src[7]]),
        id: u32::from_le_bytes([src[8], src[9], src[10], src[11]]),
        payload: u32::from_le_bytes([src[12], src[13], src[14], src[15]]),
    };
    if h.payload as usize > MAX_STREAM_PAYLOAD {
        return Err(Error::PayloadTooLarge);
    }
    Ok(h)
}

/// One complete message: header plus payload bytes.
#[derive(Debug)]
pub struct Frame {
    pub header: Header,
    pub body: Vec<u8>,
}

/// Writes header + body to `w`, then flushes. Stdout is line-buffered, and
/// PXB frames contain no newlines, so skipping the flush would leave small
/// frames stuck in the buffer.
pub fn write_frame(
    w: &mut impl Write,
    typ: u16,
    flags: u16,
    id: u32,
    body: &[u8],
) -> Result<(), Error> {
    if body.len() > MAX_PAYLOAD {
        return Err(Error::PayloadTooLarge);
    }
    let hdr = encode_header(Header {
        typ,
        flags,
        id,
        payload: body.len() as u32,
    });
    w.write_all(&hdr)?;
    w.write_all(body)?;
    w.flush()?;
    Ok(())
}

/// Reads one frame from `r` into an owned body.
pub fn read_frame(r: &mut impl Read) -> Result<Frame, Error> {
    let mut hdr = [0u8; HEADER_SIZE];
    r.read_exact(&mut hdr)?;
    let header = decode_header(&hdr)?;
    let mut body = vec![0u8; header.payload as usize];
    r.read_exact(&mut body)?;
    Ok(Frame { header, body })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    #[test]
    fn header_roundtrip() {
        let h = Header {
            typ: 42,
            flags: 2,
            id: 7,
            payload: 1024,
        };
        let b = encode_header(h);
        assert_eq!(decode_header(&b).unwrap(), h);
    }

    #[test]
    fn header_rejects_bad_input() {
        assert!(matches!(
            decode_header(&[0; 4]).unwrap_err(),
            Error::ShortBuffer
        ));
        assert!(matches!(
            decode_header(&[0; HEADER_SIZE]).unwrap_err(),
            Error::BadMagic
        ));

        let mut b = encode_header(Header {
            typ: 1,
            flags: 0,
            id: 0,
            payload: (MAX_STREAM_PAYLOAD as u32) + 1,
        });
        let err = decode_header(&b).unwrap_err();
        assert!(matches!(err, Error::PayloadTooLarge));
        // exactly the ceiling is receivable
        b[12..16].copy_from_slice(&(MAX_STREAM_PAYLOAD as u32).to_le_bytes());
        assert!(decode_header(&b).is_ok());
    }

    #[test]
    fn frame_roundtrip_via_stream() {
        let body = b"hello".to_vec();
        let mut buf = Vec::new();
        write_frame(&mut buf, 1, 0, 0, &body).unwrap();

        let mut cur = Cursor::new(buf);
        let f = read_frame(&mut cur).unwrap();
        assert_eq!(f.header.typ, 1);
        assert_eq!(f.body, body);

        // The cursor consumed exactly header + body.
        assert_eq!(cur.position(), (HEADER_SIZE + body.len()) as u64);
    }

    #[test]
    fn empty_body_roundtrip() {
        let mut buf = Vec::new();
        write_frame(&mut buf, 9, 0, 0, &[]).unwrap();
        let f = read_frame(&mut Cursor::new(buf)).unwrap();
        assert_eq!(f.header.typ, 9);
        assert_eq!(f.header.payload, 0);
        assert!(f.body.is_empty());
    }
}
