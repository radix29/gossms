package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// explorer_object_ops.go is Object Explorer's general Delete and Rename: one
// table of what the two mean per node type. A node type absent from the table
// offers neither, which is how a folder or anything gosmo can't drop stays out
// of the menu instead of failing when clicked.
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
// Both take a nodeData by value, not the *explorerNode it came off: they run on
// a background goroutine while the UI goroutine writes node.data.
// deleteObject/runRename make the copy before the safego.
type objectOp struct {
	// noun names the object in dialog titles and messages ("Table").
	noun   string
	drop   func(ctx context.Context, sc *db.ServerConn, n nodeData) error
	rename func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error
	// warning is appended to the delete confirmation when the drop does
	// something beyond removing the object itself.
	warning string
	// typed gates the delete behind retyping the object's name, for a drop
	// whose blast radius is bigger than one object. A typed confirmation asks
	// for one name, so it implies solo.
	typed bool
	// solo keeps the type out of a multi-object delete: it is deleted one at a
	// time, from either surface. It marks the principals and the database —
	// objects whose drop is a server-wide or database-wide act with
	// consequences elsewhere (orphaned users, a login's sessions, every
	// connection to a database), where the batch confirmation's shared warning
	// stops being enough and the deliberation is per object. A schema-scoped
	// object — table, view, procedure, index — is dropped in a set.
	solo bool
	// dropOption labels a checkbox on the delete confirmation and dropWithOption
	// is the drop it feeds. A type setting these sets both and leaves drop nil:
	// deleteObject picks the path from dropOption, objectOpsMenuItems from either
	// drop being present.
	dropOption     string
	dropWithOption func(ctx context.Context, sc *db.ServerConn, n nodeData, opt bool) error
	// transfer moves the object into another schema (ALTER SCHEMA ... TRANSFER),
	// which a rename cannot do. Only the sp_rename 'OBJECT' families and tables
	// have one.
	transfer func(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error
	// renameWarning is a question asked between the new-name prompt and the
	// rename itself, for a rename that costs more than the name change.
	renameWarning string
}

// dbOf is the database a node's object lives in.
//
// DatabaseRef, not DatabaseByName: every statement below names its object in
// the text and reads nothing off the *gosmo.Database but its name, so the
// sys.databases round trip buys nothing. That is the whole reason; a
// WithScript-derived context is not a second one, since WithScript intercepts
// writes only and the by-name read under it would reach the server anyway.
func dbOf(sc *db.ServerConn, n nodeData) *gosmo.Database {
	return sc.Server.DatabaseRef(n.DBName)
}

// tableOf is the table a table-scoped node (index, statistic, key, constraint)
// belongs to — nodeData.TableName, since Schema/Name there name the index or
// constraint itself. A name-only handle, for the same reason as dbOf.
func tableOf(sc *db.ServerConn, n nodeData) *gosmo.Table {
	return sc.Server.DatabaseRef(n.DBName).TableRef(n.Schema, n.TableName)
}

// objectOps is the per-type table. Every rename going through
// Database.RenameObject is sp_rename's 'OBJECT' class — view, procedure,
// function, sequence, synonym, trigger, constraint. Indexes and statistics have
// their own sp_rename object types and gosmo methods.
var objectOps = map[NodeType]objectOp{
	NodeDatabase: {
		noun:    "Database",
		warning: "Existing connections to it will be closed.",
		typed:   true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.DropDatabase(ctx, n.Name, true)
		},
		// MODIFY NAME needs exclusive access, which the tree's own metadata
		// connections deny — so the rename always closes connections, and always
		// asks first.
		renameWarning: "Renaming a database needs exclusive access to it. Existing connections will be closed and their transactions rolled back. Continue?",
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return sc.Server.RenameDatabase(ctx, n.Name, newName, true)
		},
	},
	// A snapshot's drop deletes its sparse files and leaves the source
	// database alone — so no typed confirmation, unlike a database's. solo
	// all the same: it is still a DROP DATABASE, and reverting to a snapshot
	// needs the source's *other* snapshots dropped first, which is exactly
	// the moment a batch delete would take the wrong one with it.
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
		// Unticked by default, so the plain gesture is SSMS's: a referenced
		// table is refused until the foreign key is dealt with. Ticking it drops
		// those foreign keys on the *other* tables, which is why it is a
		// decision and not a retry.
		dropOption: "Also drop the foreign keys that reference it",
		dropWithOption: func(ctx context.Context, sc *db.ServerConn, n nodeData, cascade bool) error {
			return dbOf(sc, n).DropTable(ctx, n.Schema, n.Name, cascade)
		},
		transfer: transferObjectIn,
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return dbOf(sc, n).RenameTable(ctx, n.Schema, n.Name, newName)
		},
	},
	NodeView:            {noun: "View", drop: dropIn((*gosmo.Database).DropView), rename: renameObjectIn, transfer: transferObjectIn},
	NodeStoredProcedure: {noun: "Stored Procedure", drop: dropIn((*gosmo.Database).DropStoredProcedure), rename: renameObjectIn, transfer: transferObjectIn},
	NodeFunction:        {noun: "Function", drop: dropIn((*gosmo.Database).DropFunction), rename: renameObjectIn, transfer: transferObjectIn},
	// A trigger belongs to its table and moves with it; ALTER SCHEMA TRANSFER
	// refuses one.
	NodeTrigger:  {noun: "Trigger", drop: dropIn((*gosmo.Database).DropTrigger), rename: renameObjectIn},
	NodeSequence: {noun: "Sequence", drop: dropRefIn((*gosmo.Database).SequenceRef), rename: renameObjectIn, transfer: transferObjectIn},
	NodeSynonym:  {noun: "Synonym", drop: dropRefIn((*gosmo.Database).SynonymRef), rename: renameObjectIn, transfer: transferObjectIn},

	NodeColumn: {
		noun: "Column",
		// The server refuses a column anything depends on — a default or check
		// constraint, an index, a statistic — and names the blocker in the
		// error ("The object 'DF_Orders_flagged' is dependent on column
		// 'flagged'."), so the warning says the drop is refused and leaves the
		// naming to the server rather than listing classes.
		warning: "Its data goes with it, and the drop is refused while a constraint, index or statistic depends on the column — the server's error names the object that blocks it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return tableOf(sc, n).DropColumn(ctx, n.Name)
		},
		// sp_rename updates the column and nothing that names it, and SQL
		// Server's caution ("may break scripts and stored procedures") is a
		// notice on a rename that already succeeded — so ask first.
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
	NodeForeignKey: {noun: "Foreign Key", drop: dropConstraint, rename: renameObjectIn},
	NodeCheck:      {noun: "Constraint", drop: dropConstraint, rename: renameObjectIn},

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
		// column encrypted with it becomes unreadable ciphertext.
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

	// Programmability ▸ Types, Rules, Defaults, Assemblies and Plan Guides,
	// plus the External Resources folder beside Views.
	//
	// The type families share one statement — DROP TYPE names an alias, table
	// or CLR type and nothing in it says which — and one warning: the server
	// refuses a type anything is typed on, and names the blocker in the
	// error, so the warning says the drop is refused rather than listing
	// classes (the same treatment as NodeColumn's).
	NodeUserDefinedDataType: {
		noun:    "User-Defined Data Type",
		warning: typeInUseWarning,
		drop:    dropRefIn((*gosmo.Database).UserDefinedDataTypeRef),
		// sp_rename's USERDATATYPE class covers alias types and nothing else
		// in sys.types — see gosmo's RenameUserDefinedDataType, which is why
		// the table and CLR types below have no rename.
		rename: func(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
			return dbOf(sc, n).RenameUserDefinedDataType(ctx, n.Schema, n.Name, newName)
		},
		transfer: transferTypeIn,
	},
	NodeUserDefinedTableType: {
		noun:     "User-Defined Table Type",
		warning:  typeInUseWarning,
		drop:     dropRefIn((*gosmo.Database).UserDefinedTableTypeRef),
		transfer: transferTypeIn,
	},
	NodeUserDefinedType: {
		noun:     "User-Defined Type",
		warning:  typeInUseWarning,
		drop:     dropRefIn((*gosmo.Database).ClrTypeRef),
		transfer: transferTypeIn,
	},
	NodeXMLSchemaCollection: {
		noun: "XML Schema Collection",
		// Same shape as a type's: the server refuses the drop while a column,
		// parameter or variable is bound to the collection, and names it.
		warning: "The drop is refused while a column, parameter or variable is typed on it — the server's error names what blocks it.",
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).XMLSchemaCollectionRef(n.Schema, n.Name).Drop(ctx)
		},
		// ALTER SCHEMA TRANSFER needs the XML SCHEMA COLLECTION:: class here,
		// not the default OBJECT one.
		transfer: func(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error {
			return dbOf(sc, n).TransferXMLSchemaCollection(ctx, targetSchema, n.Schema, n.Name)
		},
	},
	// Rules and defaults are ordinary sys.objects rows, so sp_rename's OBJECT
	// class and ALTER SCHEMA TRANSFER's default class both serve.
	NodeRule: {
		noun: "Rule",
		// The column or type keeps its values but stops being checked, which
		// is not something the object's absence from the tree makes visible.
		warning:  "Columns and types still bound to it stop being validated, and the drop is refused until sp_unbindrule releases them.",
		drop:     dropRefIn((*gosmo.Database).RuleRef),
		rename:   renameObjectIn,
		transfer: transferObjectIn,
	},
	NodeDefault: {
		noun:     "Default",
		warning:  "Columns and types still bound to it stop getting a default value, and the drop is refused until sp_unbindefault releases them.",
		drop:     dropRefIn((*gosmo.Database).DefaultRef),
		rename:   renameObjectIn,
		transfer: transferObjectIn,
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

	// The seven Service Broker families. None of them has a rename: there is
	// no sp_rename class for any of these, and the one ALTER that could carry
	// a name change (ALTER SERVICE) renames nothing. Only the queue has a
	// schema to be moved between.
	//
	// Every warning here says the drop is refused and leaves the naming of the
	// blocker to the server, which names it in Msg 3716 ("The message type 'x'
	// cannot be dropped because it is bound to one or more contract."). A
	// pre-check would be a second copy of a dependency graph the server
	// already walks, and ALTER on the database does not override the refusal.
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
		drop:    dropRefIn((*gosmo.Database).BrokerQueueRef),
		// The one schema-scoped family here, so the only one with a Move to
		// Schema — and its right is neither of the queue's other two: see
		// gate.ClassOneTransferRights.
		transfer: transferObjectIn,
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
		// AutoCreatedLocal is an ordinary user route here — sys.routes has no
		// system flag, so the tree marks none of them system and this Delete
		// is offered on it like any other. That matches SSMS, and dropping it
		// is a legitimate thing to do.
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
		// A credential is dropped by name and nothing cascades, but a login
		// or a job step mapped to it stops being able to reach outside the
		// server — and the secret is unrecoverable, so this is not a delete
		// that can be undone by recreating the object from what is on screen.
		warning: "Logins and job steps mapped to it lose their external identity, and the stored secret cannot be recovered.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.CredentialRef(n.Name).Drop(ctx)
		},
	},
	NodeDatabaseScopedCredential: {
		noun: "Database Scoped Credential",
		// Same as the server-level credential: nothing cascades, but an
		// external data source or a backup URL bound to it stops being able to
		// authenticate, and the secret is unrecoverable — so this is not a
		// delete that can be undone from what is on screen.
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
		// Nothing cascades, but whatever the certificate signs or protects
		// stops working, and a private key that was never backed up is gone
		// for good — the scripted CREATE brings back the public half only. A
		// mapped login or user makes the server refuse the drop (Msg 15559),
		// which is left to its own message rather than pre-checked here.
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
		// The certificate's case, less the partial undo: the scripted CREATE
		// makes a new key pair, so nothing signed or encrypted by this one can
		// be verified or decrypted again. A mapped login or user is Msg 15559,
		// again left to the server.
		warning: "Logins, users and signed modules mapped to it stop working, and the key pair cannot be recreated.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).AsymmetricKeyRef(n.Name).Drop(ctx)
		},
		// No rename: no ALTER ASYMMETRIC KEY ... WITH NAME, no sp_rename class.
	},
	NodeSymmetricKey: {
		noun: "Symmetric Key",
		// The scripted CREATE makes new key material, so what this key
		// encrypted is lost with it — unless it was made from KEY_SOURCE and
		// IDENTITY_VALUE, which re-create the same key. A key that encrypts
		// another symmetric key is refused by the server (Msg 15352), left to
		// its own message.
		warning: "Data encrypted with it cannot be decrypted again, unless the key was created with KEY_SOURCE and IDENTITY_VALUE and is re-created from them.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return dbOf(sc, n).SymmetricKeyRef(n.Name).Drop(ctx)
		},
		// No rename: no ALTER SYMMETRIC KEY ... WITH NAME, no sp_rename class.
	},
	NodeAudit: {
		noun: "Audit",
		// Dropping the audit takes every specification bound to it with it —
		// or rather leaves them orphaned, since SQL Server allows the drop and
		// the specifications stay behind pointing at nothing.
		warning: "Server audit specifications bound to it stop recording and are left without an audit.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			return sc.Server.ServerAuditRef(n.Name).Drop(ctx)
		},
		// No rename: ALTER SERVER AUDIT ... MODIFY NAME exists, but only on a
		// disabled audit, and gosmo's Rename does the off/on dance for it.
		// Wiring it here would offer a rename that silently stops auditing for
		// the duration.
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
		// The alias is all that is dropped by default; the .bak behind it stays
		// on disk, which is what SSMS does and what makes an accidental delete
		// recoverable by adding the device again.
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
		// Dropping an endpoint takes its listener away from everything using
		// it at once: a mirroring endpoint is what every availability replica
		// ships log through, and there is no per-database warning to give.
		warning: "Availability replicas, mirroring partners and Service Broker routes using it stop connecting.",
		solo:    true,
		drop: func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
			// Read the endpoint rather than acting on a name-only handle: the
			// built-in ones cannot be dropped, and IsSystem — which is what
			// gosmo refuses on — is part of what the read populates.
			e, err := sc.Server.EndpointByName(ctx, n.Name)
			if err != nil {
				return err
			}
			return e.Drop(ctx)
		},
		// No rename: ALTER ENDPOINT has no WITH NAME, and sp_rename has no
		// class for one.
	},
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
			s, err := sc.Server.ScheduleByName(ctx, n.Name)
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

