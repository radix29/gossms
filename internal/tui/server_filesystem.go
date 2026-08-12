package tui

import (
	"context"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// serverFileSystemTimeout bounds a single Browse round-trip. The file dialog
// calls its FileSystem synchronously, so this is also how long the whole TUI
// can sit unresponsive on one directory listing — short enough that a wedged
// or unreachable server can't look like a hang, long enough for a large
// directory over a slow link.
const serverFileSystemTimeout = 15 * time.Second

// serverFS browses the SQL Server host's filesystem — the machine the backup
// device path is resolved on — rather than the machine gossms runs on. The
// two are routinely different, and different OSes at that: the Backup and
// Restore dialogs' Browse buttons pick a path SQL Server will open, so
// listing the client's own disks (which is what the file dialog does by
// default) shows directories the server cannot see and returns paths it
// cannot write.
//
// PathRules is embedded rather than implemented so path handling follows the
// *server's* convention: a Linux client browsing a Windows instance still
// joins with backslashes and treats "C:\" as a root.
type serverFS struct {
	dialogs.PathRules

	sc         *db.ServerConn
	defaultDir string
}

// newServerFS returns a FileSystem for sc's host. ok is false when there is
// no usable connection to ask, and the caller must then refuse to browse at
// all: falling back to dialogs.LocalFileSystem looks like it worked and hands
// back a path off *this* machine's disks, which is a directory the server
// cannot see and a destination BACKUP cannot write — the "a click does the
// wrong thing" case CLAUDE.md § Application rules rules out.
func newServerFS(sc *db.ServerConn) (dialogs.FileSystem, bool) {
	if sc == nil || sc.Server == nil {
		return nil, false
	}
	info := sc.Server.Info()
	if info == nil {
		return nil, false
	}
	fs := &serverFS{PathRules: dialogs.PosixPathRules{}, sc: sc, defaultDir: info.DefaultBackupPath}
	if serverIsWindows(info) {
		fs.PathRules = dialogs.WindowsPathRules{}
	}
	return fs, true
}

// serverIsWindows decides which path convention the server host uses.
// ServerInfo.Platform is the direct answer; the default backup path is the
// fallback for an instance that didn't report one, and a Windows path is
// recognizable by its backslashes.
func serverIsWindows(info *gosmo.ServerInfo) bool {
	switch info.Platform {
	case "Windows":
		return true
	case "Linux":
		return false
	}
	return strings.Contains(info.DefaultBackupPath, `\`)
}

// Blocking marks every call below as a network round trip, so the file
// dialog paints a "Listing ..." frame before each one instead of freezing
// with the previous directory on screen. See dialogs.BlockingFileSystem.
func (fs *serverFS) Blocking() bool { return true }

func (fs *serverFS) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(fs.sc.Context(), serverFileSystemTimeout)
}

// List returns dir's contents on the server. The empty path is the level
// above every root: on Windows it lists the host's fixed drives, which is
// where ".." from "C:\" lands.
func (fs *serverFS) List(dir string) ([]dialogs.FileEntry, error) {
	ctx, cancel := fs.ctx()
	defer cancel()

	if dir == "" {
		drives, err := fs.sc.Server.FixedDrivesContext(ctx)
		if err != nil {
			return nil, err
		}
		entries := make([]dialogs.FileEntry, 0, len(drives))
		for _, d := range drives {
			entries = append(entries, dialogs.FileEntry{Name: strings.TrimRight(d.Name, `\/`), IsDir: true})
		}
		return entries, nil
	}

	found, err := fs.sc.Server.EnumFileSystemContext(ctx, dir)
	if err != nil {
		return nil, err
	}
	entries := make([]dialogs.FileEntry, 0, len(found))
	for _, e := range found {
		entries = append(entries, dialogs.FileEntry{
			Name:    e.Name,
			IsDir:   e.IsDirectory,
			Size:    e.Size,
			ModTime: e.LastModified,
		})
	}
	return entries, nil
}

// Default opens the browse at the server's configured backup directory —
// the destination the dialogs pre-fill — falling back to a root when the
// instance reports none.
func (fs *serverFS) Default() string {
	if fs.defaultDir != "" {
		return fs.defaultDir
	}
	if fs.Separator() == `\` {
		return `C:\`
	}
	return "/"
}

// Exists passes the failure through rather than reporting "not there": the
// probe is a network round trip, and the save-overwrite guard needs to tell
// a file that isn't there from a server that couldn't be asked.
func (fs *serverFS) Exists(path string) (bool, bool, error) {
	ctx, cancel := fs.ctx()
	defer cancel()
	return fs.sc.Server.FileSystemExistsContext(ctx, path)
}
