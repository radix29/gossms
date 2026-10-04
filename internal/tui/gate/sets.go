package gate

// sets.go is the named right sets an action asks about — one function per
// action family, so the same set is never written twice at two call sites and
// cannot drift between them. What each set may contain is docs/db-rules.md
// § Permission gating.

// DBScopedCredentialRights are what permits CREATE/ALTER/DROP DATABASE SCOPED
// CREDENTIAL: CONTROL on the database, and nothing else.
//
// The single entry is the point, and why this is a function rather than an
// inline ControlDB at each call site. Probed live (major 17, 2026-09-08) with a
// WITHOUT LOGIN user: all three statements succeeded under GRANT CONTROL ON
// DATABASE and were refused under GRANT ALTER ON DATABASE, so AlterDatabase is
// deliberately absent, unlike every other database-scoped set here; adding it
// "for symmetry" would offer New/Delete/Properties to a principal the server
// refuses. ALTER ANY CREDENTIAL is not the narrower twin: asked of a database,
// HAS_PERMS_BY_NAME reads it as NULL rather than 0, which a gate would take for
// "unknown" forever.
func DBScopedCredentialRights() []Right {
	return []Right{ControlDB}
}

// DatabaseWriteRights are what permits the ALTER DATABASE-shaped writes every
// Database Properties page but Permissions makes — any one of them.
func DatabaseWriteRights() []Right {
	return []Right{AlterDatabase, ControlDB, AlterAnyDatabase}
}

// ObjectWriteRights are what permits a write aimed at one object in a schema
// (rename, move, drop, new index, new statistics). Any one of them.
//
// The set reaches the object itself. SQL Server checks ALTER on the object, and
// a grant made directly on one table is reflected at no wider scope: such a
// principal reads 0 for every database- and schema-scope permission, so the
// four wider rights all deny and the action would be withheld from someone who
// can perform it. AlterOnObject speaks for them.
//
// OBJECT scope costs no query per object: gosmo's object block reads the whole
// database in one pass, as a fourth part of the probe already running.
func ObjectWriteRights() []Right {
	return []Right{
		AlterDatabase, ControlDB, AlterAnySchema,
		AlterOnSchema, AlterOnObject,
	}
}

// ServiceBrokerWriteRights are what permits ALTER and DROP on one of the five
// schemaless Service Broker families. Any one of them.
//
// The narrow right comes first, for ItemOn's note, and the two database-wide
// rights stay because the probe found them sufficient: ALTER on the database
// altered and dropped every one of the seven families on majors 13, 14 and 17.
// ALTER ANY SCHEMA is deliberately absent: these objects have no schema, so it
// permits nothing here, which is why ObjectWriteRights() cannot stand in (see
// TestSchemalessDatabaseOpsAreGated).
func ServiceBrokerWriteRights(narrow Right) []Right {
	return []Right{narrow, AlterDatabase, ControlDB}
}

// RouteWriteRights are what permits ALTER ROUTE and DROP ROUTE alike — the
// route's Properties page and its Delete share one entry, unlike a queue's.
func RouteWriteRights() []Right {
	return ServiceBrokerWriteRights(AlterAnyRoute)
}

// BrokerPriorityWriteRights are what permits CREATE/ALTER/DROP BROKER PRIORITY:
// ALTER on the database, and the right that subsumes it.
//
// There is no narrow right to lead with (see AlterAnyMessageType's comment), so
// unlike every other set here the note it produces names ALTER, which is what
// the server checks.
func BrokerPriorityWriteRights() []Right {
	return []Right{AlterDatabase, ControlDB}
}

// QueueAlterRights are what permits ALTER QUEUE: the one Service Broker family
// that is a schema object (sys.objects type 'SQ'), so the ordinary object set
// fits exactly. Probed live 2026-09-16 on majors 13, 14 and 17: ALTER on the
// queue, CONTROL on it, ALTER on its schema, ALTER ANY SCHEMA and ALTER or
// CONTROL on the database each altered it.
//
// Not what permits the queue's Delete (see QueueDropRights); never reuse one
// entry for both.
func QueueAlterRights() []Right { return ObjectWriteRights() }

// QueueDropRights are what permits DROP QUEUE: QueueAlterRights minus the
// object-scoped ALTER. That is the asymmetry the probe found: ALTER ON
// OBJECT::<queue> alters the queue and is refused the drop (Msg 15151), which
// needs CONTROL on the queue or ALTER on its schema.
//
// CONTROL on the queue is in the set and the object-scoped ALTER is not, which
// is the distinction ControlOnObject exists to make: gosmo's object block
// matches CONTROL alongside whatever permission it asks about, so the ALTER map
// reads 1 for a principal holding either, and AlterOnObject here would offer
// the drop to every ALTER holder the server refuses. The CONTROL map answers
// only for a CONTROL grant and the queue's owner, both of whom the server
// allows.
func QueueDropRights() []Right {
	return []Right{
		AlterDatabase, ControlDB, AlterAnySchema, AlterOnSchema,
		ControlOnObject,
	}
}

