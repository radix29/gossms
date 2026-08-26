package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// -- the classifier ----------------------------------------------------------

// permissionDenied asks classifyRefusal the question these tests are about:
// is this SQL Server refusing on permission grounds, and in whose words?
func permissionDenied(err error) (string, bool) {
	r := classifyRefusal(err)
	return r.message, r.kind != notARefusal
}

// TestPermissionRefusalIsNamedByItsFirstMessage. A refused DMV read sends
// "VIEW SERVER PERFORMANCE STATE permission was denied on object 'server'"
// (Msg 300) and then the contentless "The user does not have permission to
// perform this action" (Msg 297) — and only the second reaches a caller that
// reads the error normally. Taking the last message here would put the useless
// half on screen, which is exactly what the tree used to show.
// Both messages captured live from win10cli, 2026-08-25.
func TestPermissionRefusalIsNamedByItsFirstMessage(t *testing.T) {
	err := sqlErrOf([]mssqlMsg{
		{300, 14, "VIEW SERVER PERFORMANCE STATE permission was denied on object 'server', database 'master'."},
		{297, 16, "The user does not have permission to perform this action."},
	})

	got, ok := permissionDenied(err)
	if !ok {
		t.Fatal("a permission refusal was not recognised as one")
	}
	if !strings.HasPrefix(got, "VIEW SERVER PERFORMANCE STATE") {
		t.Errorf("message = %q, want the one that names the right", got)
	}
	// And what reaches the screen is the right, read out of that message.
	if want := accessDeniedLabel + "Requires VIEW SERVER PERFORMANCE STATE."; accessDeniedText(err) != want {
		t.Errorf("accessDeniedText = %q, want %q", accessDeniedText(err), want)
	}
}

// TestEveryMeasuredRefusalIsRecognised pins the numbers themselves, each
// captured from a live instance rather than taken from documentation. A number
// dropped from the table sends its refusal back to the raw wrapped text.
func TestEveryMeasuredRefusalIsRecognised(t *testing.T) {
	for _, tt := range []struct {
		number int32
		text   string
	}{
		{916, `The server principal "user_dr" is not able to access the database "backup_test" under the current security context.`},
		{229, "The SELECT permission was denied on the object 'sysjobservers', database 'msdb', schema 'dbo'."},
		{229, "The EXECUTE permission was denied on the object 'xp_readerrorlog', database 'mssqlsystemresource', schema 'sys'."},
		{5011, "User does not have permission to alter database 'HealthClinic', the database does not exist, or the database is not in a state that allows access checks."},
	} {
		err := sqlErrOf([]mssqlMsg{{tt.number, 14, tt.text}})
		got, ok := permissionDenied(err)
		if !ok {
			t.Errorf("Msg %d not recognised as a permission refusal", tt.number)
			continue
		}
		if got != tt.text {
			t.Errorf("Msg %d message = %q, want the server's own sentence", tt.number, got)
		}
	}
}

// TestAnOrdinaryFailureIsNotReportedAsAccessDenied is the half that matters
// most: a read that failed for any other reason must keep its own text, or the
// user is sent to fix a permission that was never the problem.
func TestAnOrdinaryFailureIsNotReportedAsAccessDenied(t *testing.T) {
	for name, err := range map[string]error{
		"invalid object name": sqlErrOf([]mssqlMsg{{208, 16, "Invalid object name 'dbo.nope'."}}),
		"timeout":             errors.New("context deadline exceeded"),
		"deadlock":            sqlErrOf([]mssqlMsg{{1205, 13, "Transaction was deadlocked on lock resources."}}),
	} {
		if _, ok := permissionDenied(err); ok {
			t.Errorf("%s: reported as a permission refusal", name)
		}
		if got := accessDeniedText(err); got != "" {
			t.Errorf("%s: accessDeniedText = %q, want empty", name, got)
		}
		if displayError(err).Error() != err.Error() {
			t.Errorf("%s: displayError rewrote an unrelated failure", name)
		}
	}
}

// -- the tree ----------------------------------------------------------------

