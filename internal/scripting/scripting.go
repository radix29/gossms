package scripting

import (
	"context"
	"errors"
	"fmt"
	"slices"

	gosmo "github.com/radix29/gosmo"
)

// Family is one kind of object Script As covers.
type Family int

// The families, in the order Object Explorer lists them.
const (
	Database Family = iota + 1

	Table
	View
	StoredProcedure
	Function
	Trigger

	Index
	Key
	Check
	ForeignKey
	Statistic
	Sequence
	Synonym

	Schema
	User
	DatabaseRole

	PartitionFunction
	PartitionScheme
	SecurityPolicy
	ColumnMasterKey
	ColumnEncryptionKey
	DatabaseAuditSpecification
	DatabaseTrigger
	DatabaseScopedCredential
	Certificate
	AsymmetricKey
	SymmetricKey

	UserDefinedDataType
	UserDefinedTableType
	UserDefinedType
	XMLSchemaCollection
	Rule
	Default
	Assembly
	PlanGuide
	ExternalDataSource
	ExternalFileFormat
	ExternalLibrary

	MessageType
	Contract
	BrokerQueue
	BrokerService
	Route
	RemoteServiceBinding
	BrokerPriority

	FullTextCatalog
	FullTextStoplist
	SearchPropertyList

	Login
	ServerRole
	Credential
	Audit
	ServerAuditSpecification
	BackupDevice
	ServerTrigger
	Endpoint
	EventSession
	ResourceGovernor
	ResourcePool
	WorkloadGroup
	ExternalResourcePool
	DatabaseMail
)

// Verb is what a script does with its object: one of gosmo's DDL verbs, a
// row-level template, or a maintenance statement.
type Verb int

const (
	Create Verb = iota + 1
	Alter
	Drop
	DropAndCreate

	Select
	Insert
	Update
	Delete
	Execute

	Rebuild
	Reorganize
	UpdateStatistics
)

// Target names the object to script.
//
// The table-scoped families (index, key, check, foreign key, statistic) name
// their table or view in TableName; Schema/Name point at the index or
// constraint itself. A database-scoped family leaves Schema empty, and a
// server-level one DBName too — except an event session, whose DBName set
// means a database-scoped session (Azure SQL Database).
type Target struct {
	DBName    string
	Schema    string
	Name      string
	TableName string
	// FuncType is a function's sys.objects type: which call template its
	// SELECT verb emits.
	FuncType gosmo.FunctionType
}

// Generator produces one object's script text.
type Generator func(ctx context.Context, srv *gosmo.Server, t Target) (string, error)

// verbGen is one verb a family offers and how it is generated.
type verbGen struct {
	verb Verb
	gen  Generator
}

// Verbs is the verbs f offers, in SSMS's order: CREATE, ALTER, DROP, DROP And
// CREATE, then the DML templates and maintenance statements. Nil when f has
// nothing to script.
func Verbs(f Family) []Verb {
	vs := families[f]
	if vs == nil {
		return nil
	}
	out := make([]Verb, len(vs))
	for i, v := range vs {
		out[i] = v.verb
	}
	return out
}

// For is f's generator for v, or nil when f does not offer v.
func For(f Family, v Verb) Generator {
	for _, vg := range families[f] {
		if vg.verb == v {
			return vg.gen
		}
	}
	return nil
}

// ErrNotOffered is returned by Script for a verb the family does not offer.
var ErrNotOffered = errors.New("not offered for this object family")

// Script generates t's script at v.
func Script(ctx context.Context, srv *gosmo.Server, f Family, v Verb, t Target) (string, error) {
	gen := For(f, v)
	if gen == nil {
		return "", fmt.Errorf("script verb %d: %w", v, ErrNotOffered)
	}
	return gen(ctx, srv, t)
}

