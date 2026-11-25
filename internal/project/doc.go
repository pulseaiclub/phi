// Package project provides the phi workspace layout and configuration.
//
// Discover creates the global phi home (~/.phi) with its standard
// subdirectories (bin, skills, session, jobs) so downloaded tool binaries,
// SKILL.md files, persisted sessions, and sub-agent transcripts have a known
// home. Startup ensures the layout exists,
// then tools such as fd/ripgrep are downloaded into the bin directory when
// missing.
package project
