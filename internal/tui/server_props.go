package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverPropPages builds the page set for Server Properties. General stays
// read-only — it is an info page with no apply at all; every other page is
// editable wherever gosmo has a writer for the field shown. Advanced is
// editable too: a group of sp_configure rows over a read-only grid of every
// remaining option (see pageServerAdvanced).
func serverPropPages(sc *db.ServerConn) []propPage {
	// Every page but General writes through sp_configure + RECONFIGURE, which
	// is ALTER SETTINGS; Permissions issues GRANT/DENY at the server, which is
	// CONTROL SERVER. General has no apply at all and so needs nothing.
	return []propPage{
		pageServerGeneral(sc),
		withRequires(pageServerMemory(sc), "", gate.AlterSettings),
		withRequires(pageServerProcessors(sc), "", gate.AlterSettings),
		withRequires(pageServerSecurity(sc), "", gate.AlterSettings),
		withRequires(pageServerConnections(sc), "", gate.AlterSettings),
		withRequires(pageServerDatabaseSettings(sc), "", gate.AlterSettings),
		withRequires(pageServerAdvanced(sc), "", gate.AlterSettings),
		withRequires(pageServerPermissions(sc), "", gate.ControlServer),
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

// configChanges returns one gosmo.ConfigChange for every dirty row in
// intRows/boolRows. It writes nothing: callers add any other sp_configure-
// backed change (the Processors page's affinity bitmasks, Database Settings'
// FILESTREAM level) and hand the whole set to Server.ApplyConfiguration, so
// the page issues one batch with one RECONFIGURE.
//
// One batch is what makes an advanced option writable at all: a bare
// sp_configure of max degree of parallelism, max server memory, fill factor
// … fails Msg 15123 on a server with "show advanced options" at 0, which is
// how every stock installation ships. ApplyConfiguration turns it on for the
// batch and puts it back, as SSMS does.
func configChanges(intRows []configRow, boolRows []configBoolRow) ([]gosmo.ConfigChange, error) {
	var changes []gosmo.ConfigChange
	for _, cr := range intRows {
		if !cr.row.Dirty() {
			continue
		}
		v, err := cr.row.IntValue()
		if err != nil {
			return nil, err
		}
		changes = append(changes, gosmo.ConfigChange{Name: cr.name, Value: v})
	}
	for _, cr := range boolRows {
		if !cr.row.Dirty() {
			continue
		}
		v := int64(0)
		if cr.row.Checked() {
			v = 1
		}
		changes = append(changes, gosmo.ConfigChange{Name: cr.name, Value: v})
	}
	return changes, nil
}

// configApply returns an apply closure for pages whose only edits are
// plain sp_configure-backed rows. Nothing dirty means nothing sent —
// ApplyConfiguration of no changes is a no-op, and a RECONFIGURE there would
// install every *other* pending sp_configure change on the instance.
func configApply(sc *db.ServerConn, intRows []configRow, boolRows []configBoolRow) propApply {
	return func(ctx context.Context) error {
		changes, err := configChanges(intRows, boolRows)
		if err != nil {
			return err
		}
		return sc.Server.ApplyConfiguration(ctx, changes, gosmo.ConfigApplyOptions{})
	}
}
