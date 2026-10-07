package clipboard

// ReadText returns plain text from the system clipboard.
//
// It reads the platform clipboard directly rather than asking the terminal
// over OSC 52: terminals that cannot answer the query are exactly the ones
// where the composer has to do the read itself.
func ReadText() (string, error) {
	text, err := readClipboardTextPlatform()
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", ErrEmpty
	}
	return text, nil
}
