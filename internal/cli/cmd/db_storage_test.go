package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func TestSizeUnit(t *testing.T) {
	for _, tc := range []struct {
		u              sizeUnit
		name, typ, txt string
	}{
		{unitKB, "size_kb", "bigint", "1572864"},
		{unitMB, "size_mb", "decimal(18,2)", "1536.00"},
		{unitGB, "size_gb", "decimal(18,2)", "1.50"},
	} {
		c := tc.u.column("size", true)
		if c.Name != tc.name || c.Type != tc.typ || !c.Nullable {
			t.Errorf("%s column %+v, want %s %s nullable", tc.u, c, tc.name, tc.typ)
		}
		if got := *tc.u.cell(1572864); got != tc.txt {
			t.Errorf("%s cell %q, want %q", tc.u, got, tc.txt)
		}
	}
	// Two decimals round, they do not truncate: 1 KB is 0.00 GB, 1023 KB 1.00 MB.
	if unitGB.text(1) != "0.00" || unitMB.text(1023) != "1.00" {
		t.Errorf("rounding: %s GB, %s MB", unitGB.text(1), unitMB.text(1023))
	}
}

func TestPctCell(t *testing.T) {
	if c := pctColumn("free_pct", true); c.Type != "decimal(18,2)" || !c.Nullable {
		t.Errorf("pct column %+v", c)
	}
	if c := pctCell(1, 3); c == nil || *c != "33.33" {
		t.Errorf("1 of 3 = %v", c)
	}
	if c := pctCell(5, 0); c != nil {
		t.Errorf("of 0 = %q, want NULL", *c)
	}
	// The exact figure is what a filter compares: 9.996 is under 10, though
	// it prints 10.00.
	if p, _ := pctOf(9996, 100000); p >= 10 || *pctCell(9996, 100000) != "10.00" {
		t.Errorf("pctOf %v", p)
	}
}

// storageCommand is a storage command's shape: an optional <name>, --all
// and --unit.
func storageCommand() *spec.Tree {
	return &spec.Tree{Commands: []*spec.Command{{Path: []string{"db"}}, {Path: []string{"db", "files"},
		Args:    []spec.Arg{{Name: "name", Help: "Database name"}},
		Options: []spec.Option{allOption(), unitOption()}}}}
}

// --unit defaults to mb; the enum is lower-case, as every enum the parser
// takes.
func TestUnitOption(t *testing.T) {
	for argv, want := range map[string]sizeUnit{"": unitMB, "--unit kb": unitKB, "--unit gb": unitGB} {
		inv, err := args.Parse(storageCommand(), append([]string{"db", "files", "app"}, strings.Fields(argv)...), args.Options{})
		if err != nil {
			t.Fatalf("%q: %v", argv, err)
		}
		if got := unitOf(inv); got != want {
			t.Errorf("%q: unit %s, want %s", argv, got, want)
		}
	}
	if _, err := args.Parse(storageCommand(), []string{"db", "files", "app", "--unit", "tb"}, args.Options{}); err == nil {
		t.Error("--unit tb parsed")
	}
}

func TestNameOrAll(t *testing.T) {
	for _, tc := range []struct {
		argv     string
		wantName string
		wantAll  bool
		wantErr  string
	}{
		{"app", "app", false, ""},
		{"--all", "", true, ""},
		{"", "", false, "give a database <name> or --all"},
		{"app --all", "", false, "not both"},
	} {
		inv, err := args.Parse(storageCommand(), append([]string{"db", "files"}, strings.Fields(tc.argv)...), args.Options{})
		if err != nil {
			t.Fatalf("%q: %v", tc.argv, err)
		}
		name, all, err := nameOrAll(inv)
		if tc.wantErr != "" {
			e, ok := errors.AsType[*output.Error](err)
			if !ok || e.Code != output.CodeUsageError || !strings.Contains(e.Message, tc.wantErr) ||
				!strings.HasPrefix(e.Message, "db files: ") {
				t.Errorf("%q: err %v, want USAGE_ERROR %q", tc.argv, err, tc.wantErr)
			}
			continue
		}
		if err != nil || name != tc.wantName || all != tc.wantAll {
			t.Errorf("%q: %q %v %v", tc.argv, name, all, err)
		}
	}
}

// storageServer is gosmo over canned rows for the storage commands: the
// databases, by name and all, and HAS_DBACCESS for each. Its fields are read
// as each query arrives, so a test may change them between commands; what
// else a command reads (files, space, log, table sizes) the test adds with
// answer before its first command runs.
type storageServer struct {
	*gosmotest.Fake
	dbs       []*gosmo.Database
	access    map[int]bool
	accessErr error
}

func newStorageServer(t *testing.T) *storageServer {
	s := &storageServer{
		Fake: gosmotest.New(t),
		dbs: []*gosmo.Database{
			{Name: "master", ID: 1, State: gosmo.DatabaseOnline},
			{Name: "app", ID: 5, State: gosmo.DatabaseOnline},
			{Name: "archive", ID: 6, State: gosmo.DatabaseOffline},
			{Name: "app_snap", ID: 7, State: gosmo.DatabaseOnline, SourceDatabaseID: 5},
			{Name: "hr", ID: 8, State: gosmo.DatabaseOnline},
		},
		// HAS_DBACCESS: false on the OFFLINE database for every login; hr
		// has no user for the login.
		access: map[int]bool{1: true, 5: true, 6: false, 7: true, 8: false},
	}
	answer(s.Fake, func(gosmotest.Query) ([][]any, error) {
		return byName(s.dbs), nil
	}, "FROM sys.databases", "ORDER BY name")
	answer(s.Fake, func(q gosmotest.Query) ([][]any, error) {
		if d := s.db(argString(q, 0)); d != nil {
			return [][]any{clitest.DatabaseOf(d)}, nil
		}
		return nil, nil
	}, "FROM sys.databases", "WHERE name = @p1")
	answer(s.Fake, func(gosmotest.Query) ([][]any, error) {
		var rows [][]any
		for _, d := range s.dbs {
			if ok, known := s.access[d.ID]; known {
				rows = append(rows, clitest.DatabaseAccessRow(d.ID, ok))
			}
		}
		return rows, s.accessErr
	}, "HAS_DBACCESS(name)")
	return s
}

