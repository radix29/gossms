package dialogs

import (
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// Tab completion
// ---------------------------------------------------------------------------

// completeField extends f's value with the longest prefix shared by every
// directory entry (dirsOnly, for the path field) or every entry (the name field)
// matching what's typed after the last separator: shell-style Tab completion.
// Returns false, leaving f untouched, when there's nothing to complete, so the
// caller falls through to Tab focus-cycling.
func (d *FileDialog) completeField(f *widgets.InputField, dirsOnly bool) bool {
	fs := d.FileSystem()
	val := f.Value()
	dirPart, base := fs.Split(val)
	searchDir := d.dir
	if dirPart != "" {
		if fs.IsAbs(dirPart) {
			searchDir = fs.Clean(dirPart)
		} else {
			searchDir = fs.Join(d.dir, dirPart)
		}
	}
	// Complete against the directory already on screen, not a second listing: this
	// runs on every Tab, a network round trip on a remote filesystem. d.entries
	// carries the dialog's own ".." row, which fs.List never reports and which would
	// poison the common prefix.
	infos := d.entries
	if searchDir != d.dir {
		var err error
		if infos, err = fs.List(searchDir); err != nil {
			return false
		}
	}
	var candidates []string
	for _, e := range infos {
		if e.Name == ".." {
			continue
		}
		if dirsOnly && !e.IsDir {
			continue
		}
		if base == "" || strings.HasPrefix(e.Name, base) {
			candidates = append(candidates, e.Name)
		}
	}
	if len(candidates) == 0 {
		return false
	}
	common := commonPrefix(candidates)
	if common == "" || common == base {
		return false
	}
	completed := dirPart + common
	if len(candidates) == 1 {
		// A failed probe just leaves the trailing separator off.
		if _, isDir, _ := fs.Exists(fs.Join(searchDir, common)); isDir {
			completed += fs.Separator()
		}
	}
	f.SetValue(completed)
	return true
}

// commonPrefix returns the longest string every element of strs starts with
// (strs is never empty). It compares rune by rune: a byte prefix could end
// mid-rune on multi-byte filenames, and InputField.SetValue's []rune(v) turns
// that invalid tail into a stray replacement character.
func commonPrefix(strs []string) string {
	prefix := []rune(strs[0])
	for _, s := range strs[1:] {
		r := []rune(s)
		n := min(len(r), len(prefix))
		i := 0
		for i < n && prefix[i] == r[i] {
			i++
		}
		prefix = prefix[:i]
	}
	return string(prefix)
}

// formatFileSize renders n bytes as a short human-readable size (e.g.
// "1.8 KB"), matching the mockup's column style.
func formatFileSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGT"[exp]) + "B"
}
