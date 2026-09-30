package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"database/sql/driver"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// Phase G of docs/xevents-plan.md: Azure. Azure SQL Database's event sessions
// are the database's own (ON DATABASE, sys.database_event_sessions), filed
// under each database; on every Azure edition an event_file is a blob URL
// written with the credential named after its container.

// newFakeConnOnAzureSQLDB is newFakeConnOnAzureMI for Azure SQL Database —
// EngineEdition 5, the only thing the scope decision reads.
func newFakeConnOnAzureSQLDB(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	info := serverInfoResponse()
	info.rows[0][1] = "SQL Azure"
	info.rows[0][2] = "12.0.2000.8"
	info.rows[0][8] = int64(5)
	info.rows[0][9] = "Microsoft SQL Azure (RTM) - 12.0.2000.8 ..."
	return newFakeConnFrom(t, append([]fakeResponse{info, sysInfoResponse()}, responses...))
}

// xeDatabaseSessionResponses is xeSessionResponses read from the database
// scope's views.
func xeDatabaseSessionResponses() []fakeResponse {
	out := xeSessionResponses()
	for i := range out {
		out[i].match = strings.Replace(out[i].match, "sys.server_event_session", "sys.database_event_session", 1)
	}
	return out
}

func TestAzureSQLDatabaseFilesExtendedEventsUnderEachDatabase(t *testing.T) {
	sc, _ := newFakeConnOnAzureSQLDB(t, capabilityResponses(true, nil, nil, nil, nil)...)
	l := loaderCtx{ctx: context.Background(), sc: sc}

	mgmt, _ := loadManagementChildren(l, &explorerNode{})
	if got := labelsOfNodes(mgmt); slices.Contains(got, "Extended Events") {
		t.Errorf("Management lists %v — Azure SQL Database has no server-scoped sessions", got)
	}
	folders, err := loadDatabaseChildren(l, &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(folders, func(n *explorerNode) bool { return n.data.Type == NodeExtendedEvents })
	if i < 0 || folders[i].data.DBName != "appdb" {
		t.Fatalf("database folders = %v, want an Extended Events folder carrying the database", labelsOfNodes(folders))
	}
	sub, _ := loadExtendedEventsChildren(l, folders[i])
	if len(sub) != 1 || sub[0].data.Type != NodeEventSessions || sub[0].data.DBName != "appdb" {
		t.Errorf("the database's Extended Events holds %v, want Sessions alone (no Profiler)", labelsOfNodes(sub))
	}

	// A Managed Instance is server-scoped like SQL Server.
	mi, _ := newFakeConnOnAzureMI(t, capabilityResponses(true, nil, nil, nil, nil)...)
	l = loaderCtx{ctx: context.Background(), sc: mi}
	if got := labelsOfNodes(must(loadManagementChildren(l, &explorerNode{}))); !slices.Contains(got, "Extended Events") {
		t.Errorf("MI Management = %v, want Extended Events", got)
	}
	if got := labelsOfNodes(must(loadDatabaseChildren(l, &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: mi}}))); slices.Contains(got, "Extended Events") {
		t.Errorf("MI database folders = %v — its sessions are the server's", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestDatabaseScopedSessionsAreReadAndWrittenInTheirDatabase(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConnOnAzureSQLDB(t, xeDatabaseSessionResponses()...)
	l := loaderCtx{ctx: context.Background(), sc: sc}
	sessions, err := loadEventSessionsChildren(l, &explorerNode{data: nodeData{Type: NodeEventSessions, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := labelsOfNodes(sessions); !slices.Equal(got, []string{"AlwaysOn_health", "system_health", "zz_trace"}) {
		t.Fatalf("sessions = %v", got)
	}
	zz := sessions[2]
	if zz.data.DBName != "appdb" {
		t.Fatalf("session node DBName = %q — every verb below would go to the server", zz.data.DBName)
	}
	zz.parent = &explorerNode{label: "Sessions"}

	a.setEventSessionState(sc, zz, true)
	waitAndDrain(t, a)
	if got := inst.StatementsIn("appdb"); !slices.Equal(got, []string{"ALTER EVENT SESSION [zz_trace] ON DATABASE STATE = START"}) {
		t.Errorf("Start ran %q in appdb (all: %q)", got, inst.Statements())
	}

	if err := objectOps[NodeEventSession].drop(t.Context(), sc, zz.data); err != nil {
		t.Fatal(err)
	}
	if got := inst.StatementsIn("appdb"); !slices.Contains(got, "DROP EVENT SESSION [zz_trace] ON DATABASE") {
		t.Errorf("Delete ran %q in appdb", got)
	}

	var objs []nodeData
	if _, _, err := eventSessionsFolderDetail(context.Background(), sc, &explorerNode{data: nodeData{Type: NodeEventSessions, DBName: "appdb"}}, &objs); err != nil {
		t.Fatal(err)
	}
	if len(objs) != 3 || objs[2].DBName != "appdb" {
		t.Errorf("Details row objects = %+v — a row's menu would act on the server", objs)
	}
}

// A database-scoped session's verbs ask the database's one right, whatever
// the server-scope ones say.
func TestDatabaseScopedSessionVerbsGateOnTheDatabaseRight(t *testing.T) {
	for _, held := range []bool{true, false} {
		var dbGranted, dbDenied []string
		if held {
			dbGranted = []string{"ALTER ANY DATABASE EVENT SESSION"}
		} else {
			dbDenied = []string{"ALTER ANY DATABASE EVENT SESSION"}
		}
		a := newTestApp()
		// The server-scope names the other way round, so a gate still asking
		// them answers wrong.
		var granted, denied []string
		if held {
			denied = []string{"ALTER ANY EVENT SESSION"}
		} else {
			granted = []string{"ALTER ANY EVENT SESSION"}
		}
		sc, _ := newFakeConnOnAzureSQLDB(t, capabilityResponses(true, granted, denied, dbGranted, dbDenied)...)
		sc.ProbeCapabilities()
		sc.DatabaseCapabilities(context.Background(), "appdb")

		stopped := &explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace", DBName: "appdb", conn: sc}}
		running := &explorerNode{data: nodeData{Type: NodeEventSession, Name: "zz_trace", DBName: "appdb", IsEnabled: true, conn: sc}}
		folder := &explorerNode{data: nodeData{Type: NodeEventSessions, DBName: "appdb", conn: sc}}
		for _, c := range []struct {
			node  *explorerNode
			label string
		}{
			{stopped, "Start Session"}, {running, "Stop Session"}, {stopped, "Delete..."}, {folder, "New Session..."},
		} {
			if got := eventSessionMenuItem(t, a, c.node, c.label).Enabled(); got != held {
				t.Errorf("database right held=%v: %s enabled = %v", held, c.label, got)
			}
		}
		if got := gate0(eventSessionPropPages(nil, sc, xeScope{db: "appdb"}, "zz_trace")); got != "ALTER ANY DATABASE EVENT SESSION" {
			t.Errorf("Properties pages require %q", got)
		}
	}
}

// gate0 is the right the first page of a page set requires.
func gate0(pages []propPage) string {
	if len(pages) == 0 || len(pages[0].requires) == 0 {
		return ""
	}
	return pages[0].requires[0].Name
}

func TestXEventProfilerIsNotOfferedOnAzureSQLDatabase(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConnOnAzureSQLDB(t, capabilityResponses(true, []string{"ALTER ANY EVENT SESSION"}, nil, nil, nil)...)
	sc.ProbeCapabilities()
	if xeProfilerAllowed(sc) {
		t.Error("XEvent Profiler allowed on Azure SQL Database, which has no server-scoped sessions")
	}
	a.launchXEventProfiler(sc, xeProfilerKinds[0])
	if a.statusText != xeProfilerNoServerScope || len(inst.Statements()) != 0 {
		t.Errorf("status %q, statements %q", a.statusText, inst.Statements())
	}
}

func TestXEBlobFilename(t *testing.T) {
	for _, c := range []struct{ container, file, want string }{
		{"https://a.blob.core.windows.net/xe", "trace", "https://a.blob.core.windows.net/xe/trace.xel"},
		{"https://a.blob.core.windows.net/xe/", "trace.XEL", "https://a.blob.core.windows.net/xe/trace.XEL"},
		{"https://a.blob.core.windows.net/xe", `C:\logs\trace.xel`, "https://a.blob.core.windows.net/xe/trace.xel"},
		{"https://a.blob.core.windows.net/new", "https://a.blob.core.windows.net/old/trace.xel", "https://a.blob.core.windows.net/new/trace.xel"},
	} {
		if got := xeBlobFilename(c.container, c.file); got != c.want {
			t.Errorf("xeBlobFilename(%q, %q) = %q, want %q", c.container, c.file, got, c.want)
		}
	}
}

// xeTestStorage is a Data Storage editor over a catalog of two targets whose
// columns are already in hand, so nothing is read.
func xeTestStorage(spec gosmo.EventSessionSpec, azure bool, containers []string) (*xeStorageEditor, *xeSessionModel) {
	cat := &xeCatalog{targets: []gosmo.XEObject{
		{Package: "package0", Name: "event_file"}, {Package: "package0", Name: "ring_buffer"},
	}}
	m := newXESessionModel(spec)
	cols := map[string][]gosmo.XEObjectColumn{"package0.event_file": nil, "package0.ring_buffer": nil}
	return newXEStorageEditor(xeHost{app: newTestApp()}, cat, m, func() string { return "trace" }, cols, azure, containers), m
}

func TestAnAzureEventFileIsABlobInThePickedContainer(t *testing.T) {
	const a, b = "https://acct.blob.core.windows.net/a", "https://acct.blob.core.windows.net/b"
	s, m := xeTestStorage(gosmo.EventSessionSpec{}, true, []string{a, b})
	s.add()
	if got := xeFieldValue(m.targets[0].Fields, "filename"); got != a+"/trace.xel" {
		t.Fatalf("added event_file's filename = %q", got)
	}
	s.containerRow.Edit(1)
	if got := xeFieldValue(m.targets[0].Fields, "filename"); got != b+"/trace.xel" {
		t.Errorf("after picking the second container, filename = %q", got)
	}
	if err := s.validate(); err != nil {
		t.Errorf("a blob URL refused: %v", err)
	}
	m.targets[0].Fields = xeSetField(m.targets[0].Fields, "filename", "trace", true)
	if err := s.validate(); err == nil || !strings.Contains(err.Error(), "https://") {
		t.Errorf("a bare filename on Azure: validate = %v, want the blob URL asked for", err)
	}

	// No credential names a container: the filename is left to be typed, and
	// until it is a URL the target is refused.
	s, m = xeTestStorage(gosmo.EventSessionSpec{}, true, nil)
	s.add()
	if got := xeFieldValue(m.targets[0].Fields, "filename"); got != "" {
		t.Errorf("with no container, filename = %q, want it left empty", got)
	}
	if s.validate() == nil {
		t.Error("an Azure event_file with no filename was accepted")
	}

	// Off Azure nothing changes: a bare name, the error-log directory's.
	s, m = xeTestStorage(gosmo.EventSessionSpec{}, false, nil)
	s.add()
	if got := xeFieldValue(m.targets[0].Fields, "filename"); got != "trace" || s.containerRow != nil || s.validate() != nil {
		t.Errorf("on-prem event_file filename %q, container row %v, validate %v", got, s.containerRow != nil, s.validate())
	}
}

// A Managed Instance's own system_health writes a local file; editing the
// session's other targets must not be refused over the one it already had.
func TestAnAzureSessionsOwnLocalEventFileIsLeftAlone(t *testing.T) {
	spec := gosmo.EventSessionSpec{Targets: []gosmo.SessionTarget{
		{Package: "package0", Name: "event_file", Fields: []gosmo.SessionField{{Name: "filename", Value: "system_health.xel", IsString: true}}},
	}}
	s, m := xeTestStorage(spec, true, nil)
	s.typeRow.SetSelected(1)
	s.add()
	if len(m.targets) != 2 {
		t.Fatalf("targets = %v", m.targets)
	}
	if err := s.validate(); err != nil {
		t.Errorf("unchanged local event_file refused: %v", err)
	}
	m.targets[0].Fields = xeSetField(m.targets[0].Fields, "max_file_size", "10", false)
	if s.validate() == nil {
		t.Error("an edited local event_file on Azure was accepted")
	}
}

func TestXEBlobContainersAreTheURLCredentials(t *testing.T) {
	now := time.Now()
	creds := fakeResponse{match: "FROM   sys.credentials", cols: 7, rows: [][]driver.Value{
		{int64(1), "https://acct.blob.core.windows.net/xe", "SHARED ACCESS SIGNATURE", now, now, nil, nil},
		{int64(2), "backup_cred", `DOMAIN\svc`, now, now, nil, nil},
		{int64(3), "HTTPS://acct.blob.core.windows.net/audit/", "SHARED ACCESS SIGNATURE", now, now, nil, nil},
	}}
	mi, _ := newFakeConnOnAzureMI(t, creds)
	got := xeBlobContainers(context.Background(), xeHost{sc: mi})
	if want := []string{"HTTPS://acct.blob.core.windows.net/audit", "https://acct.blob.core.windows.net/xe"}; !slices.Equal(got, want) {
		t.Errorf("MI containers = %q, want %q", got, want)
	}

	dbCreds := fakeResponse{match: "FROM   sys.database_scoped_credentials", cols: 5, rows: [][]driver.Value{
		{int64(1), "https://acct.blob.core.windows.net/dbxe", "SHARED ACCESS SIGNATURE", now, now},
	}}
	sqldb, _ := newFakeConnOnAzureSQLDB(t, dbCreds, creds)
	got = xeBlobContainers(context.Background(), xeHost{sc: sqldb, scope: xeScope{db: "appdb"}})
	if !slices.Equal(got, []string{"https://acct.blob.core.windows.net/dbxe"}) {
		t.Errorf("SQL Database containers = %q, want the database's credentials", got)
	}

	onPrem, _ := newFakeConn(t, creds)
	if got := xeBlobContainers(context.Background(), xeHost{sc: onPrem}); got != nil {
		t.Errorf("on-prem containers = %q, want none — an event_file there is a path", got)
	}
}

func TestXEContainerLabel(t *testing.T) {
	for in, want := range map[string]string{
		"https://acct.blob.core.windows.net/xe":       "acct/xe",
		"HTTPS://Acct.Blob.Core.Windows.Net/xe/sub":   "Acct/xe/sub",
		"https://acct.blob.core.usgovcloudapi.net/xe": "https://acct.blob.core.usgovcloudapi.net/xe",
	} {
		if got := xeContainerLabel(in); got != want {
			t.Errorf("xeContainerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// -- Local event files on a Managed Instance ------------------------------------------

// miCurrentFile is the file a Managed Instance session writes, a local path
// the server reads only through the session's wildcard.
const miCurrentFile = `C:\SF\log\system_health_0_133.xel`

// A Managed Instance refuses to read a local file by its path, so Watch Live
// Data starts with the session's wildcard from the start rather than at the
// current file, and goes on from the cursor that read returned.
func TestManagedInstanceWatchesALocalEventFileByItsPattern(t *testing.T) {
	sc, inst := newFakeConnOnAzureMI(t, slices.Concat(runningZZTrace(), []fakeResponse{
		{match: "RingBufferTarget/@truncated", arg: "zz_trace", cols: 12, rows: [][]driver.Value{
			{time.Now(), int64(0), int64(0), "package0", "event_file", int64(1), int64(0), miCurrentFile, nil, nil, nil, nil},
		}},
		{match: "fn_xe_file_target_read_file", arg: "zz_trace*.xel", cols: 3, rows: [][]driver.Value{
			{miCurrentFile, int64(4096), xeEventXML("sql_batch_completed", 1)},
		}},
	}, xeSessionResponses())...)

	r := &xeReader{sc: sc, session: "zz_trace", live: true}
	for i := range 2 {
		if _, _, err := r.read(context.Background()); err != nil {
			t.Fatalf("read %d: %v", i+1, err)
		}
	}
	if fileArgs(inst, miCurrentFile) != nil {
		t.Error("the current file was read by its path — Msg 40538 on a Managed Instance")
	}
	inst.mu.Lock()
	var reads [][]driver.NamedValue
	for i, q := range inst.reads {
		if strings.Contains(q, "fn_xe_file_target_read_file") {
			reads = append(reads, inst.readArgs[i])
		}
	}
	inst.mu.Unlock()
	if len(reads) != 2 || reads[0][1].Value != nil || reads[1][1].Value != miCurrentFile || reads[1][2].Value != int64(4096) {
		t.Fatalf("reads %v — want the wildcard from the start, then from the first read's cursor", reads)
	}
}

// View Target Data and Merge read the pattern as one set on a Managed
// Instance: the files list, but not one of them reads by its path.
func TestManagedInstanceReadsAListedFileSetByItsPattern(t *testing.T) {
	const f1, f2 = `C:\SF\log\zz_trace_0_1.xel`, `C:\SF\log\zz_trace_0_2.xel`
	dmf := func(path string) []driver.Value {
		return []driver.Value{path, path[len(`C:\SF\log\`):], int64(0), int64(1), time.Time{}}
	}
	for _, tc := range []struct {
		name string
		r    func(sc *db.ServerConn) *xeReader
	}{
		{"view target data", func(sc *db.ServerConn) *xeReader {
			return &xeReader{sc: sc, session: "zz_trace", target: gosmo.XETargetEventFile, capacity: 100}
		}},
		{"merge", func(sc *db.ServerConn) *xeReader { return &xeReader{sc: sc, files: "zz_trace*.xel", capacity: 100} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConnOnAzureMI(t, slices.Concat(xeSessionByName("zz_trace"), []fakeResponse{
				{match: "ErrorLogFileName", cols: 1, rows: [][]driver.Value{{`C:\SF\log\ERRORLOG`}}},
				{match: "sys.dm_os_enumerate_filesystem", cols: 5, rows: [][]driver.Value{dmf(f1), dmf(f2)}},
				fileRead(f1, 1), fileRead(f2, 2), fileRead("zz_trace*.xel", 1, 2),
			}, xeSessionResponses())...)
			n := 0
			if err := tc.r(sc).run(context.Background(), func(b xeBatch) { n += len(b.events) }); err != nil {
				t.Fatal(err)
			}
			if n != 2 || fileArgs(inst, "zz_trace*.xel") == nil {
				t.Errorf("%d events, pattern read %v — want both events through the pattern", n, fileArgs(inst, "zz_trace*.xel") != nil)
			}
			if fileArgs(inst, f1) != nil || fileArgs(inst, f2) != nil {
				t.Error("a listed file was read by its path — Msg 40538 on a Managed Instance")
			}
		})
	}
}

// Merge's prompt starts on the one local file set a Managed Instance reads.
func TestManagedInstanceMergeStartsOnSystemHealth(t *testing.T) {
	sc, _ := newFakeConnOnAzureMI(t)
	a := newTestApp()
	a.promptMergeXEventFiles(sc)
	if got := a.promptDialog.Value(); got != "system_health*.xel" {
		t.Errorf("pattern %q, want system_health*.xel", got)
	}
}

// Only a local path is held to the pattern, and only on Azure: a blob URL is
// read as given, and an on-premises server reads any path.
func TestOnlyAzureLocalEventFilesReadByPattern(t *testing.T) {
	mi, _ := newFakeConnOnAzureMI(t)
	onPrem, _ := newFakeConn(t)
	const blob = "https://acct.blob.core.windows.net/xe/s_0_1.xel"
	if !xeReadsOnlyByPattern(mi, miCurrentFile) || xeReadsOnlyByPattern(mi, blob) || xeReadsOnlyByPattern(onPrem, miCurrentFile) {
		t.Errorf("MI local %v, MI blob %v, on-premises local %v — want true, false, false",
			xeReadsOnlyByPattern(mi, miCurrentFile), xeReadsOnlyByPattern(mi, blob), xeReadsOnlyByPattern(onPrem, miCurrentFile))
	}
}
