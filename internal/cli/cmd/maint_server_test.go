package cmd

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
)

// maintServer is real gosmo over canned rows for the maint group: a
// catalogServer (the connection's database, objects resolved by name, the
// tables' and views' by-name reads) with the server's databases and
// availability groups, msdb's backup history, the backup media RESTORE
// reads, the instance's files, and the indexes, fragmentation and
// statistics of the objects. The writes gosmo sends — BACKUP, RESTORE,
// ALTER INDEX, UPDATE STATISTICS, sp_updatestats, DBCC CHECKDB — change
// this state as the server would (applyWrite), and the rules serve it back.
//
// Every rule answers what the state holds when the query arrives, so a test
// may change it between runs. The server metadata (Info) is read on each
// connect.
type maintServer struct {
	*catalogServer
	// dbs is sys.databases; the rules answer them in name order, as the
	// server's ORDER BY name does.
	dbs []*gosmo.Database
	ags map[string][]string // availability group → its databases
	// clock is SYSDATETIMEOFFSET().
	clock time.Time

	// history is msdb's backup sets by database name, newest first; a
	// set's media families are its Devices.
	history    map[string][]*gosmo.BackupInfo
	setIDs     map[*gosmo.BackupInfo]int64
	historyFor []string // each BackupHistory read's database

	// media is the sets each backup file holds (RESTORE HEADERONLY), by
	// its first device; headerRead is each read's devices.
	// recovery is each database's log chain and differential base
	// (sys.database_recovery_status), by name; a database missing from it
	// has no row. recoveryErr fails the read.
	recovery    map[string]*gosmo.DatabaseRecoveryStatus
	recoveryErr error
	recoveryFor []string // each RecoveryStatus read's database

	media      map[string][]*gosmo.BackupHeader
	headersErr error
	headerRead [][]string
	// fileLists is each file's sets' files (RESTORE FILELISTONLY), by
	// first device then WITH FILE (0 for none); fileListReads is each
	// read's WITH FILE.
	fileLists     map[string]map[int][]*gosmo.BackupFile
	fileListReads []int
	files         []*gosmo.InstanceFile // sys.master_files
	instanceReads int

	// indexes, frag and stats are each table's and view's, by schema.name;
	// frag is index id → percent, its page count 1000 × the id.
	indexes  map[string][]*gosmo.Index
	frag     map[string]map[int]float64
	stats    map[string][]*gosmo.Statistic
	dbFrag   []*gosmo.DatabaseIndexFragmentation
	dbFragAt []string // each Database.FragmentationStats read: database/mode/minPct/minPages
	fragErr  error

	// writes is each write statement the server was sent, as sent;
	// writeErr fails every one. noMedia makes a BACKUP succeed and write
	// no set; checkRows is what DBCC CHECKDB reports.
	writes    []string
	writeErr  error
	noMedia   bool
	checkRows []*gosmo.CheckDBError
}

// The write statements applyWrite knows.
var (
	backupStatement  = regexp.MustCompile(`^BACKUP (DATABASE|LOG) (\[(?:[^\]]|\]\])*\]) TO (.*?)(?: WITH (.*))?$`)
	restoreStatement = regexp.MustCompile(`(?s)\bRESTORE (?:DATABASE|LOG) (\[(?:[^\]]|\]\])*\])`)
	alterIndex       = regexp.MustCompile(`^ALTER INDEX (ALL|\[(?:[^\]]|\]\])*\]) ON (\S+) (?:REBUILD|REORGANIZE)`)
	updateStatistics = regexp.MustCompile(`^UPDATE STATISTICS (\[(?:[^\]]|\]\])*\]\.\[(?:[^\]]|\]\])*\])(?: \[((?:[^\]]|\]\])*)\])?`)
	noRecovery       = regexp.MustCompile(`\bNORECOVERY\b`)
)

