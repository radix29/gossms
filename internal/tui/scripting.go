package tui

import (
	"cmp"
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// scripting.go is Object Explorer's "Script <Noun> as ▸" cascade: one table of
// which verbs each node type offers and how each is generated, plus the three
// destinations every verb can be sent to. A node type absent from the table
// offers no Script item, which keeps a folder, or an object gosmo can't script,
// out of the menu instead of an item that fails when clicked.
//
// Every generator runs on a background goroutine and takes a nodeData by value,
// not the *explorerNode, as objectOp's do: the UI goroutine writes node.data
// (see applyNodeFilter).

// scriptGen produces one object's script text.
type scriptGen func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error)

// scriptVerb is one row of the "Script <Noun> as ▸" submenu.
type scriptVerb struct {
	label string
	gen   scriptGen
}

// scriptable is what a node type offers: the noun the menu names it by and
// the verbs it can be scripted at.
type scriptable struct {
	noun  string
	verbs []scriptVerb
}

// scriptFn is a Scripter method bound to the node being scripted: the part of a
// DDL verb that differs per object family.
type scriptFn func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error)

// serverScriptFn is scriptFn for the two principals that belong to no
// database, and so are scripted by a ServerScripter.
type serverScriptFn func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error)

var (
	scriptTable scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptTable(ctx, n.Schema, n.Name)
	}
	scriptView scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptView(ctx, n.Schema, n.Name)
	}
	scriptProc scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptStoredProcedure(ctx, n.Schema, n.Name)
	}
	scriptFunc scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptFunction(ctx, n.Schema, n.Name)
	}
	scriptTrigger scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptTrigger(ctx, n.Schema, n.Name)
	}
	scriptSeq scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSequence(ctx, n.Schema, n.Name)
	}
	scriptSynonym scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSynonym(ctx, n.Schema, n.Name)
	}
	scriptSchema scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSchema(ctx, n.Name)
	}
	scriptUser scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptUser(ctx, n.Name)
	}
	scriptDBRole scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabaseRole(ctx, n.Name)
	}
	scriptDB scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabase(ctx)
	}

	// The table-scoped families name their table in TableName; Schema/Name
	// point at the index or constraint itself.
	scriptIndex scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptIndex(ctx, n.Schema, n.TableName, n.Name)
	}
	scriptCheck scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptCheckConstraint(ctx, n.Schema, n.TableName, n.Name)
	}
	scriptFK scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptForeignKey(ctx, n.Schema, n.TableName, n.Name)
	}
	scriptStatistic scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptStatistic(ctx, n.Schema, n.TableName, n.Name)
	}

	scriptPartFunc scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptPartitionFunction(ctx, n.Name)
	}
	scriptPartScheme scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptPartitionScheme(ctx, n.Name)
	}
	scriptSecPolicy scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSecurityPolicy(ctx, n.Schema, n.Name)
	}
	scriptCMK scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptColumnMasterKey(ctx, n.Name)
	}
	scriptCEK scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptColumnEncryptionKey(ctx, n.Name)
	}
	scriptDBAuditSpec scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabaseAuditSpecification(ctx, n.Name)
	}
	scriptDBTrigger scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabaseTrigger(ctx, n.Name)
	}
	scriptDBScopedCredential scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabaseScopedCredential(ctx, n.Name)
	}
	scriptCertificate scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptCertificate(ctx, n.Name)
	}
	scriptAsymmetricKey scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptAsymmetricKey(ctx, n.Name)
	}
	scriptSymmetricKey scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSymmetricKey(ctx, n.Name)
	}

	// The Programmability families added with the tree's missing folders. None has
	// an ALTER form (see gosmo's scripter_programmability.go), so every one is
	// wired with alter false.
	scriptUserDefinedDataType scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptUserDefinedDataType(ctx, n.Schema, n.Name)
	}
	scriptUserDefinedTableType scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptUserDefinedTableType(ctx, n.Schema, n.Name)
	}
	scriptClrType scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptClrType(ctx, n.Schema, n.Name)
	}
	scriptXMLSchemaCollection scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptXMLSchemaCollection(ctx, n.Schema, n.Name)
	}
	scriptRule scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptRule(ctx, n.Schema, n.Name)
	}
	scriptDefault scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDefault(ctx, n.Schema, n.Name)
	}
	// Assemblies, plan guides and the external resources are database-scoped, not
	// schema-scoped: each is addressed by a single name, and nodeData.Schema is
	// empty on all of them.
	scriptAssembly scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptAssembly(ctx, n.Name)
	}
	scriptPlanGuide scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptPlanGuide(ctx, n.Name)
	}
	scriptExternalDataSource scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptExternalDataSource(ctx, n.Name)
	}
	scriptExternalFileFormat scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptExternalFileFormat(ctx, n.Name)
	}
	scriptExternalLibrary scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptExternalLibrary(ctx, n.Name)
	}

	// The seven Service Broker families. Six are database-scoped and named by one
	// name; the queue is the only schema-scoped one and the only one whose script
	// method takes a schema.
	scriptMessageType scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptMessageType(ctx, n.Name)
	}
	scriptContract scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptContract(ctx, n.Name)
	}
	scriptBrokerQueue scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptBrokerQueue(ctx, n.Schema, n.Name)
	}
	scriptBrokerService scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptBrokerService(ctx, n.Name)
	}
	scriptRoute scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptRoute(ctx, n.Name)
	}
	scriptRemoteServiceBinding scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptRemoteServiceBinding(ctx, n.Name)
	}
	scriptBrokerPriority scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptBrokerPriority(ctx, n.Name)
	}

	scriptLogin serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptLogin(ctx, n.Name)
	}
	scriptServerRole serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptServerRole(ctx, n.Name)
	}
	scriptCredential serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptCredential(ctx, n.Name)
	}
	scriptServerAudit serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptServerAudit(ctx, n.Name)
	}
	scriptServerAuditSpecification serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptServerAuditSpecification(ctx, n.Name)
	}
	scriptBackupDevice serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptBackupDevice(ctx, n.Name)
	}
	scriptServerTrigger serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptServerTrigger(ctx, n.Name)
	}
	scriptEndpoint serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptEndpoint(ctx, n.Name)
	}
	scriptEventSession serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptEventSession(ctx, n.Name)
	}
	scriptResourceGovernor serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptResourceGovernor(ctx)
	}
	scriptResourcePool serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptResourcePool(ctx, n.Name)
	}
	scriptWorkloadGroup serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptWorkloadGroup(ctx, n.Name)
	}
	scriptExternalResourcePool serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptExternalResourcePool(ctx, n.Name)
	}
	scriptDatabaseMail serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabaseMail(ctx)
	}
)

