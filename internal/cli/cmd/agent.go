package cmd

import (
	"context"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
)

// AgentContextCommand is `agent context`: the live facts an agent grounds
// itself in. `agent guide` takes the same connection options and, given one,
// adds this command's answer as its last section.
func AgentContextCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"agent", "context"},
		Summary: "Show what a profile reaches: server, login, database, ceiling, databases, missing permissions, profiles",
		Description: "Connects as every command will and reports what an agent should know before it acts: the " +
			"server's version and edition, the login and its roles, the connection's database, the default " +
			"--allow and the profile's ceiling (max_allow) that caps it, every database the login can see and " +
			"whether it can open it, and the profiles saved on this machine. missing_permissions lists each " +
			"permission the login lacks that a gossms command needs, with the commands it breaks and how: a read " +
			"that fails (exit 9), or columns that read NULL. A permission the server does not answer for is not " +
			"listed.",
		Options: ConnectOptions(),
		Class:   spec.ClassRead,
		Run:     d.agentContext,
		Examples: []string{
			"gossms agent context -p dev",
			"gossms agent context -p dev -o json",
		},
	}
}

// permissionNeed is one permission a command needs, for missing_permissions.
// It is missing when the server answers for at least one of anyOf and roles
// and grants none of them: a probe that answered nothing (Unknown) is not a
// lack.
type permissionNeed struct {
	permission string
	database   bool     // database scope (the connection's database), else server
	anyOf      []string // granted any: not missing
	roles      []string // fixed server roles, a member of any: not missing (server scope only)
	commands   []string // the command paths it breaks, as typed
	effect     string
}

// permissionNeeds is what the implemented commands need beyond connecting.
// Every command named here must exist (TestAgentContextNamesOnlyCommands).
var permissionNeeds = []permissionNeed{
	{
		permission: "VIEW SERVER STATE",
		// 2022 split the performance DMVs out under the narrower name.
		// sys.dm_db_log_space_usage and dm_db_log_info are server-scoped
		// despite their names: VIEW DATABASE STATE alone is refused (Msg 300),
		// checked on 13 and 17.
		anyOf: []string{"VIEW SERVER STATE", "VIEW SERVER PERFORMANCE STATE"},
		commands: []string{"object indexes", "db files", "db space", "db log-usage", "server volumes",
			"server memory", "server info", "server config get", "server config set"},
		effect: "db log-usage, server volumes and server memory fail (exit 9); columns read NULL (with a " +
			"warning): index usage (object indexes), volume_free_pct (db files), the log columns (db space), " +
			"the start time, uptime and CPU/memory facts (server info), the CPU/memory context (server config " +
			"get); server config set blocks a memory or maxdop value it cannot check (exit 7) unless --accept-risk",
	},
	{
		permission: "ALTER SETTINGS",
		// serveradmin and sysadmin hold it.
		anyOf:    []string{"ALTER SETTINGS"},
		commands: []string{"server config set", "server config reset"},
		effect:   "fail (exit 9); --dry-run still prints the batch",
	},
	{
		permission: "sysadmin (role)",
		// DBCC TRACEON/TRACEOFF check the role, not a permission (Msg 2571).
		roles:    []string{"sysadmin"},
		commands: []string{"server traceflag on", "server traceflag off"},
		effect:   "fail (exit 9); server traceflag list needs no permission",
	},
	{
		permission: "securityadmin, VIEW SERVER STATE or VIEW ANY ERROR LOG",
		// sp_readerrorlog's own check (17's definition; 2016–2019 have no VIEW
		// ANY ERROR LOG). xp_readerrorlog itself is sysadmin only.
		anyOf:    []string{"VIEW SERVER STATE", "VIEW ANY ERROR LOG"},
		roles:    []string{"securityadmin", "sysadmin"},
		commands: []string{"server log"},
		effect: "fails (exit 9); without sysadmin it reads through sp_readerrorlog, which reads the whole " +
			"file before --since filters it",
	},
	{
		permission: "VIEW ANY DATABASE",
		anyOf:      []string{"VIEW ANY DATABASE"},
		commands:   []string{"db list", "db info"},
		effect:     "only master, tempdb and the databases the login owns are listed",
	},
	{
		permission: "VIEW ANY DEFINITION",
		// sys.master_files shows its rows to any of the three.
		anyOf:    []string{"VIEW ANY DEFINITION", "CREATE ANY DATABASE", "ALTER ANY DATABASE"},
		commands: []string{"db list", "db info", "db files"},
		effect: "database sizes read NULL (sys.master_files hides the files); db files lists a database it " +
			"cannot read inside (not ONLINE, or no user) with no files, only a warning",
	},
	{
		permission: "VIEW DEFINITION",
		database:   true,
		anyOf:      []string{"VIEW DEFINITION"},
		commands: []string{"object list", "object search", "object describe", "object definition",
			"object dependencies", "object indexes", "object stats"},
		// Verified on 14 and 17 with a db_datareader login: the dependency
		// rows are filtered by metadata visibility, not refused.
		effect: "only objects the login holds a permission on are visible; definitions read null; " +
			"dependencies read empty (the edges are hidden, not refused)",
	},
	{
		permission: "VIEW DATABASE STATE",
		database:   true,
		anyOf:      []string{"VIEW DATABASE STATE", "VIEW DATABASE PERFORMANCE STATE"},
		commands:   []string{"object indexes"},
		effect:     "--fragmentation fails (exit 9) unless the login has CONTROL on the object",
	},
	{
		permission: "ALTER",
		database:   true,
		// ALTER ANY DATABASE on the server covers it.
		anyOf:    []string{"ALTER"},
		commands: []string{"db set"},
		effect:   "fails on this database (exit 9); --dry-run still prints the statements",
	},
	{
		permission: "ALTER ANY DATABASE SCOPED CONFIGURATION",
		database:   true,
		// ALTER and CONTROL on the database cover it.
		anyOf:    []string{"ALTER ANY DATABASE SCOPED CONFIGURATION"},
		commands: []string{"db scoped-config set"},
		effect:   "fails in this database (exit 9); db scoped-config list and get need only a user",
	},
}

