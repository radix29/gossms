package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// explorer_object_ops.go is Object Explorer's general Delete and Rename: one
// table of what they mean per node type. A type absent from it offers
// neither (folders, anything gosmo can't drop).
//
// The menu pair is in explorer_object_menu.go, what permits each action in
// explorer_object_rights.go, and the confirm/run/refresh flows around them in
// explorer_object_actions.go.
//
// SQL Server Agent objects are renameable here but keep their own Delete (see
// agent_menu.go).

// objectOp is what Delete and Rename do for one node type. A nil drop or rename
// means the node doesn't offer that action.
//
// Both take a nodeData by value, not the *explorerNode: they run on a
// background goroutine while the UI goroutine writes node.data.
// deleteObject/runRename copy before the safego.
type objectOp struct {
	// noun names the object in dialog titles and messages ("Table").
	noun   string
	drop   func(ctx context.Context, sc *db.ServerConn, n nodeData) error
	rename func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error
	// warning is appended to the delete confirmation when the drop does
	// something beyond removing the object itself.
	warning string
	// typed gates the delete behind retyping the object's name, for drops with
	// a bigger blast radius. It implies solo.
	typed bool
	// typedFor is typed decided per object. Implies solo for those objects;
	// typedWarning is appended after warning.
	typedFor     func(n nodeData) bool
	typedWarning string
	// solo keeps the type out of a multi-object delete: one at a time, from
	// either surface. Marks principals and the database, whose drops have
	// server- or database-wide consequences (orphaned users, sessions,
	// connections) that a batch's shared warning cannot cover. Schema-scoped
	// objects are dropped in a set.
	solo bool
	// dropOption labels a checkbox on the delete confirmation and dropWithOption
	// is the drop it feeds. A type sets both and leaves drop nil: deleteObject
	// picks the path from dropOption, objectOpsMenuItems from either drop.
	dropOption     string
	dropWithOption func(ctx context.Context, sc *db.ServerConn, n nodeData, opt bool) error
	// transfer moves the object into another schema (ALTER SCHEMA ... TRANSFER),
	// which a rename cannot do. Only the sp_rename 'OBJECT' families and tables
	// have one.
	transfer func(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error
	// renameWarning is a question asked between the new-name prompt and the
	// rename itself, for a rename that costs more than the name change.
	renameWarning string
	// settle runs once after a delete's drops, if any landed, for a family whose
	// DROP is stored but not in force until a further statement (Resource
	// Governor's RECONFIGURE). Runs under Script too. A delete comes from one
	// folder, so one family's settle serves the batch. settled then refreshes
	// whatever beyond the folder shows that state, settle failing or not.
	settle  func(ctx context.Context, sc *db.ServerConn) error
	settled func(a *App, sc *db.ServerConn)
}

// dbOf is the database a node's object lives in.
//
// DatabaseRef, not DatabaseByName: every statement names its object in the
// text and reads only the database name, so the sys.databases round trip
// buys nothing. WithScript intercepts writes only, so it is no reason.
func dbOf(sc *db.ServerConn, n nodeData) *gosmo.Database {
	return sc.Server.DatabaseRef(n.DBName)
}

// tableOf is the table a table-scoped node (index, statistic, key,
// constraint) belongs to: nodeData.TableName, since Schema/Name there name
// the index or constraint. Name-only handle, as dbOf.
func tableOf(sc *db.ServerConn, n nodeData) *gosmo.Table {
	return sc.Server.DatabaseRef(n.DBName).TableRef(n.Schema, n.TableName)
}

// objectOps is the per-type table. Entries call a method on the gosmo handle
// (Drop, Rename, Transfer); gosmo picks the statement and class (sp_rename
// 'OBJECT', 'USERDATATYPE' for alias types, TRANSFER's TYPE:: and XML SCHEMA
// COLLECTION:: prefixes).
var objectOps = map[NodeType]objectOp{
	NodeDatabase: {
		noun:    "Database",
		warning: "Existing connections to it will be closed.",
		typed:   true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).Drop(ctx, true)
		},
		// MODIFY NAME needs exclusive access, which the tree's metadata connections
		// deny, so the rename always closes connections and always asks first.
		renameWarning: "Renaming a database needs exclusive access to it. Existing connections will be closed and their transactions rolled back. Continue?",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return dbOf(sc, n).Rename(ctx, newName, true)
		},
	},
	// A snapshot's drop deletes its sparse files, not the source, so no typed
	// confirmation. solo anyway: reverting needs the source's other snapshots
	// dropped first, and a batch could take the wrong one.
	NodeDatabaseSnapshot: {
		noun:    "Database Snapshot",
		warning: "Its sparse files are deleted. The source database is not affected.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.DatabaseSnapshotRef(n.Name).Drop(ctx)
		},
	},
	NodeTable: {
		noun:    "Table",
		warning: "All of its data is deleted with it.",
		// Unticked by default, as SSMS: a referenced table is refused until the FK
		// is dealt with. Ticking drops FKs on the *other* tables, so it is a
		// decision, not a retry.
		dropOption: "Also drop the foreign keys that reference it",
		dropWithOption: func(ctx context.Context, sc *db.ServerConn, n nodeData, cascade bool) error {
			return dbOf(sc, n).TableRef(n.Schema, n.Name).Drop(ctx, cascade)
		},
		rename:   renameIn((*gosmo.Database).TableRef),
		transfer: transferIn((*gosmo.Database).TableRef),
	},
	NodeView:            schemaObjectOp("View", (*gosmo.Database).ViewRef),
	NodeStoredProcedure: schemaObjectOp("Stored Procedure", (*gosmo.Database).StoredProcedureRef),
	NodeFunction:        schemaObjectOp("Function", (*gosmo.Database).UserDefinedFunctionRef),
	// A trigger belongs to its table and moves with it; ALTER SCHEMA TRANSFER
	// refuses one, so gosmo's Trigger has no Transfer.
	NodeTrigger:  {noun: "Trigger", drop: dropIn((*gosmo.Database).TriggerRef), rename: renameIn((*gosmo.Database).TriggerRef)},
	NodeSequence: schemaObjectOp("Sequence", (*gosmo.Database).SequenceRef),
	NodeSynonym:  schemaObjectOp("Synonym", (*gosmo.Database).SynonymRef),

	NodeColumn: {
		noun: "Column",
		// The server refuses a column anything depends on and names the blocker in
		// the error, so the warning says the drop is refused and leaves the naming
		// to the server.
		warning: "Its data goes with it, and the drop is refused while a constraint, index or statistic depends on the column — the server's error names the object that blocks it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return tableOf(sc, n).DropColumn(ctx, n.Name)
		},
		// sp_rename updates nothing that names the column, and SQL Server's caution
		// is a notice after the rename succeeded, so ask first.
		renameWarning: "Renaming a column does not update anything that names it. Views, procedures, functions, computed columns, check constraints and filtered indexes keep the old name and break at their next use. Continue?",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return tableOf(sc, n).RenameColumn(ctx, n.Name, newName)
		},
	},

	NodeIndex: {
		noun: "Index",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			idx, err := findIndex(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return idx.Drop(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			idx, err := findIndex(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return idx.Rename(ctx, newName)
		},
	},
	NodeStatistic: {
		noun: "Statistic",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			_, st, err := findStatistic(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return st.Drop(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			_, st, err := findStatistic(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return st.Rename(ctx, newName)
		},
	},
	NodeKey: {
		noun: "Key",
		drop: dropConstraint,
		// A primary key's or unique constraint's name is its backing index's name
		// in sys.indexes, so it renames as an index, not as an object.
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			idx, err := findIndex(ctx, sc, n.DBName, n.Schema, n.TableName, n.Name)
			if err != nil {
				return err
			}
			return idx.Rename(ctx, newName)
		},
	},
	NodeForeignKey: {noun: "Foreign Key", drop: dropConstraint, rename: renameConstraint},
	NodeCheck:      {noun: "Constraint", drop: dropConstraint, rename: renameConstraint},

	NodePartitionFunction: {
		noun:    "Partition Function",
		warning: "Every partition scheme built on it must be dropped first.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			pf, err := findPartitionFunction(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return pf.Drop(ctx)
		},
	},
	NodePartitionScheme: {
		noun:    "Partition Scheme",
		warning: "Every table and index partitioned by it must be moved off it first.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			ps, err := findPartitionScheme(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return ps.Drop(ctx)
		},
	},
	NodeSecurityPolicy: {
		noun:    "Security Policy",
		warning: "The tables it protects stop being filtered — every row becomes visible.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			p, err := findSecurityPolicy(ctx, sc, n.DBName, n.Schema, n.Name)
			if err != nil {
				return err
			}
			return p.Drop(ctx)
		},
	},
	NodeColumnMasterKey: {
		noun:    "Column Master Key",
		warning: "Every column encryption key protected by it must be dropped first.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			k, err := findColumnMasterKey(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return k.Drop(ctx)
		},
	},
	NodeColumnEncryptionKey: {
		noun: "Column Encryption Key",
		// Not recoverable: the key material exists only encrypted here, so every
		// column encrypted with it becomes unreadable.
		warning: "Data in every column encrypted with it becomes permanently unreadable.",
		typed:   true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			k, err := findColumnEncryptionKey(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return k.Drop(ctx)
		},
	},

	// Programmability > Types, Rules, Defaults, Assemblies and Plan Guides, plus
	// External Resources beside Views.
	//
	// The type families share one statement — DROP TYPE names an alias, table
	// or CLR type and nothing in it says which — and one warning: the server
	// refuses a type anything is typed on, and names the blocker in the
	// error, so the warning says the drop is refused rather than listing
	// classes (the same treatment as NodeColumn's).
	NodeUserDefinedDataType: {
		noun:    "User-Defined Data Type",
		warning: typeInUseWarning,
		drop:    dropIn((*gosmo.Database).UserDefinedDataTypeRef),
		// sp_rename's USERDATATYPE class covers alias types and nothing else
		// in sys.types — see gosmo's UserDefinedDataType.Rename, which is why
		// the table and CLR types below have no rename.
		rename:   renameIn((*gosmo.Database).UserDefinedDataTypeRef),
		transfer: transferIn((*gosmo.Database).UserDefinedDataTypeRef),
	},
	NodeUserDefinedTableType: {
		noun:     "User-Defined Table Type",
		warning:  typeInUseWarning,
		drop:     dropIn((*gosmo.Database).UserDefinedTableTypeRef),
		transfer: transferIn((*gosmo.Database).UserDefinedTableTypeRef),
	},
	NodeUserDefinedType: {
		noun:     "User-Defined Type",
		warning:  typeInUseWarning,
		drop:     dropIn((*gosmo.Database).ClrTypeRef),
		transfer: transferIn((*gosmo.Database).ClrTypeRef),
	},
	NodeXMLSchemaCollection: {
		noun: "XML Schema Collection",
		// Same shape as a type's: the server refuses the drop while a column,
		// parameter or variable is bound to the collection, and names it.
		warning: "The drop is refused while a column, parameter or variable is typed on it — the server's error names what blocks it.",
		drop:    dropIn((*gosmo.Database).XMLSchemaCollectionRef),
		// ALTER SCHEMA TRANSFER needs the XML SCHEMA COLLECTION:: class here,
		// not the default OBJECT one; gosmo's Transfer on the handle adds it.
		transfer: transferIn((*gosmo.Database).XMLSchemaCollectionRef),
	},
	// Rules and defaults are ordinary sys.objects rows, so sp_rename's OBJECT
	// class and ALTER SCHEMA TRANSFER's default class both serve.
	NodeRule: {
		noun: "Rule",
		// The column or type keeps its values but stops being checked, which
		// is not something the object's absence from the tree makes visible.
		warning:  "Columns and types still bound to it stop being validated, and the drop is refused until sp_unbindrule releases them.",
		drop:     dropIn((*gosmo.Database).RuleRef),
		rename:   renameIn((*gosmo.Database).RuleRef),
		transfer: transferIn((*gosmo.Database).RuleRef),
	},
	NodeDefault: {
		noun:     "Default",
		warning:  "Columns and types still bound to it stop getting a default value, and the drop is refused until sp_unbindefault releases them.",
		drop:     dropIn((*gosmo.Database).DefaultRef),
		rename:   renameIn((*gosmo.Database).DefaultRef),
		transfer: transferIn((*gosmo.Database).DefaultRef),
	},
	NodeAssembly: {
		noun: "Assembly",
		// The binary is not recoverable from anything on screen — gossms
		// never reads it — so an assembly dropped by accident has to come
		// back from the original .dll.
		warning: "The drop is refused while a CLR routine or type is bound to it, and the assembly binary cannot be recovered from gossms.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).AssemblyRef(n.Name).Drop(ctx)
		},
		// No rename and no transfer: an assembly is database-scoped, has no
		// schema to move between, and sp_rename has no class for one.
	},
	NodePlanGuide: {
		noun: "Plan Guide",
		// A guide is invisible from the query side either way — dropping one
		// looks like nothing happening until a plan regresses.
		warning: "The queries it applies hints to go back to the plans the optimizer picks on its own.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).PlanGuideRef(n.Name).Drop(ctx)
		},
		// No rename: sp_control_plan_guide has no rename operation, and
		// sp_rename has no class for a plan guide.
	},
	NodeExternalDataSource: {
		noun:    "External Data Source",
		warning: "The drop is refused while an external table, file format reference or backup URL names it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).ExternalDataSourceRef(n.Name).Drop(ctx)
		},
	},
	NodeExternalFileFormat: {
		noun:    "External File Format",
		warning: "The drop is refused while an external table uses it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).ExternalFileFormatRef(n.Name).Drop(ctx)
		},
	},
	NodeExternalLibrary: {
		noun: "External Library",
		// The package bytes are not readable back out of the catalog, so a
		// dropped library has to be uploaded again from its source.
		warning: "R and Python scripts that load the package stop working, and the uploaded package cannot be recovered from gossms.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).ExternalLibraryRef(n.Name).Drop(ctx)
		},
	},

	// The seven Service Broker families have no rename (no sp_rename class; the
	// one ALTER that could, ALTER SERVICE, renames nothing). Only the queue has
	// a schema to move between.
	//
	// Every warning says the drop is refused and leaves the naming to the
	// server (Msg 3716). A pre-check would duplicate a dependency graph the
	// server already walks, and ALTER on the database does not override it.
	NodeMessageType: {
		noun:    "Message Type",
		warning: "The drop is refused while a contract names it — the server's error says so.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).MessageTypeRef(n.Name).Drop(ctx)
		},
	},
	NodeContract: {
		noun:    "Contract",
		warning: "The drop is refused while a service or a conversation priority names it — the server's error says so.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).ContractRef(n.Name).Drop(ctx)
		},
	},
	NodeBrokerQueue: {
		noun: "Queue",
		// The one drop in this set that destroys data: messages still sitting
		// in the queue go with it, and nothing on screen holds them.
		warning: "Messages still in the queue are deleted with it, and the drop is refused while a service is bound to it.",
		drop:    dropIn((*gosmo.Database).BrokerQueueRef),
		// The one schema-scoped family here, so the only one with a Move to
		// Schema — and its right is neither of the queue's other two: see
		// gate.ClassOneTransferRights.
		transfer: transferIn((*gosmo.Database).BrokerQueueRef),
	},
	NodeBrokerService: {
		noun:    "Service",
		warning: "Conversations addressed to it stop being delivered, and the drop is refused while a route or a conversation priority names it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).BrokerServiceRef(n.Name).Drop(ctx)
		},
	},
	NodeRoute: {
		noun: "Route",
		// AutoCreatedLocal is an ordinary user route: sys.routes has no system flag,
		// so Delete is offered, as in SSMS.
		warning: "Messages for the services it addresses stop being routed.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).RouteRef(n.Name).Drop(ctx)
		},
	},
	NodeRemoteServiceBinding: {
		noun:    "Remote Service Binding",
		warning: "Conversations with the remote service lose their security binding.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).RemoteServiceBindingRef(n.Name).Drop(ctx)
		},
	},
	NodeBrokerPriority: {
		noun:    "Broker Priority",
		warning: "Conversations it applies to fall back to the default priority of 5.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).BrokerPriorityRef(n.Name).Drop(ctx)
		},
	},

	NodeLogin: {
		noun:    "Login",
		warning: "Database users mapped to it are left orphaned.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.LoginRef(n.Name).Drop(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return sc.Server.LoginRef(n.Name).Rename(ctx, newName)
		},
	},
	NodeCredential: {
		noun: "Credential",
		// Nothing cascades, but a login or job step mapped to the credential loses
		// outside access, and the secret is unrecoverable, so recreating from what
		// is on screen is no undo.
		warning: "Logins and job steps mapped to it lose their external identity, and the stored secret cannot be recovered.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.CredentialRef(n.Name).Drop(ctx)
		},
	},
	NodeDatabaseScopedCredential: {
		noun: "Database Scoped Credential",
		// As the server-level credential: nothing cascades, but an external data
		// source or backup URL bound to it can no longer authenticate, and the
		// secret is unrecoverable.
		warning: "External data sources and backup URLs bound to it lose their identity, and the stored secret cannot be recovered.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).DatabaseScopedCredentialRef(n.Name).Drop(ctx)
		},
		// No rename: there is no ALTER DATABASE SCOPED CREDENTIAL ... WITH NAME
		// and no sp_rename class for one, the same as the server-level
		// credential.
	},
	NodeCertificate: {
		noun: "Certificate",
		// Nothing cascades, but whatever the certificate signs or protects stops
		// working, and a never-backed-up private key is gone (the scripted CREATE
		// restores the public half only). A mapped login or user makes the server
		// refuse (Msg 15559); left to its message.
		warning: "Logins, users and signed modules mapped to it stop working, and a private key held only here is lost.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).CertificateRef(n.Name).Drop(ctx)
		},
		// No rename: there is no ALTER CERTIFICATE ... WITH NAME and no
		// sp_rename class for one.
	},
	NodeAsymmetricKey: {
		noun: "Asymmetric Key",
		// As the certificate, less the partial undo: the scripted CREATE makes a new
		// key pair, so nothing signed or encrypted by this one can be verified or
		// decrypted. A mapped login or user is Msg 15559, left to the server.
		warning: "Logins, users and signed modules mapped to it stop working, and the key pair cannot be recreated.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).AsymmetricKeyRef(n.Name).Drop(ctx)
		},
		// No rename: no ALTER ASYMMETRIC KEY ... WITH NAME, no sp_rename class.
	},
	NodeSymmetricKey: {
		noun: "Symmetric Key",
		// The scripted CREATE makes new key material, so what this key encrypted is
		// lost unless it was made from KEY_SOURCE and IDENTITY_VALUE. A key that
		// encrypts another symmetric key is refused (Msg 15352), left to the server.
		warning: "Data encrypted with it cannot be decrypted again, unless the key was created with KEY_SOURCE and IDENTITY_VALUE and is re-created from them.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).SymmetricKeyRef(n.Name).Drop(ctx)
		},
		// No rename: no ALTER SYMMETRIC KEY ... WITH NAME, no sp_rename class.
	},
	NodeAudit: {
		noun: "Audit",
		// Dropping the audit leaves its specifications orphaned: SQL Server allows
		// the drop.
		warning: "Server audit specifications bound to it stop recording and are left without an audit.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.ServerAuditRef(n.Name).Drop(ctx)
		},
		// No rename: MODIFY NAME exists but only on a disabled audit (gosmo's
		// Rename does the off/on dance); offering it would silently stop auditing
		// meanwhile.
	},
	NodeServerAuditSpecification: {
		noun:    "Server Audit Specification",
		warning: "The action groups it names stop being recorded by its audit.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.ServerAuditSpecificationRef(n.Name).Drop(ctx)
		},
		// No rename: ALTER SERVER AUDIT SPECIFICATION has no MODIFY NAME form
		// at all — verified live, it is a parse error.
	},
	NodeDatabaseAuditSpecification: {
		noun:    "Database Audit Specification",
		warning: "The action groups and actions it names stop being recorded by its audit.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).DatabaseAuditSpecificationRef(n.Name).Drop(ctx)
		},
		// No rename: ALTER DATABASE AUDIT SPECIFICATION has no MODIFY NAME
		// form, the same as the server-scope one.
	},
	NodeBackupDevice: {
		noun: "Backup Device",
		// Only the alias is dropped; the .bak stays on disk (as SSMS), so an
		// accidental delete is recoverable by re-adding the device.
		warning: "Backup jobs and maintenance plans naming it stop working.",
		solo:    true,
		// Unticked by default: @delfile deletes the backup file itself, which
		// no other command here can undo.
		dropOption: "Also delete the backup file on the server",
		dropWithOption: func(ctx context.Context, sc *db.ServerConn, n nodeData, deleteFile bool) error {
			return sc.Server.BackupDeviceRef(n.Name).Drop(ctx, deleteFile)
		},
	},
	NodeDatabaseTrigger: {
		noun: "Database Trigger",
		// A DDL trigger is what enforces or audits a policy across the whole
		// database; dropping one removes that enforcement for every schema in
		// it at once.
		warning: "The DDL policy it enforces stops applying across the database.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).DatabaseTriggerRef(n.Name).Drop(ctx)
		},
		// No rename: sp_rename has no class for a DDL trigger, and there is
		// no ALTER ... MODIFY NAME form either.
	},
	NodeServerTrigger: {
		noun: "Server Trigger",
		// A DDL trigger is what enforces or audits a policy across the whole
		// instance; dropping one removes that enforcement everywhere at once,
		// and a logon trigger's removal is what unblocks logins it was
		// refusing.
		warning: "The DDL or logon policy it enforces stops applying server-wide.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.ServerTriggerRef(n.Name).Drop(ctx)
		},
		// No rename: sp_rename has no class for a server-scope trigger, and
		// the name is baked into the definition CREATE TRIGGER stores.
	},
	NodeEndpoint: {
		noun: "Endpoint",
		// Dropping an endpoint removes its listener from everything using it; a
		// mirroring endpoint carries every replica's log, so there is no
		// per-database warning.
		warning: "Availability replicas, mirroring partners and Service Broker routes using it stop connecting.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			// Read the endpoint rather than using a name-only handle: built-ins cannot
			// be dropped, and IsSystem (what gosmo refuses on) comes from the read.
			e, err := sc.Server.EndpointByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return e.Drop(ctx)
		},
		// No rename: ALTER ENDPOINT has no WITH NAME, and sp_rename has no
		// class for one.
	},
	NodeEventSession: {
		noun: "Event Session",
		// A running session needs no stopping first; DROP stops it.
		warning: "A running session stops collecting. Files an event_file target wrote stay on the server's disk.",
		// System sessions are listed like any other and SSMS deletes them on one
		// click; here the name is typed, since nothing in goSSMS can restore
		// system_health (see builtInEventSessions).
		typedFor:     func(n nodeData) bool { return isBuiltInEventSession(n.Name) },
		typedWarning: "SQL Server created this session for its own diagnostics — Script Session as CREATE first to keep a way back.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return xeScopeOf(n).ref(sc, n.Name).Drop(ctx)
		},
		// No rename: ALTER EVENT SESSION has no WITH NAME.
	},
	// Resource Governor. A drop is stored and then applied at once — see
	// applyResourceGovernor — so the tree and what is in force agree, as
	// they do after a Properties Apply. The built-ins are IsSystem, which
	// withholds Delete. No rename: T-SQL has none for any of the three.
	NodeResourcePool: rgObjectOp("Resource Pool",
		"The drop is refused while a workload group uses it — move or delete its groups first. "+rgDropApplied,
		func(s *gosmo.Server, name string) rgDroppable { return s.ResourcePoolRef(name) }),
	// The drop itself is accepted with sessions in the group, and applying it
	// is what is refused (Msg 10904), so the warning names that. A disabled
	// governor ends in DISABLE, which applies nothing, so there the refusal
	// waits for the next Enable — verified live in W7.
	NodeWorkloadGroup: rgObjectOp("Workload Group",
		"Applying the drop is refused while a session is in the group: those sessions must end first (on a disabled governor, enabling it is refused until they do). "+rgDropApplied,
		func(s *gosmo.Server, name string) rgDroppable { return s.WorkloadGroupRef(name) }),
	NodeExternalResourcePool: rgObjectOp("External Resource Pool",
		"The drop is refused while a workload group uses it. "+rgDropApplied,
		func(s *gosmo.Server, name string) rgDroppable { return s.ExternalResourcePoolRef(name) }),

	NodeServerRole: {
		noun: "Server Role",
		solo: true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.ServerRoleRef(n.Name).Drop(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			r, err := sc.Server.ServerRoleByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return r.Rename(ctx, newName)
		},
	},
	NodeUser: {
		noun: "User",
		solo: true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.UserRef(n.Name).Drop(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			d := dbOf(sc, n)
			u, err := d.UserByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return u.Rename(ctx, newName)
		},
	},
	NodeDatabaseRole: {
		noun: "Database Role",
		solo: true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.RoleRef(n.Name).Drop(ctx)
		},
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			r, err := findRole(ctx, sc, n.DBName, n.Name)
			if err != nil {
				return err
			}
			return r.Rename(ctx, newName)
		},
	},
	NodeSchema: {
		// SQL Server has no schema rename — moving a schema's contents is
		// ALTER SCHEMA ... TRANSFER — so Rename is deliberately absent.
		noun: "Schema",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			d := dbOf(sc, n)
			return d.SchemaRef(n.Name).Drop(ctx)
		},
	},

	// Agent objects: Rename only. Their Delete lives in agent_menu.go.
	NodeAgentJob: {
		noun: "Job",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			j, err := sc.Server.JobByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return j.Rename(ctx, newName)
		},
	},
	NodeAgentSchedule: {
		noun: "Schedule",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			s, err := findAgentSchedule(ctx, sc, n.AgentScheduleID)
			if err != nil {
				return err
			}
			return s.Rename(ctx, newName)
		},
	},
	NodeAgentAlert: {
		noun: "Alert",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			al, err := sc.Server.AlertByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return al.Rename(ctx, newName)
		},
	},
	NodeAgentOperator: {
		noun: "Operator",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			o, err := sc.Server.OperatorByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return o.Rename(ctx, newName)
		},
	},
}

