# CLAUDE.md

Context for Claude Code sessions on **goSSMS**. Read this first; it points to
the rest of the docs rather than repeating them.

## What this is

goSSMS is a portable, cross-platform terminal TUI reimplementation of SQL
Server Management Studio, written in Go 1.27. One build runs on
Linux/macOS/Windows — no CGO, no build-tag-split files. `runtime.GOOS`
branching exists in exactly two places: `internal/tui/os_clipboard.go`
(shells out to `xclip`/`wl-copy`/`pbcopy`/`clip`, OSC-52 fallback) and
`internal/version/version.go`.

- Module: `github.com/radix29/gossms` — https://github.com/radix29/gossms
- Depends on `github.com/radix29/gosmo`, the author's own companion library for
  SQL Server management objects (https://github.com/radix29/gosmo), and
  `github.com/gdamore/tcell/v3` (`v3.4.2`) for the TUI backend.
- `go.mod`'s `require` pins a gosmo tag, but the `replace github.com/radix29/gosmo
  => ../gosmo` directive is deliberately **active** during development, so builds
  use the sibling checkout and `HEAD` may depend on untagged gosmo code. Both
  repos are siblings: `~/go/gossms`, `~/go/gosmo`.
- Version resolves automatically from the pushed git tag — see
  `internal/version/version.go` — never hand-edited.

## Required reading by task

Read what the task actually touches — a one-file fix needs none of this. Each
entry names a section, not a whole document; read the section.

| If touching… | Read first |
|---|---|
| User-facing behavior, features, or keys | `README.md` |
| Anything spanning packages | `ARCHITECTURE.md` § Package map, § Which document owns what |
| Any widget, grid, dialog, menu, toolbar, clipboard, mouse or async UI code | `docs/ui-rules.md` |
| `HandleMouse`, overlays, focus, drag — the reasoning | `ARCHITECTURE.md` § The mouseDragging idiom |
| A goroutine delivering a result to the UI | `ARCHITECTURE.md` § Async result delivery: postAndWake |
| Permission gating, T-SQL a page emits, OE filters, query execution | `docs/db-rules.md` |
| Writing or running tests, or calling anything done | `docs/testing.md` |
| Anything under `internal/tuikit/**` | `internal/tuikit/README.md` § Design principles |
| A new tuikit control or dialog | `internal/tuikit/README.md` § Adding a new control |
| gosmo changes / the `replace` directive | the `dev-with-local-gosmo` skill; `~/go/gosmo/CLAUDE.md` |
| Known bugs, deferred scope, release blockers | `docs/open-threads.md` — check before reporting something as newly found |

## The one rule that matters most: verify against real source, don't guess

Guessing at `tcell`/`gosmo` API shapes from training data produces code that
*looks* plausible but doesn't match the real API. Before writing any code that
calls into either, check the real source — `go doc`, grep the module cache
(`go env GOMODCACHE`), or read `~/go/gosmo` directly. Don't rely on memory of
"similar" APIs from other versions or packages.

Standing gotchas:

- `Screen.PollEvent`/`PostEvent` don't exist in tcell v3 — replaced by a
  channel, `EventQ()`.
- The modifier accessor is `Modifiers()`, not `Mod()`.
- `gosmo.Database.Name` / `.State` / `.RecoveryModel` etc. are *methods*, not
  fields.
- `gosmo.Server` has **both** `Database(name)` and `DatabaseByName(name)`, and
  they are not interchangeable — picking the wrong one fails quietly.
  `Database` is a lightweight handle with no query and zero-valued metadata,
  and the only one that works under a `WithScript`-derived context;
  `DatabaseByName` queries `sys.databases`. Both are documented on the methods
  themselves — `go doc gosmo.Server.Database`.

## Build & verify

```sh
go build -o gossms ./cmd/gossms   # build
go run ./cmd/gossms                # run without building a binary
go test ./...                      # test
gofmt -w .                         # format in place
go vet ./...                       # vet
```

Version/Commit/Date (`internal/version`) resolve automatically, in priority
order: `-ldflags -X` (set by `.github/workflows/release.yml` from the pushed
tag) → `debug.BuildInfo.Main.Version` (from `go install …@<tag>`) → the literal
`"(devel)"` default. Nothing here is hand-edited before a release.

**Green tests are not verification.** Nearly every real bug in this project's
history was caught by driving the built binary, not by `go test`. Read
`docs/testing.md` before writing a test or calling a change done.

## Changing gosmo

`gosmo` is the author's own library, not a third-party dependency — add or change
functionality in it when gossms needs something it doesn't have yet, rather than
working around a missing capability inside gossms. Build and test inside `gosmo`
before relying on a change from gossms.

**Never remove or narrow a gosmo capability because gossms doesn't call it** —
it has users beyond gossms, and "no callers in gossms" is not evidence of dead
code. Report unused gosmo surface as "unused, kept deliberately", never as a
deletion candidate. `~/go/gosmo/CLAUDE.md` § This is a library, not gossms's back
end is the authority, and does not load automatically from here.

Inside **gossms**, dead code is ordinary cleanup. The no-removal rule is about
gosmo only.

## Coding conventions

- Comments match the surrounding density. The long "why" comments in `app.go`,
  `datagrid.go`, `secret.go` and `propsheet/common.go` are load-bearing — each
  names a shipped bug a plausible simplification would bring back — and are not
  a cleanup target.
- Go 1.26+ features in active use: `new(T{...})` composite-literal syntax, the
  `slices` package, `errors.AsType`.
- `core.DisplayWidth(s)`, never `len(s)`, for any column-position math.
- **A property-sheet label must fit `propsheet.LabelWidth` (30 columns).**
  Text/Password/Int/Select pad with `core.PadRight` and Static clips itself; both
  hard-clip with no ellipsis, so an over-long label silently renders as a
  shorter, different one ("Auto update statistics asynchronously" became "Auto
  update statistics asynchr", directly under "Auto update statistics").
  `TestNoPropertySheetLabelIsTruncated` fails on any literal over the limit;
  shorten the label rather than widening `LabelWidth`, which moves the value
  column on every page. Check/Radio/Section/Note draw at full width and are exempt.
- `tuikit` is a strictly one-way dependency graph and knows nothing about `tui`.
- Every `tuikit` sub-package is one file per type or tightly related group, plus
  `common.go` for cross-file helpers and `doc.go` for the package doc.
  `internal/tui` files are one-per-purpose (`app.go`, `menu.go`, …).
- Splitting a file that has grown too large: one file per type/group, plus
  `common.go` and `doc.go`. Extract each section by exact line range, never
  retype by hand, diff the extracted text byte-for-byte against the original,
  and only then delete the source.

## Repo hygiene

- `todo/` is tracked in git but is not source — scratch notes, SSMS mockups, scratch
  SQL. Don't build from it or act on it unless asked, and leave it out of cleanups.
- `CHANGELOG.md` and `RELEASE.md` cover the release process. Don't edit either as
  part of a feature or fix unless asked.
- **Check a path exists before writing to it.** A shell `cat > file` or a
  `Write` overwrites without asking, and a plausible new filename is often
  already taken — `internal/tui/agent_job_state_test.go` looked like a new test
  file and was an existing one, silently replaced. `ls` (or Read) the path
  first; recover a committed one with `git show HEAD:<path> > /tmp/...` and
  merge, never with `git checkout`.
- **Never `git checkout` / `git restore` a file to undo an edit of your own.** The
  working tree here routinely carries a large body of uncommitted work, and the
  command discards *that* along with the edit, silently and unrecoverably — a
  mutation check on `datagrid.go` took its uncommitted `restoreOverrideWidths`
  and `SetError` changes with it. Copy the file aside first and copy it back,
  or reverse the edit with the same tool that made it.

## Self learning

Turn mistakes into rules, and never repeat them — but write the rule down where it
will be loaded again:

- A rule about the code or the workflow → this file if it is one line, else the
  document that owns the area (`docs/ui-rules.md`, `docs/db-rules.md`,
  `docs/testing.md`, `ARCHITECTURE.md`). Keep this file short; it loads every
  session.
- Work knowingly left undone, or a bug found and not fixed → `docs/open-threads.md`.
