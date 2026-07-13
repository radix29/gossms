# goSSMS Plan

## Working style

This is a spare-time project — no deadlines, no sprints, no committed
velocity. The dates below are a target, not a promise. Work happens in
whatever order priorities and available time allow; this document tracks
*what's next*, not *when*.

## Release target

**First usable version: July 2026.**

"Usable" means the core SSMS workflows (connect, browse objects, run
queries, view/edit properties) are solid across all three supported
platforms and authentication modes, not that every SSMS feature is
covered — see [Feature backlog](#feature-backlog) for what can wait.

## Ongoing practices (no end date)

These continue for the life of the project, release or not:

- Bug fixing, optimizing, and refactoring as issues turn up.
- Triage incoming issues and re-prioritize the list below as they land —
  this file should stay roughly in priority order, top to bottom, but
  gets reshuffled as real-world bug reports come in.
- Keep `README.md` and `internal/tuikit/README.md` in sync with the code
  as features land — stale docs are worse than no docs.

## Next up (working priority order)

1. **Harmonize menus, context menus, and shortcuts** — audit the top menu
   bar, every right-click context menu, and the keyboard shortcuts behind
   them for consistency: same action should have the same shortcut and
   the same wording everywhere it appears (menu, context menu, and
   status/help text).

2. **Properties dialogs pass** (Server/Database/Login today):
   - Check each existing page against what SSMS actually shows — find
     gaps.
   - Add the missing SSMS functionality.
   - Add genuinely useful functionality SSMS itself doesn't have (this is
     a from-scratch reimplementation, not a pixel clone — improvements
     are fair game).
   - Add the property dialogs that don't exist yet (Table, View,
     Stored Procedure, Index, and the rest of the object tree beyond
     Server/Database/Login).

3. **Execution plan viewer.** New capability, roughly:
   - Shows as a new panel in the query window alongside Results and
     Messages (same tab row).
   - Backed by a reusable `tuikit` control (not query-window-specific),
     consistent with how `DataGrid`/`Editor` are built — see
     `internal/tuikit/README.md` for the pattern to follow.
   - Also reachable as its own standalone panel (like Object Explorer
     Detail), not just embedded in the query window.
   - Scrollable in all directions.
   - Switchable visualization modes, chosen from within the control
     itself:
     1. **Graphical plan** (default) — classic SQL Server Management
        Studio execution-plan-tree look.
     2. **Tree/table view** — flat tree-with-columns layout, closer to
        Oracle's execution plan explain output.
     3. **XML plan** — read-only, selectable/copyable text (same
        selection UX as the existing `Editor` control), no rendering.
   - Mockups for (1) and (2) to be sketched before implementation starts.

4. **Authentication testing** — no infrastructure currently available for
   any of these; blocked until access exists, not a code problem:
   - Windows Integrated Authentication.
   - Entra ID authentication against Managed Instances and Azure SQL DB.

5. **Platform testing** — build and exercise on macOS (no Mac available
   yet; blocked on hardware/CI access, same as above).

## Feature backlog (later, no particular order)

- Keyboard shortcut consistency audit across terminals — several
  terminal emulators eat or mangle specific chords; see
  `internal/tuikit/README.md`'s notes and the Known Issues below. Needs a
  documented fallback/remap story rather than one-off fixes per report.
- SQL editor autocomplete (`Ctrl+Space` or similar), plus a `Ctrl+R`-style
  metadata reload so autocomplete stays in sync with the connected
  database.
- README polish: add screenshots, and an explicit "built with AI
  assistance" disclaimer.

## Known issues to close out before/around release

Carried from `README.md`'s Known Issues section — resolving these (or at
least confirming root cause) is part of getting to a genuinely usable
v1, not just documentation:

- Windows 10 terminals (PowerShell, cmd) double-inputting characters.
- Some Linux terminals (e.g. xfce4-terminal) eating specific key
  shortcuts.
- Windows Authentication and Entra ID authentication untested (see
  Authentication testing above).
- macOS untested (see Platform testing above).

## Non-goals for v1

Nothing formally excluded yet — this section exists so exclusions have a
place to go once they're decided, rather than silently dropped from the
backlog.
