package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/debuglog"
)

const (
	// SkillFileName is the required file name inside each skill directory.
	SkillFileName        = "SKILL.md"
	frontmatterDelimiter = "---"
)

// Sentinel errors returned when parsing a skill file.
var (
	ErrInvalidFrontmatter   = errors.New("skill file must start with YAML front matter (---)")
	ErrFrontmatterNotClosed = errors.New("skill file frontmatter not properly closed with ---")
	ErrInvalidYAML          = errors.New("skill file frontmatter is invalid")
)

// Skill represents a single skill loaded from a SKILL.md file.
type Skill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	// DisableModelInvocation keeps the skill out of the system prompt catalog
	// while leaving it selectable from the skills panel. It is a catalog
	// filter, not access control: the file is still readable from disk.
	DisableModelInvocation bool
	Body                   string
	Path                   string
	SkillFilePath          string
}

// Parse parses a single SKILL.md file, extracting YAML frontmatter and body.
func Parse(skillFilePath string) (*Skill, error) {
	content, err := os.ReadFile(skillFilePath)
	if err != nil {
		return nil, err
	}

	contentStr := string(content)
	if !strings.HasPrefix(contentStr, frontmatterDelimiter) {
		return nil, ErrInvalidFrontmatter
	}

	parts := strings.SplitN(contentStr, frontmatterDelimiter, 3)
	if len(parts) < 3 {
		return nil, ErrFrontmatterNotClosed
	}

	skill, err := parseFrontmatter(parts[1])
	if err != nil {
		return nil, err
	}

	skill.Body = strings.TrimSpace(parts[2])
	skill.Path = filepath.Dir(skillFilePath)
	skill.SkillFilePath = skillFilePath
	return skill, nil
}

// parseFrontmatter reads flat "key: value" lines and common YAML block scalars
// (>, >-, |, |-). Nested YAML maps/lists are not supported.
func parseFrontmatter(fm string) (*Skill, error) {
	skill := &Skill{}
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Continuation lines belong to a previous block scalar — skip orphans.
		if raw != "" && (raw[0] == ' ' || raw[0] == '\t') {
			return nil, ErrInvalidYAML
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, ErrInvalidYAML
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			return nil, ErrInvalidYAML
		}
		if isYAMLBlockScalar(val) {
			var body string
			body, i = readBlockScalar(lines, i+1)
			val = body
		} else {
			var err error
			val, err = unquote(val)
			if err != nil {
				return nil, err
			}
		}
		switch key {
		case "name":
			skill.Name = val
		case "description":
			skill.Description = val
		case "license":
			skill.License = val
		case "compatibility":
			skill.Compatibility = val
		case "disable-model-invocation":
			skill.DisableModelInvocation = isTrue(val)
		}
	}
	return skill, nil
}

// isTrue is deliberately forgiving: only a case-insensitive "true" counts.
// A strict parser would reject the whole skill on a typo (LoadSkills drops any
// file that fails to parse), whereas the worst case here is that a skill the
// author meant to hide stays in the catalog — visible and easy to notice.
func isTrue(val string) bool {
	return strings.EqualFold(strings.TrimSpace(val), "true")
}

func isYAMLBlockScalar(val string) bool {
	switch val {
	case ">", ">-", ">|", "|", "|-", "|+":
		return true
	default:
		return false
	}
}

// readBlockScalar collects indented continuation lines for >, >-, |, |-.
// Folded style (>) joins lines with spaces; literal style (|) keeps newlines.
func readBlockScalar(lines []string, start int) (string, int) {
	var parts []string
	i := start
	for ; i < len(lines); i++ {
		raw := lines[i]
		if strings.TrimSpace(raw) == "" {
			parts = append(parts, "")
			continue
		}
		if raw == "" || (raw[0] != ' ' && raw[0] != '\t') {
			break
		}
		parts = append(parts, strings.TrimSpace(raw))
	}
	// Caller loop will i++ after return; step back one so the next key is seen.
	return strings.Join(parts, " "), i - 1
}

func unquote(val string) (string, error) {
	if val == "" {
		return "", nil
	}
	q := val[0]
	if q != '"' && q != '\'' {
		return val, nil
	}
	if len(val) < 2 || val[len(val)-1] != q {
		return "", ErrInvalidYAML
	}
	return val[1 : len(val)-1], nil
}

// ToPromptMarkdown converts skills to a Markdown fragment for injection into
// the system prompt. Skills with DisableModelInvocation are skipped — they stay
// in the loaders' list so the skills panel can still offer them. Returns an
// empty string if nothing is left.
func ToPromptMarkdown(skills []*Skill) string {
	var sb strings.Builder
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		sb.WriteString("### ")
		sb.WriteString(DisplayName(s))
		sb.WriteString("\n\n")
		sb.WriteString(s.Description)
		sb.WriteString("\n\n")
		sb.WriteString("**Location:** `")
		sb.WriteString(s.SkillFilePath)
		sb.WriteString("`\n\n")
	}
	return sb.String()
}

// Find returns the skill matching name by exact name, case-insensitive name,
// or directory basename. Returns nil if none match.
func Find(list []*Skill, name string) *Skill {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for _, s := range list {
		if s.Name == name {
			return s
		}
	}
	for _, s := range list {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	for _, s := range list {
		if strings.EqualFold(filepath.Base(s.Path), name) {
			return s
		}
	}
	return nil
}

// LoadSkills walks skillDir looking for SKILL.md files, parses each one, and
// returns the list of skills. If skillDir does not exist, it returns nil, nil.
// Skills that fail to parse are skipped so one bad file cannot hide the rest.
func LoadSkills(skillDir string) ([]*Skill, error) {
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return nil, nil
	}

	var skills []*Skill
	err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != SkillFileName {
			return nil
		}
		skill, err := Parse(path)
		if err != nil {
			return nil // skip invalid skill files
		}
		skills = append(skills, skill)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return skills, nil
}

// LoadAll loads every directory in dirs in order and drops duplicate skill
// names, so the first root (the highest-priority one) wins. A directory that
// does not exist is skipped rather than reported: a root without skills is the
// normal state of a fresh project, and callers only need the flat list.
func LoadAll(dirs []string) []*Skill {
	var out []*Skill
	seen := make(map[string]struct{})
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		list, err := LoadSkills(dir)
		if err != nil {
			// A root can exist but be unreadable; that is worth one debug
			// line, not a failed session.
			debuglog.Logf("skills: skipping root %q: %v", dir, err)
			continue
		}
		for _, s := range list {
			key := strings.ToLower(DisplayName(s))
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// DisplayName is the name a skill is listed and matched under: the frontmatter
// name when it has one, otherwise the skill directory's basename.
func DisplayName(s *Skill) string {
	if s == nil {
		return ""
	}
	if name := strings.TrimSpace(s.Name); name != "" {
		return name
	}
	return filepath.Base(s.Path)
}
