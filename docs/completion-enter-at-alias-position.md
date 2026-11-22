# Enter does not accept a keyword where an alias may be typed

Investigation of 2026-11-22 (branch `review/bugfix-2026-11-22`, `HEAD`
8d156fb). Nothing in the tree was changed; the probe was a throw-away test,
removed afterwards.

## Report

> Typing `SELECT * FROM mytable WH` offers WHERE, but Enter leaves `WH` and
> adds a new line; Tab or Ctrl+Space are needed. Yet on `GR`, Enter completes
> `GROUP BY`.

## Summary

- **Intentional, not a regression.** It was added by e6083d5 ("PR 50 review
  fixes: soft keyword suggestions"), a Claude change made during the PR 50
  review, to stop `FROM Orders o⏎` turning the alias `o` into `OUTER APPLY`.
- **What decides it is the position, not the keyword.** `WH` and `GR` behave
  the same way at the same position. Your `GR` case was almost certainly typed
  after a WHERE clause, where no alias can appear.
- **Nothing on screen shows the difference**, and F1 help still says
  "Tab/Enter Accept the selected suggestion". That mismatch is why it looks
  random.
- **You can bring Enter back.** The fix is a one-line change in gossms.
  Recommendation and alternatives are below.

## Probe

`Customers` from the test inventory, typed key by key, then Enter. The popup
was open in every case:

| Typed | After Enter |
|---|---|
| `SELECT * FROM Customers WH` | `…WH⏎` (newline) |
| `SELECT * FROM Customers GR` | `…GR⏎` (newline) |
| `SELECT * FROM Customers WHERE Id = 1 GR` | `…GROUP BY ` |
| `SELECT * FROM Customers c WH` | `…c WHERE ` |
| `SELECT * FROM Customers AS c WH` | `…AS c WHERE ` |
| `SELECT * FROM Customers WHERE` | `…WHERE ` |

## Mechanism

1. `keywordScope.aliasChoices` (`internal/tui/completion_keywords.go`)
   returns `aliasable = true` when the clause is SELECT, FROM or JOIN, the
   expression before the cursor is complete, and the cursor is where an alias
   could start (`keywordAliasPosition`). That is true right after a table name
   with no alias yet. In that case the typed word is Soft unless it is a
   reserved keyword in full (`gosmo.IsReservedKeyword`): `WH` and `GR` are
   Soft, `WHERE` is not.
2. `keywordCandidates` sets `Soft: aliasable || prefix == ""` on every
   keyword item.
3. tuikit's editor (`controls/editor/editor_completion.go`,
   `softCompletionSelected`) treats Enter on a Soft item that the popup
   preselected itself as a plain newline. This is Visual Studio's
   "suggestion mode". Tab still commits it. Up/Down, or a popup opened with
   Ctrl+Space, commits on Enter as usual.

After `mytable c`, the alias slot is taken, so `aliasable` is false and Enter
commits again.

Two other Soft rules are unrelated to this report and should stay:

- **Nothing typed** (`prefix == ""`): `WHERE Name ⏎` must not insert `=`, and
  `SELECT *⏎` must not insert `FROM`.
- **Exact catalog-name match** (`softenExactMatches`, decisions.md, entry of
  2026-11-22): `FROM sys.tables⏎` is a newline.

## The trade-off

At `FROM mytable |`, a word typed there could be either the start of a
keyword or a new alias. Enter can serve only one of them:

- **Enter commits (before e6083d5):** `FROM Orders o⏎` gives `OUTER APPLY`,
  and `JOIN t c⏎` gives `CROSS JOIN`. An alias typed without `AS` at the end
  of a line gets replaced, and Escape is needed first.
- **Enter is a newline (now):** `FROM mytable WH⏎` stays `WH`. Finishing a
  clause keyword at that position needs Tab.

Short aliases are the common case for the first, and clause keywords right
after a table are the common case for the second. The two overlap only on
words that both an alias and a clause keyword start with.

## Fix proposal

### Recommended: Enter commits again at alias positions (A)

In `aliasChoices`, stop deriving softness from the alias position. Keep
adding `AS` to the choices:

```go
// completion_keywords.go, aliasChoices
s.choices = append(s.choices, "AS")
return false   // was: prefix != "" && !gosmo.IsReservedKeyword(prefix)
```

Then remove the now-constant return value: `aliasable` and
`Soft: aliasable || prefix == ""` become `Soft: prefix == ""`. Fix the
comment above `aliasable` in `keywordCandidates` to match.

Tests that encode the current rule and must change with it, in
`internal/tui/completion_keywords_test.go`:

- `softAtAlias`: delete it and its use in
  `TestSQLKeywordCompletionAcceptsPhrase`. Every phrase then commits on both
  Tab and Enter. Its CREATE FUNCTION entry has nothing typed there
  (`WHERE` …`)`); check whether it is Soft through `prefix == ""` and keep
  that one case if so.
- `TestSQLKeywordCompletionEnterKeepsAliasAndNewline`: the three
  alias-position cases (`…Customers o`, `…Customers c`, `…Orders o`) flip to
  committing. Keep the nothing-typed cases (`WHERE Name `, `SELECT *`). Add
  `FROM Customers WH⏎ → WHERE ` as the regression this report is about.

Also add a `docs/decisions.md` entry: "Enter commits a keyword at an alias
position; an alias typed without AS at end of line needs Escape first". This
keeps the old behaviour from coming back in a later review.

Cost: `FROM Orders o⏎` becomes `OUTER APPLY` again, as it was before
e6083d5.

### Alternative B: Soft only for a one-letter word

`return len([]rune(prefix)) == 1 && …`. This keeps the `o`/`c` protection
that e6083d5 was written for, and makes `WH`/`GR`/`ORD` commit. It is a
heuristic, though: a two-letter alias such as `cr⏎` becomes `CROSS JOIN`.
The rule is harder to predict than A.

### Alternative C: keep the behaviour and make it visible

Draw a Soft preselection differently in the tuikit popup, for example an
outline or dim highlight instead of the full selection bar, as Visual Studio
does. Fix the F1 help line. This is a tuikit change and does not give you
Enter back.

### In every option

`internal/tui/help_dialog.go` says "Tab/Enter Accept the selected
suggestion". That is not accurate while any Soft rule exists, because nothing
typed and exact name matches stay Soft under A. Suggested wording:

```
  Tab         Accept the selected suggestion
  Enter       Accept it too, except where it would only restate what you
              typed or nothing is typed yet (then a new line)
```

The help text must keep within the dialog's width. Check it in the built
binary.
