package dialogs

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileEntry is one file or directory in a FileDialog listing.
type FileEntry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time

	// SizeUnknown marks an entry whose FileSystem could not report a size, as
	// opposed to a genuinely empty one. A zero ModTime needs no flag (no file is
	// dated year 1), but a zero Size cannot say it, and "0 B" for every file states
	// something false. goSSMS's server-side listing sets it on pre-2017 instances,
	// where xp_dirtree reports names only.
	SizeUnknown bool
}

// FileSystem is the filesystem a FileDialog browses. Every path operation goes
// through it, so a host can point the dialog at another machine: goSSMS browses
// the SQL Server host's filesystem, with different path conventions
// (backslashes, drive letters) from the client's. So none of these can be
// `path/filepath` calls at the call site: filepath uses the client's rules, and a
// Linux client joining a Windows server path would produce
// "/home/user/C:\Backup\db.bak".
//
// Implementations may block (LocalFileSystem doesn't, a network one does), so
// navigation costs whatever the implementation costs.
type FileSystem interface {
	PathRules

	// List returns dir's immediate children, in any order. A "." or ".."
	// entry, if the underlying filesystem reports one, is filtered by the
	// dialog; it supplies its own ".." row.
	List(dir string) ([]FileEntry, error)
	// Default is the directory to open when the caller supplies no start
	// path.
	Default() string
	// Exists reports whether path exists and whether it is a directory. A path
	// that isn't there is (false, false, nil); err is for a filesystem that couldn't
	// ask (a remote timeout or lost connection). The save-overwrite guard treats
	// "couldn't ask" as "assume it's there" and prompts; folding the two would
	// silently skip the prompt.
	Exists(path string) (exists, isDir bool, err error)
}

// BlockingFileSystem is implemented by a FileSystem whose calls reach off this
// machine. Each such call stops the event loop until it returns, so a FileDialog
// paints a "Listing ..." frame before it, and skips that repaint for a
// filesystem that doesn't claim to block (it would only flicker locally).
type BlockingFileSystem interface {
	// Blocking reports whether a call may take long enough to need feedback.
	Blocking() bool
}

// PathRules is the half of FileSystem that only manipulates path strings.
// WindowsPathRules and PosixPathRules implement it, so a remote-host FileSystem
// embeds the matching one and supplies only the three filesystem-reaching
// methods.
type PathRules interface {
	// Clean normalizes dir into the absolute form the dialog stores and
	// displays.
	Clean(dir string) string
	// Join appends a single name to dir.
	Join(dir, name string) string
	// Split separates path into its directory part (with trailing
	// separator, or "" if path names no directory) and its final element,
	// like filepath.Split.
	Split(path string) (dir, name string)
	// Parent returns dir's parent, or dir itself when dir is a root. The
	// dialog omits its ".." row when the two are equal.
	Parent(dir string) string
	// IsAbs reports whether path is absolute.
	IsAbs(path string) bool
	// Separator is the path separator, used to join and to mark directory
	// rows in the listing.
	Separator() string
}

// LocalFileSystem is the FileSystem a FileDialog uses unless the caller
// supplies another: the local machine, via os and path/filepath.
type LocalFileSystem struct{}

func (LocalFileSystem) List(dir string) ([]FileEntry, error) {
	infos, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, len(infos))
	for _, e := range infos {
		fe := FileEntry{Name: e.Name(), IsDir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			fe.Size = info.Size()
			fe.ModTime = info.ModTime()
		}
		entries = append(entries, fe)
	}
	return entries, nil
}

func (LocalFileSystem) Default() string {
	wd, _ := os.Getwd()
	return wd
}

func (LocalFileSystem) Clean(dir string) string {
	clean := filepath.Clean(dir)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return clean
}

func (LocalFileSystem) Join(dir, name string) string       { return filepath.Join(dir, name) }
func (LocalFileSystem) Split(path string) (string, string) { return filepath.Split(path) }
func (LocalFileSystem) Parent(dir string) string           { return filepath.Dir(dir) }
func (LocalFileSystem) IsAbs(path string) bool             { return filepath.IsAbs(path) }
func (LocalFileSystem) Separator() string                  { return string(filepath.Separator) }

