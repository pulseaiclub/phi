// Package skills loads and parses SKILL.md files from skill directories.
//
// Each skill is a directory containing a SKILL.md file with YAML frontmatter
// and a Markdown body. Skills are loaded from ~/.phi/skills/ (or a custom path
// set via skill_path in config or PHI_SKILL_PATH env var), plus the Agent
// Skills locations ~/.agents/skills/ and <project>/.agents/skills/.
// Callers that hold several roots use LoadAll, which applies the priority
// order: the first root wins a duplicate skill name.
//
// A skill file looks like:
//
//	---
//	name: My Skill
//	description: What this skill does
//	disable-model-invocation: true
//	---
//	Instructions for the agent to follow when this skill is relevant.
//
// disable-model-invocation keeps a skill out of the system prompt catalog while
// leaving it selectable from the skills panel. It is a catalog filter, not
// access control.
package skills