// scriptables is the per-type table. Verb order follows SSMS: CREATE, ALTER,
// DROP, DROP And CREATE, then the DML templates.
var scriptables = map[NodeType]scriptable{
	// A database scripts only as CREATE: gosmo's ScriptDatabase emits the CREATE
	// plus its recovery/compatibility settings and has no DROP form (dropping a
	// database from a script is Delete's job).
	NodeDatabase: {"Database", []scriptVerb{{"CREATE To", ddl(gosmo.ScriptCreate, scriptDB)}}},

	NodeTable: {"Table", append(ddlVerbs(scriptTable, false), rowVerbs...)},
	NodeView:  {"View", append(ddlVerbs(scriptView, true), rowVerbs...)},
	NodeStoredProcedure: {"Stored Procedure", append(ddlVerbs(scriptProc, true),
		scriptVerb{"EXECUTE To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
			return s.ScriptExecute(ctx, n.Schema, n.Name)
		})})},
	NodeFunction: {"Function", append(ddlVerbs(scriptFunc, true),
		scriptVerb{"SELECT To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
			return s.ScriptFunctionCall(ctx, n.Schema, n.Name, n.FuncType)
		})})},
	NodeTrigger: {"Trigger", ddlVerbs(scriptTrigger, true)},

	NodeIndex:      {"Index", append(ddlVerbs(scriptIndex, false), indexMaintenanceVerbs...)},
	NodeKey:        {"Key", ddlVerbs(scriptIndex, false)},
	NodeCheck:      {"Constraint", ddlVerbs(scriptCheck, false)},
	NodeForeignKey: {"Foreign Key", ddlVerbs(scriptFK, false)},
	// An index's own statistic has no CREATE or DROP: gosmo refuses to script one,
	// naming the index to script instead.
	NodeStatistic: {"Statistics", append(ddlVerbs(scriptStatistic, false), statisticUpdateVerb)},
	NodeSequence:  {"Sequence", ddlVerbs(scriptSeq, false)},
	NodeSynonym:   {"Synonym", ddlVerbs(scriptSynonym, false)},

	NodeSchema:       {"Schema", ddlVerbs(scriptSchema, false)},
	NodeUser:         {"User", ddlVerbs(scriptUser, false)},
	NodeDatabaseRole: {"Database Role", ddlVerbs(scriptDBRole, false)},

	NodePartitionFunction:   {"Partition Function", ddlVerbs(scriptPartFunc, false)},
	NodePartitionScheme:     {"Partition Scheme", ddlVerbs(scriptPartScheme, false)},
	NodeSecurityPolicy:      {"Security Policy", ddlVerbs(scriptSecPolicy, false)},
	NodeColumnMasterKey:     {"Column Master Key", ddlVerbs(scriptCMK, false)},
	NodeColumnEncryptionKey: {"Column Encryption Key", ddlVerbs(scriptCEK, false)},
	// No ALTER verb: ALTER DATABASE AUDIT SPECIFICATION replaces the whole action
	// list rather than editing it, so a scripted ALTER would differ from the CREATE
	// beside it rather than vary it.
	NodeDatabaseAuditSpecification: {"Database Audit Specification", ddlVerbs(scriptDBAuditSpec, false)},
	NodeDatabaseTrigger:            {"Database Trigger", ddlVerbs(scriptDBTrigger, false)},
	// The secret is unreadable, so every verb carries a placeholder instead (see
	// gosmo's buildDatabaseScopedCredentialScript).
	NodeDatabaseScopedCredential: {"Database Scoped Credential", ddlVerbs(scriptDBScopedCredential, false)},
	// CREATE is FROM BINARY: the public certificate exactly, the private key not at
	// all (see gosmo's ScriptCertificate). No ALTER: the only ALTERs are
	// private-key operations, which nothing on screen reproduces.
	NodeCertificate: {"Certificate", ddlVerbs(scriptCertificate, false)},
	// CREATE generates a NEW key pair with this one's owner and algorithm (the key
	// material cannot be read back) and says so in a comment; see gosmo's
	// ScriptAsymmetricKey. No ALTER, for the certificate's reason.
	NodeAsymmetricKey: {"Asymmetric Key", ddlVerbs(scriptAsymmetricKey, false)},
	// CREATE makes a NEW key with this one's algorithm, owner and ENCRYPTION BY
	// list, passwords as placeholders; the same key only with KEY_SOURCE and
	// IDENTITY_VALUE, which cannot be read back (gosmo's ScriptSymmetricKey). No
	// ALTER: ADD/DROP ENCRYPTION needs the key open and is the Encryption page's
	// write, not a script of the object.
	NodeSymmetricKey: {"Symmetric Key", ddlVerbs(scriptSymmetricKey, false)},

	// Programmability > Types, Assemblies, Rules, Defaults, Plan Guides, and the
	// External Resources folder beside Views. No ALTER anywhere in this set; none
	// of these objects has one.
	//
	// System Data Types is deliberately absent: a built-in type is not something a
	// script creates, and SSMS offers nothing for one either.
	NodeUserDefinedDataType:  {"User-Defined Data Type", ddlVerbs(scriptUserDefinedDataType, false)},
	NodeUserDefinedTableType: {"User-Defined Table Type", ddlVerbs(scriptUserDefinedTableType, false)},
	NodeUserDefinedType:      {"User-Defined Type", ddlVerbs(scriptClrType, false)},
	NodeXMLSchemaCollection:  {"XML Schema Collection", ddlVerbs(scriptXMLSchemaCollection, false)},
	NodeRule:                 {"Rule", ddlVerbs(scriptRule, false)},
	NodeDefault:              {"Default", ddlVerbs(scriptDefault, false)},
	// The binary is elided like the credential secret (gosmo's buildAssemblyScript).
	NodeAssembly: {"Assembly", ddlVerbs(scriptAssembly, false)},
	// A plan guide's CREATE is an sp_create_plan_guide call and its DROP an
	// sp_control_plan_guide one; verbs are named for what they do, not the
	// statement behind them.
	NodePlanGuide:          {"Plan Guide", ddlVerbs(scriptPlanGuide, false)},
	NodeExternalDataSource: {"External Data Source", ddlVerbs(scriptExternalDataSource, false)},
	NodeExternalFileFormat: {"External File Format", ddlVerbs(scriptExternalFileFormat, false)},
	NodeExternalLibrary:    {"External Library", ddlVerbs(scriptExternalLibrary, false)},

	// Service Broker. CREATE and DROP only, with no ALTER even for the six
	// families that have one, as elsewhere in the tree; the queue's ALTER lives on
	// its Properties page.
	NodeMessageType:   {"Message Type", ddlVerbs(scriptMessageType, false)},
	NodeContract:      {"Contract", ddlVerbs(scriptContract, false)},
	NodeBrokerQueue:   {"Queue", ddlVerbs(scriptBrokerQueue, false)},
	NodeBrokerService: {"Service", ddlVerbs(scriptBrokerService, false)},
	NodeRoute:         {"Route", ddlVerbs(scriptRoute, false)},
	// The one family whose CREATE an Azure engine edition refuses outright; see
	// editionRefusesScriptVerb, which withholds the two verbs that emit it rather
	// than letting Msg 41906 come back.
	NodeRemoteServiceBinding: {"Remote Service Binding", ddlVerbs(scriptRemoteServiceBinding, false)},
	NodeBrokerPriority:       {"Broker Priority", ddlVerbs(scriptBrokerPriority, false)},

	NodeLogin:                    {"Login", serverDDLVerbs(scriptLogin)},
	NodeServerRole:               {"Server Role", serverDDLVerbs(scriptServerRole)},
	NodeCredential:               {"Credential", serverDDLVerbs(scriptCredential)},
	NodeAudit:                    {"Audit", serverDDLVerbs(scriptServerAudit)},
	NodeServerAuditSpecification: {"Server Audit Specification", serverDDLVerbs(scriptServerAuditSpecification)},
	NodeBackupDevice:             {"Backup Device", serverDDLVerbs(scriptBackupDevice)},
	NodeServerTrigger:            {"Server Trigger", serverDDLVerbs(scriptServerTrigger)},
	NodeEndpoint:                 {"Endpoint", serverDDLVerbs(scriptEndpoint)},
	// "Session", as SSMS words it. The running state is not scripted (gosmo's
	// buildEventSessionScript).
	NodeEventSession: {"Session", eventSessionScriptVerbs()},

	// The configuration scripts as ALTER only: it always exists, and DROP means
	// nothing for it. The script ends in RECONFIGURE or DISABLE per the stored
	// state (gosmo's ScriptResourceGovernor).
	NodeResourceGovernor: {"Resource Governor", []scriptVerb{{"ALTER To", serverDDL(gosmo.ScriptAlter, scriptResourceGovernor)}}},
	// A pool's or group's script ends in a comment, not RECONFIGURE, which would
	// also enable a disabled governor for whoever runs it. The built-ins, which
	// gosmo scripts as an ALTER and refuses to DROP, offer no Script item:
	// scriptMenuItems withholds it from every system node.
	NodeResourcePool:         {"Resource Pool", serverDDLVerbs(scriptResourcePool)},
	NodeWorkloadGroup:        {"Workload Group", serverDDLVerbs(scriptWorkloadGroup)},
	NodeExternalResourcePool: {"External Resource Pool", serverDDLVerbs(scriptExternalResourcePool)},

	// The whole configuration (accounts, profiles with their accounts and grants,
	// system parameters) as CREATE only: gosmo refuses DROP, since no one statement
	// removes a configuration. Passwords are placeholders. 'Database Mail XPs' is a
	// server option, not part of it.
	NodeDatabaseMail: {"Database Mail", []scriptVerb{{"CREATE To", serverDDL(gosmo.ScriptCreate, scriptDatabaseMail)}}},
}