// dropIn adapts one of gosmo's Database.DropXxx(ctx, schema, name) methods
// into a drop function — the schema-scoped kinds gosmo has no handle type for
// (views, procedures, functions, triggers).
func dropIn(fn func(*gosmo.Database, context.Context, string, string) error) func(context.Context, *db.ServerConn, nodeData) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
		return fn(dbOf(sc, n), ctx, n.Schema, n.Name)
	}
}

// dropRefIn adapts one of gosmo's schema-scoped Database.XxxRef(schema, name)
// handles into a drop function: the handle is lookup-free, and its Drop
// addresses the object by the two names alone.
func dropRefIn[T interface{ Drop(context.Context) error }](ref func(*gosmo.Database, string, string) T) func(context.Context, *db.ServerConn, nodeData) error {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) error {
		return ref(dbOf(sc, n), n.Schema, n.Name).Drop(ctx)
	}
}

// renameObjectIn is sp_rename's 'OBJECT' class, shared by every schema-scoped
// object that isn't a table, index, or statistic.
func renameObjectIn(ctx context.Context, sc *db.ServerConn, n nodeData, newName string) error {
	return dbOf(sc, n).RenameObject(ctx, n.Schema, n.Name, newName)
}

// typeInUseWarning is the delete warning the three type families share. Like
// NodeColumn's, it says the drop is refused and leaves the naming of the
// blocker to the server, which names it in the error.
const typeInUseWarning = "The drop is refused while a column, parameter, variable or routine is typed on it — the server's error names what blocks it."

