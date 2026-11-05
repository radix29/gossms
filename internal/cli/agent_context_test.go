package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// contextFake is fakeCatalog plus agent context's reads. A nil srv or dbc
// with its error set is a failed probe.
type contextFake struct {
	*fakeCatalog
	info   *gosmo.ServerInfo
	srv    *gosmo.Capabilities
	srvErr error
	dbc    *gosmo.DatabaseCapabilities
	dbcErr error
	// dbcBy answers for the databases it names, ahead of dbc.
	dbcBy  map[string]*gosmo.DatabaseCapabilities
	probed []string // the databases DatabaseCapabilities was asked about
	access map[int]bool
	// msdbErr fails the msdb lookup agent context makes for the job row.
	msdbErr error
}

// DatabaseByName adds msdb to fakeCatalog's databases, for the msdb role
// probe; its capabilities are dbcBy["msdb"], else dbc.
func (c *contextFake) DatabaseByName(ctx context.Context, name string) (*gosmo.Database, error) {
	if name == "msdb" {
		return &gosmo.Database{Name: "msdb", ID: 4, State: gosmo.DatabaseOnline}, c.msdbErr
	}
	return c.fakeCatalog.DatabaseByName(ctx, name)
}

func (c *contextFake) ServerInfo() *gosmo.ServerInfo { return c.info }

func (c *contextFake) ServerCapabilities(context.Context) (*gosmo.Capabilities, error) {
	return c.srv, c.srvErr
}

func (c *contextFake) DatabaseCapabilities(_ context.Context, d *gosmo.Database) (*gosmo.DatabaseCapabilities, error) {
	c.probed = append(c.probed, d.Name)
	if dbc, ok := c.dbcBy[d.Name]; ok {
		return dbc, nil
	}
	return c.dbc, c.dbcErr
}

func (c *contextFake) DatabaseAccess(context.Context) (map[int]bool, error) { return c.access, nil }

// newContextFake is a db_datareader login on a 2017 instance: no VIEW
// SERVER STATE (its 2022 alternative unknown), VIEW ANY DEFINITION denied but
// ALTER ANY DATABASE held, ALTER SETTINGS and ALTER ANY CONNECTION denied,
// ALTER ANY EVENT SESSION denied with all four of 2022's narrow rights a
// capture needs granted, CREATE ANY DATABASE denied, no role but public; no
// VIEW DEFINITION, ALTER, ALTER ANY ROLE or BACKUP LOG in the database (ALTER
// ANY DATABASE SCOPED CONFIGURATION unknown), BACKUP DATABASE granted, VIEW
// DATABASE STATE denied with its 2022 alternative granted, not db_owner.
func newContextFake() *contextFake {
	return &contextFake{
		fakeCatalog: newFakeCatalog(),
		info: &gosmo.ServerInfo{Name: `WIN10CLI\SQL2017`, ProductVersion: "14.0.2140.2", VersionMajor: 14,
			Edition: "Developer Edition (64-bit)", Login: "reader"},
		srv: &gosmo.Capabilities{
			ServerRoles: map[string]bool{"sysadmin": false, "public": true},
			ServerPermissions: map[string]gosmo.CapabilityState{
				"VIEW SERVER STATE":               gosmo.CapabilityDenied,
				"VIEW ANY DEFINITION":             gosmo.CapabilityDenied,
				"ALTER ANY DATABASE":              gosmo.CapabilityGranted,
				"VIEW ANY DATABASE":               gosmo.CapabilityGranted,
				"ALTER SETTINGS":                  gosmo.CapabilityDenied,
				"ALTER ANY CONNECTION":            gosmo.CapabilityDenied,
				"ALTER ANY EVENT SESSION":         gosmo.CapabilityDenied,
				"CREATE ANY EVENT SESSION":        gosmo.CapabilityGranted,
				"ALTER ANY EVENT SESSION ENABLE":  gosmo.CapabilityGranted,
				"ALTER ANY EVENT SESSION DISABLE": gosmo.CapabilityGranted,
				"DROP ANY EVENT SESSION":          gosmo.CapabilityGranted,
				"CREATE ANY DATABASE":             gosmo.CapabilityDenied,
			},
		},
		dbc: &gosmo.DatabaseCapabilities{
			Accessible: true,
			Roles:      map[string]bool{"db_datareader": true, "db_owner": false},
			Permissions: map[string]gosmo.CapabilityState{
				"VIEW DEFINITION":                 gosmo.CapabilityDenied,
				"VIEW DATABASE STATE":             gosmo.CapabilityDenied,
				"VIEW DATABASE PERFORMANCE STATE": gosmo.CapabilityGranted,
				"ALTER":                           gosmo.CapabilityDenied,
				"SHOWPLAN":                        gosmo.CapabilityDenied,
				"BACKUP DATABASE":                 gosmo.CapabilityGranted,
				"BACKUP LOG":                      gosmo.CapabilityDenied,
				"ALTER ANY ROLE":                  gosmo.CapabilityDenied,
			},
		},
		access: map[int]bool{5: true, 6: false, 1: true, 8: false},
	}
}

