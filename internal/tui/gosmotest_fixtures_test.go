package tui

import (
	"database/sql/driver"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// Fixtures for the gosmotest-backed server (gosmotest_bridge_test.go): the
// fakeResponse DSL that scripts it and the capability-probe builders.
//
// What this is not: a SQL Server. Queries are matched by substring and
// answered with whatever the test scripted, so a test here proves the page
// asked for the right things and wrote the right statement — never that the
// statement is valid T-SQL, and never that the server would accept it.
// Statement correctness is gosmo's own tests and live runs against win10cli.
// An assertion here that reaches for server semantics is asserting the fake.

// fakeResponse is one scripted answer: any query containing match is answered
// with these rows, each of which must have exactly cols values.
type fakeResponse struct {
	match string
	cols  int
	rows  [][]driver.Value

	// db, if set, restricts this answer to a connection currently pinned to
	// that database by a USE — the only way to give two databases different
	// answers to the identical per-database query gosmo runs in each of them
	// (a login's mappings, a database's roles, its tables). Without it every
	// database on a page looks the same, which is exactly the misalignment
	// these tests exist to catch.
	db string

	// arg, if set, restricts this answer to a query one of whose parameters
	// is that string. Substring matching alone cannot tell a by-name read
	// from the list read it is a WHERE clause away from — DatabaseByName's
	// query contains "FROM sys.databases" too, so the list answer served it
	// and every database on the page resolved to whichever row sorted first.
	// An integer parameter matches its decimal form, for a read by id.
	arg string

	// colNames, if set, names the result's columns — for a read that scans by
	// column *name* rather than by position (RESTORE HEADERONLY and
	// FILELISTONLY, through gosmo's namedRow). Unnamed columns leave such a
	// read scanning nothing and reporting a grid of zero values, which looks
	// like a page that read the wrong thing. It must have exactly cols entries.
	colNames []string

	// err, if set, fails a matching query instead of answering it — the seam
	// for a read that partly succeeds. A report that fetches per object (the
	// Agent reports' one round trip per job) has an arm for the object whose
	// fetch failed, and it is reachable no other way: a live server either
	// answers every job or none. Scope it with arg so exactly one object
	// fails; a bare match fails them all and proves nothing about the arm.
	//
	// It fails a matching *write* too, which is how an apply that commits one
	// statement and fails the next is reached — the Audit pages' disable
	// window, where the settings ALTER lands and the re-enable is refused.
	// Only a response carrying an err is consulted for a write, so an answer
	// meant for a read cannot fail an exec that happens to contain its match.
	err error

	// dropsConn, with err, also leaves the connection that ran the failing
	// write invalid (driver.Validator), the way go-mssqldb reports a session
	// killed or a link dropped mid-statement — the seam for query.Session
	// finding itself lost.
	dropsConn bool

	// block holds a matching query inside the driver until it is closed, so a
	// test can act while the read is genuinely in flight. Without it this
	// driver answers instantly and every read has finished — and been
	// cancelled by its own defer — before the test regains control.
	block chan struct{}

	// blockExec holds a matching *write* inside the driver until it is closed
	// or the statement's context is cancelled — the latter answered with the
	// context's error, the way a real driver answers a cancelled statement. It
	// is how a test cancels a write genuinely in flight (the progress dialog's
	// Cancel); block is not consulted for writes, so a read answer's block
	// cannot hold an exec that happens to contain its match.
	blockExec chan struct{}
}

// capabilityResponses scripts gosmo's server- and database-scope capability
// probes, so a test can drive a page that asks what the login may do.
//
// granted/denied name server permissions; everything not named reads back as
// CapabilityUnknown, which is the fail-open answer. dbGranted/dbDenied do the
// same one scope down, and dbAccessible is HAS_DBACCESS.
//
// A page under test that gates on a capability sees nothing at all unless the
// ServerConn was probed — call ProbeCapabilities after newGConn, or the
// gate silently fails open and the test passes for the wrong reason.
func capabilityResponses(dbAccessible bool, granted, denied, dbGranted, dbDenied []string) []fakeResponse {
	access := int64(0)
	if dbAccessible {
		access = 1
	}
	answer := func(granted, denied []string) [][]driver.Value {
		var rows [][]driver.Value
		for _, n := range granted {
			rows = append(rows, []driver.Value{"P", n, int64(1)})
		}
		for _, n := range denied {
			rows = append(rows, []driver.Value{"P", n, int64(0)})
		}
		return rows
	}
	return []fakeResponse{
		{match: "HAS_DBACCESS", cols: 1, rows: [][]driver.Value{{access}}},
		{match: "IS_SRVROLEMEMBER", cols: 3, rows: answer(granted, denied)},
		{match: "IS_ROLEMEMBER", cols: 3, rows: answer(dbGranted, dbDenied)},
	}
}

// capabilityResponsesWithRoles is capabilityResponses plus the fixed database
// *role* half of the same probe. Roles come back tagged "R" where a permission
// is tagged "P" — the two travel in one query, and the membership rights SQL
// Agent gates on read only the "R" rows.
func capabilityResponsesWithRoles(dbAccessible bool, granted, denied, roleIn, roleNotIn []string) []fakeResponse {
	out := capabilityResponses(dbAccessible, granted, denied, nil, nil)
	for i, r := range out {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range roleIn {
			r.rows = append(r.rows, []driver.Value{"R", n, int64(1)})
		}
		for _, n := range roleNotIn {
			r.rows = append(r.rows, []driver.Value{"R", n, int64(0)})
		}
		out[i] = r
	}
	return out
}

// capabilityResponsesWithSchemas is capabilityResponses plus the per-schema
// block of the same database probe: gosmo asks for the schemas in the same
// query as the roles and permissions, tagging each row with the permission and
// naming the schema — see gosmo's schemaCapabilityQuery.
func capabilityResponsesWithSchemas(dbAccessible bool, granted, denied, dbGranted, dbDenied, schemaGranted, schemaDenied []string) []fakeResponse {
	out := capabilityResponses(dbAccessible, granted, denied, dbGranted, dbDenied)
	for i, r := range out {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range schemaGranted {
			r.rows = append(r.rows, []driver.Value{"S:ALTER", n, int64(1)})
		}
		for _, n := range schemaDenied {
			r.rows = append(r.rows, []driver.Value{"S:ALTER", n, int64(0)})
		}
		out[i] = r
	}
	return out
}

// probeKey turns a fixture's "schema.object" or "schema.object.column" into
// the key gosmo's probe records it under — gosmo.ObjectKey or
// gosmo.ColumnKey, whose parts are joined by a separator no name can contain,
// not by a dot. A fixture name has no dot of its own.
func probeKey(dotted string) string {
	switch p := strings.Split(dotted, "."); len(p) {
	case 2:
		return gosmo.ObjectKey(p[0], p[1])
	case 3:
		return gosmo.ColumnKey(p[0], p[1], p[2])
	}
	return dotted
}

// capabilityResponsesWithObjects is capabilityResponses plus the OBJECT-scope
// block of the same database probe. Objects come back tagged "O:<permission>"
// with the securable named "schema.object" here and keyed by probeKey — see
// gosmo's objectCapabilityQuery.
//
// Only granted, denied and owned objects appear. The map is sparse by design,
// which is the property the gate has to be right about, so a test naming an
// object here and nothing else is the realistic shape.
func capabilityResponsesWithObjects(dbAccessible bool, dbDenied, schemaDenied, objGranted, objDenied []string) []fakeResponse {
	out := capabilityResponsesWithSchemas(dbAccessible, nil, nil, nil, dbDenied, nil, schemaDenied)
	for i, r := range out {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range objGranted {
			r.rows = append(r.rows, []driver.Value{"O:ALTER", probeKey(n), int64(1)})
		}
		for _, n := range objDenied {
			r.rows = append(r.rows, []driver.Value{"O:ALTER", probeKey(n), int64(0)})
		}
		out[i] = r
	}
	return out
}

// withDeniedColumns adds a column-scope DENY of ALTER to a capability script,
// naming each column as "schema.object.column", keyed by probeKey as gosmo's
// probe records them. The rows ride on the database probe beside the object-scope
// ones, which is how the server returns them.
func withDeniedColumns(responses []fakeResponse, columns ...string) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range columns {
			r.rows = append(r.rows, []driver.Value{"C:ALTER", probeKey(n), int64(0)})
		}
		responses[i] = r
	}
	return responses
}