// scriptReadRights gate a family's whole Script cascade on the right that makes
// what the script is built from visible, so a login without it isn't offered
// an item that fails in the status line. A family absent here is not gated: its
// node is listed only to a login that can read it.
//
// Resource Governor's node is the exception: always listed, but its
// configuration row is invisible without VIEW ANY DEFINITION (no row, no
// error), which VIEW SERVER STATE does not imply.
var scriptReadRights = map[NodeType][]gate.Right{
	NodeResourceGovernor: {gate.ViewAnyDefinition},
}

// indexMaintenanceVerbs are the three maintenance statements an index offers
// below its DDL ones (SSMS's Rebuild/Reorganize/Update Statistics), as a script
// rather than an immediate action.
var indexMaintenanceVerbs = []scriptVerb{
	{"REBUILD To", indexMaintenance(func(ctx context.Context, idx *gosmo.Index) error {
		return idx.Rebuild(ctx, gosmo.IndexRebuildOptions{})
	})},
	{"REORGANIZE To", indexMaintenance(func(ctx context.Context, idx *gosmo.Index) error {
		return idx.Reorganize(ctx)
	})},
	{"UPDATE STATISTICS To", indexMaintenance(func(ctx context.Context, idx *gosmo.Index) error {
		return idx.UpdateStatistics(ctx, 0)
	})},
}