// statsAfter is when an UPDATE STATISTICS ran.
var statsAfter = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// applyWrite is the server taking q, a write (or the DBCC CHECKDB query):
// recorded, failed with writeErr, else applied to s — a backup appends a
// set to its first device's media (INIT replaces them), a restore leaves
// the database RESTORING under NORECOVERY and ONLINE otherwise, a rebuild
// or reorganize clears its indexes' fragmentation, an update stamps its
// statistics.
func (s *maintServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	s.writes = append(s.writes, q.Text)
	if s.writeErr != nil {
		return gosmotest.Result{Err: s.writeErr}
	}
	switch text := q.Text; {
	case strings.HasPrefix(text, "DBCC CHECKDB"):
		return gosmotest.Result{Sets: []gosmotest.Set{clitest.CheckDBSet(s.checkRows)}}
	case strings.HasPrefix(text, "RESTORE VERIFYONLY"), strings.HasPrefix(text, "EXEC sys.sp_updatestats"):
	case backupStatement.MatchString(text):
		m := backupStatement.FindStringSubmatch(text)
		if s.noMedia {
			break
		}
		opts := strings.Split(m[4], ", ")
		dev := devices(gosmotest.Query{Text: m[3]})[0]
		if slices.Contains(opts, "INIT") {
			s.media[dev] = nil
		}
		set := gosmo.BackupSetDatabase
		switch {
		case m[1] == "LOG":
			set = gosmo.BackupSetLog
		case slices.Contains(opts, "DIFFERENTIAL"):
			set = gosmo.BackupSetDifferential
		}
		comp := slices.Contains(opts, "COMPRESSION")
		size := int64(8 << 20)
		s.media[dev] = append(s.media[dev], &gosmo.BackupHeader{DatabaseName: bracketNames(m[2])[0], SetType: set,
			Position: len(s.media[dev]) + 1, BackupStart: agentDate("2026-11-03 12:00:00"),
			BackupFinish: agentDate("2026-11-03 12:00:04"), BackupSize: size, CompressedSize: map[bool]int64{true: size / 4, false: size}[comp],
			Compressed: comp, HasChecksums: slices.Contains(opts, "CHECKSUM")})
	case restoreStatement.MatchString(text):
		name := bracketNames(restoreStatement.FindStringSubmatch(text)[1])[0]
		state := gosmo.DatabaseOnline
		if noRecovery.MatchString(text) {
			state = gosmo.DatabaseRestoring
		}
		if d := s.db(name); d != nil {
			d.State = state
		} else {
			s.dbs = append(s.dbs, &gosmo.Database{Name: name, ID: 40, State: state})
		}
	case alterIndex.MatchString(text):
		m := alterIndex.FindStringSubmatch(text)
		key := s.objectNamed(m[2])
		for _, ix := range s.indexes[key] {
			if _, ok := s.frag[key][ix.IndexID]; ok && (m[1] == "ALL" || bracketNames(m[1])[0] == ix.Name) {
				s.frag[key][ix.IndexID] = 0.5
			}
		}
	case updateStatistics.MatchString(text):
		m := updateStatistics.FindStringSubmatch(text)
		for _, st := range s.stats[s.objectNamed(m[1])] {
			if m[2] == "" || st.Name == strings.ReplaceAll(m[2], "]]", "]") {
				st.LastUpdated, st.RowsSampled, st.ModificationCounter = statsAfter, st.TotalRows, 0
			}
		}
	default:
		s.t.Errorf("maintServer: a write it does not know: %s", q)
	}
	return gosmotest.Result{}
}

// maintDatabases is the server's databases: newStorageServer's, model and
// tempdb's absence included — app FULL, hr and master SIMPLE, archive
// OFFLINE, app_snap a snapshot of app.
func maintDatabases() []*gosmo.Database {
	full, simple := gosmo.RecoveryModelFull, gosmo.RecoveryModelSimple
	return []*gosmo.Database{
		{Name: "master", ID: 1, State: gosmo.DatabaseOnline, RecoveryModel: simple},
		{Name: "app", ID: 5, State: gosmo.DatabaseOnline, RecoveryModel: full},
		{Name: "archive", ID: 6, State: gosmo.DatabaseOffline, RecoveryModel: full},
		{Name: "app_snap", ID: 7, State: gosmo.DatabaseOnline, RecoveryModel: full, SourceDatabaseID: 5},
		{Name: "hr", ID: 8, State: gosmo.DatabaseOnline, RecoveryModel: simple},
		{Name: "model", ID: 3, State: gosmo.DatabaseOnline, RecoveryModel: full},
	}
}

func newMaintServer(t *testing.T, current string, objects ...*gosmo.ObjectInfo) *maintServer {
	s := &maintServer{dbs: maintDatabases(), setIDs: map[*gosmo.BackupInfo]int64{},
		history: map[string][]*gosmo.BackupInfo{}, media: map[string][]*gosmo.BackupHeader{},
		fileLists: map[string]map[int][]*gosmo.BackupFile{},
		recovery:  map[string]*gosmo.DatabaseRecoveryStatus{},
		indexes:   map[string][]*gosmo.Index{}, frag: map[string]map[int]float64{}, stats: map[string][]*gosmo.Statistic{}}
	newCatalogServerWith(t, current, func(c *catalogServer) { s.catalogServer = c; s.rules() }, objects...)
	return s
}