// ClassOneTransferRights are what permits Move to Schema on a class-1 object
// (every sys.objects family the tree offers a transfer on, the Service Broker
// queue among them), and they are neither of that family's other two sets:
// ALTER SCHEMA ... TRANSFER wants CONTROL on the object, which no amount of
// ALTER substitutes for.
//
// Probed live twice on major 17 (queue 2026-09-16, table 2026-09-11), a WITHOUT
// LOGIN user per right with ALTER on the target schema held throughout, the
// runs agreeing exactly: CONTROL on the object, owning it, CONTROL on the
// source schema and CONTROL on the database each transferred it, while ALTER on
// the object, ALTER on the source schema, ALTER ANY SCHEMA, db_ddladmin and
// ALTER on the database were all refused Msg 15151 (the same split
// securableTransferRights found for a type and an XML schema collection). One
// set serves all families because the server checks the class, not the family.
//
// Two of the four permitting rights are deliberately missing. CONTROL on the
// source schema reads through gosmo's schema probe as ALTER, which ALTER alone
// also reads, so asking for it would offer the move to principals the server
// refuses; and the object's own CONTROL already covers its owner, since the
// object block records an owned object under every name it probes. As with
// QueueDropRights, a principal holding only CONTROL on the schema is not
// offered a move the server would allow. Recorded in docs/decisions.md §
// Permission gating.
func ClassOneTransferRights() []Right {
	return []Right{ControlOnObject, ControlDB}
}

// AgentWriteRights are what permits SQL Agent's New Job / New Schedule / New
// Alert / New Operator. Any one of them.
//
// Three facts settled live on 2026-08-27 shape this set; assuming any the other
// way breaks the gate:
//
//   - A sysadmin reads IS_ROLEMEMBER = 0 for all three SQLAgent* roles. It
//     maps to dbo, which is not a member of any of them. Gating on the Agent
//     roles alone withholds every Agent action from the one login that
//     certainly may perform them, hence CONTROL SERVER.
//   - The roles nest, and IS_ROLEMEMBER resolves the nesting: a member of
//     SQLAgentOperatorRole reads 1 for SQLAgentUserRole. So the narrowest role
//     is the whole test for "or above"; SQLAgentReaderRole and
//     SQLAgentOperatorRole need no separate check.
//   - msdb db_owner is a real non-sysadmin case covered by neither.
//
// The order is the message, not the test: Item shows only rights[0] in a
// withheld item's note, so the narrowest sufficient right goes first. Led with
// CONTROL SERVER the note read "needs CONTROL SERVER", sending a user who wants
// to create a job off to ask for sysadmin.
func AgentWriteRights() []Right {
	return []Right{SQLAgentUser, MsdbOwner, ControlServer}
}

// AgentPropertiesRights are what permits SQL Server Agent Properties' writes —
// sp_set_sqlagent_properties. EXECUTE on it is granted to nobody, so msdb
// db_owner runs it as owner, and sysadmin by being sysadmin; CONTROL SERVER
// without sysadmin is refused partway (see Sysadmin), and SQLAgent* roles and
// public are refused Msg 229 (probed on 17, 2026-10-01).
func AgentPropertiesRights() []Right {
	return []Right{MsdbOwner, Sysadmin}
}

// DatabaseMailConfigRights are what permits configuring Database Mail —
// accounts, profiles, grants, parameters, Start/Stop, purging the log. Any
// one of them: no sysmail_* procedure checks sysadmin, so access is ordinary
// msdb permission, and CONTROL SERVER without sysadmin configures as well
// (probed on 13 and 17; docs/decisions.md).
func DatabaseMailConfigRights() []Right {
	return []Right{MsdbOwner, ControlServer}
}

// DatabaseMailSendRights are what permits sending through sp_send_dbmail —
// Send Test E-Mail. DatabaseMailUserRole leads, as the narrowest right that
// is enough (see AgentWriteRights on the order); a member sends through the
// profiles granted to them, the other two through any (W8).
func DatabaseMailSendRights() []Right {
	return []Right{DatabaseMailUser, MsdbOwner, ControlServer}
}

// DatabaseMailLogReadRights are what permits reading msdb.dbo.sysmail_event_log
// — View Database Mail Log. SELECT on the view is granted to
// DatabaseMailUserRole alone; db_datareader, db_owner and CONTROL SERVER
// read it as well, and a login holding none (public, or msdb's guest) is
// refused Msg 229 (probed on 17, 2026-10-01). A DatabaseMailUserRole member
// alone sees only the events of its own items; the other three read the whole
// log (docs/decisions.md). An explicit GRANT SELECT on the view is not probed
// and reads as withheld.
func DatabaseMailLogReadRights() []Right {
	return []Right{DatabaseMailUser, MsdbDataReader, MsdbOwner, ControlServer}
}
