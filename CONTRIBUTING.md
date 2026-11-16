# Contributing to goSSMS

Thanks for your interest in goSSMS.

## Before you start

Open an issue to discuss a feature or a non-trivial fix before sending a pull
request. Check `docs/open-threads.md` (known bugs and deferred scope) and
`docs/decisions.md` (questions already settled) first.

Read the document that owns the area you touch:

| Touching | Read |
|---|---|
| User-facing behaviour | `README.md` |
| Key bindings | `internal/tui/help_dialog.go` (the F1 help) |
| Anything spanning packages | `ARCHITECTURE.md` |
| Widgets, grids, dialogs, menus, async UI | `docs/ui-rules.md` |
| Emitted T-SQL, permissions, query execution | `docs/db-rules.md` |
| The command line | `docs/cli.md` |
| Writing or running tests | `docs/testing.md` |

## Building

goSSMS is one portable build with no CGO and no build tags (Go 1.27):

```sh
go build -o gossms ./cmd/gossms
go run ./cmd/gossms
```

It depends on `gosmo`, `tuikit` and `gotuipic`. `go.mod` may carry `replace`
directives to sibling checkouts during development; a pull request must not
depend on unpushed library code.

## Checks

```sh
go test ./...
gofmt -w . && go vet ./...
staticcheck ./... && deadcode -test ./...   # must stay clean
go test -race -count=1 ./internal/<pkg>/... # a change crossing goroutines
```

Green tests are not verification. Most real bugs were found by driving the
built binary, so run it and exercise your change; `docs/testing.md` explains
how. Never test against a server you do not own, and create only disposable
objects.

## Pull requests

- One logical change per PR, with tests for new behaviour.
- Verify tcell, gosmo and tuikit calls against their real source or `go doc`,
  not from memory of similar APIs.
- Keep user-facing documentation (`README.md`, the F1 help) in step with the
  change.
- Do not edit `CHANGELOG.md` or `RELEASE.md`; the maintainer does at release.
- The version comes from the git tag and is never hand-edited.

## Security

Do not report vulnerabilities in a public issue. Use GitHub's private
vulnerability reporting on the repository's Security tab.

## Licence

goSSMS is licensed under GPL-3.0-or-later (see `LICENSE`). By contributing you
agree that your contribution is licensed under the same terms.
