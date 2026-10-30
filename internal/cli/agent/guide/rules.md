1. Pass `-p <profile>` to every command that connects. Never put a password
   on the command line: a profile carries its own.
2. The default is read-only. `query run` and `plan actual` refuse a script
   above `--allow` (default `read`; `write`, `ddl`, `admin`) with exit 7
   before connecting, and the profile's ceiling caps `--allow`. Raise it only with the user's
   explicit approval of that change.
3. Before a change: `--dry-run` (classify only), then `--transaction
   rollback` (run and undo), then the real run, with `--max-rows-affected
   <n>` when the count is known.
4. Send T-SQL through stdin (`--file -`) or `--sql-base64`, not `--sql`: no
   shell quoting can damage them.
5. Output is capped: 200 rows per set, 256 characters per cell. Check
   `truncated`; use `--max-rows 0` or `--out-file <path>` for everything.
6. Never run `gossms` without a command: that starts the interactive UI.
7. Read the exit code first. On an error, `hint` names the next step; follow
   it rather than guessing a spelling.
<!-- full -->

- The safety check is a guardrail, not the security boundary — the login's
  permissions are. It fails closed: dynamic SQL, a procedure not on the read
  list and anything it does not recognise count as `write` at least.
- `gossms query classify --file fix.sql` shows each statement's class and
  why, without connecting.
- When a password is not saved, pass it as `--password-env <VAR>` or
  `--password-stdin`, never as an argument.