// byName is dbs as Databases' rows, in its ORDER BY name under a
// case-insensitive collation.
func byName(dbs []*gosmo.Database) [][]any {
	sorted := slices.SortedFunc(slices.Values(dbs), func(a, b *gosmo.Database) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	var rows [][]any
	for _, d := range sorted {
		rows = append(rows, clitest.DatabaseOf(d))
	}
	return rows
}

// db is s's database name, compared as the server's collation does; nil
// when there is none.
func (s *storageServer) db(name string) *gosmo.Database {
	for _, d := range s.dbs {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

// readIn is the database each of the queries containing sub ran in, in
// order: which databases a command read that way.
func (s *storageServer) readIn(sub string) []string {
	var out []string
	for _, q := range s.Queries() {
		if strings.Contains(q.Text, sub) {
			out = append(out, q.Database)
		}
	}
	return out
}

// answer answers the queries containing every one of subs with fn's rows,
// or its error as the query's.
func answer(f *gosmotest.Fake, fn func(gosmotest.Query) ([][]any, error), subs ...string) {
	f.OnFunc(func(q gosmotest.Query) bool { return has(q, subs...) },
		func(q gosmotest.Query) gosmotest.Result {
			rows, err := fn(q)
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows}}, Err: err}
		})
}

// offline is the server's refusal of a USE into a database that is not
// ONLINE.
func offline(name string) error {
	return mssql.Error{Number: 942, Class: 14, Message: fmt.Sprintf("Database '%s' cannot be opened because it is offline.", name)}
}

