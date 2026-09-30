package xevent

import "strings"

// ColumnKind says where a column's value comes from.
type ColumnKind int

const (
	// ColName is the event's name (sql_batch_completed).
	ColName ColumnKind = iota
	// ColTimestamp is the event's timestamp, in local time.
	ColTimestamp
	// ColPackage is the event's package (sqlserver).
	ColPackage
	// ColField is one of the event's own fields (its <data> elements).
	ColField
	// ColAction is one of the actions the session attached (its <action>
	// elements).
	ColAction
)

// Column is one column the viewer can show. A field and an action can share a
// name (database_id is both), so a column is the pair, not the name.
type Column struct {
	Kind ColumnKind
	Name string // the field or action name; the builtins' own name otherwise
}

// The built-in columns, named as SSMS names them.
var (
	NameColumn      = Column{Kind: ColName, Name: "name"}
	TimestampColumn = Column{Kind: ColTimestamp, Name: "timestamp"}
	PackageColumn   = Column{Kind: ColPackage, Name: "package"}
)

// Value returns what e holds for c, and whether it holds anything at all: an
// event without the field is missing, which the filter treats as NULL.
func (e *Event) Value(c Column) (Value, bool) {
	switch c.Kind {
	case ColName:
		return Value{Name: c.Name, Value: e.Name}, true
	case ColTimestamp:
		return Value{Name: c.Name, Value: FormatTimestamp(e.Timestamp)}, !e.Timestamp.IsZero()
	case ColPackage:
		return Value{Name: c.Name, Value: e.Package}, e.Package != ""
	case ColField:
		return e.Field(c.Name)
	case ColAction:
		return e.Action(c.Name)
	}
	return Value{}, false
}

// Header names c for a grid header among the columns in all: its own name,
// with " (action)" appended to an action sharing its name with a field in all,
// and " (field)" or " (action)" to one named like a built-in column — some
// events carry a field called timestamp, and without the suffix the grid shows
// two columns headed the same with nothing to say which is the event's time.
func Header(c Column, all []Column) string {
	switch c.Kind {
	case ColName, ColTimestamp, ColPackage:
		return c.Name
	}
	suffix := " (action)"
	if c.Kind == ColField {
		suffix = " (field)"
	}
	for _, b := range []Column{NameColumn, TimestampColumn, PackageColumn} {
		if c.Name == b.Name {
			return c.Name + suffix
		}
	}
	if c.Kind == ColAction {
		for _, o := range all {
			if o.Kind == ColField && o.Name == c.Name {
				return c.Name + suffix
			}
		}
	}
	return c.Name
}

// FilterName is how the filter expression names c: its bare name where that
// resolves to c, field:/action: where it would resolve to something else — a
// built-in the field shadows, or the field an action shares its name with.
func (c Column) FilterName() string {
	switch c.Kind {
	case ColField:
		for _, b := range []Column{NameColumn, TimestampColumn, PackageColumn} {
			if c.Name == b.Name {
				return "field:" + c.Name
			}
		}
	case ColAction:
		return "action:" + c.Name
	}
	return c.Name
}

// Key names c in saved settings: the built-ins by name, a field or an action
// as field:<name> or action:<name>, since the two can share a name.
func (c Column) Key() string {
	switch c.Kind {
	case ColField:
		return "field:" + c.Name
	case ColAction:
		return "action:" + c.Name
	}
	return c.Name
}

// ParseColumnKey is Key's inverse. A key naming no built-in and carrying no
// kind prefix is not one Key writes, and is refused.
func ParseColumnKey(k string) (Column, bool) {
	switch {
	case strings.HasPrefix(k, "field:") && len(k) > len("field:"):
		return Column{Kind: ColField, Name: k[len("field:"):]}, true
	case strings.HasPrefix(k, "action:") && len(k) > len("action:"):
		return Column{Kind: ColAction, Name: k[len("action:"):]}, true
	}
	for _, b := range []Column{NameColumn, TimestampColumn, PackageColumn} {
		if k == b.Name {
			return b, true
		}
	}
	return Column{}, false
}
