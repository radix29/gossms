# Bug-fix plan — review of 2026-11-14

Three bugs found reviewing gossms, gosmo and tuikit. Each was reproduced, not
argued: B1 and B3 by throwaway tests in tuikit (since deleted), B2 against the
live server (`win10cli`, major 17) and then through `safety.Classify`.

Nothing is implemented. Delete this file once the three items are done.

| ID | Repo | Severity | What |
|---|---|---|---|
| B1 ✅ done | gossms (CLI safety) | High — a write runs under the default `read` allowance | `EXEC dbo.xp_…` is classified `read`, but in a user database it runs the user's own `dbo.xp_…` procedure |
| B2 ✅ done | tuikit (crashes gossms) | Medium — panic on the UI goroutine, gossms exits | The file dialog panics drawing a file of 1 PiB or more |
| B3 ✅ done | tuikit (reachable from every gossms dialog) | Low — the value is fine, the field draws empty | `InputField.Cut()` leaves the scroll where it was, so the field can draw blank |

## Recommended implementation order

1. ✅ **B1** (done 2026-11-14): the only one that lets something past a safety gate. gossms only,
   small, with one live check (step B1.3).
2. ✅ **B2** (done 2026-11-14): a one-function fix in tuikit with a table test. It crashes the app,
   but needs a sparse file of at least 1 PiB to trigger.
3. ✅ **B3** (done 2026-11-14): same repo and same sitting as B2. Fix `Cut` and `SelectAll`
   together.
4. ✅ **gossms verification pass** (done 2026-11-14): drive the built binary in tmux for B2 and
   B3, and run `gossms query classify` for B1 (`docs/testing.md`: green tests
   are not verification). Fresh build against the uncommitted tuikit tree.
   B1: all ten test-list statements classify as planned (the inline-SQL flag
   is `-Q`; `-q` is `--quiet`), the reason names the allowed forms, and
   `query run --dry-run -Q "EXEC dbo.xp_readerrorlog"` → BLOCKED, exit 7.
   B2: File ▸ Open lists a `truncate -s 2P` file as "2.0 PB" and a
   1 MiB − 1 file as "1.0 MB"; both files deleted. B3: Connect dialog, 46-char
   server name, Shift+Left ×36, Ctrl+X → `sqlserver-` visible; Ctrl+A on a
   long value scrolls to show its tail.

B2 and B3 are one tuikit change set. gossms picks them up on rebuild because
the `replace` directive is active. B1 is a gossms change set (code plus
`docs/cli.md`). gosmo needs no change.

The review also covered gosmo's restore planner, its scripting of user-created
statistics, float→decimal rounding, `textenc`, `sqltext.SplitBatches`, the CSV
reader and writer, the Format Document guard, and tuikit's TreeView, ListBox,
DropDown and charts. Each tree widget and chart got a 300–3000-seed
random-operation run. None of these turned up a bug. One live probe is worth
recording: a point-in-time restore inside log L1 `WITH RECOVERY` sets
`fork_point_lsn` inside L1. The first log after it then starts back at the
full's `first_lsn` (`74000000032800001`). `PlanRestore` correctly plans
full + that log, and the server restores it. The existing fork fixtures
(`forkHistory`) only cover a fork at a log's end. A fixture built from this
shape would pin it:

| set | type | first_lsn | last_lsn | fork_point_lsn | first → last fork |
|---|---|---|---|---|---|
| 741 | D | 74000000032800001 | 74000000035200001 | | P → P |
| 742 | L | 74000000032800001 | 74000000039200001 | | P → P |
| 743 | L | 74000000039200001 | 74000000043200001 | | P → P (abandoned) |
| 744 | L | 74000000032800001 | 74000000048000001 | 74000000038400012 | P → C |

Want: `[741 744]`. Optional; it is not one of the three.

---

## B1 — `dbo.xp_…` read-listed procedures resolve to the user's procedure

> **Done 2026-11-14.** Step 3's probe: a user `master.dbo.xp_fixeddrives`
> can be created, but `EXEC master.dbo.xp_fixeddrives` (from master or
> tempdb) still runs the system one, so the `master.dbo.` exception stays
> (recorded in `docs/decisions.md`). Probe objects dropped. Verified on the
> built binary: classify → `write`; `query run` → BLOCKED, exit 7.