// runStorage runs db <command> over f with argv after it, an ad-hoc
// connection.
func runStorage(t *testing.T, f *gosmotest.Fake, command, argv string) (*output.Result, error) {
	t.Helper()
	d := Deps{
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			srv, err := f.Connect(ctx)
			return &db.ServerConn{Server: srv}, err
		},
	}
	tree := &spec.Tree{Commands: DBCommands(d)}
	inv, err := args.Parse(tree, append([]string{"db", command, "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...), args.Options{})
	if err != nil {
		return nil, err
	}
	return inv.Command.Run(context.Background(), inv)
}

// unreadable is name=reason for every unreadable database of ts.
func unreadable(ts []storageDB) []string {
	var out []string
	for _, t := range ts {
		if t.Unreadable != "" {
			out = append(out, t.DB.Name+"="+t.Unreadable)
		}
	}
	return out
}

func TestStorageDatabases(t *testing.T) {
	s := newStorageServer(t)
	srv := s.Server()
	res := &output.Result{}
	ts, err := storageDatabases(context.Background(), srv, true, res)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range ts {
		names = append(names, x.DB.Name)
	}
	// In the read's ORDER BY name.
	if got := strings.Join(names, ","); got != "app,app_snap,archive,hr,master" {
		t.Errorf("databases %s", got)
	}
	want := []string{"archive=the database is OFFLINE", "hr=the login has no access to it"}
	if got := unreadable(ts); !slices.Equal(got, want) {
		t.Errorf("unreadable %q, want %q", got, want)
	}
	if !ts[3].Denied || ts[2].Denied {
		t.Errorf("Denied: hr %v, archive %v", ts[3].Denied, ts[2].Denied)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q", res.Warnings)
	}
	if w := ts[2].unreadableWarning("space"); w != "[archive]: space not read: the database is OFFLINE." {
		t.Errorf("warning %q", w)
	}

	// db log-usage leaves the snapshot out: it has no log.
	ts, _ = storageDatabases(context.Background(), srv, false, &output.Result{})
	for _, x := range ts {
		if x.DB.IsSnapshot() {
			t.Errorf("snapshot %s listed", x.DB.Name)
		}
	}

	// An unread access map costs a warning; ONLINE databases are then left
	// to their own reads, a state still decides.
	s.access, s.accessErr = nil, errors.New("boom")
	res = &output.Result{}
	ts, _ = storageDatabases(context.Background(), srv, true, res)
	if got := unreadable(ts); !slices.Equal(got, want[:1]) {
		t.Errorf("unread access: unreadable %q", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "boom") {
		t.Errorf("unread access: warnings %q", res.Warnings)
	}
}

func TestStorageDatabase(t *testing.T) {
	srv := newStorageServer(t).Server()
	for _, tc := range []struct {
		name     string
		wantCode output.Code // of the read (NOT_FOUND) or of unreadableError
		wantDB   string
	}{
		{"APP", "", "app"},
		{"archive", output.CodeNotFound, "archive"},
		{"hr", output.CodePermissionDenied, "hr"},
		{"gone", output.CodeNotFound, ""},
	} {
		res := &output.Result{}
		x, err := storageDatabase(context.Background(), srv, tc.name, res)
		if err != nil {
			e, _ := errors.AsType[*output.Error](err)
			if tc.wantDB != "" || e == nil || e.Code != tc.wantCode {
				t.Errorf("%s: err %v", tc.name, err)
			}
			continue
		}
		// The envelope names the catalog spelling.
		if res.Database != tc.wantDB {
			t.Errorf("%s: envelope database %q", tc.name, res.Database)
		}
		if x.Unreadable == "" {
			if tc.wantCode != "" {
				t.Errorf("%s: readable, want %s", tc.name, tc.wantCode)
			}
			continue
		}
		e := x.unreadableError()
		if e.Code != tc.wantCode || !strings.Contains(e.Message, x.Unreadable) {
			t.Errorf("%s: %s %q, want %s naming %q", tc.name, e.Code, e.Message, tc.wantCode, x.Unreadable)
		}
	}
}

func TestFileWarnings(t *testing.T) {
	const db = "app"
	for _, tc := range []struct {
		what      string
		f         gosmo.DatabaseFileInfo
		usedKnown bool
		snapshot  bool
		vol       *fileVolume
		want      []string
	}{
		{"healthy", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, SpaceUsedKB: 500, MaxSizeKB: -1, GrowthKB: 65536},
			true, false, &fileVolume{"/var/opt", 50}, nil},
		{"under 10 % free", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, SpaceUsedKB: 950, MaxSizeKB: -1},
			true, false, nil, []string{"[app] file [d]: 5.00 % free inside the file (under 10 %)."}},
		{"exactly 10 % free", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileLog, SizeKB: 1000, SpaceUsedKB: 900, MaxSizeKB: -1},
			true, false, nil, nil},
		// The master_files fallback has no used figure: no free warning.
		{"used unknown", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, MaxSizeKB: -1},
			false, false, nil, nil},
		{"at 90 % of max_size", gosmo.DatabaseFileInfo{Name: "l", Type: gosmo.FileLog, SizeKB: 900, SpaceUsedKB: 10, MaxSizeKB: 1000},
			true, false, nil, []string{"[app] file [l]: at 90.00 % of its max_size (90 % or more)."}},
		{"no growth (max_size 0)", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 900, SpaceUsedKB: 10, MaxSizeKB: 0},
			true, false, nil, nil},
		{"percent growth", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, SpaceUsedKB: 10, MaxSizeKB: -1,
			IsPercentGrowth: true, GrowthPercent: 10},
			true, false, nil, []string{"[app] file [d]: grows by 10 % (percentage autogrowth)."}},
		{"volume low", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, SpaceUsedKB: 10, MaxSizeKB: -1},
			true, false, &fileVolume{"C:\\", 4.5}, []string{"[app] file [d]: its volume C:\\ is 4.50 % free (under 10 %)."}},
		// A FILESTREAM container has no pages, cap or growth: only the
		// volume speaks for it.
		{"filestream", gosmo.DatabaseFileInfo{Name: "fs", Type: gosmo.FileStream, SizeKB: 0, MaxSizeKB: 10, IsPercentGrowth: true},
			true, false, &fileVolume{"/fs", 2}, []string{"[app] file [fs]: its volume /fs is 2.00 % free (under 10 %)."}},
		{"every reason, in order", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, SpaceUsedKB: 999, MaxSizeKB: 1000,
			IsPercentGrowth: true, GrowthPercent: 25},
			true, false, &fileVolume{"/v", 1}, []string{
				"[app] file [d]: 0.10 % free inside the file (under 10 %).",
				"[app] file [d]: at 100.00 % of its max_size (90 % or more).",
				"[app] file [d]: grows by 25 % (percentage autogrowth).",
				"[app] file [d]: its volume /v is 1.00 % free (under 10 %).",
			}},
		// A snapshot's sparse file never grows: only the volume speaks for it.
		{"snapshot", gosmo.DatabaseFileInfo{Name: "d", Type: gosmo.FileRows, SizeKB: 1000, SpaceUsedKB: 999, MaxSizeKB: 1000,
			IsPercentGrowth: true, GrowthPercent: 10},
			true, true, &fileVolume{"C:\\", 4.5}, []string{"[app] file [d]: its volume C:\\ is 4.50 % free (under 10 %)."}},
	} {
		if got := fileWarnings(db, &tc.f, tc.usedKnown, tc.snapshot, tc.vol); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.what, got, tc.want)
		}
	}
}

func TestLogReuseWarning(t *testing.T) {
	if w := logReuseWarning("app", "NOTHING"); w != "" {
		t.Errorf("NOTHING: %q", w)
	}
	if w := logReuseWarning("app", "LOG_BACKUP"); w != "[app]: log reuse waits on LOG_BACKUP." {
		t.Errorf("LOG_BACKUP: %q", w)
	}
	// A transient wait is a fact too.
	if w := logReuseWarning("app", "CHECKPOINT"); w == "" {
		t.Error("CHECKPOINT: no warning")
	}
}

// filesServer is storageServer answering db files' reads: Database.Files
// for the ONLINE databases (the server refuses the USE into another),
// sys.master_files for every one, and the volumes.
type filesServer struct {
	*storageServer
	files      map[string][]*gosmo.DatabaseFileInfo // Database.Files by name
	master     map[string][]*gosmo.DatabaseFileInfo // Server.DatabaseFiles by name
	vols       []gosmo.FileVolume
	volsHidden bool
}

// filesRead is the databases Database.Files was read in.
func (s *filesServer) filesRead() []string { return s.readIn("FROM   sys.database_files df") }