func contextSet(t *testing.T, env map[string]any, name string) [][]any {
	t.Helper()
	for _, s := range env["resultSets"].([]any) {
		s := s.(map[string]any)
		if s["name"] == name {
			var rows [][]any
			for _, r := range s["rows"].([]any) {
				rows = append(rows, r.([]any))
			}
			return rows
		}
	}
	t.Fatalf("no set %q in %v", name, env["resultSets"])
	return nil
}

func TestAgentContext(t *testing.T) {
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, newContextFake())),
		"agent", "context", "-p", "dev", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	data := env["data"].(map[string]any)
	for k, want := range map[string]any{
		"profile": "dev", "serverName": `WIN10CLI\SQL2017`, "majorVersion": "14", "login": "reader",
		"isSysadmin": "false", "database": "app", "databaseRoles": "db_datareader", "targetDatabase": "app",
		"defaultAllow": "read", "maxAllow": "write", "maxAllowSource": "profile",
	} {
		if data[k] != want {
			t.Errorf("%s = %v, want %v", k, data[k], want)
		}
	}

	dbs := contextSet(t, env, "databases")
	access := map[string]any{}
	for _, r := range dbs {
		access[r[0].(string)] = r[2]
	}
	// app_snap (id 7) is absent from the access map: the server said nothing.
	for name, want := range map[string]any{"app": true, "archive": false, "app_snap": nil, "master": true} {
		if access[name] != want {
			t.Errorf("databases %s accessible = %v, want %v", name, access[name], want)
		}
	}

	var missing []string
	for _, r := range contextSet(t, env, "missing_permissions") {
		missing = append(missing, r[0].(string)+" @ "+r[1].(string)+" → "+r[2].(string))
		// An agent reads the rows under the default --max-col-width.
		for _, cell := range r {
			if strings.Contains(cell.(string), "…[+") {
				t.Errorf("missing_permissions cell cut by --max-col-width: %s", cell)
			}
		}
	}
	want := []string{
		"VIEW SERVER STATE @ server → object indexes, db files, db space, db log-usage, server volumes, " +
			"server memory, server info, server config get, server config set",
		"VIEW SERVER STATE @ server → monitor sessions, monitor blocking, monitor waits, monitor top-queries, " +
			"monitor deadlocks, monitor kill, plan cached, xevent sessions, xevent capture",
		"ALTER SETTINGS @ server → server config set, server config reset",
		"sysadmin (role) @ server → server traceflag on, server traceflag off",
		"securityadmin, VIEW SERVER STATE or VIEW ANY ERROR LOG @ server → server log",
		"ALTER ANY CONNECTION @ server → monitor kill",
		"VIEW DEFINITION @ database [app] → object list, object search, object describe, object definition, " +
			"object dependencies, object indexes, object stats, script object",
		"SHOWPLAN @ database [app] → plan estimated, plan actual",
		"ALTER @ database [app] → db set",
		"db_owner (role) or sysadmin @ database [app] → maint checkdb",
		"db_owner (role) or sysadmin @ database [app] → maint update-stats",
		"BACKUP LOG @ database [app] → maint backup",
		"CREATE DATABASE (in master) or CREATE ANY DATABASE @ server → db create, maint restore",
		"ALTER ANY ROLE @ database [app] → security role add-member, security role remove-member",
		"sysadmin (role) or an msdb Agent role @ server → job list, job show, job history, job start, job stop",
	}
	if strings.Join(missing, "\n") != strings.Join(want, "\n") {
		t.Errorf("missing_permissions:\n%s\nwant:\n%s", strings.Join(missing, "\n"), strings.Join(want, "\n"))
	}

	var profiles []string
	for _, r := range contextSet(t, env, "profiles") {
		profiles = append(profiles, fmt.Sprint(r))
	}
	if got := strings.Join(profiles, "; "); got != "[dev devsrv,1500 app write]; [prod-ro prod  read]" {
		t.Errorf("profiles: %s", got)
	}
}

