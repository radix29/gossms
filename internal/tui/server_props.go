package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverPropPages builds the page set for Server Properties. General and
// Advanced stay read-only (an info page, and a 100+ row raw config dump
// respectively); every other page is editable wherever gosmo has a
// writer for the field shown.
func serverPropPages(sc *db.ServerConn) []propPage {
	return []propPage{
		pageServerGeneral(sc),
		pageServerMemory(sc),
		pageServerProcessors(sc),
		pageServerSecurity(sc),
		pageServerConnections(sc),
		pageServerDatabaseSettings(sc),
		pageServerAdvanced(sc),
		pageServerPermissions(sc),
	}
}

// findConfig returns the option named name, or nil if it isn't present
// (e.g. an option that doesn't exist on this SQL Server version/edition).
func findConfig(configs []*gosmo.ConfigurationOption, name string) *gosmo.ConfigurationOption {
	for _, c := range configs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// configValue returns the in-use value of the named sp_configure option as
// a string, or "N/A" if the option isn't present on this server.
func configValue(configs []*gosmo.ConfigurationOption, name string) string {
	if c := findConfig(configs, name); c != nil {
		return strconv.FormatInt(c.ValueInUse, 10)
	}
	return "N/A"
}

// configRow pairs an editable Int row with the sys.configurations option
// name it edits, so a page's apply closure can write back only the ones
// that changed.
type configRow struct {
	name string
	row  *propsheet.TextRow
}

// configBoolRow is configRow's Check-row counterpart, for 0/1 options.
type configBoolRow struct {
	name string
	row  *propsheet.CheckRow
}

// newConfigEditor returns a builder that creates an editable Int row for
// a named sp_configure option (range-validated against the option's own
// Minimum/Maximum), appending it to *tracked so the page's apply closure
// can find it later. An option missing on this server/edition renders as
// a disabled "N/A" row instead.
func newConfigEditor(configs []*gosmo.ConfigurationOption, tracked *[]configRow) func(name, label, unit string) *propsheet.TextRow {
	return func(name, label, unit string) *propsheet.TextRow {
		c := findConfig(configs, name)
		if c == nil {
			row := propsheet.Text(label, "N/A", 12)
			row.SetEnabled(false)
			return row
		}
		row := propsheet.Int(label, c.ValueInUse, c.Minimum, c.Maximum, unit)
		*tracked = append(*tracked, configRow{name: name, row: row})
		return row
	}
}

// newConfigBoolEditor is newConfigEditor's Check-row counterpart, for
// options whose value is conventionally 0/1.
func newConfigBoolEditor(configs []*gosmo.ConfigurationOption, tracked *[]configBoolRow) func(name, label string) *propsheet.CheckRow {
	return func(name, label string) *propsheet.CheckRow {
		c := findConfig(configs, name)
		row := propsheet.Check(label, c != nil && c.ValueInUse != 0)
		if c == nil {
			return row
		}
		*tracked = append(*tracked, configBoolRow{name: name, row: row})
		return row
	}
}

// applyConfigRows writes back every dirty row in intRows/boolRows via
// ConfigurationOption.SetValueContext. It does not call Reconfigure —
// callers combine this with any other sp_configure-backed change (e.g.
// the Processors page's affinity bitmasks) and call
// Server.ReconfigureContext once at the end.
func applyConfigRows(ctx context.Context, sc *db.ServerConn, intRows []configRow, boolRows []configBoolRow) (changed bool, err error) {
	for _, cr := range intRows {
		if !cr.row.Dirty() {
			continue
		}
		v, err := cr.row.IntValue()
		if err != nil {
			return changed, err
		}
		opt, err := sc.Server.ConfigurationByNameContext(ctx, cr.name)
		if err != nil {
			return changed, err
		}
		if err := opt.SetValueContext(ctx, v); err != nil {
			return changed, err
		}
		changed = true
	}
	for _, cr := range boolRows {
		if !cr.row.Dirty() {
			continue
		}
		v := int64(0)
		if cr.row.Checked() {
			v = 1
		}
		opt, err := sc.Server.ConfigurationByNameContext(ctx, cr.name)
		if err != nil {
			return changed, err
		}
		if err := opt.SetValueContext(ctx, v); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// configApply returns an apply closure for pages whose only edits are
// plain sp_configure-backed rows: write back every dirty one, then call
// Reconfigure once if anything changed.
func configApply(sc *db.ServerConn, intRows []configRow, boolRows []configBoolRow) propApply {
	return func(ctx context.Context) error {
		changed, err := applyConfigRows(ctx, sc, intRows, boolRows)
		if err != nil {
			return err
		}
		if changed {
			return sc.Server.ReconfigureContext(ctx, false)
		}
		return nil
	}
}