// rgDropApplied ends every Resource Governor delete's warning.
const rgDropApplied = "Resource Governor is then reconfigured, so the drop takes effect at once; a disabled governor stays disabled."

// rgDroppable is a gosmo Resource Governor handle — pool, group or external
// pool.
type rgDroppable interface{ Drop(context.Context) error }

// rgObjectOp is the op the three Resource Governor families share: a
// lookup-free handle's Drop, settled by applyResourceGovernor and the node's
// label re-read.
func rgObjectOp(noun, warning string, ref func(*gosmo.Server, string) rgDroppable) objectOp {
	return objectOp{
		noun:    noun,
		warning: warning,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return ref(sc.Server, n.Name).Drop(ctx)
		},
		settle:  applyResourceGovernor,
		settled: (*App).refreshResourceGovernorLabel,
	}
}

// schemaObjectHandle is a gosmo handle for a schema-scoped object that can be
// dropped, renamed and moved to another schema.
type schemaObjectHandle interface {
	Drop(context.Context) error
	Rename(context.Context, string) error
	Transfer(context.Context, string) error
}

// schemaObjectOp is the whole op for a family whose handle has all three —
// view, procedure, function, sequence, synonym.
func schemaObjectOp[T schemaObjectHandle](noun string, ref func(*gosmo.Database, string, string) T) objectOp {
	return objectOp{noun: noun, drop: dropIn(ref), rename: renameIn(ref), transfer: transferIn(ref)}
}