**Where:** `internal/cli/safety/classify.go` — `readProcedure` (line 653), the
`readSystemProcs` case; the list in `internal/cli/safety/tables.go`
(`readSystemProcs`, which holds `xp_readerrorlog`, `xp_fixeddrives`,
`xp_msver`); the rule as documented in `docs/cli.md` § Safety ("A read
allow-list procedure is `read` only under a name the server resolves to the
real one…", around line 1531).

**What happens:** `readProcedure` accepts a read-listed system procedure with
no schema, `dbo` or `sys`, whatever the database part. That rule is right for
`sp_` names, because SQL Server resolves `sp_…` to the system procedure even
when it is `dbo`-qualified. It is wrong for `xp_` names. Probe on win10cli (17)
in a throwaway database holding `dbo.xp_msver`, `dbo.sp_who` and
`dbo.sp_helptext`, each of which inserts a row naming itself:

| call | ran |
|---|---|
| `EXEC sp_who`, `EXEC dbo.sp_who`, `EXEC db.dbo.sp_who`, `EXEC dbo.sp_helptext …` | system |
| `EXEC xp_msver`, `EXEC db..xp_msver`, `EXEC master.dbo.xp_fixeddrives` | system |
| **`EXEC dbo.xp_msver`** | **the user's `dbo.xp_msver`** |

The classifier answers `read` for `EXEC dbo.xp_msver`, `EXEC
dbo.xp_readerrorlog` and `EXEC userdb.dbo.xp_fixeddrives`. `read` is
`defaultAllow`, so `gossms query run -q "EXEC dbo.xp_readerrorlog"` runs a
user procedure that may write, with no `--allow`. This is the `evil.sp_who`
case that 9e19f5d closed for other schemas, still open for `dbo` when the
name is `xp_`. `docs/cli.md` states the wrong rule: it lists
`xp_readerrorlog` among the procedures that take "no schema, `dbo` or
`sys`".

**Fix:**

1. In `readProcedure`, split the `readSystemProcs` rule by prefix:
   - `sp_…`: keep the current rule (none, `dbo` or `sys`, any database).
   - `xp_…`: schema none (`xp_msver`, `db..xp_msver`) or `sys`. Accept `dbo`
     only when the database part is `master`.
2. `docs/cli.md` § Safety: move `xp_readerrorlog` out of the "takes no
   schema, `dbo` or `sys`" sentence. Add one sentence saying an `xp_`
   procedure is read only unqualified, as `sys.`, or as `master.dbo.`,
   because `dbo.xp_…` elsewhere is the database's own procedure.
3. **Live check before trusting `master.dbo.`**: in master, as sa, try
   `CREATE PROCEDURE dbo.xp_gossms_probe AS SELECT 1` and then `CREATE
   PROCEDURE dbo.xp_fixeddrives AS SELECT 1`. Drop whatever was created (the
   disposable-object rule; master is touched only by those two objects).
   - If the second CREATE succeeds and `EXEC master.dbo.xp_fixeddrives` then
     runs it, master is not special: drop the `master` exception and make
     `master.dbo.xp_…` `write`.
   - Otherwise keep the exception. `master.dbo.xp_fixeddrives` is the common
     idiom.

   Record the answer in `docs/decisions.md` under the classifier entry, so
   the rule is not re-derived.

**Tests (gossms, `classify_test.go`, next to the `evil.sp_who` rows at
line 229):**

- `write`: `EXEC dbo.xp_msver`, `EXEC userdb.dbo.xp_readerrorlog`,
  `EXEC srv.userdb.dbo.xp_fixeddrives`, and `INSERT #t EXEC dbo.xp_msver`
  (the floor path).
- `read`: `EXEC xp_msver`, `EXEC sys.xp_msver`, `EXEC db..xp_msver`,
  `EXEC master..xp_fixeddrives`, and (per step 3) `EXEC
  master.dbo.xp_fixeddrives`.
- Keep `EXEC dbo.sp_who` as `read` (line 235). The probe confirmed it.
- Check that the reason string names the procedure as not on the allow-list.

**Verify:** `go run ./cmd/gossms query classify -q "EXEC dbo.xp_msver"` →
`write`; and `query run` of it without `--allow write` → BLOCKED, exit 7.

---

## B2 — the file dialog panics drawing a file of 1 PiB or more

> **Done 2026-11-14.** `formatFileSize` uses `"KMGTPE"` and picks the unit
> from the rounded value. A negative size is left as "-1 B" (doc comment says
> so). The new table rows and the `math.MaxInt64` `Draw` test
> (`TestFileDialogDrawsTheLargestPossibleFileSize`) panic on the old code and
> pass on the fix. Verified on the built binary: a `truncate -s 2P` file in
> File ▸ Open shows "2.0 PB". The file was deleted afterwards.

**Where:** `~/go/tuikit/dialogs/file_dialog_complete.go` — `formatFileSize`
(line 92), called from `file_dialog_draw.go:115` for every listed file.

**What happens:** the unit index goes into `"KMGT"[exp]`. At 1 PiB (2^50)
`exp` is 4, which is out of range:

```
1125899906842624:     panic: index out of range [4] with length 4
9223372036854775807:  panic: index out of range [5] with length 4
```

A file that size takes no disk space. `truncate -s 2P /tmp/x` creates one in
an instant on tmpfs (done on this machine, size 2251799813685248, 0 blocks),
and XFS and btrfs allow the same. Opening File ▸ Open on that directory
panics in `Draw` on the UI goroutine. gossms then exits through
`cmd/gossms/main.go`'s `run` recover, after emergency-saving any dirty query
panels. A server-side listing (`server_filesystem.go`) reaches the same
`Draw`.

There is a second, cosmetic problem at the same boundary: 1 PiB − 1 prints
"1024.0 TB", and 1 MiB − 1 prints "1024.0 KB". The loop picks the unit before
rounding, so a value that rounds up to 1024 keeps the smaller unit.

**Fix:** use the unit string `"KMGTPE"`. An int64 tops out below 8 EiB, so
`exp` is at most 5. Then promote a value that rounds to 1024.0 to the next
unit (or compute the unit from the rounded value). A negative size (from a
`FileSystem` that reports garbage) already falls into the `n < unit` branch
and prints "-1 B". Leave it, or blank it like `SizeUnknown`. Either way, say
which in the doc comment.

**Tests (tuikit, `file_dialog_test.go` or a new table test):**
`formatFileSize` for 0, 1023, 1024, 1<<20 − 1, 1<<50 − 1, 1<<50, 1<<60 and
`math.MaxInt64`. Assert no panic and the expected text ("1.0 MB" for
1<<20 − 1, "1.0 PB" for 1<<50, "8.0 EB" for MaxInt64). Then a `Draw` test with
a fake `FileSystem` (see `file_dialog_remote_test.go`) listing one entry of
`math.MaxInt64` bytes.

**Verify (gossms):** `truncate -s 2P` a file in a scratch directory, File ▸
Open there in the tmux harness, and check the row shows "2.0 PB". Delete the
file afterwards.

---

## B3 — `InputField.Cut()` leaves the field drawing blank

> **Done 2026-11-14.** `Cut` (after `deleteSelection`) and `SelectAll` now
> call `adjustScroll`. One correction to the test expectation below: after the
> reproduction's Cut, `scroll` is 1 and the field draws `123456789`, not
> `scroll == 0`. Ten characters plus the caret's cell don't fit a 10-wide box,
> which is the same tail rule `TestInputFieldSetValueLongValueShowsItsTail`
> pins. The three tests (`TestInputFieldCutKeepsTheRestVisible`,
> `…CutKeepsWideRunesVisible`, `…SelectAllKeepsTheCaretVisible`) and the
> stress test `widgets/input_field_monkey_test.go` (500 seeds × 400 ops,
> invariant `scroll ≤ max(0, valueWidth − W + 1)`) all fail on the old code
> (the stress test at seed 0) and pass on the fix. Verified on the built
> binary: in the Connect dialog, after typing a 46-char server name,
> Shift+Left ×36 and Ctrl+X, the field shows `sqlserver-`.

**Where:** `~/go/tuikit/widgets/input_field.go` — `Cut` (line 182), which
calls `deleteSelection` and `changed` but not `adjustScroll`. `SelectAll`
(line 136) has the same gap. gossms reaches both through Edit ▸ Cut / Ctrl+X
and Edit ▸ Select All (`internal/tui/clipboard.go`, `cutSelection` →
`target.Cut()`), in any dialog's focused field: connect dialog, filters,
prompt and typed-confirm dialogs, property sheets.

**What happens:** `scroll` is a column offset that only `adjustScroll`
re-clamps. Every `HandleKey` and `Paste` path ends with it. `Cut` is the one
public edit that does not:

```go
f := NewInputField("", 10, false)
f.Focus(true)
f.Paste("0123456789ABCDEFGHIJ")                 // caret at end, scroll 10
for range 10 { f.HandleKey(Key(KeyLeft, ModShift)) }
f.Cut()                                         // value "0123456789", scroll still 10
f.Draw(s)                                       // "[          ]" — blank, caret off screen
```

The value is intact, but the field shows nothing and no caret until the next
key. The user reads that as the whole value being cut. `adjustScroll`'s own
comment names this symptom ("drawing blank over a set value") as the one it
exists to prevent. `SelectAll` moves the caret to the end without scrolling,
so on a value longer than the box the caret is off screen. (`ShowFromStart`
leaves the caret off screen on purpose and says so. `SelectAll` does not.)

**Fix:** call `f.adjustScroll()` at the end of `Cut` (after
`deleteSelection`) and at the end of `SelectAll`. Don't put it inside
`deleteSelection`: `HandleKey` calls that mid-edit and adjusts once at its
end anyway.

**Tests (tuikit, `input_field_test.go`):**
- The reproduction above: after `Cut`, `Draw` shows `0123456789` and
  `scroll == 0`.
- The same with wide runes (`漢` ×10 in a width-10 box): the scroll is in
  columns.
- `SelectAll` on a 30-char value in a 10-wide box: the caret column is inside
  `[scroll, scroll+W)`.

**Also worth landing (the harness that found B3):** a random-operation stress
test for `InputField`, like `datagrid_monkey_test.go`. It drives keys,
mouse, `SetWidth`, `SetValue`, `Paste`, `Cut`, `SelectAll` and enable
toggles, and checks after each step that `cursor` is in `[0, len(value)]`,
`scroll` ≤ the value's display width, and `Draw` survives. Before the fix, the
throwaway version failed within 20 seeds on the Cut invariant. After it was
patched locally, 500 seeds × 400 ops ran clean. Land it after the fix, so it
goes in green.

**Verify (gossms):** in the connect dialog, type a server name longer than
the field, Shift+Left over its tail, Ctrl+X, and check the remaining text is
visible.
