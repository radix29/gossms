package gate

// sets.go is the named right sets an action asks about — one function per
// action family, so the same set is never written twice at two call sites and
// cannot drift between them. What each set may contain is docs/db-rules.md
// § Permission gating.

// DBScopedCredentialRights are what permits CREATE/ALTER/DROP DATABASE SCOPED
// CREDENTIAL: CONTROL on the database, and nothing else.
//
// The single entry is the whole point, and is why this is a function rather
// than an inline ControlDB at each call site. Probed live on win10cli
// (major 17, 2026-09-08) with a WITHOUT LOGIN user: all three statements went
// through under GRANT CONTROL ON DATABASE and all three were refused under
// GRANT ALTER ON DATABASE — so AlterDatabase is deliberately absent,
// unlike every other database-scoped set here, and adding it "for symmetry"
// would offer New/Delete/Properties to a principal the server then refuses.
// ALTER ANY CREDENTIAL is not the narrower twin either: asked of a database,
// HAS_PERMS_BY_NAME reads it as NULL rather than 0, which a gate built on it
// would take for "unknown" forever.
func DBScopedCredentialRights() []Right {
	return []Right{ControlDB}
}

// DatabaseWriteRights are what permits the ALTER DATABASE-shaped writes every
// Database Properties page but Permissions makes — any one of them.
func DatabaseWriteRights() []Right {
	return []Right{AlterDatabase, ControlDB, AlterAnyDatabase}
}

// ObjectWriteRights are what permits a write aimed at one object in a schema —
// a rename, a move, a drop, a new index, new statistics. Any one of them.
//
// The set reaches the object itself. SQL Server checks ALTER on the object,
// and a grant made directly on one table is reflected at no wider scope — such
// a principal reads 0 for every database- and schema-scope permission, so the
// four wider rights all deny and the action was withheld from someone who
// could perform it. AlterOnObject is the one that speaks for them.
//
// OBJECT scope costs no query per object, whatever HAS_PERMS_BY_NAME would
// cost: gosmo's object block reads the whole database in one pass, as a fourth
// part of the probe that was already running.
func ObjectWriteRights() []Right {
	return []Right{
		AlterDatabase, ControlDB, AlterAnySchema,
		AlterOnSchema, AlterOnObject,
	}
}

// ServiceBrokerWriteRights are what permits ALTER and DROP on one of the five
// schemaless Service Broker families — any one of them.
//
// The narrow right comes first, for ItemOn's note, and the two database-wide
// rights stay in the set because the probe found them sufficient: ALTER on the
// database altered and dropped every one of the seven families on majors 13,
// 14 and 17. ALTER ANY SCHEMA is deliberately absent — these objects have no
// schema, so it permits nothing here, which is the whole reason
// ObjectWriteRights() cannot stand in (see TestSchemalessDatabaseOpsAreGated).
func ServiceBrokerWriteRights(narrow Right) []Right {
	return []Right{narrow, AlterDatabase, ControlDB}
}

// RouteWriteRights are what permits ALTER ROUTE and DROP ROUTE alike — the
// route's Properties page and its Delete share one entry, unlike a queue's.
func RouteWriteRights() []Right {
	return ServiceBrokerWriteRights(AlterAnyRoute)
}

// BrokerPriorityWriteRights are what permits CREATE/ALTER/DROP BROKER
// PRIORITY: ALTER on the database, and the right that subsumes it.
//
// There is no narrow right to lead with — see AlterAnyMessageType's
// comment for why one cannot be invented — so unlike every other set here the
// note this produces names ALTER, which is what the server actually checks.
func BrokerPriorityWriteRights() []Right {
	return []Right{AlterDatabase, ControlDB}
}

// QueueAlterRights are what permits ALTER QUEUE — the one Service Broker
// family that is a schema object (a sys.objects row of type 'SQ'), so the
// ordinary object set fits it exactly: probed live 2026-09-16 on majors 13, 14
// and 17, ALTER on the queue itself, CONTROL on it, ALTER on its schema,
// ALTER ANY SCHEMA and ALTER or CONTROL on the database each altered the
// queue.
//
// It is not what permits the queue's Delete — see QueueDropRights, and never
// reuse one entry for both.
func QueueAlterRights() []Right { return ObjectWriteRights() }

