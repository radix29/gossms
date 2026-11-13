// Package clitest is test support shared by the command line's test packages
// (internal/cli and internal/cli/cmd): gosmo's catalog reads as gosmotest
// rows, one encoder per read.
//
// Each encoder returns one row of the result set exactly as the server sends
// it, column for column in the order gosmo's query selects them, so a gosmo
// change to a query's shape or its scanning fails the tests built on them
// (docs/testing.md). Only tests import it.
package clitest

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
)

// Nullable is v, or NULL for the zero value.
func Nullable[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

// DatabaseRow is an online, FULL-recovery database at compatibility level
// 160 in collation Latin1_General_CI_AS, as one row of the sys.databases
// read DatabaseByName makes.
func DatabaseRow(name string, id int, created time.Time) []any {
	return []any{name, id, "ONLINE", "FULL", 160, "Latin1_General_CI_AS", "Latin1_General_CI_AS", false, created, 0, false}
}

// ObjectInfoRow is o as one row of ObjectByName's and ObjectsNamed's select.
func ObjectInfoRow(o *gosmo.ObjectInfo) []any {
	return []any{o.ObjectID, o.Schema, o.Name, o.Type, o.TypeDesc, o.ParentSchema, o.ParentName, o.Created, o.Modified, o.IsMSShipped}
}

// TableRow is t, created and modified at, as one row of the table list's
// and TableByName's select.
func TableRow(t *gosmo.Table, at time.Time) []any {
	return []any{t.ObjectID, t.Schema, t.Name, at, at, false, false, t.IsSystem, false, false, false, false}
}

// ViewRow is v, created and modified at, as one row of ViewByName's select.
func ViewRow(v *gosmo.View, at time.Time) []any {
	return []any{v.ObjectID, v.Schema, v.Name, at, at, v.IsSchemaBound}
}

// ColumnRow is c as one row of gosmo's column select.
func ColumnRow(c *gosmo.Column) []any {
	dcName, dcDef := "", ""
	if c.DefaultValue != nil {
		dcName, dcDef = c.DefaultValue.Name, c.DefaultValue.Definition
	}
	return []any{c.Name, c.OrdinalPosition, string(c.DataType),
		c.MaxLength, c.Precision, c.Scale,
		c.IsNullable, c.IsIdentity, c.IsComputed,
		c.ComputedText, dcName, dcDef,
		c.IsRowGUID, c.Collation,
		Nullable(c.IdentitySeed), Nullable(c.IdentityIncrement),
		c.IsPrimaryKey,
		Nullable(c.TypeSchema), c.IsUserDefinedType,
		c.IsPersisted, c.IdentityNotForReplication,
		c.IsSparse, c.IsColumnSet,
		c.MaskingFunction,
		c.GeneratedAlwaysType, c.IsHidden, c.IsFileStream,
		c.GraphType, false,
		"", "", "",
		"", "", false,
		0, "",
		"", "", "", "",
		c.IsTypeHidden}
}

// IndexRow is ix as one row of gosmo's index list: a rowstore index on
// PRIMARY, with a filter NULL when it is hidden.
func IndexRow(ix *gosmo.Index) []any {
	var filter any = ix.FilterDefinition
	if ix.IsFilterHidden {
		filter = nil
	}
	return []any{ix.Name, ix.IndexID, string(ix.Type),
		ix.IsUnique, ix.IsPrimaryKey, ix.IsUniqueConstraint,
		ix.IsDisabled, ix.IsHypothetical, 0, filter,
		false, false, true, true,
		nil,
		"PRIMARY", false, true, "",
		false, false, false,
		0, 0,
		false, "", "",
		false, "",
		"", nil, nil, nil, nil,
		"", "", "", "",
		0}
}

// ParameterRow is one row of gosmo's parameter select: a parameter of a
// system type.
func ParameterRow(name string, ordinal int, typ string, precision, scale int, output, hasDefault bool) []any {
	return []any{name, ordinal, typ, 0, precision, scale, output, hasDefault, "sys", false, "", "", false, 0, "", false}
}

// StatisticRow is one row of gosmo's statistics select. The select reads a
// NULL filter as an empty string, so only hasFilter tells a hidden filter
// from none; the properties are NULL (nil) for a statistic never computed.
func StatisticRow(name string, id int, auto, user, hasFilter bool, filter string,
	lastUpdated, sampled, rows, steps, modified any, noRecompute bool) []any {
	return []any{name, id, auto, user, hasFilter, filter, lastUpdated, sampled, rows, steps, nil, noRecompute, false, modified}
}

// LoginRow is l as one row of Logins' and LoginByName's select: an empty
// default database or language is NULL, as for a certificate-mapped login.
func LoginRow(l *gosmo.Login) []any {
	return []any{l.Name, l.SID, string(l.LoginType), l.IsDisabled, Nullable(l.DefaultDatabase),
		l.CreateDate, l.ModifyDate, Nullable(l.DefaultLanguage), l.IsPolicyChecked, l.IsExpirationChecked}
}

// MemberList is names as the FOR JSON PATH column the role reads aggregate
// their members into: NULL for none.
func MemberList(names []string) any {
	if len(names) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, n := range names {
		if i > 0 {
			sb.WriteByte(',')
		}
		v, _ := json.Marshal(n)
		sb.WriteString(`{"v":` + string(v) + `}`)
	}
	sb.WriteByte(']')
	return sb.String()
}

// RoleRow is one row of ServerRoles' or Database.Roles' select: a role,
// its id, owner and members.
func RoleRow(name string, id int, fixed bool, owner string, members []string) []any {
	return []any{name, id, fixed, owner, MemberList(members)}
}

// RoleByNameRow is one row of ServerRoleByName's or Database.RoleByName's
// select, created and modified at.
func RoleByNameRow(name string, id int, fixed bool, owner string, members []string, at time.Time) []any {
	return []any{name, id, fixed, owner, []byte{byte(id)}, at, at, MemberList(members)}
}

// RoleMemberRow is m as one row of a role-members read.
func RoleMemberRow(m *gosmo.RoleMember) []any { return []any{m.Name, string(m.Type)} }

// UserRow is u as one row of Users' and UserByName's select; loginJoin is
// whether the query joined sys.server_principals (not on Azure SQL
// Database's Users), else the login columns are NULL.
func UserRow(u *gosmo.User, id int, loginJoin bool) []any {
	var login, disabled any
	if loginJoin && u.LoginName != "" {
		login, disabled = u.LoginName, u.LoginDisabled
	}
	return []any{u.Name, id, string(u.UserType), Nullable(u.DefaultSchema), u.CreateDate, u.ModifyDate,
		Nullable(u.AuthType), []byte{byte(id)}, login, disabled, Nullable(u.MappedObject)}
}

// ApplicationRoleRow is r as one row of the application-role select.
func ApplicationRoleRow(r *gosmo.ApplicationRole, id int) []any {
	return []any{r.Name, Nullable(r.DefaultSchema), id, r.CreateDate, r.ModifyDate}
}

// objectTypes is the sys.objects type the explicit-permission reads report
// for each object class (the first of gosmo's types for it).
var objectTypes = map[gosmo.SecurableClass]string{
	gosmo.SecurableTable: "U", gosmo.SecurableView: "V", gosmo.SecurableProcedure: "P",
	gosmo.SecurableScalarFunction: "FN", gosmo.SecurableInlineFunction: "IF",
	gosmo.SecurableTableFunction: "TF", gosmo.SecurableSequence: "SO", gosmo.SecurableSynonym: "SN",
}

// ObjectType is the sys.objects type of an object of class.
func ObjectType(class gosmo.SecurableClass) string { return objectTypes[class] }

// PermissionRow is e as one row of PermissionsForPrincipal's select:
// class_desc, permission, state, schema_name, object_name, object_type. A
// schema's own name is the select's schema_name.
func PermissionRow(e *gosmo.PrincipalSecurable) []any {
	class, schema, name := "OBJECT_OR_COLUMN", e.Schema, e.Name
	switch e.SecurableType {
	case gosmo.SecurableDatabase:
		class = "DATABASE"
	case gosmo.SecurableSchema:
		class, schema, name = "SCHEMA", e.Name, ""
	case gosmo.SecurableUserType:
		class = "TYPE"
	case gosmo.SecurableCertificate:
		class = "CERTIFICATE"
	}
	return []any{class, e.Permission, string(e.State), schema, name, ObjectType(e.SecurableType)}
}

// ColumnPermissionRow is e as one row of the column-permission select.
func ColumnPermissionRow(e *gosmo.ColumnPermissionEntry) []any {
	return []any{e.Principal, string(e.PrincipalType), e.Grantor, e.Schema, e.Object, ObjectType(e.ObjectType),
		e.Column, string(e.Permission), string(e.State)}
}

// ServerPermissionRow is e as one row of ServerPermissions' select.
func ServerPermissionRow(e *gosmo.ServerPermissionEntry) []any {
	return []any{e.Principal, string(e.PrincipalType), e.Grantor, string(e.Permission), string(e.State)}
}

// EffectivePermissionRow is e as one row of fn_my_permissions' select.
func EffectivePermissionRow(e *gosmo.EffectivePermission) []any {
	return []any{e.Entity, e.Subentity, e.Permission}
}

// CertificateRow is one row of the certificate select: a certificate with
// no private key, valid from at for a year, never backed up.
func CertificateRow(name string, id int, at time.Time) []any {
	return []any{name, id, 1, "", "NO_PRIVATE_KEY", at, at.AddDate(1, 0, 0), []byte{byte(id)},
		"dbo", "", "", 2048, true, nil, ""}
}

// SchemaRow is one row of the schema select, owned by dbo.
func SchemaRow(name string, id int) []any { return []any{name, id, "dbo"} }

// DataTypeRow is one row of the user-defined data type select: an alias of
// nvarchar(20).
func DataTypeRow(schema, name string, id int) []any {
	return []any{name, schema, id, "nvarchar", 40, 0, 0, "Latin1_General_CI_AS", true, "", "", "", ""}
}

// TableTypeRow is one row of the user-defined table type select.
func TableTypeRow(schema, name string, id int) []any {
	return []any{name, schema, id, id + 1000, false}
}

// DatabaseOf is d as one row of the sys.databases select Databases and
// DatabaseByName read, created at d.CreateDate.
func DatabaseOf(d *gosmo.Database) []any {
	return []any{d.Name, d.ID, string(d.State), Nullable(string(d.RecoveryModel)), int(d.CompatibilityLevel),
		Nullable(d.Collation), Nullable(d.CatalogCollation), d.IsReadOnly, d.CreateDate, d.SourceDatabaseID, d.IsDistributor}
}

// headerTypes is RESTORE HEADERONLY's BackupType for each set type, and
// historyTypes msdb.dbo.backupset.type's letter.
var (
	headerTypes = map[gosmo.BackupSetType]int{
		gosmo.BackupSetDatabase: 1, gosmo.BackupSetLog: 2, gosmo.BackupSetFile: 4, gosmo.BackupSetDifferential: 5,
		gosmo.BackupSetDifferentialFile: 6, gosmo.BackupSetPartial: 7, gosmo.BackupSetDifferentialPartial: 8,
	}
	historyTypes = map[gosmo.BackupSetType]string{
		gosmo.BackupSetDatabase: "D", gosmo.BackupSetLog: "L", gosmo.BackupSetFile: "F", gosmo.BackupSetDifferential: "I",
		gosmo.BackupSetDifferentialFile: "G", gosmo.BackupSetPartial: "P", gosmo.BackupSetDifferentialPartial: "Q",
	}
)

// BackupHeaderSet is hs as RESTORE HEADERONLY's result set: gosmo reads it
// by column name (its layout varies by version), so the set names them.
// Compressed is a tinyint, as the server sends it; the sizes are bigints
// here, numeric(20,0) on the server — gosmo reads both.
func BackupHeaderSet(hs []*gosmo.BackupHeader) gosmotest.Set {
	set := gosmotest.Set{Columns: []string{"BackupName", "BackupDescription", "BackupType", "ExpirationDate", "Compressed",
		"Position", "DatabaseName", "ServerName", "BackupStartDate", "BackupFinishDate", "BackupSize", "CompressedBackupSize",
		"HasBackupChecksums", "IsCopyOnly", "DatabaseVersion", "CompatibilityLevel", "SoftwareVersionMajor", "RecoveryModel"}}
	for _, h := range hs {
		compressed := int64(0)
		if h.Compressed {
			compressed = 1
		}
		set.Rows = append(set.Rows, []any{Nullable(h.BackupName), Nullable(h.Description), headerTypes[h.SetType], nil, compressed,
			h.Position, h.DatabaseName, h.ServerName, h.BackupStart, h.BackupFinish, h.BackupSize, h.CompressedSize,
			h.HasChecksums, h.IsCopyOnly, h.DatabaseVersion, int(h.CompatibilityLevel), h.SoftwareVersionMajor, Nullable(h.RecoveryModel)})
	}
	return set
}

// BackupFileListSet is fs as RESTORE FILELISTONLY's result set, by column
// name as BackupHeaderSet; a log file's filegroup is NULL.
func BackupFileListSet(fs []*gosmo.BackupFile) gosmotest.Set {
	set := gosmotest.Set{Columns: []string{"LogicalName", "PhysicalName", "Type", "FileGroupName", "Size", "MaxSize", "FileId"}}
	for i, f := range fs {
		set.Rows = append(set.Rows, []any{f.LogicalName, f.PhysicalName, string(f.Type), Nullable(f.FileGroupName), f.Size, f.MaxSize, i + 1})
	}
	return set
}

// BackupInfoRows is b, backup set id, as the rows of gosmo's msdb history
// select: one per media family (each of b.Devices), the set's columns
// repeated.
func BackupInfoRows(b *gosmo.BackupInfo, id int64) [][]any {
	var rows [][]any
	for _, dev := range b.Devices {
		rows = append(rows, []any{b.DatabaseName, b.BackupSetName, b.Description, historyTypes[b.SetType],
			b.BackupStart, b.BackupFinish, b.BackupSize, dev, b.UserName, b.ServerName,
			b.DatabaseVersion, int(b.CompatibilityLevel), b.Position, id, id, 1, b.IsCopyOnly,
			b.FirstLSN, b.LastLSN, "", "",
			b.FirstRecoveryForkGUID, b.LastRecoveryForkGUID, b.ForkPointLSN})
	}
	return rows
}

// InstanceFileRow is f as one row of InstanceFiles' sys.master_files select.
func InstanceFileRow(f *gosmo.InstanceFile) []any {
	return []any{f.DatabaseID, f.DatabaseName, f.FileID, f.Name, f.PhysicalName, f.Type}
}

// FragmentationRow is f as one row of a table's or view's fragmentation
// select.
func FragmentationRow(f *gosmo.IndexFragmentation) []any {
	return []any{f.IndexName, f.IndexID, f.AvgFragmentationPct, f.PageCount, f.FragmentCount, f.AvgPageSpaceUsedPct}
}

// DatabaseFragmentationRow is f as one row of Database.FragmentationStats'
// select.
func DatabaseFragmentationRow(f *gosmo.DatabaseIndexFragmentation) []any {
	return append(FragmentationRow(&f.IndexFragmentation),
		f.Schema, f.ObjectName, f.ObjectID, f.IsView, string(f.IndexType), max(f.PartitionCount, 1))
}

// AvailabilityGroupRow is one row of AvailabilityGroups' select (2017 and
// later): a WSFC group, id its group_id, this replica its primary.
func AvailabilityGroupRow(id, name string) []any {
	return []any{id, name, "", "", "WSFC", "SECONDARY", 3, 30000, 1, false, false, false, false, 0, false, "srv", "ONLINE", "HEALTHY"}
}

// AvailabilityDatabaseRow is one row of AvailabilityGroup.Databases'
// select: database, on group id's primary replica, synchronized.
func AvailabilityDatabaseRow(groupID, database string) []any {
	return []any{groupID, "rep-1", "srv", database, "gdb-" + database, true, true, "SYNCHRONIZED", "HEALTHY", "ONLINE",
		false, "", 0, 0, 0, 0, 0, nil, nil, nil, nil, nil}
}

// CheckDBSet is rows as DBCC CHECKDB … WITH TABLERESULTS's result set, by
// column name as the server sends them (2016 and later); the columns gosmo
// does not read are zero.
func CheckDBSet(rows []*gosmo.CheckDBError) gosmotest.Set {
	set := gosmotest.Set{Columns: []string{"Error", "Level", "State", "MessageText", "RepairLevel", "Status", "DbId",
		"DbFragId", "ObjectId", "IndexId", "PartitionId", "AllocUnitId", "RidDbId", "RidPruId", "File", "Page", "Slot",
		"RefDbId", "RefPruId", "RefFile", "RefPage", "RefSlot", "Allocation"}}
	for _, e := range rows {
		set.Rows = append(set.Rows, []any{e.Error, e.Level, e.State, e.Message, Nullable(e.RepairLevel), 0, 5,
			1, e.ObjectID, e.IndexID, e.PartitionID, e.AllocUnitID, 5, 1, e.File, e.Page, e.Slot,
			0, 0, 0, 0, 0, 1})
	}
	return set
}

// ActiveSessionRow is s as one row of ActiveSessions' and SessionByID's
// select. The database, status, last request start and wait type are NULL
// when empty, as DB_NAME and the joined request's columns come back; the
// texts are ISNULL'd to empty by the query.
func ActiveSessionRow(s *gosmo.ActiveSession) []any {
	return []any{s.SessionID, s.LoginName, s.HostName, s.ProgramName,
		Nullable(s.DatabaseName), Nullable(s.Status),
		s.CPUTime, s.MemoryUsage, s.TotalElapsedMS, Nullable(s.LastRequestStart),
		s.CommandText, s.RecentText, s.Command,
		s.OpenTransactionCount, s.Reads, s.Writes, s.LogicalReads,
		s.BlockingSessionID, Nullable(s.WaitType), s.WaitTimeMS, s.WaitResource,
		s.HostProcessID, s.FromCallerHost, s.IsUserProcess,
		s.RequestCPUTime, s.RequestElapsedMS, s.RequestReads, s.RequestWrites, s.RequestLogicalReads}
}

// WaitStatRow is w as one row of WaitStats' sys.dm_os_wait_stats select.
func WaitStatRow(w gosmo.WaitStat) []any {
	return []any{w.WaitType, w.WaitingTasks, w.WaitTimeMs, w.SignalWaitTimeMs, w.MaxWaitTimeMs}
}

// CachedQueryStatRow is q as one row of TopQueries' select.
func CachedQueryStatRow(q *gosmo.CachedQueryStat) []any {
	return []any{q.Database, q.QueryHash, q.PlanHandle, q.StatementStart, q.StatementEnd, q.Text,
		q.ExecutionCount, q.TotalCPUMicros, q.TotalDurationMicros, q.TotalLogicalReads, q.TotalLogicalWrites,
		q.LastExecutionTime, q.CreationTime}
}

// QueryStoreInfoRow is a Query Store in state, 100 MB of 1000 used, as the
// row of QueryStore.Options' sys.database_query_store_options select; the
// custom capture policy columns are NULL, as with any capture mode but
// CUSTOM.
func QueryStoreInfoRow(state gosmo.QueryStoreState) []any {
	return []any{string(state), string(state), 0, int64(100), int64(1000), 900, 60, 200,
		"AUTO", "AUTO", 30, "ON", nil, nil, nil, nil}
}

// QSQueryStatRow is st as one row of the per-query Query Store reports'
// select: no forced plan and a query not executed in the window are NULL.
func QSQueryStatRow(st *gosmo.QSQueryStat) []any {
	var last any
	if !st.LastExecutionTime.IsZero() {
		last = st.LastExecutionTime
	}
	return []any{st.QueryID, st.QueryText, st.ObjectName, st.ExecCount, st.PlanCount, Nullable(st.ForcedPlanID),
		last, st.Value, st.BaselineValue, st.BaselineExecCount, st.Regression, st.Variation}
}

// QSWaitStatRow is w as one row of QueryStore.WaitCategories' select.
func QSWaitStatRow(w *gosmo.QSWaitStat) []any {
	return []any{string(w.Category), w.ExecCount, w.Value}
}

// EventSessionRows is es, session id, as the rows of the five reads
// XEvents.SessionByName and Sessions make: the session, its events, their
// actions (none), its targets, and their fields. Events are numbered from 1
// and targets after them, one counter as the server keeps it.
func EventSessionRows(es *gosmo.EventSession, id int) (session []any, events, actions, targets, fields [][]any) {
	session = []any{id, es.Name, es.StartupState, es.IsRunning, Nullable(es.RetentionMode),
		es.MaxDispatchLatency.Milliseconds(), es.MaxMemory, es.MaxEventSize, Nullable(es.MemoryPartitionMode),
		es.TrackCausality, int64(es.MaxDuration.Seconds()), es.DroppedEvents}
	obj := 0
	for _, e := range es.Events {
		obj++
		events = append(events, []any{id, obj, e.Package, e.Name, Nullable(e.Predicate)})
	}
	for _, t := range es.Targets {
		obj++
		targets = append(targets, []any{id, obj, t.Package, t.Name})
		for _, f := range t.Fields {
			base := "int"
			if f.IsString || !isNumber(f.Value) {
				base = "nvarchar"
			}
			fields = append(fields, []any{id, obj, f.Name, f.Value, base})
		}
	}
	return session, events, actions, targets, fields
}

func isNumber(s string) bool {
	var n int64
	_, err := fmt.Sscan(s, &n)
	return err == nil && fmt.Sprint(n) == s
}

// EventXML is e as one <event> element, as fn_xe_file_target_read_file's
// event_data and a ring_buffer's target_data carry it: an IsXML value as
// markup, any other escaped as text; a field's Text as its <text> (the map
// value of a wait type), and the event's actions after its data — with an
// event_sequence action for a Seq no action carries.
func EventXML(e gosmo.XEvent) string {
	var b strings.Builder
	esc := func(s string) string {
		var w strings.Builder
		xml.EscapeText(&w, []byte(s))
		return w.String()
	}
	value := func(f gosmo.XEValue) string {
		v := esc(f.Value)
		if f.IsXML {
			v = f.Value
		}
		out := "<value>" + v + "</value>"
		if f.Text != "" {
			out += "<text>" + esc(f.Text) + "</text>"
		}
		return out
	}
	fmt.Fprintf(&b, `<event name="%s" package="%s" timestamp="%s">`, esc(e.Name), esc(e.Package),
		e.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"))
	for _, f := range e.Fields {
		fmt.Fprintf(&b, `<data name="%s">%s</data>`, esc(f.Name), value(f))
	}
	hasSeq := false
	for _, a := range e.Actions {
		hasSeq = hasSeq || a.Name == "event_sequence"
		fmt.Fprintf(&b, `<action name="%s" package="package0">%s</action>`, esc(a.Name), value(a))
	}
	if e.Seq != 0 && !hasSeq {
		fmt.Fprintf(&b, `<action name="event_sequence" package="package0"><value>%d</value></action>`, e.Seq)
	}
	b.WriteString("</event>")
	return b.String()
}

// RingBufferXML is events as a ring_buffer's target_data.
func RingBufferXML(events []gosmo.XEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<RingBufferTarget truncated="0" processingTime="0" totalEventsProcessed="%d" eventCount="%d" droppedCount="0" memoryUsed="0">`,
		len(events), len(events))
	for _, e := range events {
		b.WriteString(EventXML(e))
	}
	b.WriteString("</RingBufferTarget>")
	return b.String()
}

// DatabaseOptionsRow is o as the row of Database.Options' select (2022 and
// later: the ADR and ledger columns present), the database owned by o.Owner.
func DatabaseOptionsRow(o *gosmo.DatabaseOptions) []any {
	return []any{Nullable(o.Owner), o.PageVerify, string(o.UserAccess), o.Containment, o.DefaultCursor == "LOCAL",
		o.SnapshotIsolation, o.AutoClose, o.AutoShrink, o.AutoCreateStats, o.AutoUpdateStats, o.AutoUpdateStatsAsync,
		o.ANSINullDefault, o.ANSINulls, o.ANSIPadding, o.ANSIWarnings, o.ArithAbort, o.ConcatNullYieldsNull,
		o.NumericRoundAbort, o.QuotedIdentifier, o.RecursiveTriggers, o.CursorCloseOnCommit, o.ReadCommittedSnapshot,
		o.IsTrustworthy, o.IsBrokerEnabled, o.IsEncrypted, o.NonTransactedAccess, o.DirectoryName,
		o.DBChaining, o.ForcedParameterization, o.DateCorrelation, o.HonorBrokerPriority,
		Nullable(o.DelayedDurability), o.TargetRecoveryTimeSeconds, o.AcceleratedDatabaseRecovery, o.IsLedger}
}

// QueryStoreOptionsRow is i as the row of QueryStore.Options' select (2019
// and later: every column present).
func QueryStoreOptionsRow(i *gosmo.QueryStoreInfo) []any {
	return []any{string(i.DesiredState), string(i.ActualState), i.ReadOnlyReason, i.CurrentStorageMB, i.MaxStorageMB,
		i.FlushIntervalSec, i.IntervalMinutes, i.MaxPlansPerQuery, string(i.CaptureMode), string(i.SizeCleanupMode),
		i.StaleThresholdDays, string(i.WaitStatsCaptureMode), int64(i.CapturePolicyExecCount),
		i.CapturePolicyCompileCPUMs, i.CapturePolicyExecCPUMs, int64(i.CapturePolicyStaleHours)}
}

// ScopedConfigRow is c as one row of Database.DatabaseScopedConfigs' select.
func ScopedConfigRow(c *gosmo.DatabaseScopedConfig) []any {
	return []any{c.ID, c.Name, c.Value, c.ValueForSecondary, c.IsValueDefault, c.ValueType, c.ValueForSecondaryType}
}

// SnapshotRow is s as one row of the database snapshot select.
func SnapshotRow(s *gosmo.DatabaseSnapshot, at time.Time) []any {
	return []any{s.Name, s.DatabaseID, s.SourceDatabase, s.SourceDatabaseID, "ONLINE", at}
}

// agentDate is t's UTC fields as msdb's YYYYMMDD and HHMMSS integers, both 0
// for the zero time (a job that never ran).
func agentDate(t time.Time) (date, clock int) {
	if t.IsZero() {
		return 0, 0
	}
	t = t.UTC()
	return t.Year()*10000 + int(t.Month())*100 + t.Day(), t.Hour()*10000 + t.Minute()*100 + t.Second()
}

// agentDuration is d as msdb's HHMMSS integer.
func agentDuration(d time.Duration) int {
	s := int(d / time.Second)
	return s/3600*10000 + s%3600/60*100 + s%60
}

// JobRow is one row of the job read Jobs and JobByName make (a sysadmin's
// route). The state column is the sysjobactivity-derived one — executing or
// idle; a server answers Agent's live state to xp_sqlagent_enum_jobs, a
// separate read.
func JobRow(j *gosmo.Job) []any {
	date, clock := agentDate(j.LastRunDate)
	state := 4
	if j.CurrentState == gosmo.JobStateExecuting {
		state = 1
	}
	return []any{j.JobID, j.Name, j.Description, j.IsEnabled, j.Category, j.OwnerLoginName,
		j.DateCreated, j.DateModified, j.StartStepID, int(j.DeleteLevel), int(j.NotifyLevelEmail),
		j.NotifyEmailOperatorName, date, clock, int(j.LastRunOutcome), agentDuration(j.LastRunDuration),
		Nullable(j.NextRunDate), state}
}

// JobStepRow is one row of a job's steps read.
func JobStepRow(s *gosmo.JobStep) []any {
	date, clock := agentDate(s.LastRunDate)
	return []any{s.StepID, s.Name, s.Subsystem, s.Command, s.Database,
		s.OnSuccessAction, s.OnSuccessStepID, s.OnFailAction, s.OnFailStepID,
		int(s.LastRunOutcome), date, clock, agentDuration(s.LastRunElapsed),
		s.RetryAttempts, s.RetryInterval, s.OutputFileName, s.Flags,
		s.ProxyName, s.AdditionalParameters, s.CmdExecSuccessCode,
		s.Server, s.DatabaseUserName, s.OSRunPriority}
}

// ScheduleRow is one row of a schedule read; a zero ActiveEndDate is msdb's
// "no end date" 99991231.
func ScheduleRow(s *gosmo.Schedule) []any {
	start, _ := agentDate(s.ActiveStartDate)
	end := 99991231
	if !s.ActiveEndDate.IsZero() {
		end, _ = agentDate(s.ActiveEndDate)
	}
	return []any{s.ID, s.Name, s.Enabled, int(s.FreqType), s.FreqInterval, int(s.FreqSubdayType),
		s.FreqSubdayInterval, s.FreqRelativeInterval, s.FreqRecurrenceFactor, start, end,
		s.ActiveStartTime, s.ActiveEndTime, s.CreateDate, s.ModifyDate, s.OwnerLoginName}
}

// JobHistoryRow is one row of SearchJobHistory's read.
func JobHistoryRow(h *gosmo.JobHistoryEntry) []any {
	date, clock := agentDate(h.RunDate)
	return []any{h.JobName, date, clock, agentDuration(h.Duration), int(h.Outcome), h.Message, h.StepID, h.StepName}
}

// AgentStatusRow is the row of the Agent service read.
func AgentStatusRow(status string, startup time.Time) []any {
	return []any{status, Nullable(startup)}
}

// --- Storage, sizes and access ------------------------------------------------

// fileGrowthCells is a file's max_size and growth as sys.database_files and
// sys.master_files hold them: pages (-1 unlimited), and pages or a percent.
func fileGrowthCells(f *gosmo.DatabaseFileInfo) (maxSize, growth int64) {
	maxSize = -1
	if f.MaxSizeKB >= 0 {
		maxSize = f.MaxSizeKB / 8
	}
	growth = f.GrowthKB / 8
	if f.IsPercentGrowth {
		growth = int64(f.GrowthPercent)
	}
	return maxSize, growth
}

// fileState is f's state_desc, ONLINE when the test left it unset.
func fileState(f *gosmo.DatabaseFileInfo) string {
	if f.State == "" {
		return "ONLINE"
	}
	return string(f.State)
}

// DatabaseFileRow is f as one row of Database.Files' sys.database_files
// select. Sizes are in KB as gosmo reports them; max_size and growth go back
// to pages.
func DatabaseFileRow(f *gosmo.DatabaseFileInfo) []any {
	maxSize, growth := fileGrowthCells(f)
	return []any{f.FileID, f.Name, f.PhysicalName, string(f.Type), f.FileGroup, fileState(f),
		f.SizeKB, maxSize, growth, f.IsPercentGrowth, f.SpaceUsedKB}
}

// MasterFileRow is f as one row of Server.DatabaseFiles' sys.master_files
// select: no filegroup or space used, and the size on disk NULL when
// SizeOnDiskKB is 0.
func MasterFileRow(f *gosmo.DatabaseFileInfo) []any {
	maxSize, growth := fileGrowthCells(f)
	return []any{f.FileID, f.Name, f.PhysicalName, string(f.Type), fileState(f),
		f.SizeKB, maxSize, growth, f.IsPercentGrowth, Nullable(f.SizeOnDiskKB)}
}

// FileVolumeRow is v as one row of FileVolumes' select by a login with VIEW
// SERVER STATE.
func FileVolumeRow(v gosmo.FileVolume) []any {
	return []any{true, v.DatabaseID, v.FileID, v.PhysicalName, Nullable(v.MountPoint), Nullable(v.VolumeName),
		v.TotalBytes, v.AvailableBytes}
}

// FileVolumesHiddenRow is FileVolumes' one row for a login without VIEW
// SERVER STATE.
func FileVolumesHiddenRow() []any { return []any{false, nil, nil, nil, nil, nil, nil, nil} }

// DatabaseSizeRow is one row of DatabaseSizes' select: database id's data
// and log KB.
func DatabaseSizeRow(id int, s gosmo.DatabaseSize) []any { return []any{id, s.DataKB, s.LogKB} }

// DatabaseAccessRow is one row of DatabaseAccess' select: HAS_DBACCESS for
// database id, nil for NULL.
func DatabaseAccessRow(id int, access any) []any { return []any{id, access} }

// SpaceUsedRow is si as the row of Database.SpaceUsed's select.
func SpaceUsedRow(si gosmo.SpaceInfo) []any {
	return []any{si.TotalMB, si.DataMB, si.LogMB, si.UnallocatedMB, si.AvailLogMB}
}

// SpaceReportRow is r as the row of Database.SpaceReport's select, in the
// pages it counts: every KB figure must be a multiple of 8. DatabaseSizeKB,
// UnallocatedKB and UnusedKB are not sent — gosmo derives them from the rest.
// The checkpoint files are NULL for a database without a memory-optimized
// filegroup (CheckpointFilesKB 0, XTPPrecreatedHidden false).
func SpaceReportRow(r gosmo.SpaceReport) []any {
	var ckpt any
	if r.CheckpointFilesKB != 0 || r.XTPPrecreatedHidden {
		ckpt = r.CheckpointFilesKB / 8
	}
	return []any{r.DataFilesKB / 8, r.LogFilesKB / 8, r.DataUsedKB / 8, r.LogUsedKB / 8, ckpt,
		nil, !r.XTPPrecreatedHidden,
		(r.DataKB + r.IndexKB + r.UnusedKB) / 8, (r.DataKB + r.IndexKB) / 8, r.DataKB / 8}
}

// LogSpaceRow is l as the row of Database.LogSpace's select. Whether gosmo
// reads the VLF counts is the server's build (2016 SP2 on), not this row.
func LogSpaceRow(l gosmo.LogSpace) []any {
	var vlfs, active any
	if l.VLFsKnown {
		vlfs, active = int64(l.VLFs), int64(l.ActiveVLFs)
	}
	return []any{l.SizeKB, l.UsedKB, l.UsedPercent, l.ReuseWait, vlfs, active}
}

// TableSizeRow is ts as one row of Database.TableSizes' select, in pages:
// its KB figures must be multiples of 8 (a memory-optimized table's are 0).
func TableSizeRow(ts *gosmo.TableSize) []any {
	return []any{ts.ObjectID, ts.Schema, ts.Name, ts.IsMemoryOptimized, ts.Rows,
		(ts.DataKB + ts.IndexKB + ts.UnusedKB) / 8, (ts.DataKB + ts.IndexKB) / 8, ts.DataKB / 8}
}

// --- Server log -----------------------------------------------------------------

// ErrorLogEntryRow is e as one row of ReadLog's xp_readerrorlog call: the
// middle column is the Agent log's severity, else the SQL Server log's
// ProcessInfo.
func ErrorLogEntryRow(e *gosmo.ErrorLogEntry, agent bool) []any {
	if agent {
		return []any{e.Date, e.ErrorLevel, e.Text}
	}
	return []any{e.Date, e.Process, e.Text}
}

// ErrorLogFileRow is f as one row of sp_enumerrorlogs.
func ErrorLogFileRow(f *gosmo.ErrorLogFile) []any { return []any{f.Number, f.Date, f.SizeBytes} }

// --- Capabilities -------------------------------------------------------------

// capabilityAnswer is st as HAS_PERMS_BY_NAME answers it: NULL (ok false)
// for CapabilityUnknown, which gosmo records as no state.
func capabilityAnswer(st gosmo.CapabilityState) (answer any, ok bool) {
	switch st {
	case gosmo.CapabilityGranted:
		return 1, true
	case gosmo.CapabilityDenied:
		return 0, true
	}
	return nil, false
}

// capabilityRows is the (kind, name, answer) rows of roles (kind R) and of
// each kind's permission map.
func capabilityRows(roles map[string]bool, perms map[string]map[string]gosmo.CapabilityState) [][]any {
	var rows [][]any
	for name, in := range roles {
		answer := 0
		if in {
			answer = 1
		}
		rows = append(rows, []any{"R", name, answer})
	}
	for kind, m := range perms {
		for name, st := range m {
			answer, _ := capabilityAnswer(st)
			rows = append(rows, []any{kind, name, answer})
		}
	}
	return rows
}

// ServerCapabilityRows is c's roles, server permissions and master
// permissions as the rows of Server.Capabilities' probe. The per-securable
// maps are not encoded.
func ServerCapabilityRows(c *gosmo.Capabilities) [][]any {
	return capabilityRows(c.ServerRoles, map[string]map[string]gosmo.CapabilityState{
		"P": c.ServerPermissions, "M": c.MasterPermissions})
}

// DatabaseCapabilityRows is c's roles and database permissions as the rows
// of Database.Capabilities' probe inside the database (its HAS_DBACCESS
// question comes first, answered apart). The per-securable maps are not
// encoded.
func DatabaseCapabilityRows(c *gosmo.DatabaseCapabilities) [][]any {
	return capabilityRows(c.Roles, map[string]map[string]gosmo.CapabilityState{"P": c.Permissions})
}
