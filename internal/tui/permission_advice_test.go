package tui

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// mssqlMsg is one message of a failed batch: number, class, text.
type mssqlMsg struct {
	number int32
	class  uint8
	text   string
}

// sqlErrOf builds the driver error a batch that sent these messages produces.
// database/sql surfaces only the last, which is exactly the problem the
// classifier exists to work around.
func sqlErrOf(msgs []mssqlMsg) error {
	all := make([]mssql.Error, len(msgs))
	for i, m := range msgs {
		all[i] = mssql.Error{Number: m.number, Class: m.class, Message: m.text}
	}
	last := all[len(all)-1]
	last.All = all
	return last
}

// wrapErr is the %w wrapping gosmo puts around a driver error, so a test acts
// on the shape the application actually holds rather than a bare mssql.Error.
func wrapErr(context string, err error) error {
	return fmt.Errorf("%s: %w", context, err)
}

// sysadminCapabilityResponses scripts a probe whose answer is "sysadmin",
// which capabilityResponses cannot express — it writes permission rows only,
// and a role test reads the 'R' ones.
func sysadminCapabilityResponses() []fakeResponse {
	r := capabilityResponses(true, nil, nil, nil, nil)
	for i := range r {
		if r[i].match == "IS_SRVROLEMEMBER" {
			r[i].rows = [][]driver.Value{{"R", "sysadmin", int64(1)}}
		}
	}
	return r
}

// Every message below was captured from win10cli (SQL Server 17.0.1125.2) as
// a least-privileged login, not written from documentation. They are the whole
// reason this table exists:
// the wording differs between numbers in ways no amount of reasoning predicts
// ("permission was denied on the object" vs "permission denied in database").
var measuredRefusals = []struct {
	name    string
	msgs    []mssqlMsg
	want    string // the advice
	certain bool
}{
	{
		name: "SELECT on an object",
		msgs: []mssqlMsg{{229, 14, "The SELECT permission was denied on the object 'sysjobservers', database 'msdb', schema 'dbo'."}},
		want: "Requires SELECT on msdb.dbo.sysjobservers.", certain: true,
	},
	{
		name: "EXECUTE on a system procedure",
		msgs: []mssqlMsg{{229, 14, "The EXECUTE permission was denied on the object 'xp_readerrorlog', database 'mssqlsystemresource', schema 'sys'."}},
		want: "Requires EXECUTE on mssqlsystemresource.sys.xp_readerrorlog.", certain: true,
	},
	{
		name: "CREATE TABLE",
		msgs: []mssqlMsg{{262, 14, "CREATE TABLE permission denied in database 'HealthClinic'."}},
		want: "Requires CREATE TABLE in HealthClinic.", certain: true,
	},
	{
		// The first message of a refused BACKUP; the second is the contentless
		// Msg 3013 "BACKUP DATABASE is terminating abnormally", which is all
		// database/sql surfaces.
		name: "BACKUP",
		msgs: []mssqlMsg{
			{262, 14, "BACKUP DATABASE permission denied in database 'HealthClinic'."},
			{3013, 16, "BACKUP DATABASE is terminating abnormally."},
		},
		want: "Requires BACKUP DATABASE in HealthClinic.", certain: true,
	},
	{
		name: "a refused DMV read",
		msgs: []mssqlMsg{
			{300, 14, "VIEW SERVER PERFORMANCE STATE permission was denied on object 'server', database 'master'."},
			{297, 16, "The user does not have permission to perform this action."},
		},
		want: "Requires VIEW SERVER PERFORMANCE STATE.", certain: true,
	},
	{
		name: "no access to the database",
		msgs: []mssqlMsg{{916, 14, `The server principal "user_dr" is not able to access the database "backup_test" under the current security context.`}},
		want: "Requires CONNECT on backup_test.", certain: true,
	},
	{
		name: "ALTER DATABASE",
		msgs: []mssqlMsg{
			{5011, 14, "User does not have permission to alter database 'HealthClinic', the database does not exist, or the database is not in a state that allows access checks."},
			{5069, 16, "ALTER DATABASE statement failed."},
		},
		want: "HealthClinic does not exist, or this login needs ALTER (db_owner) on it.",
	},
	{
		name: "ALTER LOGIN",
		msgs: []mssqlMsg{{15151, 14, "Cannot alter the login 'user_dbo', because it does not exist or you do not have permission."}},
		want: "The login user_dbo does not exist, or this login cannot alter it.",
	},
	{
		// Msg 297 on its own, with no Msg 300 ahead of it — what a refused KILL
		// and several system procedures raise. Its text names nothing, so there
		// is no right to derive from it.
		name: "a contentless refusal on its own",
		msgs: []mssqlMsg{{297, 16, "The user does not have permission to perform this action."}},
		want: "", certain: true,
	},
	{
		name: "DROP TABLE",
		msgs: []mssqlMsg{{3701, 14, "Cannot drop the table 'Patients', because it does not exist or you do not have permission."}},
		want: "The table Patients does not exist, or this login cannot drop it.",
	},
	{
		// Captured 2026-08-27 from a login holding database-wide ALTER but
		// denied it on the one table — the mismatch that reaches these paths
		// live. Msg 1088's wording differs from 3701/15151 in three ways at
		// once: double quotes, no comma before "because", and "permissions"
		// plural, which is why it needs a pattern of its own.
		name: "ALTER TABLE on a denied object",
		msgs: []mssqlMsg{{1088, 16, `Cannot find the object "Invoices" because it does not exist or you do not have permissions.`}},
		want: "The object Invoices does not exist, or this login has no permission on it.",
	},
	{
		// The same number from CREATE INDEX, where the name arrives
		// schema-qualified. Different State, same sentence shape.
		name: "CREATE INDEX on a denied object",
		msgs: []mssqlMsg{{1088, 16, `Cannot find the object "dbo.Invoices" because it does not exist or you do not have permissions.`}},
		want: "The object dbo.Invoices does not exist, or this login has no permission on it.",
	},
}

