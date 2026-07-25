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

1. **Database Restore** dialog needs a complete rework

2. **SQL Agent** needs a complete rework

3. **Database Reports** - some useful reports at database level: disk 
   usage, top tables etc

4. **Activity Monitor** - monitor the current and panst n minutes activity
   of the SQL Server. Includes output from sp_whoiactive and other goodies

5. **Availability Groups** functionality to manage Availability Groups.

6. **Authentication testing** — no infrastructure currently available for
   any of these; blocked until access exists, not a code problem:
   - Entra ID authentication against Managed Instances and Azure SQL DB.

6. **Platform testing** — build and exercise on macOS (no Mac available
   yet; blocked on hardware/CI access, same as above).

## Feature backlog (later, no particular order)

- Keyboard shortcut consistency audit across terminals — several
  terminal emulators eat or mangle specific chords; see
  `internal/tuikit/README.md`'s notes and the Known Issues below. Needs a
  documented fallback/remap story rather than one-off fixes per report.
- SQL editor autocomplete (`Ctrl+Space` or similar), plus a `Ctrl+R`-style
  metadata reload so autocomplete stays in sync with the connected
  database.
- Implement Word Wrap option in the query editor

## Known issues to close out before/around release

Carried from `README.md`'s Known Issues section — resolving these (or at
least confirming root cause) is part of getting to a genuinely usable
v1, not just documentation:

- Some  terminals (e.g. xfce4-terminal) eating specific key
  shortcuts.
- Entra ID authentication untested (see Authentication testing above).
- macOS untested (see Platform testing above).

## Non-goals for v1

Nothing formally excluded yet — this section exists so exclusions have a
place to go once they're decided, rather than silently dropped from the
backlog.
