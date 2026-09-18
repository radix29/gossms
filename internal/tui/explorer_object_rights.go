package tui

import (
	"github.com/radix29/gossms/internal/tui/gate"
)

// explorer_object_rights.go is what permits Rename/Move/Delete per node type:
// one entry point, objectOpRights, over the tables below it — securable,
// principal, database-scoped and server-scoped. Any one right in a set is
// enough, and an action is withheld only when the server has denied them all.

// objectOpRights is what permits Rename/Move/Delete on a node type — any one
// of them is enough, and the action is withheld only when the server has
// denied every one.
//
// For a schema object the permission SQL Server checks is ALTER on its own
// schema or on the object itself, which gate.AlterOnSchema and
// gate.AlterOnObject ask about by name — the database-wide rights beside them
// are the wider ones that also permit the operation, and any one of the set is
// enough. A database node has no schema and keeps the two server-side rights
// that let a database be renamed or dropped.
func objectOpRights(t NodeType) []gate.Right {
	if t == NodeDatabase || t == NodeDatabaseSnapshot {
		return []gate.Right{gate.ControlDB, gate.AlterAnyDatabase}
	}
	// A queue is a schema object and would otherwise fall to
	// gate.ObjectWriteRights(), which is right for its Properties page and wrong
	// for its Delete by exactly one right — see gate.QueueDropRights.
	if t == NodeBrokerQueue {
		return gate.QueueDropRights()
	}
	if rights, ok := serverScopedOpRights[t]; ok {
		return rights
	}
	if rights, ok := principalOpRights[t]; ok {
		return rights
	}
	if rights, ok := dbScopedOpRights[t]; ok {
		return rights
	}
	if rights, ok := securableOpRights[t]; ok {
		return rights
	}
	return gate.ObjectWriteRights()
}

// objectDataRights is objectOpRights for one node rather than its type, and
// differs from it for the one family whose right depends on the object: an
// OBJECT-scoped plan guide is controlled under ALTER on the routine it is
// bound to, where a SQL or TEMPLATE guide needs the database's own ALTER —
// see planGuideRights, which the guide's Enable/Disable and Properties share.
func objectDataRights(n nodeData) []gate.Right {
	if n.Type == NodePlanGuide {
		return planGuideRights(n.ScopeName)
	}
	return objectOpRights(n.Type)
}

// objectDataRightGroups is every right set an operation on n needs: each set
// is any-of, and all of them are required — see gate.AllowsAllOn. For every family
// but the ones in conjoinedOpRights it is objectDataRights alone, and gates
// exactly as that one set always has.
func objectDataRightGroups(n nodeData) [][]gate.Right {
	groups := [][]gate.Right{objectDataRights(n)}
	if also, ok := conjoinedOpRights[n.Type]; ok {
		groups = append(groups, also)
	}
	return groups
}

// conjoinedOpRights is a second right set a family's Rename/Delete needs *as
// well as* objectDataRights' — for a statement SQL Server refuses unless it
// holds a right from each.
//
// DROP SECURITY POLICY needs ALTER ANY SECURITY POLICY and ALTER on the
// policy's schema, and ALTER SECURITY POLICY ... WITH (STATE = ON|OFF) needs
// the same pair — which is why the policy's Disable/Enable asks these groups
// too. Probed live 2026-09-11 on majors 13 and 17 with a WITHOUT LOGIN user
// per case, identical on both, the drop and the toggle agreeing in every row:
// holding ALTER ANY SECURITY POLICY, both went through exactly when
// HAS_PERMS_BY_NAME(schema, 'SCHEMA', 'ALTER') read 1 — under ALTER or
// CONTROL on the schema, its ownership, ALTER ANY SCHEMA, db_ddladmin, ALTER
// or CONTROL on the database — and both were refused (Msg 3701, Msg 33268)
// when it read 0: the policy right alone, ALTER on another schema, CONTROL on
// the policy itself, and DENY ALTER on the schema beside a database-wide
// ALTER or CONTROL on the schema. Without the policy right, every schema
// grant was refused.
//
// So the schema half is gate.AlterOnSchema alone: gosmo's per-schema probe
// is that very HAS_PERMS_BY_NAME, which already folds in every wider grant,
// and a DENY on the schema is withheld by gate.ObjectDenial's schema arm before
// any grant is read.
var conjoinedOpRights = map[NodeType][]gate.Right{
	NodeSecurityPolicy: {gate.AlterOnSchema},
}