// TestAnInaccessibleDatabaseExpandsToOneLine. SQL Server lists a database the
// login cannot open, and every one of the nine folders below it opens with a
// USE that fails, so the user got nine copies of Msg 916 wrapped in gosmo's
// call stack. One honest line replaces them.
func TestAnInaccessibleDatabaseExpandsToOneLine(t *testing.T) {
	sc, _ := newFakeConn(t, capabilityResponses(false, nil, nil, nil, nil)...)
	sc.ProbeCapabilities()

	node := &explorerNode{label: "backup_test", data: nodeData{Type: NodeDatabase, DBName: "backup_test", conn: sc}}
	children, err := loadDatabaseChildren(loaderCtx{ctx: context.Background(), sc: sc}, node)
	if err != nil {
		t.Fatalf("loadDatabaseChildren: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("got %d children, want the single access-denied leaf", len(children))
	}
	if !strings.HasPrefix(children[0].label, accessDeniedLabel) {
		t.Errorf("leaf label = %q, want an access-denied line", children[0].label)
	}
	if children[0].data.Type != NodeError {
		t.Errorf("leaf type = %v, want NodeError so it cannot be expanded", children[0].data.Type)
	}
}

// TestAnAccessibleDatabaseStillExpands is the other direction — the check must
// not cost every login its folders. HAS_DBACCESS answering 1 keeps them.
func TestAnAccessibleDatabaseStillExpands(t *testing.T) {
	sc, _ := newFakeConn(t, capabilityResponses(true, nil, nil, nil, nil)...)
	sc.ProbeCapabilities()

	node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}}
	children, err := loadDatabaseChildren(loaderCtx{ctx: context.Background(), sc: sc}, node)
	if err != nil {
		t.Fatalf("loadDatabaseChildren: %v", err)
	}
	if len(children) != 10 {
		t.Fatalf("got %d children, want the ten object folders", len(children))
	}
}

// TestAFolderRefusalLosesThePlumbingButKeepsTheReason. The tree used to show
// `gosmo: list tables in "x": gosmo: USE x: mssql: The server principal …`.
func TestAFolderRefusalLosesThePlumbingButKeepsTheReason(t *testing.T) {
	inner := sqlErrOf([]mssqlMsg{{229, 14, "The SELECT permission was denied on the object 'sysjobservers', database 'msdb', schema 'dbo'."}})
	wrapped := errors.New("gosmo: list jobs: " + inner.Error())
	_ = wrapped // the classifier reads the wrapped error, not its text:
	n := errExplorerNode(errors.Join(errors.New("gosmo: list jobs"), inner))

	if strings.Contains(n.label, "gosmo:") {
		t.Errorf("node label = %q, still carries the call stack", n.label)
	}
	if !strings.Contains(n.label, "sysjobservers") {
		t.Errorf("node label = %q, lost the object the refusal names", n.label)
	}

	// A failure that is not a refusal keeps every word it had.
	other := errors.New("gosmo: list jobs: read tcp: connection reset by peer")
	if got := errExplorerNode(other).label; got != other.Error() {
		t.Errorf("node label = %q, want the original text %q", got, other.Error())
	}
}

// -- the Properties pages ----------------------------------------------------

// withoutResponses is configResponses minus the reads a login without VIEW
// SERVER STATE is refused, which is how the fake models that login: an
// unmatched query is an error, the same as a denial. Pair it with
// newFakeConnWithoutSysInfo — the connect-time sys.dm_os_sys_info read is
// refused for the same login, and dropping only one of the two would model a
// server that refuses one read of a view and serves another.
//
// The two are separate responses now; they were not always. sysInfoResponse
// used to match the view name and so answered both, which is how this test
// first passed with a CPU count of 16384 — see its comment in fakedb_test.go.
func withoutResponses(drop ...string) []fakeResponse {
	var out []fakeResponse
	for _, r := range configResponses() {
		if !slices.Contains(drop, r.match) {
			out = append(out, r)
		}
	}
	return out
}