// statisticUpdateVerb is the UPDATE STATISTICS script behind a statistic node's
// own "Update Statistics" item. A full scan, the same read Statistics
// Properties' Details page runs.
var statisticUpdateVerb = scriptVerb{"UPDATE STATISTICS To", func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
	t, err := findTable(ctx, sc, n.DBName, n.Schema, n.TableName)
	if err != nil {
		return "", err
	}
	st, err := t.StatisticByName(ctx, n.Name)
	if err != nil {
		return "", err
	}
	return collectScript(ctx, func(ctx context.Context) error { return st.Update(ctx, 0) })
}}

// indexMaintenance binds one of gosmo's index maintenance methods as a script
// generator. The statement is gosmo's own, collected rather than executed, so
// the script read is what gossms would run; a second copy here would drift.
func indexMaintenance(f func(ctx context.Context, idx *gosmo.Index) error) scriptGen {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		t, err := findTable(ctx, sc, n.DBName, n.Schema, n.TableName)
		if err != nil {
			return "", err
		}
		idx, err := t.IndexByName(ctx, n.Name)
		if err != nil {
			return "", err
		}
		return collectScript(ctx, func(ctx context.Context) error { return f(ctx, idx) })
	}
}

// collectScript runs one gosmo write under gosmo.WithScript and returns the
// statements it would have executed.
func collectScript(ctx context.Context, write func(context.Context) error) (string, error) {
	scriptCtx, script := gosmo.WithScript(ctx)
	if err := write(scriptCtx); err != nil {
		return "", err
	}
	return script.String(), nil
}

