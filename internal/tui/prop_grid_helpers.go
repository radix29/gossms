package tui

import (
	"context"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// permEntry is the common shape of gosmo.ServerPermissionEntry and
// gosmo.DatabasePermissionEntry (Principal/PrincipalType/Grantor/
// Permission/State, all strings) — normalized at the call site so the
// permissions-grid editor, identical for both scopes, is built once.
type permEntry struct {
	Principal     string
	PrincipalType string
	Grantor       string
	Permission    string
	State         string
}

// permEdit tracks one grid row's pending state through the cycle
// GRANT -> DENY -> "" (revoke) -> GRANT, driven by Space/Enter/click on
// the State column.
type permEdit struct {
	entry   permEntry
	orig    string
	current string
}

var permGridColumns = []string{"Principal", "Type", "Permission", "Grantor", "State"}

func nextPermState(s string) string {
	switch s {
	case "GRANT", "GRANT_WITH_GRANT_OPTION":
		return "DENY"
	case "DENY":
		return ""
	default:
		return "GRANT"
	}
}

func displayPermState(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// buildPermissionsForm builds a Section+GridRow permissions editor: a
// cell-cursor grid whose State column cycles on activation, wired to
// grant/deny/revokeFn for whichever scope (server- or database-level
// GRANT/DENY/REVOKE) the caller needs. Only existing entries can be
// edited — adding a brand-new grant for a principal/permission pair that
// has none yet isn't supported in this pass.
func buildPermissionsForm(sectionTitle string, entries []permEntry, gridHeight int,
	grantFn, denyFn, revokeFn func(ctx context.Context, permission, principal string) error,
) (*propsheet.Form, propApply) {
	edits := make([]*permEdit, len(entries))
	for i, e := range entries {
		edits[i] = &permEdit{entry: e, orig: e.State, current: e.State}
	}

	rowsFor := func() [][]string {
		rows := make([][]string, len(edits))
		for i, e := range edits {
			rows[i] = []string{e.entry.Principal, e.entry.PrincipalType, e.entry.Permission, e.entry.Grantor, displayPermState(e.current)}
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData(permGridColumns, rowsFor())
	grid.SetCellCursor(true)
	grid.OnActivateCell = func(row, col int) {
		if col != 4 || row < 0 || row >= len(edits) {
			return
		}
		edits[row].current = nextPermState(edits[row].current)
		grid.SetData(permGridColumns, rowsFor())
		grid.SetSelectedRow(row)
	}

	gridRow := propsheet.NewGridRow(grid, gridHeight)
	gridRow.DirtyFn = func() bool {
		for _, e := range edits {
			if e.current != e.orig {
				return true
			}
		}
		return false
	}
	gridRow.RevertFn = func() {
		for _, e := range edits {
			e.current = e.orig
		}
		grid.SetData(permGridColumns, rowsFor())
	}

	f := propsheet.NewForm(
		propsheet.Section(sectionTitle),
		gridRow,
		propsheet.Note("Space/Enter (or click) on State cycles Grant → Deny → (none)."),
	)

	apply := func(ctx context.Context) error {
		for _, e := range edits {
			if e.current == e.orig {
				continue
			}
			var err error
			switch e.current {
			case "GRANT":
				err = grantFn(ctx, e.entry.Permission, e.entry.Principal)
			case "DENY":
				err = denyFn(ctx, e.entry.Permission, e.entry.Principal)
			case "":
				err = revokeFn(ctx, e.entry.Permission, e.entry.Principal)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// boolStr renders a bool as "True"/"False", the Static-row convention used
// throughout the Properties pages.
func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// boolIdx maps a bool onto a two-item Select/Radio row's index — 1 for
// true, 0 for false — matching the [off, on] / [enabled, disabled] item
// ordering every such row on these pages uses.
func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

// indexOf returns the index of value within items, or 0 (the first item)
// if it isn't present — the fallback a Select/Radio row's "selected" index
// needs when the server reports a value outside the row's known options.
func indexOf(items []string, value string) int {
	for i, it := range items {
		if it == value {
			return i
		}
	}
	return 0
}

// orDefault returns s, or def if s is empty — for server fields that come
// back blank when unset but need a concrete default for indexOf/Select.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// credNames extracts the Name field of every credential, for building a
// Select row's item list.
func credNames(creds []*gosmo.Credential) []string {
	names := make([]string, len(creds))
	for i, c := range creds {
		names[i] = c.Name
	}
	return names
}