// withDeniedSchemas adds a schema-scope DENY of ALTER to a capability script,
// naming each schema as gosmo's catalog block records it — tagged "E:", the
// rows read out of sys.database_permissions rather than the "S:" rows
// HAS_PERMS_BY_NAME answers. The two are not interchangeable: an "S:" 0 is what
// a schema simply never granted anything on also produces.
func withDeniedSchemas(responses []fakeResponse, schemas ...string) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range schemas {
			r.rows = append(r.rows, []driver.Value{"E:ALTER", n, int64(0)})
		}
		responses[i] = r
	}
	return responses
}

// withSecurableAnswers adds gosmo's class 5/6/10 block to a capability script:
// the HAS_PERMS_BY_NAME answer for CONTROL on each securable, keyed as
// gosmo.DatabaseSecurableKey spells it and tagged "K:CONTROL" — see gosmo's
// securableCapabilityQuery. true is 1, false is 0. The map is not sparse in a
// real probe, so a test gating on it should answer every securable it asks
// about; one it leaves out reads unknown and fails open.
func withSecurableAnswers(responses []fakeResponse, answers map[string]bool) []fakeResponse {
	return withSecurablePermAnswers(responses, "CONTROL", answers)
}

// withSecurablePermAnswers is withSecurableAnswers for a probed permission
// other than CONTROL.
func withSecurablePermAnswers(responses []fakeResponse, perm string, answers map[string]bool) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for key, held := range answers {
			v := int64(0)
			if held {
				v = 1
			}
			r.rows = append(r.rows, []driver.Value{"K:" + perm, key, v})
		}
		responses[i] = r
	}
	return responses
}

