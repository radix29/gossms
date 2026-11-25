# Bug-fix plan, 2026-11-25 (`--param` values the server changes)

Three defects (D1, the uniqueidentifier one, and D2, the code-page one, are fixed) found reviewing gossms, gosmo and tuikit, each **reproduced
live on win10cli (17.0, `SQL_Latin1_General_CP1_CI_AS`)** with the built
binary and read-only queries. None is in `docs/open-threads.md`,
`docs/decisions.md` or `~/go/gosmo/OPEN-THREADS.md`. All three are in
`gossms query run --param` (`internal/cli/cmd/param.go`), and all three
break the promise `docs/cli.md` § Parameters makes: "A value that would
change is refused before connecting". The run reports `STATUS: OK` with a
value nobody wrote, and a `WHERE` (or an `UPDATE`/`DELETE`) acts on other
rows.

gosmo and tuikit were reviewed too (quoting, `tsql.Spans`, bulk-value
converters, retry wrappers, `fileutil`, strutil/viewport/list widgets, editor
search) and `go test -race` is clean in both: nothing new found there. No
library change is needed — the fixes reuse gosmo's existing
`BulkColumn.Converter` and `Server.CollationCodePage`. Close an item by
deleting it; delete this file with the last one. Nobody commits but the
user.

| # | Where | Effect | Severity |
|---|---|---|---|
| D3 | `parseParam`, time | A date or datetime string runs as a time (`2026-01-02` is `00:00:00`) | Silent, narrower |

Each step: `go build ./... && go test ./... && gofmt -l . && go vet ./...
&& staticcheck ./... && deadcode -test ./...`, then the item's live repro
with the built binary (`docs/testing.md`). Update the `--param` bullets of
`docs/cli.md` in the same change as each fix.

Live repro base command (win10cli, `docs/testing.md` live rules):

```sh
PW=… gossms query run -S win10cli -U sa --password-env PW \
  --trust-server-certificate --sql "SELECT @v AS v" --param "v=…:<type>"
```

## D3 — a time parameter takes a date and runs it as midnight

**Cause.** `paramDateTypes` covers every date type but `time`, which goes to
the server's conversion unchecked; that conversion takes a whole date or
datetime string and keeps only its clock part.

**Repro (live, verified 2026-11-25):** `--sql "SELECT @t AS t" --param
"t=2026-01-02:time(0)"` → `STATUS: OK`, `t = 00:00:00`. (`10:00:00 PM` also
runs, as `22:00:00`; `2026-01-02 25:00` is refused, Msg 8114.)

**Fix (gossms).** Give `time` the date types' treatment: accept only
`hh:mm[:ss[.fffffff]]` (24-hour, 1–7 fraction digits, values range-checked
as `dateLiteral` does), and send `hh:mm:ss[.f…]`. Refuse anything else as
`USAGE_ERROR` "is not an ISO 8601 time", hint `Write the time as
hh:mm[:ss[.fffffff]]`. A fraction longer than the scale still rounds, as
`docs/cli.md` already says for the time types — that is not this bug.
Easiest as a `timeLiteral` beside `dateLiteral`, reusing its clock regexp
part rather than a second one that could drift.

**Tests (`param_test.go`).** Refused: `2026-01-02`, `2026-01-02T10:00`,
`10:00 PM`, `24:00`, `10:60`, `10:00:00.12345678`. Accepted and rewritten:
`10:00` → `10:00:00`, `12:30:00.5` unchanged. The existing
`at=12:30:00:time(0)` case (value containing colons) still parses.

**Live check.** The repro exits `USAGE_ERROR`; `t=12:30:00:time(0)` still
returns `12:30:00`.

## Recommended implementation order

1. **D3** — offline, in `parseParam` beside `dateLiteral`; update the
   `docs/cli.md` "refused before connecting" bullet in the same pass.

Then: `go test ./...` and the live checks once more on the final tree.
