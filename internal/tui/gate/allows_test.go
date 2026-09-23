package gate

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// allows_test.go tests the rule itself over hand-built capability sets, so a
// gate regression fails in the package that owns it rather than only through
// internal/tui's menus.

const (
	granted = gosmo.CapabilityGranted
	denied  = gosmo.CapabilityDenied
)

// probedServer is a server answer for a login in no fixed role, holding
// exactly perms.
func probedServer(perms map[string]gosmo.CapabilityState) *gosmo.Capabilities {
	return &gosmo.Capabilities{ServerRoles: map[string]bool{}, ServerPermissions: perms}
}

// sysadminServer is a server answer for a member of sysadmin.
func sysadminServer() *gosmo.Capabilities {
	return &gosmo.Capabilities{ServerRoles: map[string]bool{"sysadmin": true}}
}

// probedDB is an accessible database's answer with no roles and no rights;
// edit adjusts it.
func probedDB(edit func(*gosmo.DatabaseCapabilities)) *gosmo.DatabaseCapabilities {
	c := &gosmo.DatabaseCapabilities{
		Accessible:  true,
		Roles:       map[string]bool{},
		Permissions: map[string]gosmo.CapabilityState{},
	}
	if edit != nil {
		edit(c)
	}
	return c
}

// only answers c for every database, the shape of a test with one database.
func only(c *gosmo.DatabaseCapabilities) func(string) *gosmo.DatabaseCapabilities {
	return func(string) *gosmo.DatabaseCapabilities { return c }
}

// unprobedDB answers the way CachedDatabaseCapabilities does for a database
// never asked: accessible, nothing known.
func unprobedDB(string) *gosmo.DatabaseCapabilities {
	return &gosmo.DatabaseCapabilities{Accessible: true}
}