// objectTransferRights is what permits Move to Schema on one node, and it is
// never objectDataRights: ALTER SCHEMA ... TRANSFER wants CONTROL on the
// securable, which the Rename/Delete set answers for nobody in particular.
//
// Two shapes, because the server asks the question at two classes. A type or
// an XML schema collection is class 6 or class 10, and its CONTROL comes from
// gosmo's per-securable probe — securableTransferRights. Every other family
// the tree offers a transfer on is a class-1 sys.objects row, whatever the
// node type calls it, so all of them share one entry read out of the object
// map: gate.ClassOneTransferRights.
func objectTransferRights(n nodeData) []gate.Right {
	if rights, ok := securableTransferRights[n.Type]; ok {
		return rights
	}
	return gate.ClassOneTransferRights()
}

// securableOpRights is Rename/Delete's right set for the user-defined types
// and the XML schema collection — class 6 and class 10 securables that live in
// a schema. gate.ObjectWriteRights() almost fits them and is wrong twice: its
// gate.AlterOnObject reads gosmo's class-1 map, where a type is never recorded
// but a same-named *table* is, and nothing in it speaks for a principal
// granted CONTROL on the type or owning it.
//
// Probed live 2026-09-11 on majors 13, 14 and 17, with a WITHOUT LOGIN user per
// case, for DROP TYPE, DROP XML SCHEMA COLLECTION and sp_rename's
// USERDATATYPE: ALTER and CONTROL on the database, ALTER ANY SCHEMA (and so
// db_ddladmin), ALTER on the schema, CONTROL on the securable and its
// ownership each permit all three, and DENY CONTROL on the securable refuses
// them — by hiding it, so its node never reaches the tree.
var securableOpRights = map[NodeType][]gate.Right{
	NodeUserDefinedDataType:  securableWriteRights(gate.ControlOnType),
	NodeUserDefinedTableType: securableWriteRights(gate.ControlOnType),
	NodeUserDefinedType:      securableWriteRights(gate.ControlOnType),
	NodeXMLSchemaCollection:  securableWriteRights(gate.ControlOnXMLSchemaCollection),
}

// securableWriteRights is gate.ObjectWriteRights() with the class-1 right swapped
// for the securable's own CONTROL.
func securableWriteRights(own gate.Right) []gate.Right {
	return []gate.Right{
		gate.AlterDatabase, gate.ControlDB, gate.AlterAnySchema,
		gate.AlterOnSchema, own,
	}
}

// securableTransferRights is Move to Schema's right set for the families whose
// transfer takes CONTROL on a class 6 or class 10 securable — securableOpRights'
// three, minus the assembly, which has no schema to move between. A class-1
// object is not here: its CONTROL is read out of the object map instead, by
// objectTransferRights' fallback (see gate.ClassOneTransferRights). Probed live
// alongside securableOpRights, with ALTER on
// the target schema held throughout: the transfer went through under CONTROL
// on the securable, its ownership, CONTROL on or ownership of the source
// schema, and CONTROL on the database — each of which gosmo's per-securable
// CONTROL reads as 1 — and was refused (Msg 15151) under ALTER on the
// database, ALTER ANY SCHEMA, db_ddladmin and ALTER on the source schema, every
// one of which permits the drop. Offering it on the Delete set offered a move
// to exactly the principals the server refuses it.
var securableTransferRights = map[NodeType][]gate.Right{
	NodeUserDefinedDataType:  {gate.ControlOnType},
	NodeUserDefinedTableType: {gate.ControlOnType},
	NodeUserDefinedType:      {gate.ControlOnType},
	NodeXMLSchemaCollection:  {gate.ControlOnXMLSchemaCollection},
}

