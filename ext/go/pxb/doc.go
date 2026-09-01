// Package pxb implements the Phi eXtension Binary wire protocol.
//
// # Frame
//
// Every message is one length-prefixed binary frame on a duplex byte
// stream (typically a child process stdin/stdout). No JSON, no newlines.
//
//	┌──────── header (16 bytes, little-endian) ────────┐
//	│ magic[4]="PXB\x01" │ typ u16 │ flags u16 │ id u32│
//	│ payload_len u32                                  │
//	└──────────────────────────────────────────────────┘
//	│ payload: tagged fields                           │
//
// Readers never scan for delimiters. Unknown frame types are skipped by
// reading payload_len bytes and ignoring the body.
//
// # Payload (tagged fields)
//
// Payloads are a sequence of protobuf-style fields:
//
//	tag u16 | kind u8 | value
//
//	kind 1 WireU64   → 8-byte little-endian integer
//	kind 2 WireBytes → u32 length + bytes
//
// Decoders MUST skip unknown tags (see Walk). Omitting a tag means the
// zero value. Empty strings/blobs are omitted on encode.
//
// # Evolution rules
//
//  1. New message fields: allocate a new tag (≥1). Never reuse a tag.
//  2. New lifecycle events: allocate a new Ev* code. Never reuse a code.
//     Old extensions that did not Subscribe simply never see them.
//  3. New frame types: allocate Type* in the Ext→Host (1–99) or
//     Host→Ext (100–199) range. Peers that do not understand a type
//     skip the frame by length.
//  4. Incompatible renames / semantic breaks: bump ProtocolVersion;
//     host refuses peers that advertise an unsupported version.
//  5. Experimental tags use 128+ and must remain skippable.
//
// These rules keep host and extension binaries independently upgradable
// without a shared JSON schema or a lockstep release.
package pxb