// Offered reports whether an object of family f is scripted at all. A system
// object's definition lives in the resource database, which sys.sql_modules in
// a user database doesn't expose, so scripting sys.objects or
// sys.sp_executesql can only fail. A system database is the exception: its
// CREATE script is assembled from metadata gossms can read, as a user
// database's is. The built-in resource pools and workload groups, which gosmo
// scripts as an ALTER and refuses to DROP, are system objects too.
func Offered(f Family, isSystem bool) bool {
	return families[f] != nil && (!isSystem || f == Database)
}

// azureRefused lists the verbs whose script an Azure engine edition refuses.
// CREATE REMOTE SERVICE BINDING is refused by a Managed Instance at *compile*
// time (Msg 41906), which aborts the whole batch before anything in it runs.
// ALTER and DROP REMOTE SERVICE BINDING are *not* refused there, nor is the
// catalog view, so the family ships on MI with only these two verbs missing.
//
// Both verbs that emit the CREATE are named: DROP And CREATE carries it too,
// and the DROP half succeeding first would leave the binding gone and the
// script half-run if this were treated as a runtime error.
var azureRefused = map[Family][]Verb{
	RemoteServiceBinding: {Create, DropAndCreate},
}

// AzureRefuses reports whether f's script at v emits a statement an Azure
// engine edition refuses. The connection is not consulted: the caller knows
// whether it is on Azure.
func AzureRefuses(f Family, v Verb) bool {
	return slices.Contains(azureRefused[f], v)
}

// Collect runs one gosmo write under gosmo.WithScript and returns the
// statements it would have executed.
func Collect(ctx context.Context, write func(context.Context) error) (string, error) {
	scriptCtx, script := gosmo.WithScript(ctx)
	if err := write(scriptCtx); err != nil {
		return "", err
	}
	return script.String(), nil
}

// Indexed is a table or a view: what an index or a statistic is on.
type Indexed interface {
	IndexByName(context.Context, string) (*gosmo.Index, error)
	StatisticByName(context.Context, string) (*gosmo.Statistic, error)
}

// FindIndexed resolves dbName/schema/name to the *gosmo.Table or *gosmo.View
// an index or a statistic is on. Tables and views share one namespace, so a
// name that is no table is read as a view: one read for a table, two for a
// view.
func FindIndexed(ctx context.Context, srv *gosmo.Server, dbName, schema, name string) (Indexed, error) {
	d, err := srv.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	t, err := d.TableByName(ctx, schema, name)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, gosmo.ErrNotFound) {
		return nil, err
	}
	v, err := d.ViewByName(ctx, schema, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, fmt.Errorf("no table or view %s in %q: %w", gosmo.QualifiedName(schema, name), dbName, err)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// scriptFn is a Scripter method bound to the object being scripted: the part
// of a DDL verb that differs per object family.
type scriptFn func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error)

// serverScriptFn is scriptFn for the objects that belong to no database, and
// so are scripted by a ServerScripter.
type serverScriptFn func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error)

