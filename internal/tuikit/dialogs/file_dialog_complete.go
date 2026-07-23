package dialogs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// Tab completion
// ---------------------------------------------------------------------------

// completeField extends f's current value with the longest name prefix
// shared by every directory entry (dirsOnly, for the path field — it only
// ever navigates directories) or every entry (the name field) matching
// what's typed after the last path separator — shell-style Tab completion.
// Returns false (leaving f untouched) when there's nothing to complete, so
// the caller can fall through to ordinary Tab focus-cycling instead, the
// same convention every other tuikit field follows for keys it doesn't act
// on.
func (d *FileDialog) completeField(f *widgets.InputField, dirsOnly bool) bool {
	val := f.Value()
	dirPart, base := filepath.Split(val)
	searchDir := d.dir
	if dirPart != "" {
		if filepath.IsAbs(dirPart) {
			searchDir = filepath.Clean(dirPart)
		} else {
			searchDir = filepath.Join(d.dir, dirPart)
		}
	}
	infos, err := os.ReadDir(searchDir)
	if err != nil {
		return false
	}
	var candidates []string
	for _, e := range infos {
		if dirsOnly && !e.IsDir() {
			continue
		}
		if base == "" || strings.HasPrefix(e.Name(), base) {
			candidates = append(candidates, e.Name())
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
		if info, err := os.Stat(filepath.Join(searchDir, common)); err == nil && info.IsDir() {
			completed += string(filepath.Separator)
		}
	}
	f.SetValue(completed)
	return true
}

// commonPrefix returns the longest string every element of strs starts
// with. strs is never empty when called.
func commonPrefix(strs []string) string {
	prefix := strs[0]
	for _, s := range strs[1:] {
		for len(prefix) > 0 && !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
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
