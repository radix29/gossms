package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// traceFlagText is what server traceflag on and off share in their
// descriptions (D6).
const traceFlagText = "Needs --allow admin, and sysadmin on the server (without it: exit 9). Always global " +
	"(DBCC TRACEON/TRACEOFF(flag, -1)): a session flag would die with this command's connection. Flags apply " +
	"in the order given; the first the server refuses stops the run (exit 5), and the change set still shows " +
	"what the others did. --dry-run prints the DBCC statements and a planned_change set (flag, current, new) " +
	"without running them. Otherwise the change set holds before, requested and after, from DBCC TRACESTATUS " +
	"read before and after. A flag is lost when the SQL Server service restarts; a lasting one is a startup " +
	"parameter (-T, or mssql-conf traceflag on Linux), which no SQL statement sets."

func serverTraceFlagCommands(d Deps) []*spec.Command {
	flagArg := []spec.Arg{{Name: "flag", Help: "A trace flag number; more may follow", Required: true, Repeat: true}}
	writeOpts := slices.Concat(ConnectOptions(), writeOptions())
	return []*spec.Command{
		{
			Path:    []string{"server", "traceflag"},
			Summary: "List the trace flags that are on, and turn them on or off globally",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"server", "traceflag", "list"},
			Summary: "List the trace flags that are on: global, or this session's",
			Description: "One row per trace flag on now, by flag (DBCC TRACESTATUS(-1), which any login may run). " +
				"global is on for every session — a startup flag (-T) shows the same way, the DBCC does not tell them " +
				"apart; session is on for this command's own session only, so it is 0 for a flag another session " +
				"set. None on is an empty set.",
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.serverTraceFlagList,
			Examples: []string{
				"gossms server traceflag list -p dev",
			},
		},
		{
			Path:    []string{"server", "traceflag", "on"},
			Summary: "Turn trace flags on for every session, until the next restart",
			Description: "DBCC TRACEON for each flag. " + traceFlagText + " The server does not refuse every " +
				"flag it will not honour: a startup-only one (834) is skipped with no error, so a flag the second " +
				"read does not show is a warning. A number the server knows no flag for is refused (Msg 2560; the " +
				"highest accepted varies by version).",
			Args:    flagArg,
			Options: writeOpts,
			Class:   spec.ClassAdmin,
			Run:     d.serverTraceFlagOn,
			Examples: []string{
				"gossms server traceflag on 7745 -p dev --allow admin --dry-run",
				"gossms server traceflag on 1222 3226 -p dev --allow admin",
			},
		},
		{
			Path:        []string{"server", "traceflag", "off"},
			Summary:     "Turn trace flags off for every session",
			Description: "DBCC TRACEOFF for each flag. " + traceFlagText + " A flag set at startup comes back at the next restart.",
			Args:        flagArg,
			Options:     writeOpts,
			Class:       spec.ClassAdmin,
			Run:         d.serverTraceFlagOff,
			Examples: []string{
				"gossms server traceflag off 7745 -p dev --allow admin",
			},
		},
	}
}

// --- server traceflag list --------------------------------------------------

func (d Deps) serverTraceFlagList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	flags, err := cat.TraceFlags(ctx)
	if err != nil {
		return res, readError(err)
	}
	set := output.Set{Columns: []output.Column{
		{Name: "flag", Type: "int"}, {Name: "global", Type: "bit"}, {Name: "session", Type: "bit"},
	}}
	for _, f := range flags {
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(f.Flag)), bitCell(f.Global), bitCell(f.Session)})
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// --- server traceflag on/off ------------------------------------------------

func (d Deps) serverTraceFlagOn(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	return d.setTraceFlags(ctx, inv, true)
}

func (d Deps) serverTraceFlagOff(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	return d.setTraceFlags(ctx, inv, false)
}