// rowVerbs are the four row-level templates a table or view offers.
var rowVerbs = []scriptVerb{
	{"SELECT To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSelect(ctx, n.Schema, n.Name)
	})},
	{"INSERT To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptInsert(ctx, n.Schema, n.Name)
	})},
	{"UPDATE To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptUpdate(ctx, n.Schema, n.Name)
	})},
	{"DELETE To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDelete(ctx, n.Schema, n.Name)
	})},
}

// ddlVerbs is the standard DDL set one Scripter method covers, with ALTER
// included only for the module objects that have an ALTER form.
func ddlVerbs(f scriptFn, alter bool) []scriptVerb {
	verbs := []scriptVerb{{"CREATE To", ddl(gosmo.ScriptCreate, f)}}
	if alter {
		verbs = append(verbs, scriptVerb{"ALTER To", ddl(gosmo.ScriptAlter, f)})
	}
	return append(verbs,
		scriptVerb{"DROP To", ddl(gosmo.ScriptDrop, f)},
		scriptVerb{"DROP And CREATE To", ddl(gosmo.ScriptDropAndCreate, f)})
}

// serverDDLVerbs is ddlVerbs for a server-level principal.
func serverDDLVerbs(f serverScriptFn) []scriptVerb {
	return []scriptVerb{
		{"CREATE To", serverDDL(gosmo.ScriptCreate, f)},
		{"DROP To", serverDDL(gosmo.ScriptDrop, f)},
		{"DROP And CREATE To", serverDDL(gosmo.ScriptDropAndCreate, f)},
	}
}

// eventSessionScriptVerbs is serverDDLVerbs for an event session, which is the
// server's or, on Azure SQL Database, a database's (xeScope): the node's DBName
// picks the scripter.
func eventSessionScriptVerbs() []scriptVerb {
	verbs := serverDDLVerbs(scriptEventSession)
	for i, v := range []gosmo.ScriptVerb{gosmo.ScriptCreate, gosmo.ScriptDrop, gosmo.ScriptDropAndCreate} {
		server, database := verbs[i].gen, ddl(v, func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
			return s.ScriptEventSession(ctx, n.Name)
		})
		verbs[i].gen = func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
			if n.DBName != "" {
				return database(ctx, sc, n)
			}
			return server(ctx, sc, n)
		}
	}
	return verbs
}

// ddl binds a Scripter method to one verb.
func ddl(v gosmo.ScriptVerb, f scriptFn) scriptGen {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		d, err := sc.Server.DatabaseByName(ctx, n.DBName)
		if err != nil {
			return "", err
		}
		opts := gosmo.DefaultScriptOptions()
		opts.Verb = v
		return f(gosmo.NewScripter(d, opts), ctx, n)
	}
}