func TestRightsAllow(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		server                 *gosmo.Capabilities
		dbCaps                 func(string) *gosmo.DatabaseCapabilities
		dbName, schema, object string
		rights                 []Right
		want                   bool
	}{
		// Fail-open: nothing known withholds nothing.
		{name: "nil server set allows a server right",
			server: nil, dbCaps: unprobedDB, rights: []Right{AlterSettings}, want: true},
		{name: "unprobed database allows a database right",
			server: probedServer(nil), dbCaps: unprobedDB, dbName: "d", rights: []Right{BackupDatabase}, want: true},
		{name: "a permission the instance never answered allows",
			server: probedServer(map[string]gosmo.CapabilityState{}), dbCaps: unprobedDB,
			rights: []Right{AlterSettings}, want: true},

		// Server scope.
		{name: "server right denied withholds",
			server: probedServer(map[string]gosmo.CapabilityState{"ALTER SETTINGS": denied}), dbCaps: unprobedDB,
			rights: []Right{AlterSettings}, want: false},
		{name: "server right granted allows",
			server: probedServer(map[string]gosmo.CapabilityState{"ALTER SETTINGS": granted}), dbCaps: unprobedDB,
			rights: []Right{AlterSettings}, want: true},
		{name: "any one of several rights allows",
			server: probedServer(map[string]gosmo.CapabilityState{"ALTER SETTINGS": denied, "CONTROL SERVER": granted}),
			dbCaps: unprobedDB, rights: []Right{AlterSettings, ControlServer}, want: true},
		{name: "a server alternate answers for its right",
			server: probedServer(map[string]gosmo.CapabilityState{
				"VIEW SERVER STATE": denied, "VIEW SERVER PERFORMANCE STATE": granted}),
			dbCaps: unprobedDB, rights: []Right{ViewServerState}, want: true},
		{name: "every alternate denied withholds",
			server: probedServer(map[string]gosmo.CapabilityState{
				"VIEW SERVER STATE": denied, "VIEW SERVER PERFORMANCE STATE": denied, "VIEW SERVER SECURITY STATE": denied}),
			dbCaps: unprobedDB, rights: []Right{ViewServerState}, want: false},

		// Database scope.
		{name: "database right denied withholds",
			server: probedServer(nil), dbName: "d", rights: []Right{BackupDatabase}, want: false,
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) { c.Permissions["BACKUP DATABASE"] = denied }))},
		{name: "database right granted allows",
			server: probedServer(nil), dbName: "d", rights: []Right{BackupDatabase}, want: true,
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) { c.Permissions["BACKUP DATABASE"] = granted }))},
		{name: "inaccessible database withholds though its answer is unknown",
			server: probedServer(nil), dbName: "d", rights: []Right{BackupDatabase}, want: false,
			dbCaps: only(&gosmo.DatabaseCapabilities{Accessible: false})},
		{name: "a database right with no database to ask allows",
			server: probedServer(nil), dbName: "", rights: []Right{BackupDatabase}, want: true,
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) { c.Permissions["BACKUP DATABASE"] = denied }))},
		{name: "a database alternate answers for its right",
			server: probedServer(nil), dbName: "d", want: true,
			rights: []Right{{Name: "ALTER ANY USER", DB: true, Alt: []string{"ALTER"}}},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.Permissions["ALTER ANY USER"] = denied
				c.Permissions["ALTER"] = granted
			}))},

		// Membership: unknown allows, "not a member" of a probed msdb withholds.
		{name: "unprobed msdb allows a membership right",
			server: probedServer(nil), dbCaps: unprobedDB, rights: []Right{SQLAgentUser}, want: true},
		{name: "probed msdb without the role withholds",
			server: probedServer(nil), dbCaps: only(probedDB(nil)), rights: []Right{SQLAgentUser}, want: false},
		{name: "probed msdb with the role allows",
			server: probedServer(nil), rights: []Right{SQLAgentUser}, want: true,
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) { c.Roles["SQLAgentUserRole"] = true }))},

		// Fixed server roles, and sysadmin standing in for all of them.
		{name: "unprobed server allows a server-role right",
			server: &gosmo.Capabilities{}, dbCaps: unprobedDB, rights: []Right{DiskAdmin}, want: true},
		{name: "probed server outside the role withholds",
			server: probedServer(nil), dbCaps: unprobedDB, rights: []Right{DiskAdmin}, want: false},
		{name: "sysadmin carries every fixed role",
			server: sysadminServer(), dbCaps: unprobedDB, rights: []Right{DiskAdmin}, want: true},

		// Object scope: sparse, so only a recorded grant answers.
		{name: "object grant answers without the database right",
			server: probedServer(nil), dbName: "d", schema: "dbo", object: "t", want: true,
			rights: []Right{AlterDatabase, AlterOnObject},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.Permissions["ALTER"] = denied
				c.ObjectPermissions = map[string]map[string]gosmo.CapabilityState{
					gosmo.ObjectKey("dbo", "t"): {"ALTER": granted}}
			}))},
		{name: "an object with no row does not answer for itself",
			server: probedServer(nil), dbName: "d", schema: "dbo", object: "t", want: false,
			rights: []Right{AlterDatabase, AlterOnObject},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) { c.Permissions["ALTER"] = denied }))},

		// Schema scope.
		{name: "schema grant answers without the database right",
			server: probedServer(nil), dbName: "d", schema: "s", object: "t", want: true,
			rights: []Right{AlterDatabase, AlterOnSchema},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.Permissions["ALTER"] = denied
				c.SchemaPermissions = map[string]map[string]gosmo.CapabilityState{"s": {"ALTER": granted}}
			}))},
		{name: "a schema right with no schema answers nothing",
			server: probedServer(nil), dbName: "d", want: false,
			rights: []Right{AlterDatabase, AlterOnSchema},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.Permissions["ALTER"] = denied
				c.SchemaPermissions = map[string]map[string]gosmo.CapabilityState{"s": {"ALTER": granted}}
			}))},

		// Securable scope: not sparse, so a missing row fails open.
		{name: "a securable with no row allows",
			server: probedServer(nil), dbName: "d", object: "asm", want: true,
			rights: []Right{ControlOnAssembly},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.SecurablePermissions = map[string]map[string]gosmo.CapabilityState{}
			}))},
		{name: "a securable denied withholds",
			server: probedServer(nil), dbName: "d", object: "asm", want: false,
			rights: []Right{ControlOnAssembly},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.SecurablePermissions = map[string]map[string]gosmo.CapabilityState{
					gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableAssembly, "", "asm"): {"CONTROL": denied}}
			}))},

		// A DENY on the object beats every wider grant.
		{name: "object DENY beats a database-wide grant",
			server: probedServer(nil), dbName: "d", schema: "dbo", object: "t", want: false,
			rights: []Right{AlterDatabase, AlterOnObject},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.Permissions["ALTER"] = granted
				c.ObjectPermissions = map[string]map[string]gosmo.CapabilityState{
					gosmo.ObjectKey("dbo", "t"): {"ALTER": denied}}
			}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RightsAllow(tc.server, tc.dbCaps, tc.dbName, tc.schema, tc.object, tc.rights...); got != tc.want {
				t.Errorf("RightsAllow = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestObjectDenial(t *testing.T) {
	alterObjDenied := func(c *gosmo.DatabaseCapabilities) {
		c.ObjectPermissions = map[string]map[string]gosmo.CapabilityState{
			gosmo.ObjectKey("dbo", "t"): {"ALTER": denied}}
	}
	for _, tc := range []struct {
		name                   string
		server                 *gosmo.Capabilities
		dbCaps                 func(string) *gosmo.DatabaseCapabilities
		dbName, schema, object string
		rights                 []Right
		wantDenied             bool
		wantRight              Right
		wantSite               Site
	}{
		{name: "nothing recorded is no denial",
			server: probedServer(nil), dbCaps: only(probedDB(nil)),
			dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterDatabase, AlterOnObject}},
		{name: "unprobed database is no denial",
			server: probedServer(nil), dbCaps: unprobedDB,
			dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterDatabase, AlterOnObject}},
		{name: "object DENY",
			server: probedServer(nil), dbCaps: only(probedDB(alterObjDenied)),
			dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterDatabase, AlterOnObject},
			wantDenied: true, wantRight: AlterOnObject, wantSite: Site{}},
		{name: "sysadmin is never denied",
			server: sysadminServer(), dbCaps: only(probedDB(alterObjDenied)),
			dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterDatabase, AlterOnObject}},
		{name: "a right not declared object-scoped ignores the object DENY",
			server: probedServer(nil), dbCaps: only(probedDB(alterObjDenied)),
			dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterDatabase}},
		{name: "column DENY names the column",
			server: probedServer(nil), dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterOnObject},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.ColumnPermissions = map[string]map[string]gosmo.CapabilityState{
					gosmo.ColumnKey("dbo", "t", "c1"): {"ALTER": denied}}
			})),
			wantDenied: true, wantRight: AlterOnObject, wantSite: Site{Column: "c1"}},
		{name: "schema DENY names the schema",
			server: probedServer(nil), dbName: "d", schema: "s", object: "t", rights: []Right{AlterDatabase, AlterOnSchema},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.ExplicitSchemaPermissions = map[string]map[string]gosmo.CapabilityState{"s": {"ALTER": denied}}
			})),
			wantDenied: true, wantRight: AlterOnSchema, wantSite: Site{Schema: "s"}},
		{name: "database DENY names the database",
			server: probedServer(nil), dbName: "d", schema: "dbo", object: "t", rights: []Right{AlterOnObject},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.ExplicitDatabasePermissions = map[string]gosmo.CapabilityState{"ALTER": denied}
			})),
			wantDenied: true, wantRight: AlterOnObject, wantSite: Site{Database: "d"}},
		{name: "a schema-less object reaches no schema-scoped arm",
			server: probedServer(nil), dbName: "d", schema: "", object: "t", rights: []Right{AlterOnObject},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.ExplicitDatabasePermissions = map[string]gosmo.CapabilityState{"ALTER": denied}
			}))},
		{name: "principal DENY on a user",
			server: probedServer(nil), dbName: "d", object: "u1", rights: []Right{AlterAnyUser},
			dbCaps: only(probedDB(func(c *gosmo.DatabaseCapabilities) {
				c.ExplicitPrincipalPermissions = map[string]map[string]gosmo.CapabilityState{"u1": {"ALTER": denied}}
			})),
			wantDenied: true, wantRight: AlterAnyUser, wantSite: Site{Principal: "u1"}},
		{name: "login DENY, outside any database",
			server: &gosmo.Capabilities{ServerRoles: map[string]bool{},
				ExplicitServerPermissions: map[string]map[string]gosmo.CapabilityState{
					gosmo.ServerSecurableKey(gosmo.ServerSecurableLogin, "l1"): {"ALTER": denied}}},
			dbCaps: unprobedDB, object: "l1", rights: []Right{AlterAnyLogin},
			wantDenied: true, wantRight: AlterAnyLogin,
			wantSite: Site{ServerSecurable: "l1", ServerKind: gosmo.ServerSecurableLogin}},
		{name: "a login DENY does not withhold an endpoint of the same name",
			server: &gosmo.Capabilities{ServerRoles: map[string]bool{},
				ExplicitServerPermissions: map[string]map[string]gosmo.CapabilityState{
					gosmo.ServerSecurableKey(gosmo.ServerSecurableLogin, "x"): {"ALTER": denied}}},
			dbCaps: unprobedDB, object: "x", rights: []Right{AlterAnyEndpoint}},
		{name: "group DENY while the server-wide right is held",
			server: &gosmo.Capabilities{ServerRoles: map[string]bool{},
				ServerPermissions:            map[string]gosmo.CapabilityState{"ALTER ANY AVAILABILITY GROUP": granted},
				AvailabilityGroupPermissions: map[string]map[string]gosmo.CapabilityState{"ag1": {"ALTER": denied}}},
			dbCaps: unprobedDB, object: "ag1", rights: []Right{AlterAnyAG},
			wantDenied: true, wantRight: AlterAnyAG, wantSite: Site{AvailabilityGroup: "ag1"}},
		{name: "group answer without the server-wide right is not a denial",
			server: &gosmo.Capabilities{ServerRoles: map[string]bool{},
				ServerPermissions:            map[string]gosmo.CapabilityState{"ALTER ANY AVAILABILITY GROUP": denied},
				AvailabilityGroupPermissions: map[string]map[string]gosmo.CapabilityState{"ag1": {"ALTER": denied}}},
			dbCaps: unprobedDB, object: "ag1", rights: []Right{AlterAnyAG}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, at, got := ObjectDenial(tc.server, tc.dbCaps, tc.dbName, tc.schema, tc.object, tc.rights...)
			if got != tc.wantDenied {
				t.Fatalf("ObjectDenial denied = %v, want %v", got, tc.wantDenied)
			}
			if !got {
				return
			}
			if r.Name != tc.wantRight.Name || r.Object != tc.wantRight.Object || r.Schema != tc.wantRight.Schema {
				t.Errorf("ObjectDenial right = %+v, want %+v", r, tc.wantRight)
			}
			if at != tc.wantSite {
				t.Errorf("ObjectDenial site = %+v, want %+v", at, tc.wantSite)
			}
		})
	}
}

