// Package job is a sub-agent job manager (spawn / list / wait / log / cancel).
//
// The agent Engine registers the agent_* tools against a [Manager] and drives
// each spawned job through a [Runner] that runs a child Engine; the TUI consumes
// job state to report sub-agent progress.
//
// Disk layout per job:
//
//	<root>/<job-id>/
//	  meta.json      # durable status + spawn metadata
//	  events.jsonl   # append-only log lines
//	  result.md      # final summary for the parent (not the full transcript)
package job
