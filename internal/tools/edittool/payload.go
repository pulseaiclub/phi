package edittool

import (
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/tools/edittool/sloppy"
)

// applySections applies a parsed payload to the files read through read, keyed
// by the payload's path spelling. Sections apply atomically: one failure and
// no file is written.
func applySections(
	sections []sloppy.Section,
	read func(path string) (string, error),
) (map[string]Result, error) {
	standalone := len(sections) == 1 && len(sections[0].Ops) == 1
	out := make(map[string]Result, len(sections))
	for _, section := range sections {
		content, err := read(section.Path)
		if err != nil {
			return nil, sectionError(section.Path, err, len(sections))
		}
		result, err := Apply(content, section, standalone)
		if err != nil {
			return nil, sectionError(section.Path, err, len(sections))
		}
		out[section.Path] = result
	}
	return out, nil
}

// sectionError attributes a failure to its file and, for multi-file payloads,
// states that no sibling was written. Single-file failures surface unwrapped.
func sectionError(path string, err error, sections int) error {
	detail := strings.TrimPrefix(err.Error(), atomicityNotice+"\n")
	if sections > 1 {
		detail = fmt.Sprintf("[%s]: %s\nNo files were modified — sections apply atomically.", path, detail)
	}
	return applyError{atomicityNotice + "\n" + detail}
}