// TestServerPropertiesPagesSurviveARefusedDMVRead. Until 2026-08-25 each of
// these pages returned the DMV's error from load, so the page a db_owner is
// allowed to see every other value on would not open at all.
func TestServerPropertiesPagesSurviveARefusedDMVRead(t *testing.T) {
	tests := []struct {
		page    string
		drop    string
		label   string
		want    string
		granted string // the value the scripted read produces when it is allowed
		present string // a row that must survive the refusal
	}{
		{"Memory", "sys.dm_os_sys_memory", "Physical memory (MB)", unreadableValue, "16384", "Maximum server memory"},
		{"Processors", "cpu_count, hyperthread_ratio", "Processors", unreadableValue, "8", "Max degree of parallelism"},
	}
	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			sc, inst := newFakeConnWithoutSysInfo(t, withoutResponses(tt.drop)...)
			form, _ := loadPage(t, configPage(t, sc, tt.page), inst)

			got, ok := findStatic(form, tt.label)
			if !ok {
				t.Fatalf("%q is not on the page at all", tt.label)
			}
			if got != tt.want {
				t.Errorf("%s = %q, want %q — a value that could not be read must not render as a number",
					tt.label, got, tt.want)
			}
			// A row that needs no such right must survive the refusal;
			// textRow fails the test naming it if it does not.
			textRow(t, form, tt.present)
			if !hasNoteMentioning(form, "VIEW SERVER STATE") {
				t.Error("the page does not say which right the N/A values need")
			}

			// And with the read allowed, the number is still the number — the
			// one the test scripted, not merely "not N/A". That is what says
			// the page read the response meant for it: a prepended response
			// whose match is too broad answers this query instead, and the
			// page then reports a number out of the wrong row without
			// erroring. See sysInfoResponse in fakedb_test.go.
			okSC, okInst := newFakeConn(t, configResponses()...)
			okForm, _ := loadPage(t, configPage(t, okSC, tt.page), okInst)
			if got, _ := findStatic(okForm, tt.label); got != tt.granted {
				t.Errorf("%s = %q with the read allowed, want the scripted %q", tt.label, got, tt.granted)
			}
		})
	}
}

// TestServerGeneralRendersUnreadableMemoryAsNA covers the third page, which
// takes its memory from the same refused read.
func TestServerGeneralRendersUnreadableMemoryAsNA(t *testing.T) {
	sc, inst := newFakeConnWithoutSysInfo(t, withoutResponses("sys.dm_os_sys_memory")...)
	form, _ := loadPage(t, pageServerGeneral(sc), inst)

	if got, _ := findStatic(form, "Memory"); got != unreadableValue {
		t.Errorf("Memory = %q, want %q", got, unreadableValue)
	}
	if got, _ := findStatic(form, "Collation"); got != "SQL_Latin1_General_CP1_CI_AS" {
		t.Errorf("Collation = %q: the refusal cost the page a value it can read", got)
	}
}

// TestServerGeneralRendersMemoryTheWayTheDetailPaneDoes. Server Properties >
// General and the Object Explorer Details pane both state the machine's
// physical memory, from two different reads (sys.dm_os_sys_memory and
// sys.dm_os_sys_info). General spelled it with a bare FormatInt and the pane
// with formatMB, so one said "16384 MB" and the other "16,384 MB" — two
// spellings of one number, which read as two different readings.
func TestServerGeneralRendersMemoryTheWayTheDetailPaneDoes(t *testing.T) {
	sc, inst := newFakeConn(t, configResponses()...)
	form, _ := loadPage(t, pageServerGeneral(sc), inst)

	// The same MB figure configResponses scripts for sys.dm_os_sys_memory,
	// through the helper the Details pane renders it with.
	want := sysInfoMB(&gosmo.ServerInfo{PhysicalMemoryMB: 16384})
	got, ok := findStatic(form, "Memory")
	if !ok {
		t.Fatal("Server Properties > General has no Memory row")
	}
	if got != want {
		t.Errorf("Memory = %q, want %q — the same quantity spelled two ways", got, want)
	}
}

// TestADeniedReadNamesTheLeastPrivilegeThatFixesIt. Every read these notes
// cover is in the performance half of the VIEW SERVER STATE that SQL Server
// 2022 split in two, so naming only the wide right asks for more than the job
// needs — in a feature whose whole point is least privilege. Naming only the
// narrow one would be wrong the other way, on 2019 and earlier where it does
// not exist.
func TestADeniedReadNamesTheLeastPrivilegeThatFixesIt(t *testing.T) {
	sc, inst := newFakeConnWithoutSysInfo(t, withoutResponses("sys.dm_os_sys_memory")...)
	form, _ := loadPage(t, pageServerGeneral(sc), inst)

	for _, want := range []string{"VIEW SERVER STATE", "VIEW SERVER PERFORMANCE STATE"} {
		if !hasNoteMentioning(form, want) {
			t.Errorf("the page's note does not mention %q", want)
		}
	}
}

