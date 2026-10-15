package urls

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// InternalURL preserves authority and path spelling without URL normalization.
// Colons in the authority belong to namespaced identifiers, not ports.
type InternalURL struct {
	Protocol    string
	Hostname    string
	Host        string
	Pathname    string
	Href        string
	RawHref     string
	RawHost     string
	RawPathname string
	Search      string
	Hash        string
}

var schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*$`)

// ParseInternalURL accepts absolute hierarchical internal URLs. It leaves dot
// segments and escapes intact so handlers can validate paths before cleaning.
func ParseInternalURL(input string) (InternalURL, error) {
	result := InternalURL{RawHref: input}
	scheme, rest, ok := strings.Cut(input, "://")
	if !ok || !schemePattern.MatchString(scheme) || strings.ContainsFunc(input, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\'
	}) {
		return result, fmt.Errorf("invalid internal URL %q: expected scheme://authority/path", input)
	}
	if _, err := url.PathUnescape(input); err != nil {
		return result, fmt.Errorf("invalid internal URL %q: %w", input, err)
	}
	if before, hash, found := strings.Cut(rest, "#"); found {
		rest, result.Hash = before, "#"+hash
	}
	if before, search, found := strings.Cut(rest, "?"); found {
		rest, result.Search = before, "?"+search
	}
	host, path, hasPath := strings.Cut(rest, "/")
	result.Protocol = strings.ToLower(scheme) + ":"
	result.Host, result.Hostname, result.RawHost = host, host, host
	if hasPath {
		result.Pathname = "/" + path
	}
	result.RawPathname = result.Pathname
	result.Href = input
	return result, nil
}