var (
	scriptTable scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptTable(ctx, t.Schema, t.Name)
	}
	scriptView scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptView(ctx, t.Schema, t.Name)
	}
	scriptProc scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptStoredProcedure(ctx, t.Schema, t.Name)
	}
	scriptFunc scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptFunction(ctx, t.Schema, t.Name)
	}
	scriptTrigger scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptTrigger(ctx, t.Schema, t.Name)
	}
	scriptSeq scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSequence(ctx, t.Schema, t.Name)
	}
	scriptSynonym scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSynonym(ctx, t.Schema, t.Name)
	}
	scriptSchema scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSchema(ctx, t.Name)
	}
	scriptUser scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptUser(ctx, t.Name)
	}
	scriptDBRole scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDatabaseRole(ctx, t.Name)
	}
	scriptDB scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDatabase(ctx)
	}

	scriptIndex scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptIndex(ctx, t.Schema, t.TableName, t.Name)
	}
	scriptCheck scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptCheckConstraint(ctx, t.Schema, t.TableName, t.Name)
	}
	scriptFK scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptForeignKey(ctx, t.Schema, t.TableName, t.Name)
	}
	scriptStatistic scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptStatistic(ctx, t.Schema, t.TableName, t.Name)
	}

	scriptPartFunc scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptPartitionFunction(ctx, t.Name)
	}
	scriptPartScheme scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptPartitionScheme(ctx, t.Name)
	}
	scriptSecPolicy scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSecurityPolicy(ctx, t.Schema, t.Name)
	}
	scriptCMK scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptColumnMasterKey(ctx, t.Name)
	}
	scriptCEK scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptColumnEncryptionKey(ctx, t.Name)
	}
	scriptDBAuditSpec scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDatabaseAuditSpecification(ctx, t.Name)
	}
	scriptDBTrigger scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDatabaseTrigger(ctx, t.Name)
	}
	scriptDBScopedCredential scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDatabaseScopedCredential(ctx, t.Name)
	}
	scriptCertificate scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptCertificate(ctx, t.Name)
	}
	scriptAsymmetricKey scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptAsymmetricKey(ctx, t.Name)
	}
	scriptSymmetricKey scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSymmetricKey(ctx, t.Name)
	}

	// The Programmability families. None has an ALTER form (see gosmo's
	// scripter_programmability.go), so every one is wired with alter false.
	scriptUserDefinedDataType scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptUserDefinedDataType(ctx, t.Schema, t.Name)
	}
	scriptUserDefinedTableType scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptUserDefinedTableType(ctx, t.Schema, t.Name)
	}
	scriptClrType scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptClrType(ctx, t.Schema, t.Name)
	}
	scriptXMLSchemaCollection scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptXMLSchemaCollection(ctx, t.Schema, t.Name)
	}
	scriptRule scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptRule(ctx, t.Schema, t.Name)
	}
	scriptDefault scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDefault(ctx, t.Schema, t.Name)
	}
	// Assemblies, plan guides and the external resources are database-scoped,
	// not schema-scoped: each is addressed by a single name, and Target.Schema
	// is empty on all of them.
	scriptAssembly scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptAssembly(ctx, t.Name)
	}
	scriptPlanGuide scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptPlanGuide(ctx, t.Name)
	}
	scriptExternalDataSource scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptExternalDataSource(ctx, t.Name)
	}
	scriptExternalFileFormat scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptExternalFileFormat(ctx, t.Name)
	}
	scriptExternalLibrary scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptExternalLibrary(ctx, t.Name)
	}

	// The seven Service Broker families. Six are database-scoped and named by
	// one name; the queue is the only schema-scoped one and the only one whose
	// script method takes a schema.
	scriptMessageType scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptMessageType(ctx, t.Name)
	}
	scriptContract scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptContract(ctx, t.Name)
	}
	scriptBrokerQueue scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptBrokerQueue(ctx, t.Schema, t.Name)
	}
	scriptBrokerService scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptBrokerService(ctx, t.Name)
	}
	scriptRoute scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptRoute(ctx, t.Name)
	}
	scriptRemoteServiceBinding scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptRemoteServiceBinding(ctx, t.Name)
	}
	scriptBrokerPriority scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptBrokerPriority(ctx, t.Name)
	}

	// The three full-text families, each named by one name. A stoplist's or
	// property list's script carries one ADD per word or property.
	scriptFullTextCatalog scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptFullTextCatalog(ctx, t.Name)
	}
	scriptFullTextStoplist scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptFullTextStoplist(ctx, t.Name)
	}
	scriptSearchPropertyList scriptFn = func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSearchPropertyList(ctx, t.Name)
	}

	scriptLogin serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptLogin(ctx, t.Name)
	}
	scriptServerRole serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptServerRole(ctx, t.Name)
	}
	scriptCredential serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptCredential(ctx, t.Name)
	}
	scriptServerAudit serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptServerAudit(ctx, t.Name)
	}
	scriptServerAuditSpecification serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptServerAuditSpecification(ctx, t.Name)
	}
	scriptBackupDevice serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptBackupDevice(ctx, t.Name)
	}
	scriptServerTrigger serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptServerTrigger(ctx, t.Name)
	}
	scriptEndpoint serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptEndpoint(ctx, t.Name)
	}
	scriptEventSession serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptEventSession(ctx, t.Name)
	}
	scriptResourceGovernor serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptResourceGovernor(ctx)
	}
	scriptResourcePool serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptResourcePool(ctx, t.Name)
	}
	scriptWorkloadGroup serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptWorkloadGroup(ctx, t.Name)
	}
	scriptExternalResourcePool serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptExternalResourcePool(ctx, t.Name)
	}
	scriptDatabaseMail serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDatabaseMail(ctx)
	}
)