// transferTypeIn moves an alias, table or CLR type into another schema.
// ALTER SCHEMA ... TRANSFER needs the TYPE:: class here: a type is not in
// sys.objects, so the default class transferObjectIn uses finds nothing.
func transferTypeIn(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error {
	return dbOf(sc, n).TransferType(ctx, targetSchema, n.Schema, n.Name)
}

// transferObjectIn moves a schema-scoped object into another schema. Shared
// by every family ALTER SCHEMA ... TRANSFER's default OBJECT class covers.
func transferObjectIn(ctx context.Context, sc *db.ServerConn, n nodeData, targetSchema string) error {
	return dbOf(sc, n).TransferObject(ctx, targetSchema, n.Schema, n.Name)
}

// dropConstraint removes a primary key, unique constraint, foreign key, or
// CHECK constraint — one ALTER TABLE ... DROP CONSTRAINT for all four.
func dropConstraint(ctx context.Context, sc *db.ServerConn, n nodeData) error {
	return tableOf(sc, n).DropConstraint(ctx, n.Name)
}

// objectOpFor returns the Delete/Rename behaviour for a node type, or nil
// when it has none.
func objectOpFor(t NodeType) *objectOp {
	if op, ok := objectOps[t]; ok {
		return &op
	}
	return nil
}

// deletedAlone reports whether a type refuses to be deleted as part of a
// selection. typed implies it: the confirmation asks for one object's name.
func deletedAlone(op *objectOp) bool {
	return op.typed || op.solo
}

// soloDeleteReason is what to tell the user about a type that has to be deleted
// on its own — the Details pane's menu note and the status line both, so the
// wording cannot drift between the withheld item and the refused action.
func soloDeleteReason(op *objectOp, n nodeData) string {
	return fmt.Sprintf("%s %q has to be deleted on its own — select just that row",
		op.noun, objectDataName(n))
}
