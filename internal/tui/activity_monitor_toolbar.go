package tui

import (
	"fmt"

	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
)

// buildTools rebuilds the toolbar for the active tab: rate and Pause/Continue
// on dashboards, Refresh on procedure tabs. Positions come from toolRect, so
// SetBounds calls this too.
func (am *ActivityMonitor) buildTools() {
	am.tools.Cells = am.tools.Cells[:0]
	switch {
	case am.tab.canvasTab():
		// One arm for all dashboards, via feed().
		f := am.feed()
		am.toolPrefix = f.prefix
		off := f.stopped()
		for i, label := range f.rateLabels {
			am.tools.Cells = append(am.tools.Cells, controls.ToolCell{
				Label:    label,
				Selected: i == f.rateIdx,
				Disabled: off,
				Action:   func() { am.setRate(i) },
			})
		}
		if off {
			// A stopped collector leaves nothing to pause; Retry is the one
			// control that can act.
			am.tools.Cells = append(am.tools.Cells, controls.ToolCell{Label: "Retry", Action: am.restartCollector})
			break
		}
		label := "Pause"
		if f.paused {
			label = "Continue"
		}
		am.tools.Cells = append(am.tools.Cells, controls.ToolCell{Label: label, Action: func() { am.setPaused(!f.paused) }})
	default:
		pt := am.procTab()
		am.toolPrefix = ""
		am.tools.Cells = append(am.tools.Cells, controls.ToolCell{
			Label:    "Refresh",
			Disabled: pt.busy,
			Action:   pt.refresh,
		})
		// Installing into master needs sysadmin; a db_owner gets "Cannot alter the
		// procedure 'sp_block'...". CONTROL SERVER rather than a role test, since
		// HAS_PERMS_BY_NAME answers 1 for sysadmin while IS_SRVROLEMEMBER doesn't.
		// Asked of the panel's connection, which the app probed, as the Kill
		// item is: the tab's own is a clone nothing probes, and an unprobed
		// connection fails open, so asking it left Install live for every login.
		denied := !gate.Allows(am.conn, "", gate.ControlServer)
		// Install while master lacks the procedure; Update while master's copy
		// is not this build's. Update is hidden, not dimmed, without
		// CONTROL SERVER: a login that can't read the definition sees it as
		// differing, so the offer would claim a staleness nobody checked.
		label := "Install in master"
		offer := pt.loc != activity.ProcMaster
		if !offer && pt.masterStale && !denied {
			label, offer = "Update in master", true
		}
		if offer {
			// The right is named only when it is why the cell is grey: while a
			// read is out the grey speaks for itself, and naming CONTROL SERVER
			// to a sysadmin would name a right that is not the reason.
			reason := ""
			if denied && !pt.busy && pt.conn != nil {
				reason = gate.RequiresText(gate.ControlServer)
			}
			am.tools.Cells = append(am.tools.Cells, controls.ToolCell{
				Label:    label,
				Disabled: pt.busy || pt.conn == nil || denied,
				Reason:   reason,
				Action:   pt.confirmInstallInMaster,
			})
		}
	}
	am.layoutTools()
}

// layoutTools assigns control rects, collapsing what doesn't fit into "More ▾".
func (am *ActivityMonitor) layoutTools() {
	am.toolsEnd = am.tools.Layout(am.toolRect, am.toolPrefix)
}

// prefixVisible reports whether the rate selector's label is drawn; "More ▾"
// replaces it on a narrow row, and they'd otherwise overlap.
func (am *ActivityMonitor) prefixVisible() bool {
	return am.tools.PrefixShown(am.toolRect)
}

// runTool invokes cell i's action or explains why not — the single gate for
// clicks and the overflow menu (ToolRow.Run, reading the cells' own fields).
func (am *ActivityMonitor) runTool(i int) {
	am.tools.Run(i, nil, nil, am.app.setStatus)
}

// showOverflowMenu pops the hidden controls under "More ▾".
func (am *ActivityMonitor) showOverflowMenu() {
	r := am.tools.More.Rect
	if r.IsZero() {
		r = core.Rect{X: am.rect.X, Y: am.rect.Y}
	}
	am.app.contextMenu.Show(r.X, r.Y+1, am.tools.OverflowItems(nil, nil, am.runTool))
}

// resolution names what one plotted column covers. Follows drawInterval, not
// the feed rate: TempDB and Instance columns differ from the activity rate. The
// toolbar selector shows the poll rate.
func (am *ActivityMonitor) resolution() string {
	return fmt.Sprintf("%d sec", int(am.drawInterval().Seconds()))
}

// collectionState is the toolbar's right-hand summary: collecting or not, the
// sample shown, and sample spacing. Longest variant first; the caller draws the
// first that fits, so narrow panels drop the message before the stopped state.
func (am *ActivityMonitor) collectionState() []string {
	f := am.feed()
	paused, collecting, sampleTime, status := f.paused, f.collecting, f.sampleTime, f.status
	state := "not collecting"
	switch {
	case paused:
		state = "PAUSED"
	case collecting:
		state = "collecting"
	}
	if sampleTime != "" {
		state += "  " + sampleTime
	}
	short := state + "  (" + am.resolution() + ")"
	if status == "" {
		return []string{short, state}
	}
	return []string{status + "  \u2014  " + short, short, state}
}

// header is both dashboards' identification strip, from the connection and
// collector state.
func (am *ActivityMonitor) header() dashboard.Header {
	f := am.feed()
	h := dashboard.Header{
		Instance:   am.conn.Opts.Server,
		Resolution: am.resolution(),
		SampleTime: f.sampleTime,
		Status:     f.status,
		Paused:     f.paused,
	}
	if am.conn.Server != nil && am.conn.Server.Info() != nil {
		info := am.conn.Server.Info()
		h.Version = "SQL Server " + info.ProductVersion
		h.Host = info.Edition
	}
	return h
}