// families is the per-family table. Verb order follows SSMS: CREATE, ALTER,
// DROP, DROP And CREATE, then the DML templates.
var families = map[Family][]verbGen{
	// A database has no ALTER that restates it. Its CREATE carries the
	// collation, recovery model and compatibility level only, not the files.
	Database: {
		{Create, scriptIn(gosmo.ScriptCreate, scriptDB, false)},
		{Drop, scriptIn(gosmo.ScriptDrop, scriptDB, false)},
		{DropAndCreate, scriptIn(gosmo.ScriptDropAndCreate, scriptDB, false)},
	},

	Table: append(ddlVerbs(scriptTable, false), rowVerbs...),
	View:  append(ddlVerbs(scriptView, true), rowVerbs...),
	StoredProcedure: append(ddlVerbs(scriptProc, true),
		verbGen{Execute, dml(func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
			return s.ScriptExecute(ctx, t.Schema, t.Name)
		})}),
	Function: append(ddlVerbs(scriptFunc, true),
		verbGen{Select, dml(func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
			return s.ScriptFunctionCall(ctx, t.Schema, t.Name, t.FuncType)
		})}),
	Trigger: ddlVerbs(scriptTrigger, true),

	Index:      append(ddlVerbs(scriptIndex, false), indexMaintenanceVerbs...),
	Key:        ddlVerbs(scriptIndex, false),
	Check:      ddlVerbs(scriptCheck, false),
	ForeignKey: ddlVerbs(scriptFK, false),
	// An index's own statistic has no CREATE or DROP: gosmo refuses to script
	// one, naming the index to script instead.
	Statistic: append(ddlVerbs(scriptStatistic, false), statisticUpdateVerb),
	Sequence:  ddlVerbs(scriptSeq, false),
	Synonym:   ddlVerbs(scriptSynonym, false),

	Schema:       ddlVerbs(scriptSchema, false),
	User:         ddlVerbs(scriptUser, false),
	DatabaseRole: ddlVerbs(scriptDBRole, false),

	PartitionFunction:   ddlVerbs(scriptPartFunc, false),
	PartitionScheme:     ddlVerbs(scriptPartScheme, false),
	SecurityPolicy:      ddlVerbs(scriptSecPolicy, false),
	ColumnMasterKey:     ddlVerbs(scriptCMK, false),
	ColumnEncryptionKey: ddlVerbs(scriptCEK, false),
	// No ALTER verb: ALTER DATABASE AUDIT SPECIFICATION replaces the whole
	// action list rather than editing it, so a scripted ALTER would differ from
	// the CREATE beside it rather than vary it.
	DatabaseAuditSpecification: ddlVerbs(scriptDBAuditSpec, false),
	DatabaseTrigger:            ddlVerbs(scriptDBTrigger, false),
	// The secret is unreadable, so every verb carries a placeholder instead
	// (see gosmo's buildDatabaseScopedCredentialScript).
	DatabaseScopedCredential: ddlVerbs(scriptDBScopedCredential, false),
	// CREATE is FROM BINARY: the public certificate exactly, the private key not
	// at all (see gosmo's ScriptCertificate). No ALTER: the only ALTERs are
	// private-key operations, which nothing on screen reproduces.
	Certificate: ddlVerbs(scriptCertificate, false),
	// CREATE generates a NEW key pair with this one's owner and algorithm (the
	// key material cannot be read back) and says so in a comment; see gosmo's
	// ScriptAsymmetricKey. No ALTER, for the certificate's reason.
	AsymmetricKey: ddlVerbs(scriptAsymmetricKey, false),
	// CREATE makes a NEW key with this one's algorithm, owner and ENCRYPTION BY
	// list, passwords as placeholders; the same key only with KEY_SOURCE and
	// IDENTITY_VALUE, which cannot be read back (gosmo's ScriptSymmetricKey). No
	// ALTER: ADD/DROP ENCRYPTION needs the key open and is the Encryption page's
	// write, not a script of the object.
	SymmetricKey: ddlVerbs(scriptSymmetricKey, false),

	// Programmability > Types, Assemblies, Rules, Defaults, Plan Guides, and
	// the External Resources. No ALTER anywhere in this set; none of these
	// objects has one.
	//
	// System data types are deliberately absent: a built-in type is not
	// something a script creates, and SSMS offers nothing for one either.
	UserDefinedDataType:  ddlVerbs(scriptUserDefinedDataType, false),
	UserDefinedTableType: ddlVerbs(scriptUserDefinedTableType, false),
	UserDefinedType:      ddlVerbs(scriptClrType, false),
	XMLSchemaCollection:  ddlVerbs(scriptXMLSchemaCollection, false),
	Rule:                 ddlVerbs(scriptRule, false),
	Default:              ddlVerbs(scriptDefault, false),
	// The binary is elided like the credential secret (gosmo's
	// buildAssemblyScript).
	Assembly: ddlVerbs(scriptAssembly, false),
	// A plan guide's CREATE is an sp_create_plan_guide call and its DROP an
	// sp_control_plan_guide one; verbs are named for what they do, not the
	// statement behind them.
	PlanGuide:          ddlVerbs(scriptPlanGuide, false),
	ExternalDataSource: ddlVerbs(scriptExternalDataSource, false),
	ExternalFileFormat: ddlVerbs(scriptExternalFileFormat, false),
	ExternalLibrary:    ddlVerbs(scriptExternalLibrary, false),

	// Service Broker. CREATE and DROP only, with no ALTER even for the six
	// families that have one, as elsewhere; the queue's ALTER lives on its
	// Properties page.
	MessageType:   ddlVerbs(scriptMessageType, false),
	Contract:      ddlVerbs(scriptContract, false),
	BrokerQueue:   ddlVerbs(scriptBrokerQueue, false),
	BrokerService: ddlVerbs(scriptBrokerService, false),
	Route:         ddlVerbs(scriptRoute, false),
	// The one family whose CREATE an Azure engine edition refuses outright; see
	// AzureRefuses.
	RemoteServiceBinding: ddlVerbs(scriptRemoteServiceBinding, false),
	BrokerPriority:       ddlVerbs(scriptBrokerPriority, false),

	// Full-text. No ALTER: a catalog's ALTERs are REBUILD/REORGANIZE/AS DEFAULT
	// and a stoplist's or list's are one ADD or DROP each, none a script of the
	// object.
	FullTextCatalog:    ddlVerbs(scriptFullTextCatalog, false),
	FullTextStoplist:   ddlVerbs(scriptFullTextStoplist, false),
	SearchPropertyList: ddlVerbs(scriptSearchPropertyList, false),

	Login:                    serverDDLVerbs(scriptLogin),
	ServerRole:               serverDDLVerbs(scriptServerRole),
	Credential:               serverDDLVerbs(scriptCredential),
	Audit:                    serverDDLVerbs(scriptServerAudit),
	ServerAuditSpecification: serverDDLVerbs(scriptServerAuditSpecification),
	BackupDevice:             serverDDLVerbs(scriptBackupDevice),
	ServerTrigger:            serverDDLVerbs(scriptServerTrigger),
	Endpoint:                 serverDDLVerbs(scriptEndpoint),
	// The running state is not scripted (gosmo's buildEventSessionScript).
	EventSession: eventSessionVerbs(),

	// The configuration scripts as ALTER only: it always exists, and DROP means
	// nothing for it. The script ends in RECONFIGURE or DISABLE per the stored
	// state (gosmo's ScriptResourceGovernor).
	ResourceGovernor: {{Alter, serverDDL(gosmo.ScriptAlter, scriptResourceGovernor)}},
	// A pool's or group's script ends in a comment, not RECONFIGURE, which
	// would also enable a disabled governor for whoever runs it. The built-ins,
	// which gosmo scripts as an ALTER and refuses to DROP, are not offered (see
	// Offered).
	ResourcePool:         serverDDLVerbs(scriptResourcePool),
	WorkloadGroup:        serverDDLVerbs(scriptWorkloadGroup),
	ExternalResourcePool: serverDDLVerbs(scriptExternalResourcePool),

	// The whole configuration (accounts, profiles with their accounts and
	// grants, system parameters) as CREATE only: gosmo refuses DROP, since no
	// one statement removes a configuration. Passwords are placeholders.
	// 'Database Mail XPs' is a server option, not part of it.
	DatabaseMail: {{Create, serverDDL(gosmo.ScriptCreate, scriptDatabaseMail)}},
}

