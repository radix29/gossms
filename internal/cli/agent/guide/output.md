- Text: `KEY: value` header lines (`STATUS`, `COMMAND`, `SERVER`,
  `DATABASE`), then a record and/or result sets under fixed markers such as
  `=== RESULT 1 (12 rows, truncated: no) ===`.
- JSON: one envelope, every key always present — `status`, `command`,
  `server`, `database`, `data` (a record), `resultSets[]` (`name`, `columns`,
  `rows`, `totalRowCount`, `truncated`), `messages`, `warnings`, `errors[]`
  (`code`, `message`, `hint`).
- An error is in the same format on stdout, plus one line on stderr.
  `status` is `ok`, `error`, `blocked` or `cancelled`.
<!-- full -->
- `-o jsonl` writes one JSON line per row as it arrives, then a `_summary`
  line holding the envelope. `-o csv` and `-o tsv` put rows only on stdout
  and the summary on stderr. `-o markdown` is the text envelope with tables.
- A JSON cell is typed by its SQL type: integers and floats are numbers,
  `bit` is `true`/`false`, NULL is `null`; `decimal`, `money` and dates are
  strings, so nothing is rounded.
- Limits cut the output, never the run: `--max-rows` (default 200, `0` all),
  `--max-col-width` (256), `--max-output-bytes` (1048576). A cut set says
  `truncated` and a `NOTE:` names the option; `totalRowCount` stays exact.
- `--out-file <path>` writes every row to a `.csv`, `.tsv`, `.jsonl`,
  `.json`, `.md` or `.txt` file and puts the summary on stdout; an existing
  file needs `--force`.
- `object definition` prints the module's text after `=== DEFINITION (n
  lines) ===`: take exactly n lines, whatever they look like.
- Read the warnings: a part the login may not read is NULL with a warning,
  not an error.