// dropIn adapts one of gosmo's schema-scoped Database.XxxRef(schema, name)
// handles into a drop function: the handle is lookup-free, and its Drop
// addresses the object by the two names alone.
func dropIn[T interface{ Drop(context.Context) error }](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Drop(ctx)
	}
}

// renameIn is dropIn for a rename.
func renameIn[T interface {
	Rename(context.Context, string) error
}](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData, string) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Rename(ctx, newName)
	}
}

// transferIn is dropIn for a move to another schema (ALTER SCHEMA ...
// TRANSFER). The handle's Transfer picks the securable class — TYPE:: for
// the type families, XML SCHEMA COLLECTION:: for that one — so the choice
// is not repeated here.
func transferIn[T interface {
	Transfer(context.Context, string) error
}](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData, string) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Transfer(ctx, targetSchema)
	}
}

// typeInUseWarning is the delete warning the three type families share. Like
// NodeColumn's, it says the drop is refused and leaves the naming of the
// blocker to the server, which names it in the error.
const typeInUseWarning = "The drop is refused while a column, parameter, variable or routine is typed on it — the server's error names what blocks it."

// dropConstraint removes a primary key, unique constraint, foreign key, or
// CHECK constraint — one ALTER TABLE ... DROP CONSTRAINT for all four.
func dropConstraint(ctx context.Context, sc *db.ServerConn, n nodeData) error {
	return tableOf(sc, n).DropConstraint(ctx, n.Name)
}

