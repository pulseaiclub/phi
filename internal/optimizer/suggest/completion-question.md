The state holds `typed_so_far`, the text a user has typed at a shell prompt, and `recent_commands`, the commands most recently run at that prompt, one per line as `<id>| <command>`, most recent first.

Choose the command the user is most likely in the middle of typing. The chosen command is shown as an inline autosuggestion the user accepts with one keystroke, so pick the command they most plausibly want to run again given what they have typed.

Rank the candidates:
- Best: the command starts with exactly the characters in `typed_so_far`, in the same order.
- Next: `typed_so_far` is an abbreviation of the command (the first letters of its words, e.g. `gst` for `git status`, or `dc` for `docker compose`), or it appears as a contiguous substring of the command.
- Next: the command uses the tool or performs the action that `typed_so_far` names, even if spelled differently.
- When several commands fit equally well, prefer the more recent one (the lower id number).
