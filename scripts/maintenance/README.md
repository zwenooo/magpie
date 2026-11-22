# Registered maintenance locations

`library maintain skill|mcp NAME library` declares the Magpie library the editable source. `library maintain skill|mcp NAME checkout PATH` declares an already checked-out Git directory. Maintenance location is a local editing decision, not the remote repository Magpie updates from. A generated install or sync store is not an editing location just because its files have familiar names.

Before editing an enrolled item, ask `library where skill|mcp NAME`. For each proposed file or destination, run the same command with the path as the final argument. A refusal or an unknown registration is not permission to pick another copy. Do not refresh copies by hand after an edit. Use the normal Library or repository update mechanism instead.

## Agent edit hook

`guard.py` accepts Claude Code `PreToolUse` for `Edit`, `Write`, and `NotebookEdit`, or Codex `PreToolUse` for `apply_patch`. The sibling `watch.json` contains `cli` (the absolute executable path) and `watch` (an array of `{root, kind, name}` records identifying managed, installed and historical directories, or `{root, blocked: true}` for generated stores). Neither the source itself nor secret values belong in the hook configuration. The hook delegates the actual allow decision to `library where`; aliases are not separate writable copies. An unrelated edit is left alone, and an unreadable configuration or a failed check blocks the file edit rather than guessing.

This is a guardrail for cooperating agents, not a filesystem ACL. It cannot prevent writes made by arbitrary programs or shell commands, and another device needs its own local registrations and supported hooks. Do not add hooks for shell commands which would have to guess their write targets. Enforce repository retirements separately in CI.