// Allows and Missing over a connection: a nil or unprobed one fails open, and
// Missing names the first right of the first group when nothing fails.
func TestAllowsAndMissingFailOpenOnAConnectionWithNoAnswers(t *testing.T) {
	sc := &db.ServerConn{}
	for _, c := range []*db.ServerConn{nil, sc} {
		if !Allows(c, "d", AlterSettings, BackupDatabase) {
			t.Errorf("Allows(%v) withheld with nothing known", c)
		}
		if !AllowsAllOn(c, "d", "dbo", "t", []Right{AlterSettings}, []Right{AlterDatabase, AlterOnObject}) {
			t.Errorf("AllowsAllOn(%v) withheld with nothing known", c)
		}
	}
	if !Allows(sc, "d") {
		t.Error("Allows with no rights withheld")
	}
	r, ok := Missing(sc, "d", "", "", nil, []Right{AlterAnySchema, ControlDB}, []Right{AlterSettings})
	if !ok || r.Name != AlterAnySchema.Name {
		t.Errorf("Missing = %+v, %v; want the first right of the first non-empty group", r, ok)
	}
	if _, ok := Missing(sc, "d", "", ""); ok {
		t.Error("Missing with no groups found a right")
	}
	if _, _, denied := DeniedOn(nil, "d", "dbo", "t", AlterOnObject); denied {
		t.Error("DeniedOn(nil) reported a denial")
	}
}