// missing reports whether the need is known to be unmet under state and, for
// a need with roles, the server probe srv.
func (n permissionNeed) missing(state func(string) gosmo.CapabilityState, srv *gosmo.Capabilities) bool {
	answered := false
	if len(n.roles) > 0 && srv.Probed() {
		if slices.ContainsFunc(n.roles, srv.InServerRole) {
			return false
		}
		answered = true
	}
	for _, p := range n.anyOf {
		switch state(p) {
		case gosmo.CapabilityGranted:
			return false
		case gosmo.CapabilityDenied:
			answered = true
		}
	}
	return answered
}

func (d Deps) agentContext(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	cfg, profile, ceiling, err := d.profileCeiling(inv)
	if err != nil {
		return nil, err
	}
	_, sc, err := d.dial(ctx, inv, cfg, profile, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	cat := d.Catalog(sc)
	dbase, err := currentDatabase(ctx, cat, res)
	if err != nil {
		return res, err
	}

	info := cat.ServerInfo()
	if info == nil {
		info = new(gosmo.ServerInfo)
		res.Warnings = append(res.Warnings, "The server's version and login were not read at connect.")
	}
	maxAllow, source := string(ceiling), "profile"
	switch {
	case profile == "":
		maxAllow, source = string(config.AllowAdmin), "none (ad-hoc connection: only --allow applies)"
	default:
		if p, err := cfg.ProfileByName(profile); err == nil && p.MaxAllow == "" {
			source = "default (the profile sets no max_allow)"
		}
	}

	srvCaps, err := cat.ServerCapabilities(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "Server permissions were not probed: "+err.Error())
	}
	dbCaps, err := cat.DatabaseCapabilities(ctx, dbase)
	if err != nil {
		res.Warnings = append(res.Warnings, "Permissions in the database were not probed: "+err.Error())
	}
	var roles []string
	for _, r := range gosmo.ProbedDatabaseRoles {
		if dbCaps != nil && dbCaps.Roles[r] {
			roles = append(roles, r)
		}
	}
	sysadmin := output.Field{Key: "isSysadmin", Null: !srvCaps.Probed()}
	if srvCaps.Probed() {
		sysadmin.Value = boolText(srvCaps.IsSysadmin())
	}
	res.Fields = slices.Concat([]output.Field{{Key: "profile", Value: profile, Null: profile == ""}}, serverFields(info), []output.Field{
		{Key: "login", Value: info.Login},
		sysadmin,
		{Key: "database", Value: dbase.Name},
		{Key: "databaseRoles", Value: strings.Join(roles, ", ")},
		{Key: "defaultAllow", Value: string(spec.ClassRead)},
		{Key: "maxAllow", Value: maxAllow},
		{Key: "maxAllowSource", Value: source},
	})

	res.Sets = append(res.Sets, d.contextDatabases(ctx, cat, res), missingPermissions(srvCaps, dbCaps, dbase.Name))
	profiles, err := d.contextProfiles(cfg, profile)
	if err != nil {
		res.Warnings = append(res.Warnings, "Profiles were not read: "+err.Error())
	}
	res.Sets = append(res.Sets, profiles)
	return res, nil
}

