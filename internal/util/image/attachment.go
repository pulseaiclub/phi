package image

import (
	"encoding/base64"
	"path/filepath"

	"github.com/pulseaiclub/phi/internal/core"
)

// Attachment is a pending image: display label plus raw bytes for the model.
type Attachment struct {
	Label  string
	Result Result
}

// AttachmentLabel returns a short display name for a file path.
func AttachmentLabel(path string) string {
	base := filepath.Base(path)
	if base == "." || base == "" {
		return path
	}
	return base
}

// ToLLM encodes r for core.Message.Images.
func ToLLM(r Result) core.Image {
	return core.Image{
		Data:     base64.StdEncoding.EncodeToString(r.Data),
		MimeType: r.MimeType,
	}
}

// AttachmentFromResult builds an attachment with the given label.
func AttachmentFromResult(label string, r Result) Attachment {
	if label == "" {
		label = "image"
	}
	return Attachment{Label: label, Result: r}
}