// principalOpRights is Rename/Delete's right set for the database-level node
// types that are not schema objects. A user and a database role are
// class-4 securables, and not one member of gate.ObjectWriteRights() speaks for
// them: verified live 2026-09-04 on win10cli, a member of db_accessadmin drops
// a user while reading HAS_PERMS_BY_NAME 0 for ALTER, CONTROL and
// ALTER ANY SCHEMA on the database alike, and db_securityadmin reads the same
// three zeroes and drops a role. So Rename and Delete were withheld from
// exactly the two fixed roles that exist to perform them — while User
// Properties and Role Properties, which gate on ALTER ANY USER and
// ALTER ANY ROLE, stayed writable on the same node.
//
// The wider database rights stay in the set rather than being replaced by the
// narrow one: sys.fn_builtin_permissions gives ALTER on DATABASE as
// ALTER ANY USER's and ALTER ANY ROLE's covering permission, so a principal
// holding it may perform these and must not be gated out either.
//
// Narrowest first, because gateOn shows only rights[0] in a withheld item's
// note — see gate.AgentWriteRights for the same ordering rule.
var principalOpRights = map[NodeType][]gate.Right{
	NodeUser:         {gate.AlterAnyUser, gate.AlterDatabase, gate.ControlDB},
	NodeDatabaseRole: {gate.AlterAnyDBRole, gate.AlterDatabase, gate.ControlDB},
	// A database audit specification is not a class-4 principal, but it lands
	// here for the same reason: it has no schema and no object securable, so
	// gate.ObjectWriteRights() would ask about neither and the drop would be
	// offered to a principal holding nothing.
	NodeDatabaseAuditSpecification: {gate.AlterAnyDBAudit, gate.AlterDatabase, gate.ControlDB},
	// The one entry with a single right, and the omission is deliberate: see
	// gate.DBScopedCredentialRights. ALTER on the database does not permit the
	// drop, so listing it here would offer Delete to a principal the server
	// then refuses.
	NodeDatabaseScopedCredential: gate.DBScopedCredentialRights(),
}

// dbScopedOpRights is Delete's right set for the remaining database-level
// families that have no schema: principalOpRights' reason without the
// principals. Their nodes carry no schema and no object securable gosmo
// probes, so gate.ObjectWriteRights() collapses to ALTER and CONTROL on the
// database plus ALTER ANY SCHEMA — and ALTER ANY SCHEMA permits none of these
// drops, while the narrow right that does was never asked.
//
// Each set was probed live 2026-09-10 on majors 13 and 17 (and 14 where the
// family exists there) with a WITHOUT LOGIN user per right, running the DROP:
// the narrow right, ALTER on the database and CONTROL on it each permit the
// drop, and ALTER ANY SCHEMA is refused every one. sys.fn_builtin_permissions
// gives ALTER on DATABASE as each narrow right's covering permission, which is
// why the wider pair stays in the set. Two families differ:
//
//   - A plan guide has no narrow right at all. sp_control_plan_guide checks
//     ALTER on the database for a SQL or TEMPLATE guide — db_ddladmin is
//     refused it — and ALTER on the routine for an OBJECT guide, which
//     objectDataRights answers instead.
//   - A security policy has a schema, and its drop needs ALTER ANY SECURITY
//     POLICY *and* ALTER on that schema: each alone is refused (Msg 3701).
//     This set is the policy half; conjoinedOpRights carries the schema
//     half, and the gate requires both.
//
// An assembly also takes CONTROL on itself, which its owner holds: probed
// live 2026-09-11 on 13, 14 and 17, CONTROL on the assembly or its ownership
// permits DROP ASSEMBLY while every database-scope right reads 0. DENY CONTROL
// on it refuses the drop over ALTER ANY ASSEMBLY — by hiding the assembly, so
// there is no node to withhold it on — and DENY ALTER refuses nothing.
//
// Not every drop could be run everywhere. Without PolyBase, 13 has no external
// data sources and 13/14 no file formats, so those drops ran on 14 and 17 and
// on 17 alone; and no test instance has Machine Learning Services, so no
// external library could be created and its set rests on HAS_PERMS_BY_NAME
// (narrow right, ALTER, CONTROL and db_ddladmin read 1, ALTER ANY SCHEMA 0, on
// 14 and 17) and the documented DROP EXTERNAL LIBRARY permission. The library
// right is 2017-only and reads unknown on 2016, which fails open — correct,
// since there is nothing there to delete.
//
// Narrowest first, for gateOn's note — see principalOpRights.
var dbScopedOpRights = map[NodeType][]gate.Right{
	NodePartitionFunction:   {gate.AlterAnyDataspace, gate.AlterDatabase, gate.ControlDB},
	NodePartitionScheme:     {gate.AlterAnyDataspace, gate.AlterDatabase, gate.ControlDB},
	NodeColumnMasterKey:     {gate.AlterAnyCMK, gate.AlterDatabase, gate.ControlDB},
	NodeColumnEncryptionKey: {gate.AlterAnyCEK, gate.AlterDatabase, gate.ControlDB},
	NodeDatabaseTrigger:     {gate.AlterAnyDatabaseDDLTrigger, gate.AlterDatabase, gate.ControlDB},
	NodeAssembly:            {gate.AlterAnyAssembly, gate.AlterDatabase, gate.ControlDB, gate.ControlOnAssembly},
	NodeExternalDataSource:  {gate.AlterAnyExtDataSource, gate.AlterDatabase, gate.ControlDB},
	NodeExternalFileFormat:  {gate.AlterAnyExtFileFormat, gate.AlterDatabase, gate.ControlDB},
	NodeExternalLibrary:     {gate.AlterAnyExtLibrary, gate.AlterDatabase, gate.ControlDB},
	NodePlanGuide:           planGuideWriteRights(),
	// The five schemaless Service Broker families that have a narrow right of
	// their own, and the one that does not. Probed live 2026-09-16 on majors
	// 13, 14 and 17, identically on all three: each narrow right alone drops
	// its family, a right from one family drops nothing in another, and
	// ALTER on the database drops every one of them. A broker priority has no
	// grantable right at all — see gate.BrokerPriorityWriteRights.
	NodeMessageType:          gate.ServiceBrokerWriteRights(gate.AlterAnyMessageType),
	NodeContract:             gate.ServiceBrokerWriteRights(gate.AlterAnyContract),
	NodeBrokerService:        gate.ServiceBrokerWriteRights(gate.AlterAnyService),
	NodeRoute:                gate.RouteWriteRights(),
	NodeRemoteServiceBinding: gate.ServiceBrokerWriteRights(gate.AlterAnyRSB),
	NodeBrokerPriority:       gate.BrokerPriorityWriteRights(),
	// No wider pair: a database-wide ALTER reads 0 for ALTER ANY SECURITY
	// POLICY and is refused the drop (see gate.AlterAnySecPolicy), and CONTROL
	// already answers 1 for the narrow right. Only half of what the drop
	// needs — see conjoinedOpRights.
	NodeSecurityPolicy: {gate.AlterAnySecPolicy},
}