// TestNumberOfUsersIsUnreadableWithoutViewDefinition. sys.database_principals
// is metadata-visibility filtered, so the count is not wrong-looking — it is a
// smaller number stated as fact. Measured on both test servers: 5 of
// HealthClinic's 8 users for a db_datareader.
func TestNumberOfUsersIsUnreadableWithoutViewDefinition(t *testing.T) {
	responses := append(databaseGeneralResponses("appuser"),
		capabilityResponses(true, nil, nil, nil, []string{"VIEW DEFINITION"})...)
	sc, inst := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	form, _ := loadPage(t, pageDatabaseGeneral(sc, genDatabase), inst)

	if got, _ := findStatic(form, "Number of users"); got != unreadableValue {
		t.Errorf("Number of users = %q, want %q — the login can only see some of them", got, unreadableValue)
	}

	// Granted, the count is shown. Without this the placeholder is
	// indistinguishable from having deleted the row.
	granted := append(databaseGeneralResponses("appuser"),
		capabilityResponses(true, nil, nil, []string{"VIEW DEFINITION"}, nil)...)
	okSC, okInst := newFakeConn(t, granted...)
	okSC.ProbeCapabilities()
	okForm, _ := loadPage(t, pageDatabaseGeneral(okSC, genDatabase), okInst)
	if got, _ := findStatic(okForm, "Number of users"); got != "1" {
		t.Errorf("Number of users = %q with VIEW DEFINITION, want the scripted count", got)
	}

	// And a connection that was never probed keeps today's behaviour. This is
	// the arm that separates the two gates: blanking on "not known to be
	// granted" also blanks here, and a sysadmin whose probe timed out would
	// read N/A for every value on every page.
	unprobed, unprobedInst := newFakeConn(t, databaseGeneralResponses("appuser")...)
	unprobedForm, _ := loadPage(t, pageDatabaseGeneral(unprobed, genDatabase), unprobedInst)
	if got, _ := findStatic(unprobedForm, "Number of users"); got != "1" {
		t.Errorf("Number of users = %q without a probe, want the count — an unknown right must fail open", got)
	}
}

// TestAFilteredPermissionsGridSaysSo. The grid's cells read "(none)" for a
// grant that exists but is invisible, which is a positive claim. There is no
// cell to blank, so the page carries the caveat.
func TestAFilteredPermissionsGridSaysSo(t *testing.T) {
	denied := append(serverPermissionsResponses(),
		capabilityResponses(true, nil, []string{"VIEW ANY DEFINITION"}, nil, nil)...)
	sc, inst := newFakeConn(t, denied...)
	sc.ProbeCapabilities()
	form, _ := loadPage(t, pageServerPermissions(sc), inst)
	if !hasNoteMentioning(form, "VIEW ANY DEFINITION") {
		t.Error("a login that cannot see every permission is not told so")
	}

	granted := append(serverPermissionsResponses(),
		capabilityResponses(true, []string{"VIEW ANY DEFINITION"}, nil, nil, nil)...)
	okSC, okInst := newFakeConn(t, granted...)
	okSC.ProbeCapabilities()
	okForm, _ := loadPage(t, pageServerPermissions(okSC), okInst)
	if hasNoteMentioning(okForm, "VIEW ANY DEFINITION") {
		t.Error("a login that can see everything is warned anyway")
	}
}

// serverPermissionsResponses scripts the three reads the server-scope
// Permissions page makes.
func serverPermissionsResponses() []fakeResponse {
	return []fakeResponse{
		{match: "sp.class_desc = 'SERVER'", cols: 5, rows: [][]driver.Value{
			{"appuser", "SQL_LOGIN", "sa", "ALTER ANY LOGIN", "GRANT"},
		}},
		loginListResponse(),
		serverRoleListResponse(),
	}
}

// rowText is a row's prose, for the note rows that have no label to be found
// by. Every other row kind answers with an empty string.
func rowText(r propsheet.Row) string {
	if t, ok := r.(interface{ Text() string }); ok {
		return t.Text()
	}
	return ""
}

// hasNoteMentioning reports whether any row on the page renders text
// containing want. Notes are the only unlabelled prose rows on these pages.
func hasNoteMentioning(f *propsheet.Form, want string) bool {
	for _, r := range f.Rows() {
		if strings.Contains(rowText(r), want) {
			return true
		}
	}
	return false
}
