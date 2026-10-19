package clipboard

import "errors"

// ErrUnavailable means no image is present or required platform tools are missing.
var ErrUnavailable = errors.New("clipboard: unavailable")
