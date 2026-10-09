package gate

import (
	"github.com/radix29/gosmo"
)

// gate.go holds the [Right] type and the catalogue of rights the application
// asks about. The rule is in allows.go, wording in text.go, menu wrappers in
// menu.go, named sets in sets.go.

// Right is one permission an action needs. Role names the fixed role that also
// confers it, for the user-facing sentence only: the gate always asks about the
// permission, because HAS_PERMS_BY_NAME answers 1 for a role member (and for
// sysadmin), while IS_SRVROLEMEMBER does not fold sysadmin in.
type Right struct {
	Name string
	Role string
	DB   bool // database-scope rather than server-scope

	// Schema narrows the question to the object's schema, asked of gosmo's
	// per-schema probe. A principal granted ALTER on one schema holds no
	// database-wide permission, so this is the only right that can speak for it.
	// It is also how ObjectDenial asks the schema DENY question; only there can a
	// schema-scoped right *withhold*.
	Schema bool

	// Membership makes Name a fixed database *role* to be a member of, in database
	// InDB, rather than a permission to hold. It exists for SQL Agent, whose
	// actions are permitted by msdb role membership and nothing
	// HAS_PERMS_BY_NAME can answer.
	//
	// It cannot fail open on its own: InRole answers false for a role never asked
	// about as for one the login is not in, so AllowsOn checks
	// gosmo.DatabaseCapabilities.Probed before believing a false.
	Membership bool
	InDB       string

	// ServerRole is Membership's server-scope twin: Name is a fixed server role to
	// be a member of. It exists for writes permitted by role membership alone: a
	// backup device is added/dropped by sp_addumpdevice/sp_dropdevice, which
	// diskadmin carries and no server permission answers for.
	//
	// RightsAllow checks gosmo.Capabilities.Probed before believing a false. It
	// also asks about sysadmin: sysadmin implies membership of no other fixed role,
	// so a sysadmin reads 0 for diskadmin while being permitted everything it
	// carries.
	ServerRole bool

	// Object narrows the question to the object itself, asked of gosmo's per-object
	// probe. It is the only right that can speak for a principal granted ALTER on
	// one table and nothing else, who reads 0 at schema and database scope.
	//
	// It can only *add* permission. gosmo's ObjectPermissions map holds a row only
	// for an object explicitly granted, denied or owned, so silence means "no
	// explicit grant", never "not probed"; see
	// gosmo.DatabaseCapabilities.HasOnObject.
	Object bool

	// Securable narrows the question to one assembly, user-defined type or XML
	// schema collection (classes 5, 6, 10), asked of gosmo's per-securable probe.
	// Object is class 1 only, and no wider right helps: a principal granted
	// CONTROL on one assembly, or owning it, reads 0 at every database and schema
	// scope.
	//
	// Unlike Object it is read Permits-wise and so can withhold: gosmo's map is a
	// HAS_PERMS_BY_NAME answer with a row for every securable the login can see, so
	// 0 is the server's answer, not silence. That is what lets it be Move to
	// Schema's only right.
	Securable gosmo.DatabaseSecurableKind

	// DeniedOnPrincipal names the DATABASE_PRINCIPAL-scope (class 4) permission
	// whose DENY withholds this right, or "" for a right no class-4 DENY can
	// reach. It is deliberately not Name: the right is the database-wide ALTER ANY
	// USER, while the DENY that beats it sits on the user itself as plain ALTER.
	//
	// It also stops the principal arm of ObjectDenial firing on the wrong kind of
	// node: ObjectDenial is asked about a table by the same name as a denied user
	// just as readily, and only a right declaring this field makes it ask the
	// class-4 question.
	DeniedOnPrincipal string

	// DeniedOnServer is DeniedOnPrincipal's server-scope twin: the server-scope
	// permission whose explicit DENY withholds this right, or "". ServerSecurable
	// says which kind of securable that DENY sits on.
	//
	// The kind is declared, not inferred, because ObjectDenial is handed a bare
	// name; it keeps the arm from firing on the wrong family (a login and an
	// endpoint of the same name are two securables).
	DeniedOnServer  string
	ServerSecurable gosmo.ServerSecurableKind

	// DeniedOnAG names the AVAILABILITY GROUP-scope (class 108) permission whose
	// absence on the group withholds this right, or "". Class 108's major_id is an
	// internal id no supported view maps to a name, so gosmo asks HAS_PERMS_BY_NAME
	// per group instead of reading sys.server_permissions.
	//
	// The answer is read as a *denial* only while the right beside it is held:
	// HAS_PERMS_BY_NAME reads 0 both for a group carrying DENY ALTER and for a
	// login holding nothing at this scope, and the latter is already withheld by
	// the server-wide right.
	DeniedOnAG string

	// Alt are narrower permissions that also satisfy this one and are not named in
	// the message. SQL Server 2022 split VIEW SERVER STATE into two halves and
	// either suffices, but naming all three says less than naming the one the user
	// is likely to be granted.
	Alt []string
}