func TestDeniedTextNamesTheSite(t *testing.T) {
	for _, tc := range []struct {
		r    Right
		at   Site
		want string
	}{
		{AlterOnObject, Site{}, "ALTER is denied on this object."},
		{AlterOnObject, Site{Column: "c1"}, "ALTER is denied on column c1 of this object."},
		{AlterOnSchema, Site{Schema: "s"}, "ALTER is denied on schema s."},
		{AlterOnObject, Site{Database: "d"}, "ALTER is denied on database d."},
		{AlterAnyUser, Site{Principal: "u1"}, "ALTER is denied on principal u1."},
		{AlterAnyLogin, Site{ServerSecurable: "l1", ServerKind: gosmo.ServerSecurableLogin}, "ALTER is denied on login l1."},
		{AlterAnyServerRoleMembers, Site{ServerSecurable: "r1", ServerKind: gosmo.ServerSecurableServerRole}, "ALTER is denied on server role r1."},
		{AlterAnyAG, Site{AvailabilityGroup: "ag1"}, "ALTER is denied on availability group ag1."},
	} {
		if got := DeniedText(tc.r, tc.at); got != tc.want {
			t.Errorf("DeniedText(%s, %+v) = %q, want %q", tc.r.Name, tc.at, got, tc.want)
		}
	}
}