func newFilesServer(t *testing.T) *filesServer {
	const gb = 1024 * 1024
	s := &filesServer{
		storageServer: newStorageServer(t),
		files: map[string][]*gosmo.DatabaseFileInfo{
			"master": {
				{FileID: 1, Name: "master", PhysicalName: "/data/master.mdf", Type: gosmo.FileRows, FileGroup: "PRIMARY",
					SizeKB: 4096, SpaceUsedKB: 3000, MaxSizeKB: -1, GrowthKB: 10240},
				{FileID: 2, Name: "mastlog", PhysicalName: "/log/mastlog.ldf", Type: gosmo.FileLog,
					SizeKB: 2048, SpaceUsedKB: 1000, MaxSizeKB: -1, IsPercentGrowth: true, GrowthPercent: 10},
			},
			"app": {
				{FileID: 3, Name: "app_fs", PhysicalName: "/data/app_fs", Type: gosmo.FileStream, FileGroup: "FS",
					MaxSizeKB: -1},
				{FileID: 1, Name: "app", PhysicalName: "/data/app.mdf", Type: gosmo.FileRows, FileGroup: "PRIMARY",
					SizeKB: 100 * 1024, SpaceUsedKB: 95 * 1024, MaxSizeKB: -1, GrowthKB: 64 * 1024},
				{FileID: 2, Name: "app_log", PhysicalName: "/log/app_log.ldf", Type: gosmo.FileLog,
					SizeKB: 50 * 1024, SpaceUsedKB: 10 * 1024, MaxSizeKB: 2 * gb * 1024, GrowthKB: 64 * 1024},
				{FileID: 4, Name: "app_arch", PhysicalName: "/data/app_arch.ndf", Type: gosmo.FileRows, FileGroup: "Archive",
					SizeKB: 200 * 1024, SpaceUsedKB: 20 * 1024, MaxSizeKB: 210 * 1024, GrowthKB: 0},
			},
			// A snapshot's sys.database_files is the source's list: its
			// paths and its log, whose FILEPROPERTY reads NULL (0).
			"app_snap": {
				{FileID: 2, Name: "app_log", PhysicalName: "/log/app_log.ldf", Type: gosmo.FileLog,
					SizeKB: 50 * 1024, MaxSizeKB: -1, GrowthKB: 64 * 1024},
				{FileID: 1, Name: "app", PhysicalName: "/data/app.mdf", Type: gosmo.FileRows, FileGroup: "PRIMARY",
					SizeKB: 100 * 1024, SpaceUsedKB: 95 * 1024, MaxSizeKB: -1, GrowthKB: 64 * 1024},
			},
		},
		master: map[string][]*gosmo.DatabaseFileInfo{
			"archive": {
				{FileID: 1, Name: "archive", PhysicalName: "D:\\Data\\archive.mdf", Type: gosmo.FileRows,
					SizeKB: 8192, MaxSizeKB: -1, GrowthKB: 65536},
				{FileID: 2, Name: "archive_log", PhysicalName: "L:\\Log\\archive_log.ldf", Type: gosmo.FileLog,
					SizeKB: 1024, MaxSizeKB: -1, IsPercentGrowth: true, GrowthPercent: 10},
			},
			"app_snap": {{FileID: 1, Name: "app", PhysicalName: "/data/app.ss", Type: gosmo.FileRows,
				SizeKB: 100 * 1024, SizeOnDiskKB: 512, MaxSizeKB: -1, GrowthKB: 64 * 1024}},
			// hr: no user for the login, and no VIEW ANY DEFINITION — no rows.
		},
		vols: []gosmo.FileVolume{
			{DatabaseID: 5, FileID: 1, MountPoint: "/data", TotalBytes: 100, AvailableBytes: 5},
			{DatabaseID: 5, FileID: 2, MountPoint: "/log", TotalBytes: 100, AvailableBytes: 50},
			// Azure MI's nonsense figures: dropped, the cell NULL.
			{DatabaseID: 5, FileID: 4, MountPoint: "/data", TotalBytes: 0, AvailableBytes: 5},
			{DatabaseID: 6, FileID: 1, VolumeName: "Data", TotalBytes: 1000, AvailableBytes: 500},
		},
	}
	answer(s.Fake, func(q gosmotest.Query) ([][]any, error) {
		if d := s.db(q.Database); d == nil || d.State != gosmo.DatabaseOnline {
			return nil, offline(q.Database)
		}
		var rows [][]any
		for _, f := range s.files[q.Database] {
			rows = append(rows, clitest.DatabaseFileRow(f))
		}
		return rows, nil
	}, "FROM   sys.database_files df")
	answer(s.Fake, func(q gosmotest.Query) ([][]any, error) {
		var rows [][]any
		for _, f := range s.master[argString(q, 0)] {
			rows = append(rows, clitest.MasterFileRow(f))
		}
		return rows, nil
	}, "FROM   sys.master_files mf", "DB_ID(@p1)")
	answer(s.Fake, func(gosmotest.Query) ([][]any, error) {
		if s.volsHidden {
			return [][]any{clitest.FileVolumesHiddenRow()}, nil
		}
		var rows [][]any
		for _, v := range s.vols {
			rows = append(rows, clitest.FileVolumeRow(v))
		}
		return rows, nil
	}, "sys.dm_os_volume_stats")
	return s
}

// runDBFiles runs db files over s with argv after "db files".
func runDBFiles(t *testing.T, s *filesServer, argv string) (*output.Result, error) {
	t.Helper()
	return runStorage(t, s.Fake, "files", argv)
}

// column is set's column name as a list of cells, "NULL" for a NULL.
func column(set output.Set, name string) []string {
	i := slices.IndexFunc(set.Columns, func(c output.Column) bool { return c.Name == name })
	if i < 0 {
		return nil
	}
	out := make([]string, len(set.Rows))
	for r, row := range set.Rows {
		out[r] = "NULL"
		if row[i] != nil {
			out[r] = *row[i]
		}
	}
	return out
}