// db is s's database name, any case; nil when there is none.
func (s *maintServer) db(name string) *gosmo.Database {
	for _, d := range s.dbs {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

// byName is s's databases in name order.
func (s *maintServer) byName() []*gosmo.Database {
	dbs := slices.Clone(s.dbs)
	slices.SortFunc(dbs, func(a, b *gosmo.Database) int { return strings.Compare(a.Name, b.Name) })
	return dbs
}

// objectKey is the schema.name of the object with id, "" for none.
func (s *maintServer) objectKey(id any) string {
	n, _ := id.(int64)
	for _, o := range s.objects {
		if o.ObjectID == int(n) {
			return o.Schema + "." + o.Name
		}
	}
	return ""
}

// objectNamed is the schema.name of the object a query names as
// [schema].[name], "" for none.
func (s *maintServer) objectNamed(qualified any) string {
	for _, o := range s.objects {
		if gosmo.QualifiedName(o.Schema, o.Name) == qualified {
			return o.Schema + "." + o.Name
		}
	}
	return ""
}

// setID is b's backup_set_id, the same for every read.
func (s *maintServer) setID(b *gosmo.BackupInfo) int64 {
	if id, ok := s.setIDs[b]; ok {
		return id
	}
	id := int64(len(s.setIDs) + 1)
	s.setIDs[b] = id
	return id
}

var (
	// deviceOf is one device of a RESTORE's FROM list.
	deviceOf = regexp.MustCompile(`(?:DISK|URL) = N'((?:[^']|'')*)'|\[([^\]]*)\]`)
	fileOf   = regexp.MustCompile(`WITH FILE = (\d+)`)
	modeOf   = regexp.MustCompile(`N'(LIMITED|SAMPLED|DETAILED)'\)`)
)

// devices is the devices a RESTORE … FROM names, in order.
func devices(q gosmotest.Query) []string {
	var out []string
	for _, m := range deviceOf.FindAllStringSubmatch(q.Text, -1) {
		out = append(out, strings.ReplaceAll(cmp.Or(m[1], m[2]), "''", "'"))
	}
	return out
}

func (s *maintServer) rules() {
	sets := func(set gosmotest.Set) gosmotest.Result { return gosmotest.Result{Sets: []gosmotest.Set{set}} }
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec || q.Messages || has(q, "DBCC CHECKDB") }, s.applyWrite)
	// The databases, by name, and listed.
	s.on(func(q gosmotest.Query) [][]any {
		if d := s.db(argString(q, 0)); d != nil {
			return [][]any{clitest.DatabaseOf(d)}
		}
		return nil
	}, "FROM sys.databases", "WHERE name = @p1")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, d := range s.byName() {
			rows = append(rows, clitest.DatabaseOf(d))
		}
		return rows
	}, "FROM sys.databases", "ORDER BY name")
	s.on(func(gosmotest.Query) [][]any { return [][]any{{s.clock}} }, "SELECT SYSDATETIMEOFFSET()")

	// Availability groups and their databases.
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, name := range slices.Sorted(maps.Keys(s.ags)) {
			rows = append(rows, clitest.AvailabilityGroupRow("ag-"+name, name))
		}
		return rows
	}, "FROM sys.availability_groups ag", "ORDER BY ag.name")
	s.on(func(q gosmotest.Query) [][]any {
		id := argString(q, 0)
		var rows [][]any
		for _, d := range s.ags[strings.TrimPrefix(id, "ag-")] {
			rows = append(rows, clitest.AvailabilityDatabaseRow(id, d))
		}
		return rows
	}, "FROM sys.availability_databases_cluster adc")

	// msdb: one database's history, and LastBackups' two reads.
	s.on(func(q gosmotest.Query) [][]any {
		name := argString(q, 0)
		s.historyFor = append(s.historyFor, name)
		var rows [][]any
		for _, b := range s.history[name] {
			rows = append(rows, clitest.BackupInfoRows(b, s.setID(b))...)
		}
		return rows
	}, "FROM   msdb.dbo.backupset bs", "WHERE  bs.database_name = @p1")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, d := range s.byName() {
			if d.ID != 2 && !d.IsSnapshot() {
				rows = append(rows, []any{d.Name, clitest.Nullable(string(d.RecoveryModel))})
			}
		}
		return rows
	}, "SELECT name, recovery_model_desc", "source_database_id IS NULL")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for _, name := range slices.Sorted(maps.Keys(s.history)) {
			newest := map[gosmo.BackupSetType]*gosmo.BackupInfo{}
			for _, b := range s.history[name] {
				switch b.SetType {
				case gosmo.BackupSetDatabase, gosmo.BackupSetDifferential, gosmo.BackupSetLog:
					if n := newest[b.SetType]; n == nil || b.BackupFinish.After(n.BackupFinish) {
						newest[b.SetType] = b
					}
				}
			}
			for _, typ := range []gosmo.BackupSetType{gosmo.BackupSetDatabase, gosmo.BackupSetDifferential, gosmo.BackupSetLog} {
				if b := newest[typ]; b != nil {
					rows = append(rows, clitest.BackupInfoRows(b, s.setID(b))...)
				}
			}
		}
		return rows
	}, "FROM   msdb.dbo.backupset bs", "ROW_NUMBER() OVER (PARTITION BY database_name, type")

	// Database.RecoveryStatus: one database's row.
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "sys.database_recovery_status", "WHERE  d.name = @p1") },
		func(q gosmotest.Query) gosmotest.Result {
			s.recoveryFor = append(s.recoveryFor, argString(q, 0))
			if s.recoveryErr != nil {
				return gosmotest.Result{Err: s.recoveryErr}
			}
			var rows [][]any
			if st := s.recovery[argString(q, 0)]; st != nil {
				rows = append(rows, []any{st.DatabaseName, st.LastLogBackupLSN, st.DifferentialBaseLSN, st.DifferentialBaseKnown})
			}
			return sets(gosmotest.Set{Rows: rows})
		})

	// RESTORE HEADERONLY and FILELISTONLY, from the first device's media.
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "RESTORE HEADERONLY FROM") }, func(q gosmotest.Query) gosmotest.Result {
		devs := devices(q)
		s.headerRead = append(s.headerRead, devs)
		if s.headersErr != nil {
			return gosmotest.Result{Err: s.headersErr}
		}
		return sets(clitest.BackupHeaderSet(s.media[devs[0]]))
	})
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "RESTORE FILELISTONLY FROM") }, func(q gosmotest.Query) gosmotest.Result {
		n := 0
		if m := fileOf.FindStringSubmatch(q.Text); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
		s.fileListReads = append(s.fileListReads, n)
		return sets(clitest.BackupFileListSet(s.fileLists[devices(q)[0]][n]))
	})
	s.on(func(gosmotest.Query) [][]any {
		s.instanceReads++
		var rows [][]any
		for _, f := range s.files {
			rows = append(rows, clitest.InstanceFileRow(f))
		}
		return rows
	}, "FROM   sys.master_files mf", "ORDER  BY mf.database_id, mf.file_id")

	// A table's or view's indexes (no columns: maint reads none), their
	// fragmentation, the database's, and the statistics.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, ix := range s.indexes[s.objectKey(q.Arg(0))] {
			rows = append(rows, clitest.IndexRow(ix))
		}
		return rows
	}, "FROM   sys.indexes i", "WHERE  i.object_id = @p1 AND i.type > 0")
	s.on(func(gosmotest.Query) [][]any { return nil }, "FROM   sys.index_columns ic")
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "sys.dm_db_index_physical_stats(DB_ID(), i.object_id") },
		func(q gosmotest.Query) gosmotest.Result {
			if s.fragErr != nil {
				return gosmotest.Result{Err: s.fragErr}
			}
			if mode := modeOf.FindStringSubmatch(q.Text)[1]; mode != string(gosmo.FragmentationLimited) {
				return gosmotest.Result{Err: fmt.Errorf("mode %s, want LIMITED", mode)}
			}
			key := s.objectNamed(q.Arg(0))
			var rows [][]any
			for _, ix := range s.indexes[key] {
				// The select leaves a disabled index out.
				if pct, ok := s.frag[key][ix.IndexID]; ok && !ix.IsDisabled {
					rows = append(rows, clitest.FragmentationRow(&gosmo.IndexFragmentation{IndexName: ix.Name, IndexID: ix.IndexID,
						AvgFragmentationPct: pct, PageCount: int64(1000 * ix.IndexID)}))
				}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
		})
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "sys.dm_db_index_physical_stats(DB_ID(), NULL") },
		func(q gosmotest.Query) gosmotest.Result {
			minPct, _ := q.Arg(0).(float64)
			minPages, _ := q.Arg(1).(int64)
			s.dbFragAt = append(s.dbFragAt, fmt.Sprintf("%s/%s/%g/%d", q.Database, modeOf.FindStringSubmatch(q.Text)[1], minPct, minPages))
			if s.fragErr != nil {
				return gosmotest.Result{Err: s.fragErr}
			}
			// The HAVING: at least minPct fragmented, at least minPages pages.
			var rows [][]any
			for _, f := range s.dbFrag {
				if f.AvgFragmentationPct >= minPct && f.PageCount >= minPages {
					rows = append(rows, clitest.DatabaseFragmentationRow(f))
				}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}}
		})
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for i, st := range s.stats[s.objectKey(q.Arg(0))] {
			rows = append(rows, clitest.StatisticRow(st.Name, i+1, st.IsAutoCreated, st.IsUserCreated, false, "",
				st.LastUpdated, st.RowsSampled, st.TotalRows, 0, st.ModificationCounter, false))
		}
		return rows
	}, "OUTER  APPLY sys.dm_db_stats_properties(s.object_id, s.stats_id) sp", "ORDER  BY s.name")
}
