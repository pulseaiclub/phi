package pxb

import (
	"encoding/binary"
	"errors"
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
	copy(dst[0:4], magic[:])
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
	if src[0] != magic[0] || src[1] != magic[1] || src[2] != magic[2] || src[3] != magic[3] {
		return Header{}, ErrBadMagic
	}
	h := Header{
		Type:    binary.LittleEndian.Uint16(src[4:6]),
		Flags:   binary.LittleEndian.Uint16(src[6:8]),
		ID:      binary.LittleEndian.Uint32(src[8:12]),
		Payload: binary.LittleEndian.Uint32(src[12:16]),
	}
	if h.Payload > MaxStreamPayload {
		return Header{}, ErrPayloadLarge
	}
	return h, nil
}

// Frame is one complete message.
type Frame struct {
	Header
	Body []byte // payload only; may alias a pooled buffer
}

// WriteFrame writes header + body to w. The size check here is the transport
// ceiling; connection-level policy (negotiated caps) lives in Writer.Write.
func WriteFrame(w io.Writer, typ, flags uint16, id uint32, body []byte) error {
	if len(body) > MaxStreamPayload {
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

// readFrameBuf reads one frame, reusing buf for the body when large enough.
// Returned body must not be used after the next readFrameBuf on the same buf
// unless the caller copies it.
func readFrameBuf(r io.Reader, buf *[]byte) (Frame, error) {
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
	// max is this connection's send cap: MaxPayload until the peer negotiates
	// CapLargeFrames, then MaxStreamPayload. Set before traffic starts.
	max uint32
}

// NewWriter wraps w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, buf: make([]byte, 0, 256), max: MaxPayload}
}

// SetMaxPayload raises the send cap after the handshake advertised support.
func (wr *Writer) SetMaxPayload(n uint32) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	if n > MaxStreamPayload {
		n = MaxStreamPayload
	}
	wr.max = n
}

// Write sends one frame. body is copied into an internal buffer only when
// needed for the header write path; the body is written as-is.
func (wr *Writer) Write(typ, flags uint16, id uint32, body []byte) error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	if uint64(len(body)) > uint64(wr.max) {
		return ErrPayloadLarge
	}
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
	return readFrameBuf(rd.r, &rd.buf)
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
