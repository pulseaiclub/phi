package pxb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	ErrBadMagic     = errors.New("pxb: bad magic")
	ErrPayloadLarge = errors.New("pxb: payload too large")
	ErrShortBuffer  = errors.New("pxb: short buffer")
	ErrTruncated    = errors.New("pxb: truncated payload")
)

// Header is the 16-byte frame prefix.
type Header struct {
	Type    uint16
	Flags   uint16
	ID      uint32
	Payload uint32
}

// EncodeHeader writes a 16-byte header into dst (must be ≥ HeaderSize).
func EncodeHeader(dst []byte, h Header) {
	copy(dst[0:4], Magic[:])
	binary.LittleEndian.PutUint16(dst[4:6], h.Type)
	binary.LittleEndian.PutUint16(dst[6:8], h.Flags)
	binary.LittleEndian.PutUint32(dst[8:12], h.ID)
	binary.LittleEndian.PutUint32(dst[12:16], h.Payload)
}

// DecodeHeader parses a 16-byte header.
func DecodeHeader(src []byte) (Header, error) {
	if len(src) < HeaderSize {
		return Header{}, ErrShortBuffer
	}
	if src[0] != Magic[0] || src[1] != Magic[1] || src[2] != Magic[2] || src[3] != Magic[3] {
		return Header{}, ErrBadMagic
	}
	h := Header{
		Type:    binary.LittleEndian.Uint16(src[4:6]),
		Flags:   binary.LittleEndian.Uint16(src[6:8]),
		ID:      binary.LittleEndian.Uint32(src[8:12]),
		Payload: binary.LittleEndian.Uint32(src[12:16]),
	}
	if h.Payload > MaxPayload {
		return Header{}, ErrPayloadLarge
	}
	return h, nil
}

// Frame is one complete message.
type Frame struct {
	Header
	Body []byte // payload only; may alias a pooled buffer
}

// WriteFrame writes header + body to w.
func WriteFrame(w io.Writer, typ, flags uint16, id uint32, body []byte) error {
	if len(body) > MaxPayload {
		return ErrPayloadLarge
	}
	var hdr [HeaderSize]byte
	//nolint:gosec // G115: len(body) is bounded by the MaxPayload check above
	EncodeHeader(hdr[:], Header{Type: typ, Flags: flags, ID: id, Payload: uint32(len(body))})
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	_, err := w.Write(body)
	return err
}

// ReadFrame reads one frame from r into a fresh body slice.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	h, err := DecodeHeader(hdr[:])
	if err != nil {
		return Frame{}, err
	}
	var body []byte
	if h.Payload > 0 {
		body = make([]byte, h.Payload)
		if _, err := io.ReadFull(r, body); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Header: h, Body: body}, nil
}

// ReadFrameBuf reads one frame, reusing buf for the body when large enough.
// Returned body must not be used after the next ReadFrameBuf on the same buf
// unless the caller copies it.
func ReadFrameBuf(r io.Reader, buf *[]byte) (Frame, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	h, err := DecodeHeader(hdr[:])
	if err != nil {
		return Frame{}, err
	}
	need := int(h.Payload)
	if cap(*buf) < need {
		*buf = make([]byte, need)
	} else {
		*buf = (*buf)[:need]
	}
	if need > 0 {
		if _, err := io.ReadFull(r, *buf); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Header: h, Body: *buf}, nil
}

// Writer serializes frames with a scratch buffer.
type Writer struct {
	w   io.Writer
	mu  sync.Mutex
	buf []byte
}

// NewWriter wraps w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, buf: make([]byte, 0, 256)}
}

// Write sends one frame. body is copied into an internal buffer only when
// needed for the header write path; the body is written as-is.
func (wr *Writer) Write(typ, flags uint16, id uint32, body []byte) error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	return WriteFrame(wr.w, typ, flags, id, body)
}

// Reader deserializes frames with a reusable body buffer.
type Reader struct {
	r   io.Reader
	buf []byte
}

// NewReader wraps r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, buf: make([]byte, 0, 256)}
}

// Read returns the next frame. Body is valid until the next Read.
func (rd *Reader) Read() (Frame, error) {
	return ReadFrameBuf(rd.r, &rd.buf)
}

