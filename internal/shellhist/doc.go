// Package shellhist reads the shell history file behind phi's "!" mode.
//
// "!" mode runs bash, but the history worth completing from is whatever the
// user's own login shell recorded, so the reader accepts both shapes on disk:
// zsh's extended format and the plain one-line-per-entry format bash writes.
//
// Reading is deliberately separate from judging: this package returns commands,
// and the optimizer decides which one the user is completing.
package shellhist