// serverScopedOpRights is Rename/Delete's right set for the node types that
// live outside a database, keyed to the same rights the matching New-X item
// already gates on.
//
// Without an entry a node falls to gate.ObjectWriteRights(), every member of which
// is database-, schema- or object-scoped, and the node carries no DBName — so
// gate.RightsAllow takes its `dbName == ""` branch and answers yes unconditionally.
// That branch is right for what it was written for (a folder-level action that
// prompts for a database) but not for Delete on a login, which was offered to
// every principal and then refused by the server. A new server-level family
// missing from this table is what TestServerScopedOpsAreGated catches.
var serverScopedOpRights = map[NodeType][]gate.Right{
	NodeLogin:                    {gate.AlterAnyLogin},
	NodeServerRole:               {gate.AlterAnyServerRole},
	NodeCredential:               {gate.AlterAnyCredential},
	NodeAudit:                    {gate.AlterAnyAudit},
	NodeServerAuditSpecification: {gate.AlterAnyAudit},
	NodeBackupDevice:             {gate.DiskAdmin},
	NodeEndpoint:                 {gate.AlterAnyEndpoint},
	// There is no ALTER ANY SERVER DDL TRIGGER; CONTROL SERVER is what SQL
	// Server checks for a server-scoped DDL trigger.
	NodeServerTrigger: {gate.ControlServer},
	// The Agent objects live in msdb, and the msdb role memberships are what
	// permit them — the same set the Agent menus use.
	NodeAgentJob:      gate.AgentWriteRights(),
	NodeAgentSchedule: gate.AgentWriteRights(),
	NodeAgentAlert:    gate.AgentWriteRights(),
	NodeAgentOperator: gate.AgentWriteRights(),
}