// withDatabaseDenials adds a database-scope (class 0) DENY of each permission
// to a capability script. The rows ride on the database probe beside the
// schema- and object-scope ones, tagged "D:" with the database as the name,
// which is how gosmo's explicitDatabaseCapabilityQuery returns them.
//
// It is deliberately separate from capabilityResponsesWithSchemas's dbDenied
// argument: that one makes HAS_PERMS_BY_NAME answer 0, which means "does not
// hold" and is what a principal working through a narrower grant reads
// anyway. Only these rows say a DENY exists.
func withDatabaseDenials(responses []fakeResponse, perms ...string) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range perms {
			r.rows = append(r.rows, []driver.Value{"D:" + n, "appdb", int64(0)})
		}
		responses[i] = r
	}
	return responses
}

// withPrincipalDenials adds a DATABASE_PRINCIPAL-scope (class 4) DENY of perm
// on each named principal to a capability script. The rows ride on the database
// probe tagged "N:" with the principal as the name, which is how gosmo's
// explicitPrincipalCapabilityQuery returns them.
//
// Only the DENY direction is scriptable here because it is the only one that
// exists: a GRANT ALTER ON USER::x permits neither the rename nor the drop —
// both need ALTER ANY USER at database scope — so gosmo's block selects DENY
// rows alone. See gosmo.ProbedPrincipalPermissions.
// withServerDenials is withPrincipalDenials at server scope: it appends the
// "V:" rows gosmo's server-scope catalog block returns, one per securable, to
// the *server* probe's answer rather than the database one.
//
// The key is ServerSecurableKey's, kind and all, because that is what the
// probe builds and what DeniedOnServerSecurable looks up — a row filed under
// the bare name reads back as no denial at all.
func withServerDenials(responses []fakeResponse, kind gosmo.ServerSecurableKind, perm string, names ...string) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_SRVROLEMEMBER" {
			continue
		}
		for _, n := range names {
			r.rows = append(r.rows, []driver.Value{"V:" + perm, gosmo.ServerSecurableKey(kind, n), int64(0)})
		}
		responses[i] = r
	}
	return responses
}

// withAGAnswers appends the "G:" rows gosmo's availability-group block returns
// to the server probe's answer — HAS_PERMS_BY_NAME answers, not catalog rows,
// so a group must be listed as permitted as well as denied: the block is not
// sparse, and a group with no row means "the probe did not run".
func withAGAnswers(responses []fakeResponse, permitted, denied []string) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_SRVROLEMEMBER" {
			continue
		}
		for _, n := range permitted {
			r.rows = append(r.rows, []driver.Value{"G:ALTER", n, int64(1)})
		}
		for _, n := range denied {
			r.rows = append(r.rows, []driver.Value{"G:ALTER", n, int64(0)})
		}
		responses[i] = r
	}
	return responses
}

func withPrincipalDenials(responses []fakeResponse, perm string, principals ...string) []fakeResponse {
	for i, r := range responses {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for _, n := range principals {
			r.rows = append(r.rows, []driver.Value{"N:" + perm, n, int64(0)})
		}
		responses[i] = r
	}
	return responses
}
