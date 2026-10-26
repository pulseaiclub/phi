package pxb

import (
	"encoding/binary"
	"errors"
)

// Tagged payload wire kinds. Only these two exist so unknown fields are
// always skippable without a schema.
const (
	wireU64   uint8 = 1 // 8-byte little-endian (u16/u32/bool/event codes)
	wireBytes uint8 = 2 // u32 length + bytes
)

// Encoded field sizes: a 3-byte field header (tag u16 + kind u8) plus the value,
// or plus a u32 length for blobs. Generated encoders sum these to reserve the
// exact payload, so they must stay in step with the put* methods below.
const (
	u64FieldSize   = 3 + 8
	blobHeaderSize = 3 + 4
)

var (
	ErrBadWire = errors.New("pxb: bad wire kind")
	ErrBadTag  = errors.New("pxb: bad field tag")
)

// fieldWriter builds a tagged-field payload (protobuf-style, fixed-width).
//
// Layout per field:
//
//	tag u16 | kind u8 | value…
//
// Unknown tags are skipped by kind. New fields must use new tag numbers;
// never reuse or reorder meaning of an existing tag. Package-internal: the
// generated encoders in msg_gen.go are the only intended callers.
type fieldWriter struct {
	b []byte
}

// bytes returns the encoded payload.
func (fw *fieldWriter) bytes() []byte { return fw.b }

// sizedWriter starts a writer with n bytes reserved. An encoder that knows its
// exact payload size allocates once and never grows.
func sizedWriter(n int) fieldWriter { return fieldWriter{b: make([]byte, 0, n)} }

func (fw *fieldWriter) putHdr(tag uint16, kind uint8) {
	var tmp [3]byte
	binary.LittleEndian.PutUint16(tmp[0:2], tag)
	tmp[2] = kind
	fw.b = append(fw.b, tmp[:]...)
}

// putU64 writes an 8-byte integer field.
func (fw *fieldWriter) putU64(tag uint16, v uint64) {
	fw.putHdr(tag, wireU64)
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	fw.b = append(fw.b, tmp[:]...)
}

// putU16 writes v as wireU64.
func (fw *fieldWriter) putU16(tag, v uint16) { fw.putU64(tag, uint64(v)) }

// putU32 writes v as wireU64.
func (fw *fieldWriter) putU32(tag uint16, v uint32) { fw.putU64(tag, uint64(v)) }

// putBool writes v as wireU64 0/1.
func (fw *fieldWriter) putBool(tag uint16, v bool) {
	if v {
		fw.putU64(tag, 1)
	} else {
		fw.putU64(tag, 0)
	}
}

// putBytes writes a length-prefixed blob. Empty blobs are omitted so
// decoders stay compact on the hot path.
func (fw *fieldWriter) putBytes(tag uint16, p []byte) {
	if len(p) == 0 {
		return
	}
	fw.putHdr(tag, wireBytes)
	n := uint32(len(p)) //nolint:gosec // G115: blob length bounded by MaxPayload on the frame path
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], n)
	fw.b = append(fw.b, tmp[:]...)
	fw.b = append(fw.b, p...)
}

// putString writes s as wireBytes. Empty strings are omitted.
func (fw *fieldWriter) putString(tag uint16, s string) {
	if s == "" {
		return
	}
	fw.putBytes(tag, []byte(s))
}

// putU16s writes a packed list as wireBytes: u16 count + u16 values, packed in
// place so encoding stays single-allocation.
func (fw *fieldWriter) putU16s(tag uint16, vs []uint16) {
	if len(vs) == 0 {
		return
	}
	fw.putHdr(tag, wireBytes)
	fw.b = binary.LittleEndian.AppendUint32(fw.b, uint32(2+len(vs)*2)) //nolint:gosec // G115: bounded by MaxPayload
	fw.b = binary.LittleEndian.AppendUint16(fw.b, uint16(len(vs)))     //nolint:gosec // G115: bounded by MaxPayload
	for _, v := range vs {
		fw.b = binary.LittleEndian.AppendUint16(fw.b, v)
	}
}

// strSize is the encoded size of a string field, or 0 when it is omitted.
func strSize(s string) int {
	if s == "" {
		return 0
	}
	return blobHeaderSize + len(s)
}

// blobSize is strSize for opaque bytes.
func blobSize(p []byte) int {
	if len(p) == 0 {
		return 0
	}
	return blobHeaderSize + len(p)
}