// A probe that fails answers nothing: no permission is reported missing on
// the strength of it, and the record still comes back, exit 0.
func TestAgentContextFailedProbes(t *testing.T) {
	cat := newContextFake()
	cat.srv, cat.srvErr = nil, errors.New("gosmo: read server capabilities: boom")
	cat.dbc, cat.dbcErr = nil, errors.New("gosmo: read capabilities for database \"app\": boom")
	cat.info = nil
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "agent", "context", "-p", "dev", "-o", "json")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	data := env["data"].(map[string]any)
	if data["isSysadmin"] != nil || data["databaseRoles"] != "" {
		t.Errorf("isSysadmin %v, databaseRoles %v; want null and empty", data["isSysadmin"], data["databaseRoles"])
	}
	if rows := contextSet(t, env, "missing_permissions"); len(rows) != 0 {
		t.Errorf("missing_permissions from failed probes: %v", rows)
	}
	if w := env["warnings"].([]any); len(w) != 3 {
		t.Errorf("warnings %v, want three (info, server probe, database probe)", w)
	}
}

// A need met by a role: a member is not missing it, and an unprobed role
// list (ServerRoles nil) is not a lack.
func TestAgentContextRoleNeeds(t *testing.T) {
	permissions := func(cat *contextFake) []string {
		r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "agent", "context", "-p", "dev", "-o", "json")
		var out []string
		for _, row := range contextSet(t, envelope(t, r.stdout), "missing_permissions") {
			out = append(out, row[0].(string))
		}
		return out
	}
	cat := newContextFake()
	cat.srv.ServerRoles["securityadmin"] = true
	if got := permissions(cat); slices.Contains(got, "securityadmin, VIEW SERVER STATE or VIEW ANY ERROR LOG") ||
		!slices.Contains(got, "sysadmin (role)") {
		t.Errorf("securityadmin member: %q", got)
	}
	cat = newContextFake()
	cat.srv.ServerRoles = nil
	got := permissions(cat)
	if slices.Contains(got, "sysadmin (role)") {
		t.Errorf("unprobed roles read as a lack: %q", got)
	}
	// VIEW SERVER STATE still answers for the error log.
	if !slices.Contains(got, "securityadmin, VIEW SERVER STATE or VIEW ANY ERROR LOG") {
		t.Errorf("unprobed roles hid the denied VIEW SERVER STATE: %q", got)
	}

	// A database role: a member (a sysadmin reads as one too) is not
	// missing it, and an unprobed role list is not a lack.
	const owner = "db_owner (role) or sysadmin"
	if got := permissions(newContextFake()); !slices.Contains(got, owner) {
		t.Errorf("not db_owner, not reported: %q", got)
	}
	cat = newContextFake()
	cat.dbc.Roles["db_owner"] = true
	if got := permissions(cat); slices.Contains(got, owner) {
		t.Errorf("db_owner member reported: %q", got)
	}
	cat = newContextFake()
	cat.dbc.Roles = nil
	if got := permissions(cat); slices.Contains(got, owner) {
		t.Errorf("unprobed database roles read as a lack: %q", got)
	}
}

