```text
gossms <group> <command> [arguments] -p <profile> [-o text|json]
```

- An object is `schema.name`, or a name alone when exactly one schema has it;
  bracket a part as in T-SQL: `Sales.[Order Detail]`.
- Object commands read the profile's database; `-d <database>` picks another.
- `-o json` is a stable envelope to parse; text, the default, is compact.
- `gossms agent context -p <profile>` shows the server, login, ceiling,
  databases and the permissions the login lacks, with what each breaks.
<!-- full -->
- `--name value` and `--name=value` are the same; a bool is `--name` or
  `--no-name`; a list repeats or takes commas (`--type table,view`). An
  unknown option is an error, never ignored.
- `@args.txt` reads arguments from a file, one per line — no shell quoting.
- `gossms <command> --help` is one command's usage, options and examples;
  `gossms agent schema` is every command as JSON, to build tool definitions
  or check a call before making it.
- `gossms profile list` lists the profiles; `gossms profile test <name>`
  connects through one and reports the server, edition and login.