// indexMaintenanceVerbs are the three maintenance statements an index offers
// below its DDL ones (SSMS's Rebuild/Reorganize/Update Statistics), as a
// script rather than an immediate action.
var indexMaintenanceVerbs = []verbGen{
	{Rebuild, indexMaintenance(func(ctx context.Context, idx *gosmo.Index) error {
		return idx.Rebuild(ctx, gosmo.IndexRebuildOptions{})
	})},
	{Reorganize, indexMaintenance(func(ctx context.Context, idx *gosmo.Index) error {
		return idx.Reorganize(ctx)
	})},
	{UpdateStatistics, indexMaintenance(func(ctx context.Context, idx *gosmo.Index) error {
		return idx.UpdateStatistics(ctx, 0)
	})},
}

// statisticUpdateVerb is a statistic's UPDATE STATISTICS script. A full scan,
// the same read Statistics Properties' Details page runs.
var statisticUpdateVerb = verbGen{UpdateStatistics, func(ctx context.Context, srv *gosmo.Server, t Target) (string, error) {
	o, err := FindIndexed(ctx, srv, t.DBName, t.Schema, t.TableName)
	if err != nil {
		return "", err
	}
	st, err := o.StatisticByName(ctx, t.Name)
	if err != nil {
		return "", err
	}
	return Collect(ctx, func(ctx context.Context) error { return st.Update(ctx, 0) })
}}