// dml binds one of the row-level templates, which have no verb of their own.
func dml(f scriptFn) scriptGen { return ddl(gosmo.ScriptCreate, f) }

// serverDDL is ddl for a ServerScripter.
func serverDDL(v gosmo.ScriptVerb, f serverScriptFn) scriptGen {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		opts := gosmo.DefaultScriptOptions()
		opts.Verb = v
		return f(gosmo.NewServerScripter(sc.Server, opts), ctx, n)
	}
}

// scriptMenuItems is the "Script <Noun> as ▸" cascade for a node, or nil when
// its type has nothing to script. Spliced into every node's menu by
// contextMenuItemsForNode, so no per-type branch mentions scripting.
func (a *App) scriptMenuItems(node *explorerNode) []controls.MenuItem {
	s, ok := scriptables[node.data.Type]
	if !ok {
		return nil
	}
	// A system object's definition lives in the resource database, which
	// sys.sql_modules in a user database doesn't expose, so scripting sys.objects
	// or sys.sp_executesql can only fail and the item isn't offered. A system
	// database is the exception: its CREATE script is assembled from metadata
	// gossms can read, as a user database's is.
	if node.data.IsSystem && node.data.Type != NodeDatabase {
		return nil
	}
	sc := resolveConn(node)
	verbs := make([]controls.MenuItem, 0, len(s.verbs))
	for _, v := range s.verbs {
		item := controls.MenuItem{Label: v.label, Sub: a.scriptDestinations(sc, node.data, v)}
		if editionRefusesScriptVerb(node.data.Type, v.label) {
			item = gateAzure(item, sc)
		}
		verbs = append(verbs, item)
	}
	item := controls.MenuItem{Label: "Script " + s.noun + " as", Sub: verbs}
	if rights := scriptReadRights[node.data.Type]; len(rights) > 0 {
		item = gate.Item(item, sc, "", rights...)
	}
	return []controls.MenuItem{item}
}

// scriptDestinations is the third cascade level: where the generated script
// goes. Each generates first and only then commits to the destination, so a
// failure reports itself without having prompted for a file or overwritten the
// clipboard.
func (a *App) scriptDestinations(sc *db.ServerConn, n nodeData, v scriptVerb) []controls.MenuItem {
	return []controls.MenuItem{
		{Label: "New Query Editor Window", Action: func() {
			a.generateScript(sc, n, v, func(text string) { a.openQueryWithText(sc, n.DBName, text) })
		}},
		{Label: "File...", Action: func() {
			a.generateScript(sc, n, v, func(text string) { a.saveScriptAs(n, text) })
		}},
		{Label: "Clipboard", Action: func() {
			a.generateScript(sc, n, v, func(text string) { a.copyWithStatus(text) })
		}},
	}
}

// generateScript runs one verb's generator in the background and hands the
// text to then on the UI goroutine.
func (a *App) generateScript(sc *db.ServerConn, n nodeData, v scriptVerb, then func(string)) {
	if !a.requireConn(sc) {
		return
	}
	a.setStatus("Scripting " + cmp.Or(n.Name, scriptables[n.Type].noun) + "...")
	// safegoRepair, not safego: the status line is latched to "Scripting..." before
	// the goroutine starts and only the posted callback clears it, so a panic would
	// leave the app claiming to still be working.
	a.safegoRepair("scripting an object", func() { a.setStatus("") }, func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		text, err := v.gen(ctx, sc, n)
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Script error: %v", err))
				return
			}
			a.setStatus("")
			then(text)
		})
	})
}

// saveScriptAs prompts for a path and writes the generated script to it.
// LF-separated UTF-8, the shape a gossms-generated script has; there is no
// source file whose encoding could be preserved (see writeQueryFile).
func (a *App) saveScriptAs(n nodeData, text string) {
	a.fileDialog.ShowSave("Script To File", scriptFileName(n), func(path string) {
		if err := fileutil.WriteAtomic(path, []byte(text), 0o644); err != nil {
			a.setStatus(fmt.Sprintf("Save failed: %v", err))
			return
		}
		a.setStatus("Saved to " + path)
	})
}

// scriptFileName is the name the save prompt starts on: the object's, as SSMS
// proposes one.
//
// A singleton node has no name (Resource Governor, Database Mail) and is
// proposed under its noun, not as ".sql".
func scriptFileName(n nodeData) string {
	switch {
	case n.Schema != "":
		return n.Schema + "." + n.Name + ".sql"
	case n.Name == "":
		return scriptables[n.Type].noun + ".sql"
	}
	return n.Name + ".sql"
}
