package tui

import (
	"database/sql/driver"
	"time"
)

// Shared fixtures for the principal and ownership pages — Database Role,
// Server Role, Database User and Schema Properties, plus the Owned Schemas /
// Owned Roles transfer pages and Extended Properties.
//
// One database, appdb, with a deliberately tangled ownership graph: the role
// app_admin owns two other roles and two schemas, so every page here has more
// than one row to get wrong and none of the objects acted on is the first in
// its list. Every write is database-scoped (gosmo pins the connection with a
// USE), so they are read back with StatementsIn(principalDatabase) — except
// the server role pages, which have no database at all.

const (
	principalDatabase = "appdb"
	principalRole     = "app_admin"
	principalUser     = "appuser"
	principalSchema   = "sales"
	principalSrvRole  = "app_operators"
)

var principalEpoch = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

// databaseUsersResponse is the 7-column user list every page on these dialogs
// draws its principal picker from.
func databaseUsersResponse() fakeResponse {
	return fakeResponse{match: "FROM   sys.database_principals\nWHERE  type IN ('S','U','G')", cols: 7, rows: [][]driver.Value{
		{principalUser, int64(5), "SQL_USER", "dbo", principalEpoch, principalEpoch, "INSTANCE"},
		{"dbo", int64(1), "SQL_USER", "dbo", principalEpoch, principalEpoch, "INSTANCE"},
		{"reporting", int64(6), "SQL_USER", "dbo", principalEpoch, principalEpoch, "INSTANCE"},
	}}
}

// userByNameResponse answers the by-name user read, which carries the SID and
// the mapped login the list read leaves out.
func userByNameResponse(name, defaultSchema, login string) fakeResponse {
	return fakeResponse{match: "sp.sid = dp.sid", arg: name, cols: 9, rows: [][]driver.Value{
		{int64(5), "SQL_USER", defaultSchema, principalEpoch, principalEpoch, "INSTANCE", []byte{0x01, 0x02}, login, false},
	}}
}

// databaseRoleResponses answers both the role list and the by-name read. The
// by-name read goes first: the two queries differ only by a WHERE clause, so
// behind the list answer every lookup resolves to whichever role sorts first.
//
// app_admin owns audit_reader and report_reader; db_owner is fixed and public
// is not but is equally unwritable, so both must come back read-only.
func databaseRoleResponses() []fakeResponse {
	return []fakeResponse{
		{match: "WHERE  r.type = 'R' AND r.name = @p1", arg: principalRole, cols: 7, rows: [][]driver.Value{
			{int64(11), false, "dbo", []byte{0x0A}, principalEpoch, principalEpoch, principalUser},
		}},
		{match: "WHERE  r.type = 'R' AND r.name = @p1", arg: "db_owner", cols: 7, rows: [][]driver.Value{
			{int64(16384), true, "dbo", []byte{0x0B}, principalEpoch, principalEpoch, nil},
		}},
		{match: "FROM   sys.database_principals r", cols: 5, rows: [][]driver.Value{
			{principalRole, int64(11), false, "dbo", principalUser},
			{"audit_reader", int64(12), false, principalRole, nil},
			{"db_owner", int64(16384), true, "dbo", nil},
			{"public", int64(0), false, "dbo", nil},
			{"report_reader", int64(13), false, principalRole, "reporting"},
		}},
	}
}

// databaseSchemaResponses answers the schema list and the by-name read.
// app_admin owns archive and sales; dbo is a system schema and read-only.
func databaseSchemaResponses() []fakeResponse {
	return []fakeResponse{
		{match: "WHERE  s.name = @p1", arg: principalSchema, cols: 3, rows: [][]driver.Value{
			{principalSchema, int64(7), principalRole},
		}},
		{match: "WHERE  s.name = @p1", arg: "dbo", cols: 3, rows: [][]driver.Value{
			{"dbo", int64(1), "dbo"},
		}},
		{match: "FROM   sys.schemas s", cols: 3, rows: [][]driver.Value{
			{"archive", int64(8), principalRole},
			{"dbo", int64(1), "dbo"},
			{principalSchema, int64(7), principalRole},
			{"staging", int64(9), principalUser},
		}},
	}
}

// principalPermissionResponses answer the two permission reads the General
// pages count for their Summary section. Both are read-only on these pages —
// the editable permission matrices are permissions_pages_test.go's.
func principalPermissionResponses() []fakeResponse {
	return []fakeResponse{
		{match: "AND    dp.class_desc IN ('DATABASE','SCHEMA','OBJECT_OR_COLUMN')", cols: 6, rows: [][]driver.Value{
			{"SCHEMA", "SELECT", "GRANT", principalSchema, "", ""},
		}},
		{match: "dp.class_desc = 'SCHEMA' AND dp.major_id = SCHEMA_ID(@p1)", cols: 5, rows: [][]driver.Value{
			{"reporting", "SQL_USER", "dbo", "SELECT", "GRANT"},
		}},
	}
}

func schemaObjectCountResponses() []fakeResponse {
	return []fakeResponse{
		{match: "DECLARE @sid INT = SCHEMA_ID(@p1)", cols: 6, rows: [][]driver.Value{
			{int64(4), int64(2), int64(1), int64(0), int64(0), int64(0)},
		}},
		{match: "FROM sys.objects o WHERE o.schema_id = SCHEMA_ID(@p1)", cols: 1, rows: [][]driver.Value{
			{int64(7)},
		}},
	}
}

// serverRoleResponses is the server-side counterpart of
// databaseRoleResponses. The two by-name queries are told apart by their
// whitespace — gosmo indents the server one with tabs — which is fragile
// enough to be worth saying out loud: they are never scripted together.
func serverRoleResponses() []fakeResponse {
	return []fakeResponse{
		{match: "\tWHERE r.type = 'R' AND r.name = @p1", arg: principalSrvRole, cols: 7, rows: [][]driver.Value{
			{int64(20), false, "sa", []byte{0x0C}, principalEpoch, principalEpoch, "appuser"},
		}},
		{match: "\tWHERE r.type = 'R' AND r.name = @p1", arg: "sysadmin", cols: 7, rows: [][]driver.Value{
			{int64(3), true, "sa", []byte{0x0D}, principalEpoch, principalEpoch, nil},
		}},
		{match: "FROM sys.server_principals r", cols: 5, rows: [][]driver.Value{
			{principalSrvRole, int64(20), false, "sa", "appuser"},
			{"app_readers", int64(21), false, principalSrvRole, nil},
			{"app_writers", int64(22), false, principalSrvRole, nil},
			{"sysadmin", int64(3), true, "sa", nil},
		}},
	}
}

// serverRolePermissionsResponse is the explicit-permission read the Server
// Role General page counts for its Summary section.
func serverRolePermissionsResponse() fakeResponse {
	return fakeResponse{match: "sp.class_desc = 'SERVER'", cols: 5, rows: [][]driver.Value{
		{principalSrvRole, "SERVER_ROLE", "sa", "VIEW ANY DATABASE", "GRANT"},
	}}
}