// u16sSize is the encoded size of a packed u16 list (count + values).
func u16sSize(vs []uint16) int {
	if len(vs) == 0 {
		return 0
	}
	return blobHeaderSize + 2 + len(vs)*2
}

// fieldReader walks a tagged-field payload.
type fieldReader struct {
	b []byte
	i int
}

func (fr *fieldReader) done() bool { return fr.i >= len(fr.b) }

func (fr *fieldReader) need(n int) error {
	if len(fr.b)-fr.i < n {
		return ErrTruncated
	}
	return nil
}

// next returns the next field tag and wire kind.
func (fr *fieldReader) next() (tag uint16, kind uint8, err error) {
	if fr.done() {
		return 0, 0, ErrTruncated
	}
	if err := fr.need(3); err != nil {
		return 0, 0, err
	}
	tag = uint16(fr.b[fr.i]) | uint16(fr.b[fr.i+1])<<8
	kind = fr.b[fr.i+2]
	fr.i += 3
	if kind != wireU64 && kind != wireBytes {
		return tag, kind, ErrBadWire
	}
	if tag == 0 {
		return tag, kind, ErrBadTag
	}
	return tag, kind, nil
}

// u64 reads a wireU64 value (call after next returned wireU64).
func (fr *fieldReader) u64() (uint64, error) {
	if err := fr.need(8); err != nil {
		return 0, err
	}
	b := fr.b[fr.i:]
	fr.i += 8
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, nil
}

// bytes reads a wireBytes value (call after next returned wireBytes).
// The returned slice aliases the underlying buffer.
func (fr *fieldReader) bytes() ([]byte, error) {
	if err := fr.need(4); err != nil {
		return nil, err
	}
	n := uint32(fr.b[fr.i]) | uint32(fr.b[fr.i+1])<<8 | uint32(fr.b[fr.i+2])<<16 | uint32(fr.b[fr.i+3])<<24
	fr.i += 4
	if err := fr.need(int(n)); err != nil {
		return nil, err
	}
	out := fr.b[fr.i : fr.i+int(n)]
	fr.i += int(n)
	return out, nil
}

// skip discards the value for kind.
func (fr *fieldReader) skip(kind uint8) error {
	switch kind {
	case wireU64:
		_, err := fr.u64()
		return err
	case wireBytes:
		_, err := fr.bytes()
		return err
	default:
		return ErrBadWire
	}
}

// walk calls fn for each field. Unknown tags should skip via the reader.
// fn may call u64/bytes exactly once for the current field, or skip.
func walk(b []byte, fn func(tag uint16, kind uint8, fr *fieldReader) error) error {
	fr := &fieldReader{b: b}
	for !fr.done() {
		tag, kind, err := fr.next()
		if err != nil {
			return err
		}
		before := fr.i
		if err := fn(tag, kind, fr); err != nil {
			return err
		}
		// If the callback neither consumed nor skipped, skip for them.
		if fr.i == before {
			if err := fr.skip(kind); err != nil {
				return err
			}
		}
	}
	return nil
}

// takeU64 reads a wireU64 value, rejecting a field that claims another kind.
func takeU64(kind uint8, fr *fieldReader) (uint64, error) {
	if kind != wireU64 {
		_ = fr.skip(kind)
		return 0, ErrBadWire
	}
	return fr.u64()
}

// takeBytes reads a wireBytes value, rejecting a field that claims another kind.
func takeBytes(kind uint8, fr *fieldReader) ([]byte, error) {
	if kind != wireBytes {
		_ = fr.skip(kind)
		return nil, ErrBadWire
	}
	return fr.bytes()
}

// takeString reads a wireBytes value as a string.
func takeString(kind uint8, fr *fieldReader) (string, error) {
	p, err := takeBytes(kind, fr)
	if err != nil {
		return "", err
	}
	return string(p), nil
}

// decodeU16s reads a packed u16 list written by putU16s.
func decodeU16s(p []byte) ([]uint16, error) {
	if len(p) < 2 {
		return nil, ErrTruncated
	}
	n := int(binary.LittleEndian.Uint16(p))
	if len(p) < 2+n*2 {
		return nil, ErrTruncated
	}
	out := make([]uint16, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(p[2+i*2:])
	}
	return out, nil
}