// The job commands need sysadmin or one of msdb's three Agent roles: a
// member of either is not missing it; msdb is probed only for a login known
// not to be sysadmin; an msdb that cannot be probed or opened answers
// nothing, with a warning.
func TestAgentContextMsdbAgentRoles(t *testing.T) {
	const row = "sysadmin (role) or an msdb Agent role @ server → job list, job show, job history, job start, job stop"
	agentDB := func(roles map[string]bool) map[string]*gosmo.DatabaseCapabilities {
		return map[string]*gosmo.DatabaseCapabilities{"msdb": {Accessible: true, Roles: roles}}
	}
	cat := newContextFake()
	cat.dbcBy = agentDB(map[string]bool{"SQLAgentUserRole": false})
	if got, _ := contextMissing(t, cat); !slices.Contains(got, row) {
		t.Errorf("no Agent role, not reported: %q", got)
	}
	for _, role := range []string{"SQLAgentUserRole", "SQLAgentReaderRole", "SQLAgentOperatorRole"} {
		cat := newContextFake()
		cat.dbcBy = agentDB(map[string]bool{role: true})
		if got, _ := contextMissing(t, cat); slices.Contains(got, row) {
			t.Errorf("%s member reported: %q", role, got)
		}
	}

	cat = newContextFake()
	cat.srv.ServerRoles["sysadmin"] = true
	if got, _ := contextMissing(t, cat); slices.Contains(got, row) || slices.Contains(cat.probed, "msdb") {
		t.Errorf("sysadmin: listed %q, probed %q; want neither", got, cat.probed)
	}
	cat = newContextFake()
	cat.srv.ServerRoles = nil
	if contextMissing(t, cat); slices.Contains(cat.probed, "msdb") {
		t.Errorf("unprobed server roles still probed msdb: %q", cat.probed)
	}

	for name, cat := range map[string]*contextFake{
		"unopenable": func() *contextFake {
			c := newContextFake()
			c.dbcBy = map[string]*gosmo.DatabaseCapabilities{"msdb": {Roles: map[string]bool{}}}
			return c
		}(),
		"unread": func() *contextFake {
			c := newContextFake()
			c.msdbErr = errors.New("gosmo: read database \"msdb\": boom")
			return c
		}(),
	} {
		got, env := contextMissing(t, cat)
		if slices.Contains(got, row) {
			t.Errorf("%s msdb read as a lack: %q", name, got)
		}
		if w := fmt.Sprint(env["warnings"]); !strings.Contains(w, "msdb") {
			t.Errorf("%s msdb: no warning, %s", name, w)
		}
	}
}

// ALTER ANY EVENT SESSION denied is a lack only when 2022's narrow rights do
// not cover every verb a capture runs, as xevent capture's own check.
func TestAgentContextEventSessionRights(t *testing.T) {
	cat := newContextFake()
	cat.srv.ServerPermissions["DROP ANY EVENT SESSION"] = gosmo.CapabilityUnknown
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "agent", "context", "-p", "dev", "-o", "json")
	var got []string
	for _, row := range contextSet(t, envelope(t, r.stdout), "missing_permissions") {
		got = append(got, row[0].(string)+" → "+row[2].(string))
	}
	if !slices.Contains(got, "ALTER ANY EVENT SESSION → xevent capture") {
		t.Errorf("three of four narrow rights not reported: %q", got)
	}
}

// contextMissing runs agent context with extra arguments and returns its
// missing_permissions as "permission @ scope → commands", and the envelope.
func contextMissing(t *testing.T, cat *contextFake, extra ...string) ([]string, map[string]any) {
	t.Helper()
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)),
		append([]string{"agent", "context", "-p", "dev", "-o", "json"}, extra...)...)
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	env := envelope(t, r.stdout)
	var out []string
	for _, row := range contextSet(t, env, "missing_permissions") {
		out = append(out, row[0].(string)+" @ "+row[1].(string)+" → "+row[2].(string))
	}
	return out, env
}

// CREATE DATABASE granted in master alone is enough for db create and maint
// restore (and for sys.master_files), though CREATE ANY DATABASE reads 0.
func TestAgentContextCreateDatabaseInMaster(t *testing.T) {
	const row = "CREATE DATABASE (in master) or CREATE ANY DATABASE @ server → db create, maint restore"
	const files = "VIEW ANY DEFINITION @ server → db list, db info, db files"
	cat := newContextFake()
	cat.srv.ServerPermissions["ALTER ANY DATABASE"] = gosmo.CapabilityDenied
	cat.srv.MasterPermissions = map[string]gosmo.CapabilityState{"CREATE DATABASE": gosmo.CapabilityDenied}
	if got, _ := contextMissing(t, cat); !slices.Contains(got, row) || !slices.Contains(got, files) {
		t.Errorf("neither form held, not reported: %q", got)
	}
	cat.srv.MasterPermissions["CREATE DATABASE"] = gosmo.CapabilityGranted
	if got, _ := contextMissing(t, cat); slices.Contains(got, row) || slices.Contains(got, files) {
		t.Errorf("CREATE DATABASE in master read as a lack: %q", got)
	}
}

