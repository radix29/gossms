package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// xevent_session_advanced.go is the Advanced page of New Session and Session
// Properties (see xevent_session_dialog.go): retention, dispatch latency,
// buffer memory and partitioning, and on SQL Server 2025 the maximum
// duration.

// xeRetentionItems and xeRetentionValues are one table split in two: item i
// means value i. xevent_session_dialog_test.go pins the pairing by name.
var (
	xeRetentionItems  = []string{"Single event loss", "Multiple event loss", "No event loss"}
	xeRetentionValues = []string{gosmo.XERetentionAllowSingleEventLoss, gosmo.XERetentionAllowMultipleEventLoss, gosmo.XERetentionNoEventLoss}

	xePartitionItems  = []string{"None", "Per node", "Per CPU"}
	xePartitionValues = []string{gosmo.XEPartitionNone, gosmo.XEPartitionPerNode, gosmo.XEPartitionPerCPU}
)

// xeNewSessionDefaults are the options New Session opens with — SSMS's New
// Session defaults, written out in the CREATE rather than left to the server,
// so the script says what the session is.
func xeNewSessionDefaults() gosmo.EventSessionSpec {
	return gosmo.EventSessionSpec{
		RetentionMode:       gosmo.XERetentionAllowSingleEventLoss,
		MaxDispatchLatency:  30 * time.Second,
		MaxMemory:           4096,
		MemoryPartitionMode: gosmo.XEPartitionNone,
	}
}

// xeOptionRows are the Advanced page's rows. maxDuration is nil where the
// server has no MAX_DURATION (before SQL Server 2025), so the option is
// neither offered nor written there.
type xeOptionRows struct {
	retention, partition             *propsheet.SelectRow
	latency, maxMemory, maxEventSize *propsheet.TextRow
	maxDuration                      *propsheet.TextRow
}

func newXEOptionRows(spec gosmo.EventSessionSpec, major int, azure bool) *xeOptionRows {
	r := &xeOptionRows{
		retention:    propsheet.Select("Event retention mode", xeRetentionItems, 0),
		partition:    propsheet.Select("Memory partition mode", xePartitionItems, 0),
		latency:      propsheet.Int("Maximum dispatch latency", 0, 0, 2147483, "s (0 = unlimited)"),
		maxMemory:    propsheet.Int("Maximum memory size", 0, 1, 2147483647, "KB"),
		maxEventSize: propsheet.Int("Maximum event size", 0, 0, 2147483647, "KB (0 = none)"),
	}
	if major >= 17 && !azure {
		r.maxDuration = propsheet.Int("Maximum duration", 0, 0, 2147483647, "s (0 = unlimited)")
	}
	r.set(spec)
	return r
}

// set shows spec's options and makes them the rows' baseline — on load, and
// when a New Session template replaces them.
func (r *xeOptionRows) set(spec gosmo.EventSessionSpec) {
	r.retention.SetSelected(max(slices.Index(xeRetentionValues, spec.RetentionMode), 0))
	r.partition.SetSelected(max(slices.Index(xePartitionValues, spec.MemoryPartitionMode), 0))
	latency := int64(0) // XEInfinite
	if spec.MaxDispatchLatency > 0 {
		latency = int64(spec.MaxDispatchLatency / time.Second)
	}
	r.latency.SetValue(strconv.FormatInt(latency, 10))
	r.maxMemory.SetValue(strconv.FormatInt(int64(spec.MaxMemory), 10))
	r.maxEventSize.SetValue(strconv.FormatInt(int64(spec.MaxEventSize), 10))
	if r.maxDuration != nil {
		r.maxDuration.SetValue(strconv.FormatInt(int64(spec.MaxDuration/time.Second), 10))
	}
}

// apply writes the rows into spec. Rows that don't parse have already been
// refused by validate; a zero written here is then never a typo's.
func (r *xeOptionRows) apply(spec *gosmo.EventSessionSpec) {
	spec.RetentionMode = xeRetentionValues[r.retention.Selected()]
	spec.MemoryPartitionMode = xePartitionValues[r.partition.Selected()]
	spec.MaxDispatchLatency = gosmo.XEInfinite
	if n, _ := r.latency.IntValue(); n > 0 {
		spec.MaxDispatchLatency = time.Duration(n) * time.Second
	}
	if n, _ := r.maxMemory.IntValue(); n > 0 {
		spec.MaxMemory = int(n)
	}
	n, _ := r.maxEventSize.IntValue()
	spec.MaxEventSize = int(n)
	if r.maxDuration != nil {
		n, _ := r.maxDuration.IntValue()
		spec.MaxDuration = time.Duration(n) * time.Second
	}
}

// validate is every numeric row's own check, for New Session's preflight —
// the sheet validates only the rows that changed, and a New dialog writes
// them all.
func (r *xeOptionRows) validate() error {
	for _, row := range []*propsheet.TextRow{r.latency, r.maxMemory, r.maxEventSize, r.maxDuration} {
		if row == nil {
			continue
		}
		if err := row.Validate(); err != nil {
			return fmt.Errorf("%s: %w", row.Label(), err)
		}
	}
	return nil
}

// form lays the rows out. running, when non-nil, is Session Properties
// saying the session runs, which is when an Apply here stops and restarts it.
func (r *xeOptionRows) form(running *bool) *propsheet.Form {
	rows := []propsheet.Row{
		propsheet.Section("Event retention"),
		r.retention,
		propsheet.Section("Dispatch"),
		r.latency,
		propsheet.Section("Memory"),
		r.maxMemory,
		r.maxEventSize,
		r.partition,
	}
	if r.maxDuration != nil {
		rows = append(rows, propsheet.Section("Duration"), r.maxDuration)
	}
	rows = append(rows, propsheet.Note("Single event loss drops an event when the buffers are full; multiple event loss drops a whole buffer; no event loss makes the queries that fire events wait instead. The dispatch latency bounds how long an event waits in memory before reaching the targets — the XEvent viewer's live lag. A maximum event size above 0 sets aside a separate buffer for events too large for the others."))
	f := propsheet.NewForm(rows...)
	if running != nil {
		f.SetApplyConfirm(func() string {
			if *running {
				return xeRunningAlterWarning
			}
			return ""
		})
	}
	return f
}

// pageXESessionAdvanced is Session Properties > Advanced.
func pageXESessionAdvanced(sc *db.ServerConn, scope xeScope, name string) propPage {
	return propPage{
		title: "Advanced",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			es, err := scope.byName(ctx, sc, name)
			if err != nil {
				return nil, nil, err
			}
			rows := newXEOptionRows(es.Spec(), serverMajor(sc), sc.Server.Info().IsAzure())
			running := es.IsRunning
			apply := func(ctx context.Context) error {
				return alterXESession(ctx, sc, scope, name, rows.apply)
			}
			return rows.form(&running), apply, nil
		},
	}
}
