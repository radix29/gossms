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
			b.FirstLSN, b.LastLSN, "", ""})
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
