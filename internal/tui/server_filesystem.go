package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// serverFileSystemTimeout bounds a single Browse round-trip. The file dialog
// calls its FileSystem synchronously, so this is also how long the whole TUI
// can sit unresponsive on one directory listing — short enough that a wedged
// or unreachable server can't look like a hang, long enough for a directory
// that is merely big.
//
// 15s deliberately, and *not* raised to the 30s the app's other fetch
// timeouts use (childFetchTimeout, propFetchTimeout, agDashboardTimeout,
// completionInventoryTimeout). Those bound background work; this one bounds a
// frozen UI, so the two halves of the sentence above pull in opposite
// directions and the freeze is the side that should win. Doubling it would
// double how long an unreachable server looks like a hang, to buy headroom
// nothing needs.
//
// Measured on win10cli 2026-08-14, through EnumFileSystemContext, best of
// three: C:\Windows\System32 4551 entries in 1.2s, C:\Windows 101 in 35ms,
// C:\Program Files 29 in 11ms. The worst directory on a Windows box already
// clears this by more than 10x.
//
// Do not read the "ten seconds" in ARCHITECTURE.md § The other direction:
// FileDialog.showBusy as a live figure — it predates gosmo's WHERE level = 0
// filter on sys.dm_os_enumerate_filesystem, which stops a listing walking the
// whole subtree. showBusy still earns its place (a second of frozen UI is worth
// labelling), but the wait it was written for is ~8x smaller.
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
// wrong thing" case docs/ui-rules.md rules out.
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
		return nil, displayError(err)
	}
	if err := legacyListingRefusal(fs.sc, found); err != nil {
		return nil, err
	}
	// A pre-2017 instance is listed through xp_dirtree, which reports names
	// and the directory flag only: every Size is 0 and every LastModified is
	// the zero time. Saying so is what stops the dialog printing "0 B" and
	// 0001-01-01 for every file on such a server.
	sizeUnknown := fs.sc.Server.EnumFileSystemIsLegacy()
	entries := make([]dialogs.FileEntry, 0, len(found))
	for _, e := range found {
		entries = append(entries, dialogs.FileEntry{
			Name:        e.Name,
			IsDir:       e.IsDirectory,
			Size:        e.Size,
			ModTime:     e.LastModified,
			SizeUnknown: sizeUnknown,
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

// legacyListingRefusal turns the one silent failure in this file into a spoken
// one.
//
// A pre-2017 instance is listed through xp_dirtree, which returns *no rows and
// no error* to a login that is not sysadmin — indistinguishable from an empty
// directory, so the browser showed one and the user concluded the folder was
// empty. There is nothing in the result to detect: the three facts that make it
// a refusal are the version gate, the empty result, and the login's role.
//
// Deliberately conservative on the last of those. The claim is made only when
// the probe actually ran and actually said "not a sysadmin" — Capabilities
// answers false for a role it was never asked about, so without Probed() every
// unprobed connection would report an empty directory as a permissions problem.
// A sysadmin, or a login we could not ask, still sees the empty listing.
func legacyListingRefusal(sc *db.ServerConn, found []*gosmo.FileSystemEntry) error {
	if len(found) > 0 || !sc.Server.EnumFileSystemIsLegacy() {
		return nil
	}
	caps := sc.Capabilities()
	if !caps.Probed() || caps.IsSysadmin() {
		return nil
	}
	return errors.New("this directory cannot be listed: before SQL Server 2017 the server " +
		"lists directories through xp_dirtree, which requires sysadmin")
}
