package activity

import (
	"context"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// The tempdb readings are gosmo's types (tempdb_usage.go there), named here so
// the dashboard needn't import gosmo.
type (
	// TempDBSpace is tempdb data-file usage in MB. The four allocated parts
	// plus Free sum to Total, so they stack as one column.
	TempDBSpace = gosmo.TempDBSpace
	// TempDBFile is one tempdb file.
	TempDBFile = gosmo.TempDBFile
	// TempDBObjectKind groups tempdb contents by creator, which decides whom
	// to talk to about it.
	TempDBObjectKind = gosmo.TempDBObjectKind
	// TempDBObjects is one kind's footprint in tempdb.
	TempDBObjects = gosmo.TempDBObjects
	// TempDBSession is one session's tempdb footprint, net of deallocations.
	TempDBSession = gosmo.TempDBSession
)

const (
	TempDBUserTemp   = gosmo.TempDBLocalTemp
	TempDBGlobalTemp = gosmo.TempDBGlobalTemp
	TempDBUserTable  = gosmo.TempDBUserTable
	TempDBInternal   = gosmo.TempDBInternal
	TempDBSystem     = gosmo.TempDBSystem

	tempDBObjectKindCount = int(TempDBSystem) + 1
)

// TempDBObjectKindNames are in declaration order, so chart series and legend
// stay aligned.
var TempDBObjectKindNames = [tempDBObjectKindCount]string{
	"Local temp tables",
	"Global temp tables",
	"User tables",
	"Internal tables",
	"System tables",
}

// TempDBSample is one TempDB tab tick. Only naturally-rate counters are rates;
// tempdb space is a level.
type TempDBSample struct {
	At       time.Time
	Interval time.Duration

	Space    TempDBSpace
	Files    []TempDBFile
	Objects  [tempDBObjectKindCount]TempDBObjects
	Sessions []TempDBSession

	// Activity counters.
	ActiveTempTables    float64
	TempTableCreateSec  float64
	SnapshotTx          float64
	NonSnapshotTx       float64
	VersionStoreMB      float64
	VersionGenKBSec     float64
	VersionCleanupKBSec float64
	LongestTxSec        float64

	// Cores is the host's logical CPU count, for the one-data-file-per-core (up
	// to eight) rule.
	Cores int
}

// DataFiles returns the data files, which the file-count rule and space
// breakdown cover.
func (s TempDBSample) DataFiles() []TempDBFile {
	out := make([]TempDBFile, 0, len(s.Files))
	for _, f := range s.Files {
		if f.Type == "ROWS" {
			out = append(out, f)
		}
	}
	return out
}

// tempdbCounterNames are this tab's counters. They're in objects the main
// dashboard doesn't collect, so they get their own query.
var tempdbCounterNames = []string{
	// General Statistics
	"Active Temp Tables", "Temp Tables Creation Rate",
	// Transactions
	"Snapshot Transactions", "NonSnapshot Version Transactions",
	"Version Store Size (KB)", "Version Generation rate (KB/s)",
	"Version Cleanup rate (KB/s)", "Longest Transaction Running Time",
}

// tempdbSnapshot is one raw reading, before counter rates are derived.
type tempdbSnapshot struct {
	at       time.Time
	counters counterSet
	sample   TempDBSample
}

// collectTempDB reads the full tempdb picture, one reading after another,
// like Collect.
func collectTempDB(ctx context.Context, src Source) (*tempdbSnapshot, error) {
	snap := &tempdbSnapshot{at: time.Now()}
	var err error

	if snap.counters, err = collectCounterSet(ctx, src, tempdbCounterNames); err != nil {
		return nil, err
	}
	if snap.sample.Space, err = src.TempDBSpace(ctx); err != nil {
		return nil, err
	}
	if snap.sample.Files, err = src.TempDBFiles(ctx); err != nil {
		return nil, err
	}
	if snap.sample.Objects, err = collectTempDBObjects(ctx, src); err != nil {
		return nil, err
	}
	if snap.sample.Sessions, err = src.TempDBSessions(ctx); err != nil {
		return nil, err
	}
	// The core count is static, and connect already read it.
	if info := src.Info(); info != nil {
		snap.sample.Cores = info.LogicalCPUCount
	}
	return snap, nil
}

// collectTempDBObjects places gosmo's per-kind sums, which list only the kinds
// present, in a slot per kind, so an absent kind reads as zero.
func collectTempDBObjects(ctx context.Context, src Source) ([tempDBObjectKindCount]TempDBObjects, error) {
	var out [tempDBObjectKindCount]TempDBObjects
	for i := range out {
		out[i].Kind = TempDBObjectKind(i)
	}
	objs, err := src.TempDBObjects(ctx)
	if err != nil {
		return out, err
	}
	for _, o := range objs {
		if k := int(o.Kind); k >= 0 && k < tempDBObjectKindCount {
			out[k] = o
		}
	}
	return out, nil
}

// deriveTempDB turns a snapshot into a sample, decoding counters against the
// previous reading. With nil prev, rate counters are zero (as in Derive).
func deriveTempDB(prev, cur *tempdbSnapshot) TempDBSample {
	s := cur.sample
	s.At = cur.at

	var prevCounters counterSet
	var elapsed float64
	if prev != nil {
		prevCounters = prev.counters
		s.Interval = cur.at.Sub(prev.at)
		elapsed = s.Interval.Seconds()
	}
	c := cur.counters

	s.ActiveTempTables = c.value(prevCounters, objGeneralStats, "Active Temp Tables", "", elapsed)
	s.TempTableCreateSec = c.value(prevCounters, objGeneralStats, "Temp Tables Creation Rate", "", elapsed)
	s.SnapshotTx = c.value(prevCounters, objTransactions, "Snapshot Transactions", "", elapsed)
	s.NonSnapshotTx = c.value(prevCounters, objTransactions, "NonSnapshot Version Transactions", "", elapsed)
	s.VersionStoreMB = c.value(prevCounters, objTransactions, "Version Store Size (KB)", "", elapsed) / 1024
	s.VersionGenKBSec = c.value(prevCounters, objTransactions, "Version Generation rate (KB/s)", "", elapsed)
	s.VersionCleanupKBSec = c.value(prevCounters, objTransactions, "Version Cleanup rate (KB/s)", "", elapsed)
	s.LongestTxSec = c.value(prevCounters, objTransactions, "Longest Transaction Running Time", "", elapsed)
	return s
}