// renameConstraint renames a foreign key or CHECK constraint. A primary key
// or unique constraint renames through its backing index instead — see
// NodeKey.
func renameConstraint(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
	return tableOf(sc, n).RenameConstraint(ctx, n.Name, newName)
}

// objectOpFor returns the Delete/Rename behaviour for a node type, or nil
// when it has none.
func objectOpFor(t NodeType) *objectOp {
	if op, ok := objectOps[t]; ok {
		return &op
	}
	return nil
}

// typedDelete reports whether deleting n asks for its name to be typed.
func typedDelete(op *objectOp, n nodeData) bool {
	return op.typed || (op.typedFor != nil && op.typedFor(n))
}

// deletedAlone reports whether n refuses to be deleted as part of a
// selection. A typed confirmation implies it: it asks for one object's name.
func deletedAlone(op *objectOp, n nodeData) bool {
	return typedDelete(op, n) || op.solo
}

// soloDeleteReason is what to tell the user about a type that has to be deleted
// on its own — the Details pane's menu note and the status line both, so the
// wording cannot drift between the withheld item and the refused action.
func soloDeleteReason(op *objectOp, n nodeData) string {
	return fmt.Sprintf("%s %q has to be deleted on its own — select just that row",
		op.noun, objectDataName(n))
}
