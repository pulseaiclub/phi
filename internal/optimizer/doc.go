// Package optimizer defines advisory decision contracts for agent execution.
//
// Optimizers observe command intent and execution results. They do not execute
// commands or replace the permission gate; callers decide how to apply a
// returned Decision.
package optimizer
