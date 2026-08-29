package tui

import (
	"context"
	"fmt"
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
	// Every page but General writes through sp_configure + RECONFIGURE, which
	// is ALTER SETTINGS; Permissions issues GRANT/DENY at the server, which is
	// CONTROL SERVER. General has no apply at all and so needs nothing.
	return []propPage{
		pageServerGeneral(sc),
		withRequires(pageServerMemory(sc), "", rightAlterSettings),
		withRequires(pageServerProcessors(sc), "", rightAlterSettings),
		withRequires(pageServerSecurity(sc), "", rightAlterSettings),
		withRequires(pageServerConnections(sc), "", rightAlterSettings),
		withRequires(pageServerDatabaseSettings(sc), "", rightAlterSettings),
		withRequires(pageServerAdvanced(sc), "", rightAlterSettings),
		withRequires(pageServerPermissions(sc), "", rightControlServer),
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

// newConfigBoolEditor is newConfigEditor's Check-row counterpart, for options
// whose value is conventionally 0/1. It returns a Row, not a CheckRow, because
// an option missing on this server/edition renders as the same disabled "N/A"
// row newConfigEditor uses.
//
// The N/A row is the point, not a detail: sys.configurations is edition- and
// version-dependent, so several of the options the Advanced page lists are
// simply absent on an older or lesser instance. A live checkbox there is left
// out of *tracked, so the user ticks "xp_cmdshell", presses OK, and is told it
// succeeded while nothing was ever sent — the "never let a control silently do
// nothing" rule, one page down from the menus it is usually stated about.
func newConfigBoolEditor(configs []*gosmo.ConfigurationOption, tracked *[]configBoolRow) func(name, label string) propsheet.Row {
	return func(name, label string) propsheet.Row {
		c := findConfig(configs, name)
		if c == nil {
			row := propsheet.Text(label, "N/A", 12)
			row.SetEnabled(false)
			return row
		}
		row := propsheet.Check(label, c.ValueInUse != 0)
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
	// sys.configurations comes back once for the whole apply instead of once
	// per dirty row: it is a single ~80-row read either way, so a page with
	// three dirty options pays one round trip rather than three. Fetched
	// lazily so an apply with nothing dirty still costs nothing.
	lookup := configLookup(sc)

	for _, cr := range intRows {
		if !cr.row.Dirty() {
			continue
		}
		v, err := cr.row.IntValue()
		if err != nil {
			return changed, err
		}
		opt, err := lookup(ctx, cr.name)
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
		opt, err := lookup(ctx, cr.name)
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

// configLookup returns a by-name option lookup that reads sys.configurations
// at most once, on the first call. An unknown name is reported the way
// Server.ConfigurationByNameContext reports it, so callers see no difference.
func configLookup(sc *db.ServerConn) func(context.Context, string) (*gosmo.ConfigurationOption, error) {
	var byName map[string]*gosmo.ConfigurationOption
	return func(ctx context.Context, name string) (*gosmo.ConfigurationOption, error) {
		if byName == nil {
			opts, err := sc.Server.ConfigurationsContext(ctx)
			if err != nil {
				return nil, err
			}
			byName = make(map[string]*gosmo.ConfigurationOption, len(opts))
			for _, o := range opts {
				byName[o.Name] = o
			}
		}
		opt, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("gosmo: configuration option %q not found", name)
		}
		return opt, nil
	}
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