// IMPERSONATE ANY LOGIN denied is listed for security permissions; granted,
// or unknown, it is not.
func TestAgentContextImpersonateAnyLogin(t *testing.T) {
	const row = "IMPERSONATE ANY LOGIN @ server → security permissions"
	cat := newContextFake()
	if got, _ := contextMissing(t, cat); slices.Contains(got, row) {
		t.Errorf("unprobed IMPERSONATE ANY LOGIN read as a lack: %q", got)
	}
	cat.srv.ServerPermissions["IMPERSONATE ANY LOGIN"] = gosmo.CapabilityDenied
	if got, _ := contextMissing(t, cat); !slices.Contains(got, row) {
		t.Errorf("denied IMPERSONATE ANY LOGIN not reported: %q", got)
	}
	cat.srv.ServerPermissions["IMPERSONATE ANY LOGIN"] = gosmo.CapabilityGranted
	if got, _ := contextMissing(t, cat); slices.Contains(got, row) {
		t.Errorf("granted IMPERSONATE ANY LOGIN reported: %q", got)
	}
}

// --target-database: the rows of the commands acting on a <database>
// argument answer for it; every other database row stays the connection's.
func TestAgentContextTargetDatabase(t *testing.T) {
	cat := newContextFake()
	// archive: db_owner there, so it holds every database right.
	cat.dbcBy = map[string]*gosmo.DatabaseCapabilities{"archive": {
		Accessible: true,
		Roles:      map[string]bool{"db_owner": true},
		Permissions: map[string]gosmo.CapabilityState{
			"ALTER": gosmo.CapabilityGranted, "BACKUP DATABASE": gosmo.CapabilityGranted,
			"BACKUP LOG": gosmo.CapabilityGranted, "ALTER ANY DATABASE SCOPED CONFIGURATION": gosmo.CapabilityGranted,
		},
	}}
	got, env := contextMissing(t, cat, "--target-database", "ARCHIVE")
	if data := env["data"].(map[string]any); data["targetDatabase"] != "archive" || data["database"] != "app" {
		t.Errorf("targetDatabase %v, database %v; want archive (catalog spelling), app", data["targetDatabase"], data["database"])
	}
	for _, gone := range []string{
		"ALTER @ database [app] → db set",
		"db_owner (role) or sysadmin @ database [app] → maint checkdb",
		"BACKUP LOG @ database [app] → maint backup",
	} {
		if slices.Contains(got, gone) {
			t.Errorf("%q answered for the connection's database, not the target: %q", gone, got)
		}
	}
	for _, kept := range []string{
		"db_owner (role) or sysadmin @ database [app] → maint update-stats",
		"SHOWPLAN @ database [app] → plan estimated, plan actual",
	} {
		if !slices.Contains(got, kept) {
			t.Errorf("%q, a connection-database row, is gone: %q", kept, got)
		}
	}

	// The other way round: the connection's database grants BACKUP LOG...
	cat = newContextFake()
	cat.dbc.Permissions["BACKUP LOG"] = gosmo.CapabilityGranted
	cat.dbcBy = map[string]*gosmo.DatabaseCapabilities{"archive": {
		Accessible: true, Roles: map[string]bool{"db_owner": false},
		Permissions: map[string]gosmo.CapabilityState{"BACKUP DATABASE": gosmo.CapabilityDenied, "BACKUP LOG": gosmo.CapabilityDenied},
	}}
	got, _ = contextMissing(t, cat, "--target-database", "archive")
	for _, want := range []string{
		"BACKUP DATABASE @ database [archive] → maint backup",
		"BACKUP LOG @ database [archive] → maint backup",
		"db_owner (role) or sysadmin @ database [archive] → maint checkdb",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("target's lack %q not reported: %q", want, got)
		}
	}
	// ...and the connection's database itself is probed once, not twice.
	cat = newContextFake()
	contextMissing(t, cat, "--target-database", "app")
	if strings.Join(cat.probed, ",") != "app,msdb" {
		t.Errorf("probed %q, want app once (and msdb, for the job row)", cat.probed)
	}
}

// A target the login cannot open answers nothing and is warned of; a missing
// one is NOT_FOUND.
func TestAgentContextTargetDatabaseUnreadable(t *testing.T) {
	cat := newContextFake()
	// gosmo's answer for one it cannot open: an empty role map, which reads
	// as probed.
	cat.dbcBy = map[string]*gosmo.DatabaseCapabilities{"archive": {Accessible: false, Roles: map[string]bool{}}}
	got, env := contextMissing(t, cat, "--target-database", "archive")
	if slices.ContainsFunc(got, func(r string) bool { return strings.Contains(r, "[archive]") }) {
		t.Errorf("an unopenable target listed rows: %q", got)
	}
	if w := fmt.Sprint(env["warnings"]); !strings.Contains(w, "cannot open [archive]") {
		t.Errorf("warnings %s", w)
	}
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, newContextFake())),
		"agent", "context", "-p", "dev", "--target-database", "nosuch", "-o", "json")
	if r.exit != 8 {
		t.Errorf("missing target: exit %d, want 8\n%s%s", r.exit, r.stdout, r.stderr)
	}
}