func TestDBFilesOneDatabase(t *testing.T) {
	res, err := runDBFiles(t, newFilesServer(t), "app")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	if set.Columns[0].Name != "logical_name" || res.Database != "app" {
		t.Errorf("first column %q, envelope database %q", set.Columns[0].Name, res.Database)
	}
	for name, want := range map[string][]string{
		"logical_name":    {"app", "app_log", "app_fs", "app_arch"},
		"type":            {"data", "log", "filestream", "data"},
		"filegroup":       {"PRIMARY", "NULL", "FS", "Archive"},
		"size_mb":         {"100.00", "50.00", "0.00", "200.00"},
		"used_mb":         {"95.00", "10.00", "NULL", "20.00"},
		"free_mb":         {"5.00", "40.00", "NULL", "180.00"},
		"free_pct":        {"5.00", "80.00", "NULL", "90.00"},
		"growth":          {"64 MB", "64 MB", "NULL", "none"},
		"max_size_mb":     {"unlimited", "2097152.00", "NULL", "210.00"},
		"volume_free_pct": {"5.00", "50.00", "NULL", "NULL"},
	} {
		if got := column(set, name); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	// The FILESTREAM container raises nothing; the capped Archive file is
	// at 95 % of its max_size.
	want := []string{
		"[app] file [app]: 5.00 % free inside the file (under 10 %).",
		"[app] file [app]: its volume /data is 5.00 % free (under 10 %).",
		"[app] file [app_arch]: at 95.24 % of its max_size (90 % or more).",
	}
	if !slices.Equal(res.Warnings, want) {
		t.Errorf("warnings %q, want %q", res.Warnings, want)
	}
}

// --unit renames the size columns and reformats every size, growth
// included.
func TestDBFilesUnit(t *testing.T) {
	res, err := runDBFiles(t, newFilesServer(t), "app --unit kb --type log")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	for name, want := range map[string]string{"size_kb": "51200", "used_kb": "10240", "growth": "65536 KB", "max_size_kb": "2147483648"} {
		if got := column(set, name); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if column(set, "size_mb") != nil {
		t.Error("size_mb present under --unit kb")
	}
}

// An OFFLINE database, named, is listed from sys.master_files — exit 0, the
// database-side columns NULL, no free-space warning, one warning why.
func TestDBFilesOfflineFallback(t *testing.T) {
	s := newFilesServer(t)
	res, err := runDBFiles(t, s, "archive")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.filesRead(), "archive") {
		t.Error("Database.Files was tried on an OFFLINE database")
	}
	set := res.Sets[0]
	for name, want := range map[string][]string{
		"physical_path":   {"D:\\Data\\archive.mdf", "L:\\Log\\archive_log.ldf"},
		"filegroup":       {"NULL", "NULL"},
		"used_mb":         {"NULL", "NULL"},
		"free_pct":        {"NULL", "NULL"},
		"growth":          {"64 MB", "10 %"},
		"volume_free_pct": {"50.00", "NULL"},
	} {
		if got := column(set, name); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	want := []string{
		"[archive]: filegroup and space used not read (the database is OFFLINE); files listed from sys.master_files.",
		"[archive] file [archive_log]: grows by 10 % (percentage autogrowth).",
	}
	if !slices.Equal(res.Warnings, want) {
		t.Errorf("warnings %q, want %q", res.Warnings, want)
	}
}

// --all: every database, a database column first, the unreadable ones
// listed from master_files or named under warnings, a snapshot's on-disk
// size.
func TestDBFilesAll(t *testing.T) {
	res, err := runDBFiles(t, newFilesServer(t), "--all")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	if set.Columns[0].Name != "database" {
		t.Errorf("first column %q", set.Columns[0].Name)
	}
	if got, want := column(set, "database"), []string{"app", "app", "app", "app", "app_snap", "archive", "archive", "master", "master"}; !slices.Equal(got, want) {
		t.Errorf("databases %q, want %q", got, want)
	}
	for _, w := range []string{
		"[archive]: filegroup and space used not read (the database is OFFLINE); files listed from sys.master_files.",
		"[app_snap] file [app]: a snapshot's sparse file; its size is a ceiling, it occupies 0.50 MB on disk.",
		"[hr]: files not read (the login has no access to it), and sys.master_files shows none to the login (VIEW ANY DEFINITION).",
	} {
		if !slices.Contains(res.Warnings, w) {
			t.Errorf("no warning %q in %q", w, res.Warnings)
		}
	}
}

// --max-free-pct and the other filters drop a row's warnings with the row:
// warnings speak only for the rows shown.
func TestDBFilesFiltersKeepWarningsInStep(t *testing.T) {
	res, err := runDBFiles(t, newFilesServer(t), "--all --max-free-pct 10 --sort-by free-pct")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	// Only app's data file is at most 10 % free (5 %, and app_snap's copy
	// of it); unknown free_pct (OFFLINE archive, FILESTREAM) is not kept.
	if got, want := column(set, "database"), []string{"app", "app_snap"}; !slices.Equal(got, want) {
		t.Errorf("databases %q, want %q", got, want)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "[master]") || strings.Contains(w, "app_arch") || strings.Contains(w, "percentage autogrowth") ||
			strings.Contains(w, "sparse") && !strings.Contains(w, "[app_snap] file [app]") {
			t.Errorf("warning for a row filtered out: %q", w)
		}
	}

	res, err = runDBFiles(t, newFilesServer(t), "app --filegroup archive")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "logical_name"); !slices.Equal(got, []string{"app_arch"}) {
		t.Errorf("--filegroup archive: %q", got)
	}
	if want := []string{"[app] file [app_arch]: at 95.24 % of its max_size (90 % or more)."}; !slices.Equal(res.Warnings, want) {
		t.Errorf("--filegroup archive warnings %q, want %q", res.Warnings, want)
	}
}