// The rights the application gates on. Every name is in gosmo's Probed* list
// *for its scope*; one that is not reads back as CapabilityUnknown forever and
// gates nothing, indistinguishable at run time from a login that holds the
// right. Hence declared here rather than spelled at each call site, and
// permission_gate_names_test.go checks each literal against gosmo.
var (
	ControlServer   = Right{Name: "CONTROL SERVER", Role: "sysadmin"}
	ViewServerState = Right{Name: "VIEW SERVER STATE", Role: "sysadmin",
		Alt: []string{"VIEW SERVER PERFORMANCE STATE", "VIEW SERVER SECURITY STATE"}}
	// ViewAnyDefinition makes server-scope metadata visible, including the
	// Resource Governor catalog views, which return no rows (and no error) without
	// it. VIEW SERVER STATE does not imply it.
	ViewAnyDefinition = Right{Name: "VIEW ANY DEFINITION", Role: "sysadmin"}
	AlterSettings     = Right{Name: "ALTER SETTINGS", Role: "serveradmin"}
	// The login's class-101 DENY is all-or-nothing, so one arm on one right covers
	// Login Properties, Rename and Delete alike: verified live on majors 13 and 17,
	// DENY ALTER ON LOGIN::x withholds ALTER LOGIN (rename and password) and DROP
	// LOGIN, every refusal Msg 15151.
	AlterAnyLogin = Right{Name: "ALTER ANY LOGIN", Role: "securityadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableLogin}
	// No DeniedOnServer twin: verified live on majors 13 and 17, ALTER SERVER ROLE
	// ... WITH NAME and DROP SERVER ROLE succeed with DENY ALTER ON SERVER ROLE::r
	// in place, while the same DENY refuses membership edits. Declaring it would
	// withhold a rename and drop the server allows. See docs/decisions.md §
	// Permission gating.
	AlterAnyServerRole = Right{Name: "ALTER ANY SERVER ROLE", Role: "securityadmin"}
	// AlterAnyServerRoleMembers is the same right for the page that edits a server
	// role's *membership*, where the class-101 DENY does withhold: ALTER SERVER
	// ROLE r ADD MEMBER is refused under DENY ALTER ON SERVER ROLE::r (Msg 15151),
	// majors 13 and 17. The split is per action, not per object; see
	// AlterAnyDBRole/AlterAnyDBRoleMembers one scope up.
	//
	// Unlike the database scope, the *member* is not asked about: adding a login
	// carrying a class-101 DENY to an undenied server role succeeds (majors 13 and
	// 17), so Login Properties > Server Roles declares the plain right.
	AlterAnyServerRoleMembers = Right{Name: "ALTER ANY SERVER ROLE", Role: "securityadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableServerRole}
	// ALTER ANY CREDENTIAL is server-scope only, with no database-scoped twin: a
	// database-scoped credential is permitted by CONTROL on the database alone; see
	// DBScopedCredentialRights().
	AlterAnyCredential = Right{Name: "ALTER ANY CREDENTIAL", Role: "securityadmin"}
	CreateAnyDatabase  = Right{Name: "CREATE ANY DATABASE", Role: "dbcreator"}
	AlterAnyDatabase   = Right{Name: "ALTER ANY DATABASE", Role: "dbcreator"}
	// The endpoint's class-105 DENY withholds ALTER ENDPOINT, refused with Msg 6004
	// rather than the 15151 every class-101 DENY carries (verified live, majors 13
	// and 17). One arm on the one right: the folder's New Endpoint names no
	// securable and never reaches it.
	AlterAnyEndpoint = Right{Name: "ALTER ANY ENDPOINT", Role: "sysadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableEndpoint}
	AlterAnyAudit = Right{Name: "ALTER ANY SERVER AUDIT", Role: "sysadmin"}
	// The event-session rights, one per verb. SQL Server 2022 split ALTER ANY EVENT
	// SESSION into nine granular names, each covered by it; the wide name is Name
	// and the verb's granular one its Alt, so either permits the verb. On
	// 2016-2019 the granular names read CapabilityUnknown, which an Alt never
	// counts as a grant, so the wide name alone decides there.
	EventSessionStart = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"ALTER ANY EVENT SESSION ENABLE"}}
	EventSessionStop = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"ALTER ANY EVENT SESSION DISABLE"}}
	EventSessionDrop = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"DROP ANY EVENT SESSION"}}
	EventSessionCreate = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"CREATE ANY EVENT SESSION"}}
	EventSessionAddTarget = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"ALTER ANY EVENT SESSION ADD TARGET"}}
	// Session Properties' Events and Data Storage pages add and drop, and either
	// granular half permits part of it, so either is an Alt; the server refuses the
	// half a login lacks.
	EventSessionEvents = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"ALTER ANY EVENT SESSION ADD EVENT", "ALTER ANY EVENT SESSION DROP EVENT"}}
	EventSessionTargets = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"ALTER ANY EVENT SESSION ADD TARGET", "ALTER ANY EVENT SESSION DROP TARGET"}}
	EventSessionOption = Right{Name: "ALTER ANY EVENT SESSION", Role: "sysadmin",
		Alt: []string{"ALTER ANY EVENT SESSION OPTION"}}
	// DatabaseEventSession is every verb on a database-scoped session (Azure SQL
	// Database): one right, not split per verb.
	DatabaseEventSession = Right{Name: "ALTER ANY DATABASE EVENT SESSION", Role: "db_owner", DB: true}
	// The availability group's class-108 DENY is all-or-nothing: probed live on the
	// two-node cluster, DENY ALTER ON AVAILABILITY GROUP::g withholds every ALTER
	// AVAILABILITY GROUP (options SET, ADD/REMOVE DATABASE, MODIFY REPLICA,
	// FAILOVER), each Msg 15151, with the server-wide right reading 1 throughout.
	//
	// It leaves the ALTER DATABASE ... SET HADR family alone, checked against the
	// database instead, so Suspend/Resume, Join and Unjoin stay on plain gate; see
	// alwayson_menu.go.
	AlterAnyAG = Right{Name: "ALTER ANY AVAILABILITY GROUP", Role: "sysadmin",
		DeniedOnAG: "ALTER"}
	// Held for a feature that does not exist yet (nothing creates or alters a
	// linked server). Declared so permission_gate_names_test.go checks the
	// literal against gosmo's Probed* list; a misspelled name would read
	// CapabilityUnknown forever and gate nothing.
	//lint:ignore U1000 declared ahead of a New/Alter Linked Server feature
	AlterAnyLinkedSrv = Right{Name: "ALTER ANY LINKED SERVER", Role: "sysadmin"}

	BackupDatabase = Right{Name: "BACKUP DATABASE", Role: "db_backupoperator", DB: true}
	AlterDatabase  = Right{Name: "ALTER", Role: "db_owner", DB: true}
	ControlDB      = Right{Name: "CONTROL", Role: "db_owner", DB: true}
	AlterAnyUser   = Right{Name: "ALTER ANY USER", Role: "db_accessadmin", DB: true,
		DeniedOnPrincipal: "ALTER"}
	// No DeniedOnPrincipal twin: verified live on majors 13, 14 and 17, DROP ROLE
	// and ALTER ROLE ... WITH NAME succeed with DENY ALTER ON ROLE::x in place,
	// while the same DENY on a *user* refuses both. Declaring it would make the
	// gate refuse what the server would run. See docs/decisions.md § Permission
	// gating.
	AlterAnyDBRole = Right{Name: "ALTER ANY ROLE", Role: "db_securityadmin", DB: true}
	// A database audit specification is gated at *database* scope, not by
	// AlterAnyAudit: SQL Server checks ALTER ANY DATABASE AUDIT for CREATE/ALTER/
	// DROP DATABASE AUDIT SPECIFICATION; the server-scope ALTER ANY SERVER AUDIT
	// answers for the audit the specification writes to, not the specification.
	AlterAnyDBAudit = Right{Name: "ALTER ANY DATABASE AUDIT", Role: "db_owner", DB: true}
	// The right SQL Server checks for ENABLE/DISABLE/DROP TRIGGER ... ON DATABASE.
	// It stands alone: HAS_PERMS_BY_NAME folds in what implies it, so db_ddladmin
	// and a database-wide ALTER both answer 1, and a principal with neither
	// answers 0 (verified live).
	AlterAnyDatabaseDDLTrigger = Right{Name: "ALTER ANY DATABASE DDL TRIGGER", Role: "db_ddladmin", DB: true}
	// AlterAnyDBRoleMembers is the same right for the pages that edit a role's
	// *membership*, where the class-4 DENY does withhold: ALTER ROLE r ADD MEMBER u
	// is refused under DENY ALTER ON ROLE::r (Msg 15151), verified live on majors
	// 13 and 17.
	//
	// The split is per action, not per object: naming this one on the General page
	// would withhold a rename the server allows, and naming the plain one on
	// Members offers an edit it refuses.
	AlterAnyDBRoleMembers = Right{Name: "ALTER ANY ROLE", Role: "db_securityadmin", DB: true,
		DeniedOnPrincipal: "ALTER"}
	// Held for a feature that does not exist yet (no New Table), for the reason
	// at AlterAnyLinkedSrv.
	//lint:ignore U1000 declared ahead of a New Table feature
	CreateTable = Right{Name: "CREATE TABLE", Role: "db_ddladmin", DB: true}

	AlterAnySchema = Right{Name: "ALTER ANY SCHEMA", Role: "db_ddladmin", DB: true}
	ViewDBState    = Right{Name: "VIEW DATABASE STATE", Role: "db_owner", DB: true}

	// The three below name db_owner because no narrower role carries them
	// (verified live): db_ddladmin answers 0 to all three, and a database-wide
	// ALTER answers 1 for the two key permissions but 0 for ALTER ANY SECURITY
	// POLICY.
	AlterAnyCMK       = Right{Name: "ALTER ANY COLUMN MASTER KEY", Role: "db_owner", DB: true}
	AlterAnyCEK       = Right{Name: "ALTER ANY COLUMN ENCRYPTION KEY", Role: "db_owner", DB: true}
	AlterAnySecPolicy = Right{Name: "ALTER ANY SECURITY POLICY", Role: "db_owner", DB: true}

	// What DROP PARTITION FUNCTION/SCHEME, DROP ASSEMBLY and DROP EXTERNAL DATA
	// SOURCE/FILE FORMAT/LIBRARY check; see dbScopedOpRights for the live
	// evidence. The role is the one that confers the right on *every* supported
	// version, hence db_owner for the external data source and file format:
	// db_ddladmin carries both on major 17 but reads 0, and is refused the drop,
	// on 13 and 14.
	AlterAnyDataspace     = Right{Name: "ALTER ANY DATASPACE", Role: "db_ddladmin", DB: true}
	AlterAnyAssembly      = Right{Name: "ALTER ANY ASSEMBLY", Role: "db_ddladmin", DB: true}
	AlterAnyExtDataSource = Right{Name: "ALTER ANY EXTERNAL DATA SOURCE", Role: "db_owner", DB: true}
	AlterAnyExtFileFormat = Right{Name: "ALTER ANY EXTERNAL FILE FORMAT", Role: "db_owner", DB: true}
	// 2017 and later. On 2016 it reads CapabilityUnknown and fails open, which
	// gates nothing since that version has no external libraries.
	AlterAnyExtLibrary = Right{Name: "ALTER ANY EXTERNAL LIBRARY", Role: "db_ddladmin", DB: true}

	// The five Service Broker rights. Each alone is enough to ALTER and DROP its
	// family (probed live with a WITHOUT LOGIN user per right on majors 13, 14 and
	// 17, identical throughout), and a right from one family confers nothing on
	// another (ALTER ANY MESSAGE TYPE cannot alter a route, Msg 15151).
	//
	// The role is db_owner because no narrower one carries them on every supported
	// major, and it is the name the *user* is sent to ask for. See docs/db-rules.md
	// on per-version role contents.
	//
	// There is deliberately no broker-priority twin: SQL Server enforces a
	// CREATE/ALTER BROKER PRIORITY permission it does not publish
	// (HAS_PERMS_BY_NAME answers NULL for every spelling, and
	// sys.fn_builtin_permissions has no DATABASE-class row matching %PRIORITY%), so
	// a right for it would read CapabilityUnknown forever. A broker priority takes
	// ALTER on the database alone; see BrokerPriorityWriteRights.
	AlterAnyMessageType = Right{Name: "ALTER ANY MESSAGE TYPE", Role: "db_owner", DB: true}
	AlterAnyContract    = Right{Name: "ALTER ANY CONTRACT", Role: "db_owner", DB: true}
	AlterAnyService     = Right{Name: "ALTER ANY SERVICE", Role: "db_owner", DB: true}
	AlterAnyRoute       = Right{Name: "ALTER ANY ROUTE", Role: "db_owner", DB: true}
	AlterAnyRSB         = Right{Name: "ALTER ANY REMOTE SERVICE BINDING", Role: "db_owner", DB: true}

	// What DROP CERTIFICATE checks at database scope. Probed live 2026-09-22 on
	// majors 13, 14 and 17 and Managed Instance, identical throughout
	// (docs/decisions.md § Keys and certificates): the right alone drops any
	// certificate, CREATE CERTIFICATE alone drops none it does not own, and
	// db_ddladmin carries it on every one while db_securityadmin carries nothing.
	AlterAnyCertificate = Right{Name: "ALTER ANY CERTIFICATE", Role: "db_ddladmin", DB: true}
	// CREATE CERTIFICATE is the narrow right New Certificate needs. It is not
	// implied by AlterAnyCertificate (the hierarchy puts it under ALTER on the
	// database), so the menu asks for either. Same probe: a CREATE-only principal
	// creates, and owns what it created.
	CreateCertificate = Right{Name: "CREATE CERTIFICATE", Role: "db_ddladmin", DB: true}

	// The asymmetric-key pair, twin of the certificate pair: the same probe found
	// every holder and verb answering identically for both families.
	AlterAnyAsymmetricKey = Right{Name: "ALTER ANY ASYMMETRIC KEY", Role: "db_ddladmin", DB: true}
	CreateAsymmetricKey   = Right{Name: "CREATE ASYMMETRIC KEY", Role: "db_ddladmin", DB: true}

	// The symmetric-key pair, same again: CREATE and DROP answered exactly as for
	// the other two families.
	AlterAnySymmetricKey = Right{Name: "ALTER ANY SYMMETRIC KEY", Role: "db_ddladmin", DB: true}
	CreateSymmetricKey   = Right{Name: "CREATE SYMMETRIC KEY", Role: "db_ddladmin", DB: true}

	// What DROP FULLTEXT CATALOG, DROP FULLTEXT STOPLIST and DROP SEARCH PROPERTY
	// LIST check at database scope: one right for all three families. Probed live
	// 2026-10-07 on majors 14 and 17, identical (docs/decisions.md § The rest):
	// the right alone drops any of the three, ALTER and CONTROL on the database
	// and db_ddladmin read 1 for it and drop too, and CREATE FULLTEXT CATALOG and
	// ALTER ANY SCHEMA drop nothing.
	AlterAnyFullTextCatalog = Right{Name: "ALTER ANY FULLTEXT CATALOG", Role: "db_ddladmin", DB: true}
	// What CREATE FULLTEXT CATALOG, CREATE FULLTEXT STOPLIST and CREATE SEARCH
	// PROPERTY LIST check: one right for all three, read 1 under ALTER ANY
	// FULLTEXT CATALOG, ALTER and CONTROL on the database and db_ddladmin, each
	// of which creates too (2026-10-08, majors 14 and 17, identical).
	CreateFullTextCatalog = Right{Name: "CREATE FULLTEXT CATALOG", Role: "db_ddladmin", DB: true}

	// A backup device is added and dropped by sp_addumpdevice/sp_dropdevice, which
	// diskadmin carries and no server *permission* answers for, so this is a role
	// membership. CONTROL SERVER would be a knowingly wrong gate: a pure
	// diskadmin, the feature's target, would see a read-only banner on a page they
	// can write.
	DiskAdmin = Right{Name: "diskadmin", ServerRole: true}
	// Sysadmin is membership of sysadmin itself, for a write CONTROL SERVER does
	// not reach: sp_sqlagent_notify checks the role and refuses CONTROL SERVER
	// alone with Msg 14260 after the registry write has gone through. See
	// AgentPropertiesRights. sp_cycle_errorlog (Msg 15247) and
	// sp_cycle_agent_errorlog (Msg 14260) refuse it the same way.
	Sysadmin = Right{Name: "sysadmin", ServerRole: true}

	// The two SQL Agent rights are memberships, not permissions: New Job and its
	// siblings are permitted by membership of an msdb role, which grants EXECUTE on
	// individual procedures rather than the database-scope EXECUTE a permission
	// probe can ask about. See AgentWriteRights.
	SQLAgentUser = Right{Name: "SQLAgentUserRole", Membership: true, InDB: "msdb"}
	MsdbOwner    = Right{Name: "db_owner", Membership: true, InDB: "msdb"}
	// DatabaseMailUser is msdb's DatabaseMailUserRole, whose grants are EXECUTE on
	// sp_send_dbmail and two more procedures; a membership for SQLAgentUser's
	// reason. See DatabaseMailSendRights.
	DatabaseMailUser = Right{Name: "DatabaseMailUserRole", Membership: true, InDB: "msdb"}
	// MsdbDataReader is msdb's db_datareader, which reads the Database Mail views a
	// DatabaseMailUserRole member is granted SELECT on. See
	// DatabaseMailLogReadRights.
	MsdbDataReader = Right{Name: "db_datareader", Membership: true, InDB: "msdb"}

	// AlterOnObject is the grant made directly on one object, which no wider scope
	// reflects: a principal granted ALTER on one table reads 0 for every database-
	// and schema-scope permission.
	AlterOnObject = Right{Name: "ALTER", DB: true, Object: true}

	// ControlOnObject is CONTROL on one object, not AlterOnObject with a wider
	// Name: gosmo's object block matches CONTROL alongside whatever permission it
	// asks about, so the ALTER map answers 1 for either holder and cannot tell
	// them apart. The one statement needing the distinction is ALTER SCHEMA ...
	// TRANSFER; see ClassOneTransferRights.
	ControlOnObject = Right{Name: "CONTROL", DB: true, Object: true}

	// CONTROL on one assembly, type or XML schema collection: what its owner holds
	// implicitly, and what CONTROL on (or ownership of) its schema, or CONTROL on
	// the database, confers. Probed live on majors 13, 14 and 17: it permits the
	// drop and rename alongside the wider rights that also do, and is the *only*
	// thing that permits ALTER SCHEMA ... TRANSFER; see securableTransferRights.
	ControlOnAssembly            = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableAssembly}
	ControlOnType                = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableType}
	ControlOnXMLSchemaCollection = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableXMLSchemaCollection}

	// CONTROL on one certificate: what its owner holds implicitly, and so what lets
	// a CREATE CERTIFICATE-only principal drop the certificate it made. ALTER on
	// the certificate does not permit the drop (Msg 15151), hence the CONTROL
	// probe. Same probe as AlterAnyCertificate.
	ControlOnCertificate = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableCertificate}
	// ControlOnAsymmetricKey is ControlOnCertificate for an asymmetric key.
	ControlOnAsymmetricKey = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableAsymmetricKey}
	// ControlOnSymmetricKey is ControlOnCertificate for a symmetric key.
	ControlOnSymmetricKey = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableSymmetricKey}
	// CONTROL on one full-text catalog, stoplist or search property list: the
	// certificate's shape, the same 2026-10-07 probe. Its owner or a CONTROL
	// grantee drops it with no database-scope right; ALTER and TAKE OWNERSHIP on
	// it permit nothing.
	ControlOnFullTextCatalog    = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableFullTextCatalog}
	ControlOnFullTextStoplist   = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableFullTextStoplist}
	ControlOnSearchPropertyList = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableSearchPropertyList}
	// The effective ALTER on one full-text catalog, stoplist or search property
	// list: the exact test for REBUILD/REORGANIZE, ADD/DROP of a stopword and
	// ADD/DROP of a property (2026-10-08, majors 14 and 17). It folds in ALTER
	// ANY FULLTEXT CATALOG, ALTER/CONTROL on the database, db_ddladmin and
	// ownership. AS DEFAULT is not its to answer — AlterAnyFullTextCatalog is.
	AlterOnFullTextCatalog    = Right{Name: "ALTER", DB: true, Securable: gosmo.DatabaseSecurableFullTextCatalog}
	AlterOnFullTextStoplist   = Right{Name: "ALTER", DB: true, Securable: gosmo.DatabaseSecurableFullTextStoplist}
	AlterOnSearchPropertyList = Right{Name: "ALTER", DB: true, Securable: gosmo.DatabaseSecurableSearchPropertyList}
	// REFERENCES on one of them is what naming it in a full-text index checks,
	// beside ALTER on the table: the catalog in CREATE FULLTEXT INDEX, the
	// stoplist or property list in its STOPLIST/SEARCH PROPERTY LIST clause
	// (same probe). It reads 0 under ALTER on the database and ALTER ANY
	// FULLTEXT CATALOG, which the server refuses there (Msg 7666, 30023, 30025).
	ReferencesOnFullTextCatalog    = Right{Name: "REFERENCES", DB: true, Securable: gosmo.DatabaseSecurableFullTextCatalog}
	ReferencesOnFullTextStoplist   = Right{Name: "REFERENCES", DB: true, Securable: gosmo.DatabaseSecurableFullTextStoplist}
	ReferencesOnSearchPropertyList = Right{Name: "REFERENCES", DB: true, Securable: gosmo.DatabaseSecurableSearchPropertyList}
	// AlterOnSymmetricKey is the effective ALTER on one symmetric key, the exact
	// test for ALTER SYMMETRIC KEY ... ADD / DROP ENCRYPTION (probed live on 13 and
	// 17, 2026-09-22). It folds in ALTER ANY SYMMETRIC KEY, ALTER on the database
	// and ownership, and reads 0 for CONTROL on the key with ALTER denied, which
	// the server refuses (Msg 15151). It stands alone; ControlOnSymmetricKey
	// beside it would over-offer.
	AlterOnSymmetricKey = Right{Name: "ALTER", DB: true, Securable: gosmo.DatabaseSecurableSymmetricKey}
	// AlterOnCertificate and AlterOnAsymmetricKey are the effective ALTER on one
	// certificate or asymmetric key, what ALTER CERTIFICATE / ALTER ASYMMETRIC KEY
	// ... REMOVE PRIVATE KEY checks. Probed on 13 and 17 (2026-09-22): permitted
	// for ALTER or CONTROL on the object, ALTER ANY <family>, ALTER or CONTROL on
	// the database and db_ddladmin (all read 1 here), refused (Msg 15151) to
	// db_securityadmin.
	AlterOnCertificate   = Right{Name: "ALTER", DB: true, Securable: gosmo.DatabaseSecurableCertificate}
	AlterOnAsymmetricKey = Right{Name: "ALTER", DB: true, Securable: gosmo.DatabaseSecurableAsymmetricKey}

	// AlterOnSchema is what SQL Server checks for a rename, move or drop of a
	// schema object. No role carries it: it is granted on the schema itself, and a
	// principal holding it may hold nothing else.
	AlterOnSchema = Right{Name: "ALTER", DB: true, Schema: true}
)
