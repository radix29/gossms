gossms manages Microsoft SQL Server from the command line: run T-SQL, check
a script before it runs, read its execution plan, explore databases and their
objects — tables, views, modules, indexes, statistics and dependencies —
script them, read, export and load table data, and see what a server is
doing: sessions, blocking, waits, its costliest queries, Query Store and
deadlocks. Every call is stateless
and non-interactive: it prints one result on stdout and exits with a code
that says how it went.
<!-- full -->

It is the command-line half of gossms, a terminal re-implementation of SQL
Server Management Studio. A connection is a profile the user saved in its
interactive UI; the command line only reads that configuration and never
saves a profile or a password.