// QueueDropRights are what permits DROP QUEUE, and they are QueueAlterRights
// minus the object-scoped ALTER. That one right is the asymmetry the probe
// found: ALTER ON OBJECT::<queue> alters the queue and is refused the drop
// (Msg 15151), which needs CONTROL on the queue or ALTER on its schema.
//
// CONTROL on the queue is in the set and the object-scoped ALTER is not, which
// is the distinction ControlOnObject exists to make: gosmo's object block
// matches CONTROL alongside whatever permission it asks about, so the ALTER map
// reads 1 for a principal holding either and putting AlterOnObject here
// would offer the drop to every ALTER holder the server refuses. The CONTROL
// map answers only for a CONTROL grant and for the queue's owner, both of whom
// the server does allow.
func QueueDropRights() []Right {
	return []Right{
		AlterDatabase, ControlDB, AlterAnySchema, AlterOnSchema,
		ControlOnObject,
	}
}

// ClassOneTransferRights are what permits Move to Schema on a class-1 object —
// every sys.objects family the tree offers a transfer on, the Service Broker
// queue among them — and they are neither of that family's other two sets:
// ALTER SCHEMA ... TRANSFER wants CONTROL on the object, which no amount of
// ALTER substitutes for.
//
// Probed live twice, both on major 17 with a WITHOUT LOGIN user per right and
// ALTER on the target schema held throughout, the two runs agreeing exactly:
// the queue on 2026-09-16, and a table on 2026-09-11. CONTROL on the object,
// owning it, CONTROL on the source schema and CONTROL on the database each
// transferred it, while ALTER on the object, ALTER on the source schema,
// ALTER ANY SCHEMA, db_ddladmin and ALTER on the database were every one
// refused Msg 15151 — the same split securableTransferRights found for a type
// and an XML schema collection. One set serves all of them because the server
// makes no distinction between the families here: the class is what it checks.
//
// Two of the four permitting rights are missing from the set, deliberately.
// CONTROL on the source schema reads through gosmo's schema probe as ALTER,
// which ALTER alone also reads, so asking for it would offer the move to the
// principals the server refuses; and the object's own CONTROL covers its owner
// already, since the object block records an owned object under every name it
// probes. The consequence is the same one QueueDropRights accepts: a principal
// holding only CONTROL on the schema is not offered a move the server would
// allow. Recorded in docs/decisions.md § Permission gating.
func ClassOneTransferRights() []Right {
	return []Right{ControlOnObject, ControlDB}
}

// AgentWriteRights are what permits SQL Agent's New Job / New Schedule /
// New Alert / New Operator — any one of them.
//
// Three facts settled live on 2026-08-27 shape this set, and each of them
// breaks the gate if it is assumed the other way:
//
//   - A sysadmin reads IS_ROLEMEMBER = 0 for all three SQLAgent* roles. It
//     maps to dbo, and dbo is not a member of any of them. Gating on the
//     Agent roles alone withholds every Agent action from the one login that
//     certainly may perform them, which is why CONTROL SERVER is here.
//   - The roles nest, and IS_ROLEMEMBER resolves the nesting: a member of
//     SQLAgentOperatorRole reads 1 for SQLAgentUserRole. So the narrowest role
//     is the whole test for "or above", and SQLAgentReaderRole and
//     SQLAgentOperatorRole need no separate check.
//   - msdb db_owner is a real non-sysadmin case and is not covered by either
//     of the above.
//
// The order is the message, not the test: Item shows only rights[0] in a
// withheld item's note, so the narrowest sufficient right goes first. Led with
// CONTROL SERVER the note read "needs CONTROL SERVER", which sends a user who
// wants to create a job away to ask for sysadmin.
func AgentWriteRights() []Right {
	return []Right{SQLAgentUser, MsdbOwner, ControlServer}
}