// parseTraceFlags is the flags args name, in order, a repeat once. A flag is
// a positive whole number; anything else is exit 2 before connecting. The
// upper bound is the server's (it varies by version).
func parseTraceFlags(args []string) ([]int, error) {
	var flags []int
	for _, a := range args {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 {
			return nil, usageError(fmt.Sprintf("trace flag %q is not a positive whole number", a), "")
		}
		if !slices.Contains(flags, n) {
			flags = append(flags, n)
		}
	}
	return flags, nil
}

// globalFlags is the flags on for every session in a TRACESTATUS read.
func globalFlags(flags []gosmo.TraceFlag) map[int]bool {
	on := map[int]bool{}
	for _, f := range flags {
		if f.Global {
			on[f.Flag] = true
		}
	}
	return on
}

// restartNote is server traceflag on's note (§10.6), when a flag is or would
// be on.
const restartNote = "Trace flags turned on here are lost when the SQL Server service restarts; a lasting one is a " +
	"startup parameter (-T in the service's settings, or mssql-conf traceflag on Linux)."

// setTraceFlags is server traceflag on and off (D6): one TRACESTATUS read,
// the DBCCs, and a second read for what took.
func (d Deps) setTraceFlags(ctx context.Context, inv *spec.Invocation, on bool) (*output.Result, error) {
	flags, err := parseTraceFlags(inv.Args)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	read, err := cat.TraceFlags(ctx)
	if err != nil {
		return res, readError(err)
	}
	before := globalFlags(read)
	apply := func(ctx context.Context) error {
		for _, f := range flags {
			if err := w.SetTraceFlag(ctx, f, on); err != nil {
				return err
			}
		}
		return nil
	}
	if dryRun(inv) {
		if on {
			res.Warnings = append(res.Warnings, restartNote)
		}
		set := output.Set{Name: "planned_change", Columns: []output.Column{
			{Name: "flag", Type: "int"}, {Name: "current", Type: "bit"}, {Name: "new", Type: "bit"},
		}}
		for _, f := range flags {
			set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(f)), bitCell(before[f]), bitCell(on)})
		}
		set.TotalRowCount = len(set.Rows)
		res.Sets = []output.Set{set}
		return res, applyWrite(ctx, inv, res, apply)
	}

	// A refused flag stops the run after the ones before it took, so the
	// second read runs either way.
	werr := applyWrite(ctx, inv, res, apply)
	if e, ok := errors.AsType[*output.Error](werr); ok {
		switch {
		case e.Code == output.CodePermissionDenied:
			e.Hint = "DBCC TRACEON and TRACEOFF need membership in sysadmin. Use a login that has it."
		case e.SQL != nil && e.SQL.Number == 2560:
			e.Hint = "SQL Server knows no trace flag with this number; the highest it accepts varies by version."
		}
	}
	read, err = cat.TraceFlags(ctx)
	if err != nil {
		if werr != nil {
			return res, werr
		}
		e := readError(err)
		e.Message = "the change ran, but DBCC TRACESTATUS could not be read back: " + e.Message
		return res, e
	}
	after := globalFlags(read)
	set := output.Set{Name: "change", Columns: []output.Column{
		{Name: "flag", Type: "int"}, {Name: "before", Type: "bit"}, {Name: "requested", Type: "bit"}, {Name: "after", Type: "bit"},
	}}
	var missed []string
	for _, f := range flags {
		set.Rows = append(set.Rows, []*string{ptr(strconv.Itoa(f)), bitCell(before[f]), bitCell(on), bitCell(after[f])})
		if after[f] != on {
			missed = append(missed, strconv.Itoa(f))
		}
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	if on && len(missed) < len(flags) {
		res.Warnings = append(res.Warnings, restartNote)
	}
	what, is := "trace flag "+strings.Join(missed, ", "), "is"
	if len(missed) > 1 {
		what, is = "trace flags "+strings.Join(missed, ", "), "are"
	}
	switch {
	case werr != nil || len(missed) == 0:
	case on:
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s not on: the server skips a startup-only flag without an "+
			"error (set it with -T, or mssql-conf traceflag on Linux).", what, is))
	default:
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s still on after DBCC TRACEOFF.", what, is))
	}
	return res, werr
}