// indexMaintenance binds one of gosmo's index maintenance methods as a script
// generator. The statement is gosmo's own, collected rather than executed, so
// the script read is what gossms would run; a second copy here would drift.
func indexMaintenance(f func(ctx context.Context, idx *gosmo.Index) error) Generator {
	return func(ctx context.Context, srv *gosmo.Server, t Target) (string, error) {
		o, err := FindIndexed(ctx, srv, t.DBName, t.Schema, t.TableName)
		if err != nil {
			return "", err
		}
		idx, err := o.IndexByName(ctx, t.Name)
		if err != nil {
			return "", err
		}
		return Collect(ctx, func(ctx context.Context) error { return f(ctx, idx) })
	}
}

// rowVerbs are the four row-level templates a table or view offers.
var rowVerbs = []verbGen{
	{Select, dml(func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptSelect(ctx, t.Schema, t.Name)
	})},
	{Insert, dml(func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptInsert(ctx, t.Schema, t.Name)
	})},
	{Update, dml(func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptUpdate(ctx, t.Schema, t.Name)
	})},
	{Delete, dml(func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
		return s.ScriptDelete(ctx, t.Schema, t.Name)
	})},
}

// ddlVerbs is the standard DDL set one Scripter method covers, with ALTER
// included only for the module objects that have an ALTER form.
func ddlVerbs(f scriptFn, alter bool) []verbGen {
	verbs := []verbGen{{Create, ddl(gosmo.ScriptCreate, f)}}
	if alter {
		verbs = append(verbs, verbGen{Alter, ddl(gosmo.ScriptAlter, f)})
	}
	return append(verbs,
		verbGen{Drop, ddl(gosmo.ScriptDrop, f)},
		verbGen{DropAndCreate, ddl(gosmo.ScriptDropAndCreate, f)})
}