// contextDatabases is every database the login can see, and whether it can
// open it now: false for one not ONLINE, NULL when the server did not say.
func (d Deps) contextDatabases(ctx context.Context, cat Catalog, res *output.Result) output.Set {
	set := output.Set{Name: "databases", Columns: []output.Column{
		{Name: "name", Type: "sysname"}, {Name: "state", Type: "nvarchar(60)"},
		{Name: "accessible", Type: "bit", Nullable: true}, {Name: "is_system", Type: "bit"},
	}}
	dbs, err := cat.Databases(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "Databases were not read: "+err.Error())
		return set
	}
	access, err := cat.DatabaseAccess(ctx)
	if err != nil {
		res.Warnings = append(res.Warnings, "Database access was not read: "+err.Error())
	}
	for _, db := range dbs {
		var accessible *string
		if ok, known := access[db.ID]; known {
			accessible = bitCell(ok)
		}
		set.Rows = append(set.Rows, []*string{ptr(db.Name), ptr(string(db.State)), accessible,
			bitCell(db.IsSystem() || db.IsDistributor)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// missingPermissions is permissionNeeds the login is known to lack; a nil
// probe answers nothing, so lists nothing.
func missingPermissions(srv *gosmo.Capabilities, dbc *gosmo.DatabaseCapabilities, database string) output.Set {
	set := output.Set{Name: "missing_permissions", Columns: []output.Column{
		{Name: "permission"}, {Name: "scope"}, {Name: "commands"}, {Name: "effect"},
	}}
	for _, n := range permissionNeeds {
		state, scope := srv.Permission, "server"
		if n.database {
			state, scope = dbc.Permission, "database "+gosmo.QuoteName(database)
		}
		if n.missing(state, srv) {
			set.Rows = append(set.Rows, []*string{ptr(n.permission), ptr(scope),
				ptr(strings.Join(n.commands, ", ")), ptr(n.effect)})
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// contextProfiles is the saved profiles, by name. An ad-hoc call has read no
// configuration yet; it reads it here, and a failure is only a warning.
func (d Deps) contextProfiles(cfg *config.Config, profile string) (output.Set, error) {
	set := output.Set{Name: "profiles", Columns: []output.Column{
		{Name: "name"}, {Name: "server"}, {Name: "database"}, {Name: "max_allow"},
	}}
	if profile == "" {
		loaded, err := d.load()
		if err != nil {
			return set, err
		}
		cfg = loaded
	}
	for _, c := range cfg.Profiles() {
		set.Rows = append(set.Rows, []*string{ptr(c.Profile), ptr(connect.Address(c)), ptr(c.Database),
			ptr(string(c.Ceiling()))})
	}
	set.TotalRowCount = len(set.Rows)
	return set, nil
}

// PermissionNeedCommands is every command path missing_permissions names,
// for the test that keeps them real.
func PermissionNeedCommands() []string {
	var out []string
	for _, n := range permissionNeeds {
		out = append(out, n.commands...)
	}
	return out
}
