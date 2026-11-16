#!/usr/bin/env bash
# Run `go test` (or any go subcommand) on the Windows dev box (ssh host alias
# "win10cli"). Syncs gossms + the sibling gosmo/tuikit checkouts (go.mod
# replace directives) as tar over ssh, then runs the command remotely.
#   scripts/wintest.sh                      -> go test ./...
#   scripts/wintest.sh test ./internal/tui/...
#   scripts/wintest.sh vet ./...
# Env: WIN_HOST (default win10cli), WIN_ROOT (default C:\src; must contain no spaces).
set -euo pipefail
host=${WIN_HOST:-win10cli}
root=${WIN_ROOT:-'C:\src'}
here=$(cd "$(dirname "$0")/.." && pwd)
parent=$(dirname "$here")

sync() { # sync <local dir> <name>
  # Wipe then extract so deleted files don't linger. Windows 10+ ships tar.exe.
  ssh "$host" "(rd /s /q $root\\$2 2>nul) & mkdir $root\\$2" >/dev/null
  (cd "$1" && git ls-files -co --exclude-standard -z | tar --null -T - --ignore-failed-read -cf - 2>/dev/null) |
    ssh "$host" "tar -xf - -C $root\\$2"
}

sync "$here" gossms
for lib in gosmo tuikit; do sync "$parent/$lib" "$lib"; done

if [ $# -eq 0 ]; then set -- test ./...; fi
# Double-quote every argument: the remote shell is cmd, where an unquoted | or &
# in a -run pattern is a pipe or command separator.
args=""
for a in "$@"; do args+=" \"$a\""; done
ssh "$host" "cd /d $root\\gossms && go$args"
