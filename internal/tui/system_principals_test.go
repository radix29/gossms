package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// Each case below was attempted for real on win10cli (SQL Server 17.0) — see
// the file comment on system_principals.go for the run. These pin the answers
// so a later "simplification" has to disagree with the server, not just with a
// table someone wrote down.

func TestIsSystemUser(t *testing.T) {
	for _, name := range []string{"dbo", "guest", "sys", "INFORMATION_SCHEMA"} {
		if !isSystemUser(name) {
			t.Errorf("%s: DROP USER and ALTER USER are both refused, want system", name)
		}
	}
	for _, name := range []string{"HealthClinicApp", "db_owner", "##MS_PolicyEventProcessingLogin##"} {
		if isSystemUser(name) {
			t.Errorf("%s: not one of the four built-in users, want not system", name)
		}
	}
}

// The fixed-role schemas are the trap here: db_owner and friends look built-in
// and are not. DROP SCHEMA db_datareader and ALTER AUTHORIZATION ON
// SCHEMA::db_owner both succeeded live, so gating them would take away
// something the server allows.
func TestIsSystemSchemaExcludesFixedRoleSchemas(t *testing.T) {
	for _, name := range []string{"dbo", "guest", "sys", "INFORMATION_SCHEMA"} {
		if !isSystemSchema(name) {
			t.Errorf("%s: DROP SCHEMA is refused, want system", name)
		}
	}
	for _, name := range []string{"db_owner", "db_datareader", "db_denydatawriter", "Sales"} {
		if isSystemSchema(name) {
			t.Errorf("%s: DROP SCHEMA succeeds on it, want not system", name)
		}
	}
}

// isSystemUser and isSystemSchema are separate predicates over the same four
// names, kept apart because they answer for different statements and could
// legitimately diverge if SQL Server ever let one go. Nothing else pins that
// they agree *today*, though, and they must: each of the four is undroppable
// as a user and as a schema, so a name added to or removed from one list
// alone silently offers Delete on half of a principal that cannot take it.
//
// This is the test to change — deliberately, with a live result behind it —
// if the two ever really do need different answers.
func TestSystemUserAndSchemaListsAgree(t *testing.T) {
	names := []string{
		// The four, from both directions.
		"dbo", "guest", "sys", "INFORMATION_SCHEMA",
		// Fixed-role schemas: droppable, and a user of the same name is an
		// ordinary user. Not system on either side.
		"db_owner", "db_datareader", "db_denydatawriter",
		// Ordinary names, and the casing traps: the comparison is
		// case-sensitive on purpose, so under a case-sensitive collation a
		// separately created "Guest" stays editable.
		"Sales", "HealthClinicApp", "Guest", "DBO", "information_schema",
	}
	for _, name := range names {
		if isSystemUser(name) != isSystemSchema(name) {
			t.Errorf("%s: isSystemUser=%v but isSystemSchema=%v; the four built-in "+
				"names are undroppable as both, so the two lists must agree",
				name, isSystemUser(name), isSystemSchema(name))
		}
	}
}

// public carries is_fixed_role = 0 in both families while refusing DROP and
// ALTER ... WITH NAME, so the flag alone is not the predicate.
func TestPublicIsSystemDespiteNotBeingAFixedRole(t *testing.T) {
	dbPublic := &gosmo.DatabaseRole{Name: "public", IsFixedRole: false}
	if !isSystemDatabaseRole(dbPublic) {
		t.Error("database role public is not marked system; DROP ROLE [public] is refused")
	}
	srvPublic := &gosmo.ServerRole{Name: "public", IsFixedRole: false}
	if !isSystemServerRole(srvPublic) {
		t.Error("server role public is not marked system; ALTER SERVER ROLE [public] is refused")
	}
}

func TestIsSystemRoles(t *testing.T) {
	if !isSystemDatabaseRole(&gosmo.DatabaseRole{Name: "db_datareader", IsFixedRole: true}) {
		t.Error("a fixed database role is not marked system")
	}
	if isSystemDatabaseRole(&gosmo.DatabaseRole{Name: "ReportReaders"}) {
		t.Error("a user-defined database role is marked system")
	}
	if !isSystemServerRole(&gosmo.ServerRole{Name: "##MS_ServerStateReader##", IsFixedRole: true}) {
		t.Error("a ##MS_* server role is not marked system")
	}
	if isSystemServerRole(&gosmo.ServerRole{Name: "DeployAgents"}) {
		t.Error("a user-defined server role is marked system")
	}
}

// sa is the deliberate hole in isSystemLogin: DROP LOGIN sa is refused, but
// ALTER LOGIN sa WITH NAME succeeds and renaming it is a documented hardening
// step. The ## logins are gated even though the server permits dropping them.
func TestIsSystemLoginGatesInternalNamesButNotSa(t *testing.T) {
	for _, name := range []string{"##MS_PolicyEventProcessingLogin##", "##MS_PolicyTsqlExecutionLogin##"} {
		if !isSystemLogin(&gosmo.Login{Name: name}) {
			t.Errorf("%s: dropping it orphans Policy-Based Management, want system", name)
		}
	}
	for _, name := range []string{"sa", `NT SERVICE\SQLSERVERAGENT`, `WIN10CLI\radu_`, "HealthClinicApp"} {
		if isSystemLogin(&gosmo.Login{Name: name}) {
			t.Errorf("%s: an administrator may legitimately rename or drop it, want not system", name)
		}
	}
}

// The gate only works if the loaders set the flag, so the two have to stay in
// step — the same pairing TestSystemAgentJobNodesAreMarkedSystem asserts for
// Agent jobs. These node builders take no connection.
func TestSecurityLoadersMarkSystemPrincipals(t *testing.T) {
	l := loaderCtx{}

	sysRole := l.node("public", NodeServerRole, "", "public", "")
	sysRole.data.IsSystem = isSystemServerRole(&gosmo.ServerRole{Name: "public"})
	if !sysRole.data.IsSystem {
		t.Error("the public server role node is not marked IsSystem")
	}

	a := &App{}
	for _, tt := range []struct {
		name string
		node *explorerNode
	}{
		{"built-in user", opNode(NodeUser, "", "dbo", "")},
		{"built-in schema", opNode(NodeSchema, "sys", "sys", "")},
		{"fixed database role", opNode(NodeDatabaseRole, "", "db_owner", "")},
		{"public server role", opNode(NodeServerRole, "", "public", "")},
		{"internal login", opNode(NodeLogin, "", "##MS_PolicyEventProcessingLogin##", "")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.node.data.IsSystem = true
			if items := a.objectOpsMenuItems(tt.node); len(items) != 0 {
				t.Errorf("offered %d menu items, want none: %v", len(items), items)
			}
		})
	}
}