// TestEveryMeasuredRefusalYieldsTheRightItNeeds.
func TestEveryMeasuredRefusalYieldsTheRightItNeeds(t *testing.T) {
	for _, tt := range measuredRefusals {
		t.Run(tt.name, func(t *testing.T) {
			err := sqlErrOf(tt.msgs)
			if got := classifyRefusal(err).advice(); got != tt.want {
				t.Errorf("advice =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

// TestAnAmbiguousRefusalIsNeverCalledAccessDenied. SQL Server refuses to say
// whether the object is missing or merely invisible — telling the two apart
// would let an unprivileged login enumerate what it cannot see — so neither may
// the UI. "Access denied" on Msg 3701 is a claim the server did not make, and
// it sends someone hunting a permission for a table that was dropped last week.
func TestAnAmbiguousRefusalIsNeverCalledAccessDenied(t *testing.T) {
	for _, tt := range measuredRefusals {
		t.Run(tt.name, func(t *testing.T) {
			text := accessDeniedText(sqlErrOf(tt.msgs))
			if text == "" {
				t.Fatal("not recognised as a refusal at all")
			}
			switch {
			case tt.certain && !strings.HasPrefix(text, accessDeniedLabel):
				t.Errorf("text = %q, want the access-denied prefix", text)
			case !tt.certain && strings.HasPrefix(text, accessDeniedLabel):
				t.Errorf("text = %q claims access denied for a message that says "+
					"\"does not exist or you do not have permission\"", text)
			}
			if !tt.certain && !strings.Contains(text, "does not exist") {
				t.Errorf("text = %q dropped the half of the answer that is not about permissions", text)
			}
		})
	}
}

// TestAContentlessRefusalNamesNoRight. Msg 297 says nothing about which right
// is missing, and it is not only the follow-up to Msg 300 — a refused KILL,
// sp_readerrorlog and several other procedures raise it alone. It once
// answered "Requires VIEW SERVER STATE (sysadmin).", a right the server never
// named and, for a KILL, the wrong one to go and ask for.
func TestAContentlessRefusalNamesNoRight(t *testing.T) {
	const text = "The user does not have permission to perform this action."
	err := sqlErrOf([]mssqlMsg{{297, 16, text}})

	if got := classifyRefusal(err).advice(); got != "" {
		t.Errorf("advice = %q, want none — Msg 297 names no right", got)
	}
	// Still a refusal, and still shown as one: only the invented right goes.
	if got := accessDeniedText(err); got != accessDeniedLabel+text {
		t.Errorf("accessDeniedText = %q, want the server's own sentence", got)
	}

	// The pairing 297 does appear in must still name the right — which comes
	// from the Msg 300 ahead of it, and never from 297 itself.
	pair := sqlErrOf([]mssqlMsg{
		{300, 14, "VIEW SERVER PERFORMANCE STATE permission was denied on object 'server', database 'master'."},
		{297, 16, text},
	})
	if got, want := classifyRefusal(pair).advice(), "Requires VIEW SERVER PERFORMANCE STATE."; got != want {
		t.Errorf("advice = %q, want %q", got, want)
	}
}

// TestUnreadableWordingFallsBackToTheServersOwn. The numbers are not localized
// and the wording is, so a message this build cannot parse must keep the
// server's sentence rather than lose it — and must never be reported as an
// unrecognised failure, because the number still says it was a refusal.
func TestUnreadableWordingFallsBackToTheServersOwn(t *testing.T) {
	const german = "Die SELECT-Berechtigung wurde für das Objekt 'sysjobservers' verweigert."
	err := sqlErrOf([]mssqlMsg{{229, 14, german}})

	if got := classifyRefusal(err).advice(); got != "" {
		t.Errorf("advice = %q, want none — the wording could not be read", got)
	}
	if got := accessDeniedText(err); got != accessDeniedLabel+german {
		t.Errorf("accessDeniedText = %q, want the server's own sentence", got)
	}
}

// TestWithPermissionAdviceAddsToTheFailureRatherThanReplacingIt. The user
// asked for something and it did not happen; the full failure is what they are
// owed, and the advice is what they do next.
func TestWithPermissionAdviceAddsToTheFailureRatherThanReplacingIt(t *testing.T) {
	inner := sqlErrOf([]mssqlMsg{
		{5011, 14, "User does not have permission to alter database 'HealthClinic', the database does not exist, or the database is not in a state that allows access checks."},
		{5069, 16, "ALTER DATABASE statement failed."},
	})
	wrapped := wrapErr("gosmo: set recovery model", inner)

	got := withPermissionAdvice(wrapped).Error()
	if !strings.Contains(got, "set recovery model") {
		t.Errorf("advice replaced the failure: %q", got)
	}
	if !strings.Contains(got, "ALTER (db_owner)") {
		t.Errorf("advice not appended: %q", got)
	}

	// Idempotent: reporting the same error twice must not stack the sentence.
	if once, twice := got, withPermissionAdvice(withPermissionAdvice(wrapped)).Error(); once != twice {
		t.Errorf("advice appended twice:\n  %q\n  %q", once, twice)
	}

	// And an unrelated failure is returned untouched.
	plain := errors.New("read tcp: connection reset by peer")
	if withPermissionAdvice(plain).Error() != plain.Error() {
		t.Error("an unrelated failure was rewritten")
	}
	if withPermissionAdvice(nil) != nil {
		t.Error("nil did not stay nil")
	}
}

// -- the silent-empty guard --------------------------------------------------

// TestLegacyListingRefusalIsClaimedOnlyWhenAllThreeFactsHold. xp_dirtree
// returns no rows and no error to a non-sysadmin, so the refusal has to be
// inferred — and every one of the three facts is needed. Getting the role test
// wrong is the dangerous one: Capabilities answers "not a member" for a role it
// was never asked about, so without Probed() every unprobed connection reports
// an empty directory as a permissions problem.
func TestLegacyListingRefusalIsClaimedOnlyWhenAllThreeFactsHold(t *testing.T) {
	legacyDenied := func(t *testing.T) *db.ServerConn {
		sc, _ := newFakeConnAtVersion(t, "13.0.5026.0", capabilityResponses(true, nil, nil, nil, nil)...)
		sc.ProbeCapabilities()
		return sc
	}

	if err := legacyListingRefusal(legacyDenied(t), nil); err == nil {
		t.Error("an empty pre-2017 listing for a non-sysadmin was reported as an empty directory")
	} else if !strings.Contains(err.Error(), "sysadmin") {
		t.Errorf("error = %q, want the right named", err)
	}

	// A directory that really has something in it.
	if err := legacyListingRefusal(legacyDenied(t), []*gosmo.FileSystemEntry{{Name: "backup.bak"}}); err != nil {
		t.Errorf("a listing with rows was reported as a refusal: %v", err)
	}

	// 2017 and later never take that path, so an empty listing means empty.
	modern, _ := newFakeConnAtVersion(t, "16.0.4085.2", capabilityResponses(true, nil, nil, nil, nil)...)
	modern.ProbeCapabilities()
	if err := legacyListingRefusal(modern, nil); err != nil {
		t.Errorf("a modern instance claimed the xp_dirtree refusal: %v", err)
	}

	// Never probed: nothing measured, so nothing claimed.
	unprobed, _ := newFakeConnAtVersion(t, "13.0.5026.0")
	if err := legacyListingRefusal(unprobed, nil); err != nil {
		t.Errorf("an unprobed connection claimed the refusal: %v", err)
	}

	// A sysadmin's empty listing is an empty directory.
	admin, _ := newFakeConnAtVersion(t, "13.0.5026.0", sysadminCapabilityResponses()...)
	admin.ProbeCapabilities()
	if err := legacyListingRefusal(admin, nil); err != nil {
		t.Errorf("a sysadmin's empty directory was reported as a refusal: %v", err)
	}
}