// Without VIEW SERVER STATE: volume_free_pct NULL and one warning naming the
// permission, exit 0.
func TestDBFilesNoViewServerState(t *testing.T) {
	s := newFilesServer(t)
	s.volsHidden = true
	res, err := runDBFiles(t, s, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "volume_free_pct"); !slices.Equal(got, []string{"NULL", "NULL", "NULL", "NULL"}) {
		t.Errorf("volume_free_pct %q", got)
	}
	n := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "VIEW SERVER STATE") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d VIEW SERVER STATE warnings in %q, want 1", n, res.Warnings)
	}
}

// A snapshot lists its own sparse files from sys.master_files, with the
// filegroup and space used of its inside read; the source's log is not
// listed.
func TestDBFilesSnapshot(t *testing.T) {
	res, err := runDBFiles(t, newFilesServer(t), "app_snap")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	for name, want := range map[string][]string{
		"logical_name":  {"app"},
		"physical_path": {"/data/app.ss"},
		"filegroup":     {"PRIMARY"},
		"used_mb":       {"95.00"},
	} {
		if got := column(set, name); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if w := "[app_snap] file [app]: a snapshot's sparse file; its size is a ceiling, it occupies 0.50 MB on disk."; !slices.Contains(res.Warnings, w) {
		t.Errorf("no warning %q in %q", w, res.Warnings)
	}
}

// A data or log file whose space used reads 0 — FILEPROPERTY NULL, the
// login cannot see it — has used, free and free_pct NULL, not 100 % free.
func TestDBFilesUsedUnknown(t *testing.T) {
	s := newFilesServer(t)
	s.files["app"][1].SpaceUsedKB = 0
	res, err := runDBFiles(t, s, "app --type data")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "free_pct"); !slices.Equal(got, []string{"NULL", "90.00"}) {
		t.Errorf("free_pct %q", got)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "free inside") {
			t.Errorf("free-space warning on an unknown figure: %q", w)
		}
	}
}

// Growth stays in MB under --unit gb, where two decimals would round it to 0.
func TestDBFilesGrowthInGB(t *testing.T) {
	res, err := runDBFiles(t, newFilesServer(t), "app --unit gb --type data")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "growth"); !slices.Equal(got, []string{"64 MB", "none"}) {
		t.Errorf("growth %q", got)
	}
}

func TestDBFilesSort(t *testing.T) {
	for by, want := range map[string][]string{
		"name":     {"app", "app_arch", "app_fs", "app_log"},
		"size":     {"app_arch", "app", "app_log", "app_fs"},
		"used":     {"app", "app_arch", "app_log", "app_fs"},
		"free-pct": {"app", "app_log", "app_arch", "app_fs"},
	} {
		res, err := runDBFiles(t, newFilesServer(t), "app --sort-by "+by)
		if err != nil {
			t.Fatal(err)
		}
		if got := column(res.Sets[0], "logical_name"); !slices.Equal(got, want) {
			t.Errorf("--sort-by %s: %q, want %q", by, got, want)
		}
	}
}

func TestDBFilesUsage(t *testing.T) {
	for argv, want := range map[string]string{
		"app --max-free-pct 101": "--max-free-pct: 101 is not between 0 and 100",
		"":                       "give a database <name> or --all",
		"nope":                   "no database [nope] on the server",
	} {
		_, err := runDBFiles(t, newFilesServer(t), argv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", argv, err, want)
		}
	}
}

// spaceServer is storageServer answering db space's and db log-usage's
// reads: the sp_spaceused figures and the log usage, inside each database.
type spaceServer struct {
	*storageServer
	space  map[string]gosmo.SpaceReport
	logs   map[string]gosmo.LogSpace
	logErr error // every LogSpace read's error, when set
}

// logsRead is the databases LogSpace was read in.
func (s *spaceServer) logsRead() []string { return s.readIn("sys.dm_db_log_space_usage") }

func newSpaceServer(t *testing.T) *spaceServer {
	s := &spaceServer{
		storageServer: newStorageServer(t),
		space: map[string]gosmo.SpaceReport{
			"master": {DataFilesKB: 4096, LogFilesKB: 2048, DataUsedKB: 3000,
				DataKB: 2000, IndexKB: 896, UnusedKB: 200},
			"app": {DataFilesKB: 8192, LogFilesKB: 8192, DataUsedKB: 5632,
				DataKB: 3176, IndexKB: 1528, UnusedKB: 864},
			"app_snap": {DataFilesKB: 8192, LogFilesKB: 8192, DataUsedKB: 4096,
				DataKB: 2136, IndexKB: 1456, UnusedKB: 448},
		},
		logs: map[string]gosmo.LogSpace{
			"master": {SizeKB: 2040, UsedKB: 900, UsedPercent: 44.1176, ReuseWait: "NOTHING"},
			"app":    {SizeKB: 8184, UsedKB: 1023, UsedPercent: 12.5049, ReuseWait: "LOG_BACKUP"},
		},
	}
	answer(s.Fake, func(q gosmotest.Query) ([][]any, error) {
		sp, ok := s.space[q.Database]
		if !ok {
			return nil, errors.New("boom")
		}
		return [][]any{clitest.SpaceReportRow(sp)}, nil
	}, "FROM   sys.sysfiles")
	answer(s.Fake, func(q gosmotest.Query) ([][]any, error) {
		if s.logErr != nil {
			return nil, s.logErr
		}
		return [][]any{clitest.LogSpaceRow(s.logs[q.Database])}, nil
	}, "sys.dm_db_log_space_usage")
	return s
}