// CloneBody copies f.Body so it survives the next Read.
func CloneBody(f Frame) []byte {
	if len(f.Body) == 0 {
		return nil
	}
	out := make([]byte, len(f.Body))
	copy(out, f.Body)
	return out
}

// ByteWriter builds payloads without fmt/json.
type ByteWriter struct {
	b []byte
}

// Reset clears the buffer keeping capacity.
func (bw *ByteWriter) Reset() { bw.b = bw.b[:0] }

// Bytes returns the current payload.
func (bw *ByteWriter) Bytes() []byte { return bw.b }

// Grow ensures capacity for n more bytes.
func (bw *ByteWriter) Grow(n int) {
	if cap(bw.b)-len(bw.b) < n {
		nb := make([]byte, len(bw.b), len(bw.b)+n+256)
		copy(nb, bw.b)
		bw.b = nb
	}
}

func (bw *ByteWriter) U8(v uint8) {
	bw.b = append(bw.b, v)
}

func (bw *ByteWriter) U16(v uint16) {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	bw.b = append(bw.b, tmp[:]...)
}

func (bw *ByteWriter) U32(v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	bw.b = append(bw.b, tmp[:]...)
}

func (bw *ByteWriter) U64(v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	bw.b = append(bw.b, tmp[:]...)
}

func (bw *ByteWriter) Bool(v bool) {
	if v {
		bw.b = append(bw.b, 1)
	} else {
		bw.b = append(bw.b, 0)
	}
}

func (bw *ByteWriter) Blob(p []byte) {
	bw.U32(uint32(len(p))) //nolint:gosec // G115: payload bounded by MaxPayload on the frame path
	bw.b = append(bw.b, p...)
}

func (bw *ByteWriter) String(s string) {
	bw.U32(uint32(len(s))) //nolint:gosec // G115: payload bounded by MaxPayload on the frame path
	bw.b = append(bw.b, s...)
}

func (bw *ByteWriter) Strings(ss []string) {
	bw.U16(uint16(len(ss))) //nolint:gosec // G115: element count bounded by MaxPayload
	for _, s := range ss {
		bw.String(s)
	}
}

func (bw *ByteWriter) U16s(vs []uint16) {
	bw.U16(uint16(len(vs))) //nolint:gosec // G115: element count bounded by MaxPayload
	for _, v := range vs {
		bw.U16(v)
	}
}

// ByteReader parses payloads.
type ByteReader struct {
	b []byte
	i int
}

// NewByteReader wraps b.
func NewByteReader(b []byte) *ByteReader { return &ByteReader{b: b} }

func (br *ByteReader) remaining() int { return len(br.b) - br.i }

func (br *ByteReader) need(n int) error {
	if br.remaining() < n {
		return ErrTruncated
	}
	return nil
}

func (br *ByteReader) U8() (uint8, error) {
	if err := br.need(1); err != nil {
		return 0, err
	}
	v := br.b[br.i]
	br.i++
	return v, nil
}

func (br *ByteReader) U16() (uint16, error) {
	if err := br.need(2); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint16(br.b[br.i:])
	br.i += 2
	return v, nil
}

func (br *ByteReader) U32() (uint32, error) {
	if err := br.need(4); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(br.b[br.i:])
	br.i += 4
	return v, nil
}

func (br *ByteReader) U64() (uint64, error) {
	if err := br.need(8); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint64(br.b[br.i:])
	br.i += 8
	return v, nil
}

func (br *ByteReader) Bool() (bool, error) {
	v, err := br.U8()
	return v != 0, err
}

func (br *ByteReader) Blob() ([]byte, error) {
	n, err := br.U32()
	if err != nil {
		return nil, err
	}
	if err := br.need(int(n)); err != nil {
		return nil, err
	}
	out := br.b[br.i : br.i+int(n)]
	br.i += int(n)
	return out, nil
}

func (br *ByteReader) String() (string, error) {
	b, err := br.Blob()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (br *ByteReader) Strings() ([]string, error) {
	n, err := br.U16()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := 0; i < int(n); i++ {
		s, err := br.String()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (br *ByteReader) U16s() ([]uint16, error) {
	n, err := br.U16()
	if err != nil {
		return nil, err
	}
	out := make([]uint16, 0, n)
	for i := 0; i < int(n); i++ {
		v, err := br.U16()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DumpType is a debug helper.
func DumpType(typ uint16) string {
	return fmt.Sprintf("0x%04x", typ)
}