// Exists never reports an error: os.Stat's only interesting failure here is
// "not there", which is an answer rather than an inability to answer.
func (LocalFileSystem) Exists(path string) (bool, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, false, nil
	}
	return true, info.IsDir(), nil
}

// WindowsPathRules implements the path half of FileSystem for Windows-style
// paths (backslash, "C:\" roots) without path/filepath, so it behaves the same
// on any client OS. A remote Windows host's FileSystem embeds it and supplies
// List, Default and Exists.
type WindowsPathRules struct{}

func (WindowsPathRules) Separator() string { return `\` }

// IsAbs accepts a drive-letter path ("C:\...") or a UNC path
// (`\\server\share`).
func (WindowsPathRules) IsAbs(path string) bool {
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func (WindowsPathRules) Split(path string) (dir, name string) {
	i := strings.LastIndexAny(path, `\/`)
	if i < 0 {
		return "", path
	}
	return path[:i+1], path[i+1:]
}

func (w WindowsPathRules) Join(dir, name string) string {
	if dir == "" {
		return name
	}
	return strings.TrimRight(dir, `\/`) + `\` + name
}

// Clean normalizes slashes to backslashes, collapses repeats, resolves "." and
// "..", and leaves a drive root ("C:\") its trailing separator so it stays
// distinct from the drive-list level above. A UNC root comes back as
// `\\server\share` without one; Parent recognizes it by component count.
func (w WindowsPathRules) Clean(dir string) string {
	if dir == "" {
		return ""
	}
	s := strings.ReplaceAll(dir, "/", `\`)
	prefix := ""
	if strings.HasPrefix(s, `\\`) {
		prefix = `\\`
		s = s[2:]
	} else if len(s) >= 2 && s[1] == ':' {
		prefix = s[:2] + `\`
		s = s[2:]
	}
	var parts []string
	for p := range strings.SplitSeq(s, `\`) {
		switch p {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, p)
		}
	}
	if prefix == "" {
		return strings.Join(parts, `\`)
	}
	return prefix + strings.Join(parts, `\`)
}

// Parent returns "" for a drive root, which is the drive-list level: a
// FileSystem built on these rules lists the host's fixed drives for "".
func (w WindowsPathRules) Parent(dir string) string {
	clean := w.Clean(dir)
	if clean == "" {
		return ""
	}
	// A UNC path bottoms out at the share root, the shallowest thing any host can
	// enumerate, so its parent is the drive list. Walking by separator would offer
	// `\\server` then `\`, which list nothing.
	if strings.HasPrefix(clean, `\\`) {
		if strings.Count(clean[2:], `\`) < 2 {
			return ""
		}
	}
	i := strings.LastIndex(clean, `\`)
	if i <= 0 {
		return ""
	}
	// Clean leaves a trailing separator only on a root ("C:\"), so this is the drive
	// root, whose parent is the drive list. Checked before the drive-letter case,
	// which would return "C:\" as its own parent and strand the browse.
	if i == len(clean)-1 {
		return ""
	}
	// "C:\Backup" -> "C:\", not "C:"; a bare "C:" is not a directory.
	if clean[i-1] == ':' {
		return clean[:i+1]
	}
	return clean[:i]
}

// PosixPathRules is the WindowsPathRules counterpart for "/"-separated hosts,
// likewise avoiding path/filepath so a Windows client browsing Linux is correct.
type PosixPathRules struct{}

func (PosixPathRules) Separator() string   { return "/" }
func (PosixPathRules) IsAbs(p string) bool { return strings.HasPrefix(p, "/") }

func (PosixPathRules) Split(path string) (dir, name string) {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "", path
	}
	return path[:i+1], path[i+1:]
}

func (PosixPathRules) Join(dir, name string) string {
	if dir == "" {
		return name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

func (PosixPathRules) Clean(dir string) string {
	if dir == "" {
		return "/"
	}
	abs := strings.HasPrefix(dir, "/")
	var parts []string
	for p := range strings.SplitSeq(dir, "/") {
		switch p {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, p)
		}
	}
	joined := strings.Join(parts, "/")
	if abs {
		return "/" + joined
	}
	return joined
}

func (p PosixPathRules) Parent(dir string) string {
	clean := p.Clean(dir)
	parent, _, found := strings.CutLast(clean, "/")
	if !found {
		return clean
	}
	if parent == "" {
		return "/"
	}
	return parent
}