// runDBSpace runs db space over s with argv after "db space".
func runDBSpace(t *testing.T, s *spaceServer, argv string) (*output.Result, error) {
	t.Helper()
	return runStorage(t, s.Fake, "space", argv)
}

func TestDBSpaceOneDatabase(t *testing.T) {
	res, err := runDBSpace(t, newSpaceServer(t), "app")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	var names []string
	for _, c := range set.Columns {
		names = append(names, c.Name)
	}
	if want := []string{"database", "total_mb", "data_size_mb", "data_used_mb", "unallocated_mb", "reserved_mb",
		"data_mb", "index_mb", "unused_mb", "log_size_mb", "log_used_mb", "log_used_pct"}; !slices.Equal(names, want) {
		t.Errorf("columns %q, want %q", names, want)
	}
	for col, want := range map[string]string{
		"database": "app", "total_mb": "16.00", "data_size_mb": "8.00", "data_used_mb": "5.50",
		"unallocated_mb": "2.56", "reserved_mb": "5.44", "data_mb": "3.10", "index_mb": "1.49",
		"unused_mb": "0.84", "log_size_mb": "7.99", "log_used_mb": "1.00", "log_used_pct": "12.50",
	} {
		if got := column(set, col); !slices.Equal(got, []string{want}) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
	if res.Database != "app" || len(res.Warnings) != 0 {
		t.Errorf("database %q, warnings %q", res.Database, res.Warnings)
	}
}

// --unit kb prints sp_spaceused's own KB figures.
func TestDBSpaceUnitKB(t *testing.T) {
	res, err := runDBSpace(t, newSpaceServer(t), "app --unit kb")
	if err != nil {
		t.Fatal(err)
	}
	for col, want := range map[string]string{"reserved_kb": "5568", "data_kb": "3176", "index_kb": "1528",
		"unused_kb": "864", "log_used_pct": "12.50"} {
		if got := column(res.Sets[0], col); !slices.Equal(got, []string{want}) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
}

// --all lists the readable databases, a snapshot without log figures, and
// names the unreadable ones under warnings (D3).
func TestDBSpaceAll(t *testing.T) {
	s := newSpaceServer(t)
	res, err := runDBSpace(t, s, "--all")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	if got := column(set, "database"); !slices.Equal(got, []string{"app", "app_snap", "master"}) {
		t.Errorf("databases %q", got)
	}
	if got := column(set, "log_size_mb"); !slices.Equal(got, []string{"7.99", "NULL", "1.99"}) {
		t.Errorf("log_size_mb %q", got)
	}
	if !slices.Equal(s.logsRead(), []string{"app", "master"}) {
		t.Errorf("LogSpace read for %q: a snapshot has no log", s.logsRead())
	}
	want := []string{"snapshot", "[archive]: space not read: the database is OFFLINE.", "[hr]: space not read: the login has no access to it."}
	if len(res.Warnings) != len(want) {
		t.Fatalf("warnings %q", res.Warnings)
	}
	for i, w := range want {
		if !strings.Contains(res.Warnings[i], w) {
			t.Errorf("warning %d %q, want %q", i, res.Warnings[i], w)
		}
	}
}

// Without VIEW SERVER STATE the log columns are NULL with one warning, and
// LogSpace is not asked again for the other databases.
func TestDBSpaceNoViewServerState(t *testing.T) {
	s := newSpaceServer(t)
	s.logErr = mssql.Error{Number: 300, Class: 14, Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}
	res, err := runDBSpace(t, s, "--all")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "log_used_pct"); !slices.Equal(got, []string{"NULL", "NULL", "NULL"}) {
		t.Errorf("log_used_pct %q", got)
	}
	if got := column(res.Sets[0], "reserved_mb"); got[0] == "NULL" {
		t.Errorf("reserved_mb %q: the rest of the row stands", got)
	}
	if len(s.logsRead()) != 1 {
		t.Errorf("LogSpace read for %q, want once", s.logsRead())
	}
	if n := strings.Count(strings.Join(res.Warnings, "\n"), "VIEW SERVER STATE"); n != 1 {
		t.Errorf("%d VIEW SERVER STATE warnings in %q, want 1", n, res.Warnings)
	}
}

// A named database that cannot be read fails: 8 for its state, 9 for access
// (D3); a failed SpaceReport fails a named database and is a warning under
// --all.
func TestDBSpaceUnreadable(t *testing.T) {
	for argv, want := range map[string]output.Code{
		"archive": output.CodeNotFound,
		"hr":      output.CodePermissionDenied,
		"nope":    output.CodeNotFound,
	} {
		_, err := runDBSpace(t, newSpaceServer(t), argv)
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != want {
			t.Errorf("%q: %v, want %s", argv, err, want)
		}
	}
	s := newSpaceServer(t)
	delete(s.space, "master")
	if _, err := runDBSpace(t, s, "master"); err == nil {
		t.Error("master: a failed SpaceReport succeeded")
	}
	res, err := runDBSpace(t, s, "--all")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "database"); !slices.Equal(got, []string{"app", "app_snap"}) {
		t.Errorf("databases %q", got)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "[master]: space not read: gosmo: space report") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestDBSpaceXTPHidden(t *testing.T) {
	s := newSpaceServer(t)
	sp := s.space["app"]
	sp.XTPPrecreatedHidden = true
	s.space["app"] = sp
	res, err := runDBSpace(t, s, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "PRECREATED") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// runDBLogUsage runs db log-usage over s with argv after "db log-usage".
func runDBLogUsage(t *testing.T, s *spaceServer, argv string) (*output.Result, error) {
	t.Helper()
	return runStorage(t, s.Fake, "log-usage", argv)
}

// newLogServer is newSpaceServer with VLF counts read.
func newLogServer(t *testing.T) *spaceServer {
	s := newSpaceServer(t)
	for name, l := range s.logs {
		l.VLFs, l.ActiveVLFs, l.VLFsKnown = 4, 1, true
		s.logs[name] = l
	}
	return s
}

func TestDBLogUsageOneDatabase(t *testing.T) {
	res, err := runDBLogUsage(t, newLogServer(t), "app")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	var names []string
	for _, c := range set.Columns {
		names = append(names, c.Name)
	}
	if want := []string{"database", "log_size_mb", "log_used_mb", "log_used_pct", "log_reuse_wait_desc",
		"vlf_count", "vlf_active"}; !slices.Equal(names, want) {
		t.Errorf("columns %q, want %q", names, want)
	}
	for col, want := range map[string]string{
		"database": "app", "log_size_mb": "7.99", "log_used_mb": "1.00", "log_used_pct": "12.50",
		"log_reuse_wait_desc": "LOG_BACKUP", "vlf_count": "4", "vlf_active": "1",
	} {
		if got := column(set, col); !slices.Equal(got, []string{want}) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
	if res.Database != "app" || !slices.Equal(res.Warnings, []string{"[app]: log reuse waits on LOG_BACKUP."}) {
		t.Errorf("database %q, warnings %q", res.Database, res.Warnings)
	}
}

// --all skips snapshots silently (no log) and names the unreadable databases
// under warnings (D3); only a reuse wait other than NOTHING warns.
func TestDBLogUsageAll(t *testing.T) {
	s := newLogServer(t)
	res, err := runDBLogUsage(t, s, "--all --unit kb")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	if got := column(set, "database"); !slices.Equal(got, []string{"app", "master"}) {
		t.Errorf("databases %q", got)
	}
	if got := column(set, "log_used_kb"); !slices.Equal(got, []string{"1023", "900"}) {
		t.Errorf("log_used_kb %q", got)
	}
	if !slices.Equal(s.logsRead(), []string{"app", "master"}) {
		t.Errorf("LogSpace read for %q", s.logsRead())
	}
	want := []string{"[archive]: log usage not read: the database is OFFLINE.",
		"[hr]: log usage not read: the login has no access to it.", "[app]: log reuse waits on LOG_BACKUP."}
	if !slices.Equal(res.Warnings, want) {
		t.Errorf("warnings %q, want %q", res.Warnings, want)
	}
}

// Before 2016 SP2 the VLF columns are NULL with one warning for the server.
func TestDBLogUsageNoLogInfo(t *testing.T) {
	s := newLogServer(t)
	s.Info.ProductVersion = "13.0.4001.0" // 2016 SP1
	res, err := runDBLogUsage(t, s, "--all")
	if err != nil {
		t.Fatal(err)
	}
	if got := column(res.Sets[0], "vlf_count"); !slices.Equal(got, []string{"NULL", "NULL"}) {
		t.Errorf("vlf_count %q", got)
	}
	if n := strings.Count(strings.Join(res.Warnings, "\n"), "sys.dm_db_log_info"); n != 1 {
		t.Errorf("%d dm_db_log_info warnings in %q, want 1", n, res.Warnings)
	}
}

// Without VIEW SERVER STATE the command fails at the first refusal, named or
// --all: the permission is the server's.
func TestDBLogUsageNoViewServerState(t *testing.T) {
	for _, argv := range []string{"app", "--all"} {
		s := newLogServer(t)
		s.logErr = mssql.Error{Number: 300, Class: 14, Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}
		_, err := runDBLogUsage(t, s, argv)
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "VIEW SERVER STATE") {
			t.Errorf("%q: %v, want PERMISSION_DENIED naming VIEW SERVER STATE", argv, err)
		}
		if len(s.logsRead()) != 1 {
			t.Errorf("%q: LogSpace read for %q, want once", argv, s.logsRead())
		}
	}
}

// A named database that cannot be read, or a snapshot, fails: 8 for the
// state, the snapshot and an unknown name, 9 for access; another LogSpace
// error fails a named database and is a warning under --all.
func TestDBLogUsageUnreadable(t *testing.T) {
	for argv, want := range map[string]output.Code{
		"archive":  output.CodeNotFound,
		"app_snap": output.CodeNotFound,
		"hr":       output.CodePermissionDenied,
		"nope":     output.CodeNotFound,
	} {
		s := newLogServer(t)
		_, err := runDBLogUsage(t, s, argv)
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != want {
			t.Errorf("%q: %v, want %s", argv, err, want)
		}
		if len(s.logsRead()) != 0 {
			t.Errorf("%q: LogSpace read for %q", argv, s.logsRead())
		}
	}
	s := newLogServer(t)
	s.logErr = errors.New("boom")
	if _, err := runDBLogUsage(t, s, "master"); err == nil {
		t.Error("master: a failed LogSpace succeeded")
	}
	res, err := runDBLogUsage(t, s, "--all")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sets[0].Rows) != 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "[master]: log usage not read: gosmo: log space") {
		t.Errorf("rows %d, warnings %q", len(res.Sets[0].Rows), res.Warnings)
	}
}

func TestDBLogUsageUsage(t *testing.T) {
	for _, argv := range []string{"", "app --all"} {
		_, err := runDBLogUsage(t, newLogServer(t), argv)
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodeUsageError || !strings.HasPrefix(e.Message, "db log-usage:") {
			t.Errorf("%q: %v, want a usage error naming db log-usage", argv, err)
		}
	}
}