// Every command missing_permissions names is one the tree has.
func TestAgentContextNamesOnlyCommands(t *testing.T) {
	tree := commandTree()
	for _, path := range cmd.PermissionNeedCommands() {
		if c := tree.Lookup(strings.Fields(path)); c == nil || c.Run == nil {
			t.Errorf("missing_permissions names %q, which is not a runnable command", path)
		}
	}
}

func TestAgentGuideEnvironment(t *testing.T) {
	tree := newCommandTree(catalogDeps(t, newContextFake()))

	r := runTree(t, context.Background(), tree, "agent", "guide", "--brief", "-p", "dev")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	_, env, ok := strings.Cut(r.stdout, "\n## Your environment (profile: dev)\n")
	if !ok {
		t.Fatalf("no environment section:\n%s", r.stdout)
	}
	for _, want := range []string{"- login: reader", "- maxAllow: write", "### missing_permissions",
		"| VIEW SERVER STATE | server | object indexes, db files, db space, db log-usage, server volumes, server memory, ", "### profiles"} {
		if !strings.Contains(env, want) {
			t.Errorf("environment lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "\n## ") {
		t.Errorf("environment is not the last section:\n%s", env)
	}

	// JSON: the last section, and a topic keeps it.
	r = runTree(t, context.Background(), tree, "agent", "guide", "--topic", "rules", "-p", "dev", "-o", "json")
	var g struct{ Sections []struct{ ID, Title string } }
	if err := json.Unmarshal([]byte(r.stdout), &g); err != nil || r.exit != 0 {
		t.Fatalf("exit %d, %v\n%s", r.exit, err, r.stdout)
	}
	if len(g.Sections) != 2 || g.Sections[0].ID != "rules" || g.Sections[1].ID != "environment" {
		t.Errorf("sections %+v, want rules then environment", g.Sections)
	}

	// No connection option: the guide never connects (Connect is nil here).
	r = runTree(t, context.Background(), newCommandTree(profileDeps(t, nil)), "agent", "guide", "--brief")
	if r.exit != 0 || strings.Contains(r.stdout, "Your environment") {
		t.Errorf("guide without -p: exit %d, environment present %v", r.exit, strings.Contains(r.stdout, "Your environment"))
	}
}

// A brief environment shows each table's first rows and counts the rest.
func TestAgentGuideEnvironmentBriefCapsTables(t *testing.T) {
	cat := newContextFake()
	for i := range 20 {
		cat.dbs = append(cat.dbs, &gosmo.Database{Name: fmt.Sprintf("extra%02d", i), ID: 100 + i, State: gosmo.DatabaseOnline})
	}
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "agent", "guide", "--brief", "-p", "dev")
	if !strings.Contains(r.stdout, "10 more: `gossms agent context`") {
		t.Errorf("brief environment does not cap 25 databases at 15:\n%s", r.stdout)
	}
	r = runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), "agent", "guide", "--topic", "rules", "-p", "dev")
	if strings.Contains(r.stdout, "more: `gossms agent context`") || !strings.Contains(r.stdout, "extra19") {
		t.Errorf("full environment capped its tables:\n%s", r.stdout)
	}
}

// A connection that fails still prints the guide; the section names the
// error and the exit code is the error's.
func TestAgentGuideEnvironmentConnectFails(t *testing.T) {
	d := profileDeps(t, func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	r := runTree(t, context.Background(), newCommandTree(d), "agent", "guide", "--brief", "-p", "dev")
	if r.exit != 3 {
		t.Errorf("exit %d, want 3 (CONNECTION_FAILED)\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "## Rules you must follow") || !strings.Contains(r.stdout, "Not available: `CONNECTION_FAILED`") {
		t.Errorf("guide or error section missing:\n%s", r.stdout)
	}
	if !strings.HasPrefix(r.stderr, "gossms: CONNECTION_FAILED: ") {
		t.Errorf("stderr %q", r.stderr)
	}
}
