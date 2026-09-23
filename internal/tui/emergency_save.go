package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/radix29/gossms/internal/config"
)

// EmergencySave writes the text of every query panel with unsaved changes to
// config.RecoveredDir, and returns the files it wrote plus one error per panel
// it could not save. cmd/gossms's run calls it from its recover, after App.Run
// has restored the terminal: a panic on the UI goroutine ends the process, and
// without this it takes every unsaved query with it.
//
// Resuming the event loop after the panic instead was rejected — the state the
// panic left mid-mutation is worse than a clean exit with the text saved.
//
// Safe on a nil App and on one whose UI was never built (a panic during
// startup). It draws nothing and touches no connection: it runs after
// screen.Fini, with state the panic may have left half-updated.
func (a *App) EmergencySave() (paths []string, errs []error) {
	if a == nil || a.panels == nil {
		return nil, nil
	}
	dir, err := config.RecoveredDir()
	if err != nil {
		return nil, []error{fmt.Errorf("recovered-query directory: %w", err)}
	}
	return a.emergencySaveTo(dir, time.Now())
}

// emergencySaveTo is EmergencySave writing into dir, stamped with now.
//
// Each panel is saved under its own recover: the panic that brought us here
// may have left one panel's editor inconsistent, and that must not cost the
// others theirs.
func (a *App) emergencySaveTo(dir string, now time.Time) (paths []string, errs []error) {
	stamp := now.Format("20060102-150405")
	for i := 0; i < a.panels.Count(); i++ {
		qp, ok := a.panels.PanelAt(i).(*QueryPanel)
		if !ok {
			continue
		}
		path, err := saveRecoveredPanel(dir, stamp, qp)
		switch {
		case err != nil:
			errs = append(errs, err)
		case path != "":
			paths = append(paths, path)
		}
	}
	return paths, errs
}

// saveRecoveredPanel writes one panel's text if it is dirty, returning "" when
// there was nothing to save.
func saveRecoveredPanel(dir, stamp string, qp *QueryPanel) (path string, err error) {
	title := "query"
	defer func() {
		if r := recover(); r != nil {
			path, err = "", fmt.Errorf("%s: panicked while saving: %v", title, r)
		}
	}()
	title = qp.Title()
	if !qp.Dirty() {
		return "", nil
	}
	text := qp.editor.Text()
	base := stamp + "-" + recoveredFileStem(title)
	// O_EXCL with a counter, never an overwrite: two panels can share a title
	// (the same file opened twice, or two files named alike in different
	// directories), and an earlier crash's files must survive a later one.
	for n := 1; ; n++ {
		name := base + ".sql"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.sql", base, n)
		}
		path = filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) && n < 1000 {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("%s: %w", title, err)
		}
		_, werr := f.WriteString(text)
		serr := f.Sync()
		cerr := f.Close()
		if err := errors.Join(werr, serr, cerr); err != nil {
			return "", fmt.Errorf("%s: %w", title, err)
		}
		return path, nil
	}
}

// recoveredFileStem makes a tab title safe as a file name on every platform:
// anything other than a letter, a digit, '.', '-' and '_' becomes '_', and a
// trailing ".sql" is dropped since one is added back.
func recoveredFileStem(title string) string {
	title = strings.TrimSuffix(title, ".sql")
	stem := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, title)
	if stem == "" || strings.Trim(stem, ".") == "" {
		return "query"
	}
	return stem
}