// serverDDLVerbs is ddlVerbs for a server-level object.
func serverDDLVerbs(f serverScriptFn) []verbGen {
	return []verbGen{
		{Create, serverDDL(gosmo.ScriptCreate, f)},
		{Drop, serverDDL(gosmo.ScriptDrop, f)},
		{DropAndCreate, serverDDL(gosmo.ScriptDropAndCreate, f)},
	}
}

// eventSessionVerbs is serverDDLVerbs for an event session, which is the
// server's or, on Azure SQL Database, a database's: the target's DBName picks
// the scripter.
func eventSessionVerbs() []verbGen {
	verbs := serverDDLVerbs(scriptEventSession)
	for i, v := range []gosmo.ScriptVerb{gosmo.ScriptCreate, gosmo.ScriptDrop, gosmo.ScriptDropAndCreate} {
		server, database := verbs[i].gen, ddl(v, func(s *gosmo.Scripter, ctx context.Context, t Target) (string, error) {
			return s.ScriptEventSession(ctx, t.Name)
		})
		verbs[i].gen = func(ctx context.Context, srv *gosmo.Server, t Target) (string, error) {
			if t.DBName != "" {
				return database(ctx, srv, t)
			}
			return server(ctx, srv, t)
		}
	}
	return verbs
}

// ddl binds a Scripter method to one verb.
//
// The script opens with USE [db] for every destination, as SSMS's does: a
// CREATE pasted into a window on master otherwise lands in master, and a DROP
// copied from one database and run in another drops that database's namesake.
// A database's own script switches to master itself, so takes none (scriptIn
// with use false).
func ddl(v gosmo.ScriptVerb, f scriptFn) Generator { return scriptIn(v, f, true) }

// scriptIn is ddl with the USE prefix chosen by use.
func scriptIn(v gosmo.ScriptVerb, f scriptFn, use bool) Generator {
	return func(ctx context.Context, srv *gosmo.Server, t Target) (string, error) {
		d, err := srv.DatabaseByName(ctx, t.DBName)
		if err != nil {
			return "", err
		}
		opts := gosmo.DefaultScriptOptions()
		opts.Verb = v
		s := gosmo.NewScripter(d, opts)
		text, err := f(s, ctx, t)
		if err != nil || !use {
			return text, err
		}
		return s.DatabaseContext() + text, nil
	}
}

// dml binds one of the row-level templates, which have no verb of their own.
func dml(f scriptFn) Generator { return ddl(gosmo.ScriptCreate, f) }

// serverDDL is ddl for a ServerScripter.
func serverDDL(v gosmo.ScriptVerb, f serverScriptFn) Generator {
	return func(ctx context.Context, srv *gosmo.Server, t Target) (string, error) {
		opts := gosmo.DefaultScriptOptions()
		opts.Verb = v
		return f(gosmo.NewServerScripter(srv, opts), ctx, t)
	}
}
