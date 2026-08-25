# Engineering journal

Dated record of the work behind goSSMS and gosmo **since the current tag**:
what was built, what bugs were found and how, and which decisions were made
deliberately. Trimmed at each release — entries for work that has shipped come
out, since `CHANGELOG.md` records what shipped and git history keeps the rest.
Trimmed to `v0.0.8` (2026-08-25) on 2026-08-25.

Nothing here is required reading. `CLAUDE.md` carries the rules that still
apply; `docs/open-threads.md` carries the work that is still open. Newest
entries at the bottom. A `slug` under a heading is a note's name from the
Claude Code memory store this file was migrated out of, kept for older
cross-references.

The v0.0.8 entries are in git history at the `v0.0.8` tag and its parent
commits — the least-privilege audit and its five phases (splitting the
connect-time read, the capability probe, the read paths that stopped lying,
the action gate, and the refusal-to-required-right table), the
`Script <object> as ▸` cascade and the menu submenus under it, gosmo's
scripter growing the rest of the object families, New Index and New
Statistics, the four object families that arrived in the tree, the
missing-index banner and `.sqlplan`, the Log File Viewer's search and
Recycle, job-step reordering, Column Rename and the object-op gaps beside
it, the ten by-name finders and the Schema Properties summary query, the
Object Explorer filter's server-side push-down, the write-path Properties
page tests in five phases and the fake-driver harness they needed, and the
cross-repo review passes of 2026-08-19 through 2026-08-25.

The v0.0.7 entries are at the `v0.0.7` tag — Always On in seven phases,
Backup and Restore learning to browse the server's own filesystem, the
Restore File Locations view, the SQL Server / Agent log viewer, the Object
Explorer folder filter, general Delete and Rename, the text-encoding work
behind File > Open and Save, the small-terminal dialog thread, the
grid-cursor sweep, and the busy-latch passes behind `safegoRepair`.
