# CLAUDE.md

Context for Claude Code sessions on **goSSMS**. It routes to the other docs
rather than repeating them.

## What this is

goSSMS is a portable terminal TUI reimplementation of SQL Server Management
Studio in Go 1.27: one build for Linux/macOS/Windows, no CGO, no build tags.
`runtime.GOOS` branches only in `internal/tui/os_clipboard.go` and
`internal/version/version.go`.

- Module `github.com/radix29/gossms`. Depends on `github.com/radix29/gosmo`
  (the author's own SMO-style library, sibling checkout `~/go/gosmo`) and
  `github.com/gdamore/tcell/v3` (`v3.5.0`).
- `go.mod`'s `replace github.com/radix29/gosmo => ../gosmo` is deliberately
  **active** during development, so `HEAD` may depend on untagged gosmo code.
  It is commented out only to tag a release, against an already-pushed gosmo tag.
- The version resolves from the git tag (`internal/version/version.go`) — never
  hand-edited.

## Required reading by task

Read the named section, only when the task touches it.

| If touching… | Read first |
|---|---|
| User-facing behavior or features | `README.md` |
| Key bindings | `internal/tui/help_dialog.go` (F1 help — the keyboard reference) |
| Anything spanning packages | `ARCHITECTURE.md` § Package map, § Which document owns what |
| Widgets, grids, dialogs, menus, toolbars, clipboard, mouse, async UI | `docs/ui-rules.md` |
| `HandleMouse`, overlays, focus, drag | `ARCHITECTURE.md` § The mouseDragging idiom |
| A goroutine delivering a result to the UI | `ARCHITECTURE.md` § Async result delivery: postAndWake |
| An async load a newer one supersedes | `ARCHITECTURE.md` § Latest-only loads: latest |
| Permission gating, emitted T-SQL, OE filters, query execution | `docs/db-rules.md` |
| Writing/running tests, or calling anything done | `docs/testing.md` |
| `internal/tuikit/**` | `internal/tuikit/README.md` § Design principles (new control/dialog: § Adding a new control) |
| gosmo changes / the `replace` directive | the `dev-with-local-gosmo` skill; `~/go/gosmo/CLAUDE.md` |
| Known bugs, deferred scope, release blockers | `docs/open-threads.md` — check before reporting something as new |
| Whether a question is already settled | `docs/decisions.md` — the "do not re-raise" record |

## The one rule that matters most: verify against real source, don't guess

Never write `tcell`/`gosmo` calls from memory of similar APIs — check `go doc`,
the module cache (`go env GOMODCACHE`) or `~/go/gosmo` first. Standing gotchas:

- tcell v3 has no `Screen.PollEvent`/`PostEvent` — use the `EventQ()` channel.
  The modifier accessor is `Modifiers()`, not `Mod()`.
- gosmo catalog state is exported *fields* (`Database.Name`, `.State`, …);
  only derivations (`IsSystem()`, `IsSnapshot()`) and back-pointers
  (`Server()`) are methods.
- **A gosmo method ending in `Ref` is a lookup-free handle**: `DatabaseRef(name)`
  issues no query and leaves every field but the name zero (`IsSystem()` is
  `false` for `master`); `DatabaseByName(name)` reads the catalog. Not
  interchangeable. `~/go/gosmo/CLAUDE.md` § Conventions (the `Ref` bullet) is
  the authority on when each applies — note `WithScript` intercepts writes
  only, so it alone is not a reason to use a `Ref`. See `go doc
  gosmo.Server.DatabaseRef`.

## Build & verify

```sh
go build -o gossms ./cmd/gossms   # build
go run ./cmd/gossms                # run
go test ./...                      # test
gofmt -w .  &&  go vet ./...       # format, vet
```

Version/Commit/Date (`internal/version`) resolve in order: `-ldflags -X` (set
by `.github/workflows/release.yml`) → `debug.BuildInfo.Main.Version` (a tag, or
a pseudo-version from a checkout build) → `"(devel)"` (`go run`,
`-buildvcs=false`).

**Green tests are not verification** — nearly every real bug was caught by
driving the built binary. Read `docs/testing.md` before writing a test or
calling a change done.

## Changing gosmo

gosmo is the author's library, not a third-party dependency: add what gossms
needs there rather than working around it. Build and test inside gosmo first.

**Never remove or narrow a gosmo capability because gossms doesn't call it** —
it has other users. Report unused gosmo surface as "unused, kept deliberately",
never as a deletion candidate (`~/go/gosmo/CLAUDE.md` § This is a library, not
gossms's back end). Dead code inside gossms is ordinary cleanup.

## Coding conventions

- Match surrounding comment density. The long "why" comments in `app.go`,
  `datagrid.go`, `secret.go` and `propsheet/common.go` each name a shipped bug
  a plausible simplification would bring back — not a cleanup target.
- Go 1.26+ in use: `new(T{...})`, `slices`, `errors.AsType`.
- `core.DisplayWidth(s)`, never `len(s)`, for column-position math.
- **Property-sheet labels must fit `propsheet.LabelWidth` (30).** Rows
  hard-clip with no ellipsis, so an over-long label silently renders as a
  different one. `TestNoPropertySheetLabelIsTruncated` enforces it; shorten the
  label, don't widen `LabelWidth`. Check/Radio/Section/Note are exempt.
- `tuikit` is a one-way dependency graph and knows nothing about `tui`.
- `tuikit` sub-packages: one file per type/group, plus `common.go` and
  `doc.go`. `internal/tui` files are one-per-purpose.
- **Over ~900 non-test lines** is a prompt to split a file (not a defect) when
  a change lands there anyway, along its own section comments: extract by
  exact line range, diff byte-for-byte against the original, then delete the
  source. A new `internal/tui` file needs an `ARCHITECTURE.md` § Package map
  row in the same commit (`TestPackageMapListsEveryTUIFile`).

## Repo hygiene

- `todo/` is tracked scratch (notes, mockups, SQL) — don't build from it, act on
  it, or clean it up unless asked.
- Don't edit `CHANGELOG.md`/`RELEASE.md` in a feature or fix unless asked.
- **A doc-link exemption ships with the document it excuses, never before** —
  `exemptSource` in `internal/tui/doc_links_test.go` checks each path exists,
  so an early entry breaks `TestNoDanglingDocReference` on `main`.
- **Check a path exists before writing to it** — `Write`/`cat >` overwrite
  silently, and plausible new names are often taken. Recover a committed file
  with `git show HEAD:<path> > /tmp/...` and merge.
- **Never `git checkout`/`git restore` a file to undo your own edit** — the tree
  often carries uncommitted work that goes with it. Copy the file aside and
  back, or reverse the edit with the tool that made it.

## Self learning

Turn mistakes into rules where they will be loaded again: a one-line rule →
this file; longer → the owning doc (`docs/ui-rules.md`, `docs/db-rules.md`,
`docs/testing.md`, `ARCHITECTURE.md`). Work knowingly left undone, or a bug
found and not fixed → `docs/open-threads.md`.
