package tui

import (
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// explorer_filter.go is the model behind Object Explorer's folder filter (SSMS's
// "Filter Settings"): what a folder can be filtered on, how a criterion matches
// a child node, and where the filtering happens. The dialog that edits one is in
// filter_dialog.go.
//
// A filter is applied at fetch time — fetchChildren for the tree, filterObjects
// for the Detail Browser's loaders — not at draw time, so changing or clearing
// one always goes through a folder reload. See App.applyNodeFilter.

// filterPropID names one property a folder's children can be filtered on. Every
// property maps to a field nodeData already carries, so matching needs no extra
// round trip.
type filterPropID int

const (
	fpName filterPropID = iota
	fpSchema
	fpCreationDate
	fpMemoryOptimized
)

// filterPropKind is a property's value type, deciding both the operators offered
// and how its value text is compared.
type filterPropKind int

const (
	filterText filterPropKind = iota
	filterDate
	filterBool
)

// filterProp is one filterable property, as the dialog lists it.
type filterProp struct {
	id   filterPropID
	name string
	kind filterPropKind
}

// filterOp is one comparison a criterion makes.
type filterOp int

const (
	opContains filterOp = iota
	opNotContains
	opEquals
	opNotEquals
	opOn
	opBefore
	opAfter
)

var filterOpNames = map[filterOp]string{
	opContains:    "Contains",
	opNotContains: "Does not contain",
	opEquals:      "Equals",
	opNotEquals:   "Does not equal",
	opOn:          "On",
	opBefore:      "Before",
	opAfter:       "After",
}

func (o filterOp) String() string { return filterOpNames[o] }

// filterOps returns the operators offered for a property of this kind, in dialog
// order; the first is the default, matching SSMS.
func filterOps(kind filterPropKind) []filterOp {
	switch kind {
	case filterDate:
		return []filterOp{opEquals, opOn, opBefore, opAfter}
	case filterBool:
		return []filterOp{opEquals, opNotEquals}
	default:
		return []filterOp{opContains, opNotContains, opEquals, opNotEquals}
	}
}

// filterDateLayout is the one date format a Creation Date criterion accepts. The
// dialog validates it before a filter is built, so matchCriterion never reports
// a parse failure.
const filterDateLayout = "2006-01-02"

// filterProps returns the properties a folder of type t offers, or nil when the
// folder can't be filtered — also the test callers use to decide whether to
// offer the Filter menu items.
//
// The list is limited to what the folder's own loader already fetches: SSMS
// offers Owner and Durability Type on Tables, both of which would cost a
// per-table query here, so neither is offered.
func filterProps(t NodeType) []filterProp {
	name := filterProp{id: fpName, name: "Name", kind: filterText}
	schema := filterProp{id: fpSchema, name: "Schema", kind: filterText}
	created := filterProp{id: fpCreationDate, name: "Creation Date", kind: filterDate}
	memOpt := filterProp{id: fpMemoryOptimized, name: "Is Memory Optimized", kind: filterBool}

	switch t {
	case NodeTables:
		return []filterProp{name, schema, created, memOpt}
	case NodeViews, NodeSystemViews,
		NodeStoredProcedures, NodeSystemProcedures,
		NodeFunctions, NodeSystemFunctions:
		return []filterProp{name, schema, created}
	case NodeSequences, NodeSynonyms, NodeTriggers:
		return []filterProp{name, schema}
	case NodeDatabases, NodeSystemDatabases, NodeLogins:
		return []filterProp{name, created}
	case NodeUsers, NodeDatabaseRoles, NodeSchemas, NodeServerRoles,
		NodePartitionFunctions, NodePartitionSchemes,
		NodeColumnMasterKeys, NodeColumnEncryptionKeys:
		return []filterProp{name}
	case NodeSecurityPolicies:
		return []filterProp{name, schema}
	case NodeCredentials, NodeAudits, NodeServerAuditSpecifications, NodeServerTriggers:
		return []filterProp{name, created}
	case NodeBackupDevices, NodeEndpoints:
		// No Creation Date: neither sys.backup_devices nor sys.endpoints
		// records one, so the criterion would reject every row.
		return []filterProp{name}
	}
	return nil
}

// filterCriterion is one property/operator/value triple.
type filterCriterion struct {
	prop  filterProp
	op    filterOp
	value string
}

// nodeFilter is a folder's live filter. nil means unfiltered; criteria with an
// empty value are dropped when one is built, so a non-nil filter always has at
// least one criterion.
type nodeFilter struct {
	criteria []filterCriterion
}

// active reports whether f filters anything — nil-safe, so callers can ask
// it of any folder node's Filter field.
func (f *nodeFilter) active() bool { return f != nil && len(f.criteria) > 0 }

// matches reports whether a child node passes every criterion — SSMS's AND
// semantics, each row narrowing the folder further.
func (f *nodeFilter) matches(d nodeData) bool {
	if !f.active() {
		return true
	}
	for _, c := range f.criteria {
		if !matchCriterion(c, d) {
			return false
		}
	}
	return true
}

// summary renders the filter for the status line, e.g.
// `Name contains "cust", Schema equals "dbo"`.
func (f *nodeFilter) summary() string {
	if !f.active() {
		return ""
	}
	parts := make([]string, 0, len(f.criteria))
	for _, c := range f.criteria {
		parts = append(parts, fmt.Sprintf("%s %s %q", c.prop.name, strings.ToLower(c.op.String()), c.value))
	}
	return strings.Join(parts, ", ")
}

func matchCriterion(c filterCriterion, d nodeData) bool {
	switch c.prop.kind {
	case filterDate:
		return matchDate(c.op, d.CreateDate, c.value)
	case filterBool:
		return matchBool(c.op, filterBoolValue(c.prop.id, d), c.value)
	default:
		return matchText(c.op, filterTextValue(c.prop.id, d), c.value)
	}
}

// filterTextValue is the child node's value for a text property. Name is the
// bare, schema-free name, the rule the rest of the tree follows, so filtering on
// "Name" never matches the schema prefix the label carries.
func filterTextValue(id filterPropID, d nodeData) string {
	if id == fpSchema {
		return d.Schema
	}
	return d.Name
}

func filterBoolValue(id filterPropID, d nodeData) bool {
	if id == fpMemoryOptimized {
		return d.IsMemoryOptimized
	}
	return false
}

// matchText compares case-insensitively, as SQL Server's default collation
// does.
func matchText(op filterOp, got, want string) bool {
	got, want = strings.ToLower(got), strings.ToLower(strings.TrimSpace(want))
	switch op {
	case opNotContains:
		return !strings.Contains(got, want)
	case opEquals:
		return got == want
	case opNotEquals:
		return got != want
	default:
		return strings.Contains(got, want)
	}
}

// matchDate compares by calendar day: a criterion is typed as a date, so "Equals
// 2026-08-13" means the whole day, not midnight. A node with no creation date
// never matches.
func matchDate(op filterOp, got time.Time, want string) bool {
	day, err := parseFilterDate(want)
	if err != nil || got.IsZero() {
		return false
	}
	gotDay := time.Date(got.Year(), got.Month(), got.Day(), 0, 0, 0, 0, time.UTC)
	switch op {
	case opBefore:
		return gotDay.Before(day)
	case opAfter:
		return gotDay.After(day)
	default: // opEquals, opOn
		return gotDay.Equal(day)
	}
}

// parseFilterDate parses a Creation Date criterion's value. Shared with the
// dialog, which rejects an unparseable one before a filter is built.
func parseFilterDate(s string) (time.Time, error) {
	return time.Parse(filterDateLayout, strings.TrimSpace(s))
}

func matchBool(op filterOp, got bool, want string) bool {
	b, err := parseFilterBool(want)
	if err != nil {
		return false
	}
	if op == opNotEquals {
		return got != b
	}
	return got == b
}

// parseFilterBool accepts the spellings a user is likely to type for a
// True/False criterion. Shared with the dialog, which rejects anything else.
func parseFilterBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true, nil
	case "false", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("expected True or False")
}

// filterChildren drops the children a folder's filter rejects. Container children
// — "System Views" inside Views, "System Databases" inside Databases — and error
// placeholders are always kept: the filter applies to a folder's objects, not
// its sub-folders, and hiding the error node would make a failed expand look
// empty.
func filterChildren(f *nodeFilter, children []*explorerNode) []*explorerNode {
	if !f.active() {
		return children
	}
	out := make([]*explorerNode, 0, len(children))
	for _, c := range children {
		if isContainerNode(c.data.Type) || c.data.Type == NodeError || f.matches(c.data) {
			out = append(out, c)
		}
	}
	return out
}

// filterObjects drops the objects a folder's filter rejects, given a function
// mapping one object to the nodeData fields the criteria read.
//
// This is the Detail Browser's half of the filter. Its loaders query gosmo
// directly rather than expanding the tree, so they hold gosmo objects rather
// than *explorerNode and can't use filterChildren; filtering the collection
// before rows are built keeps a progressive loader's row indices lined up.
//
// Takes the filter, not the folder node: both halves run on a background loader
// goroutine while the UI goroutine writes node.data.Filter underneath them (see
// explorerNode.snapshot), so the caller reads it where that is safe.
func filterObjects[T any](f *nodeFilter, items []T, key func(T) nodeData) []T {
	if !f.active() {
		return items
	}
	out := make([]T, 0, len(items))
	for _, it := range items {
		if f.matches(key(it)) {
			out = append(out, it)
		}
	}
	return out
}

// filterKey identifies a filterable folder by what it is rather than by the
// *explorerNode holding it, since disconnecting drops the subtree and
// reconnecting builds fresh nodes. Schema and table keep a table-scoped folder
// apart from the database-scoped folder of the same type.
type filterKey struct {
	conn   string
	dbName string
	schema string
	table  string
	typ    NodeType
}

func newFilterKey(sc *dbconn.ServerConn, d nodeData) filterKey {
	return filterKey{
		conn:   sysCompletionInventoryKey(sc.Opts),
		dbName: d.DBName,
		schema: d.Schema,
		table:  d.TableName,
		typ:    d.Type,
	}
}

// rememberFilter records, or for a nil f forgets, a folder's filter so a
// reconnect within the session comes back filtered, as SSMS does. Nothing is
// persisted — a filter lives as long as the process.
func (a *App) rememberFilter(sc *dbconn.ServerConn, d nodeData, f *nodeFilter) {
	if sc == nil {
		return
	}
	a.filterMu.Lock()
	defer a.filterMu.Unlock()
	if !f.active() {
		delete(a.savedFilters, newFilterKey(sc, d))
		return
	}
	if a.savedFilters == nil {
		a.savedFilters = make(map[filterKey]*nodeFilter)
	}
	a.savedFilters[newFilterKey(sc, d)] = f
}

// restoreFilters reattaches remembered filters to freshly loaded folder nodes.
// Called from fetchChildren on the loading goroutine, because a folder's filter
// must be in place before its children are fetched — restoring it afterwards
// leaves the node labelled "(filtered)" over an unfiltered list.
func (a *App) restoreFilters(sc *dbconn.ServerConn, children []*explorerNode) {
	a.filterMu.Lock()
	defer a.filterMu.Unlock()
	if len(a.savedFilters) == 0 {
		return
	}
	for _, c := range children {
		if len(filterProps(c.data.Type)) == 0 {
			continue
		}
		if f, ok := a.savedFilters[newFilterKey(sc, c.data)]; ok {
			c.data.Filter = f
		}
	}
}

// applyNodeFilter installs f (nil to clear) on a folder node and reloads it. The
// reload is what applies the filter, since fetchChildren filters the loader's
// result, and the rebuild repaints the "(filtered)" label suffix even while the
// node is collapsed.
func (a *App) applyNodeFilter(node *explorerNode, f *nodeFilter) {
	node.data.Filter = f
	a.rememberFilter(resolveConn(node), node.data, f)
	refreshExplorerNode(a, node)
	a.explorer.rebuild()
	if f.active() {
		a.setStatus(fmt.Sprintf("Filter applied to %s: %s", node.label, f.summary()))
		return
	}
	a.setStatus(fmt.Sprintf("Filter removed from %s", node.label))
}

// pushdown translates f into the server-side form, reporting false when any
// criterion cannot be expressed there.
//
// The push-down is an optimisation and nothing more: filterChildren and
// filterObjects still run over whatever comes back and remain the authority on
// what a filter means. The only way a translation can change a result is by
// narrowing *further* than the client pass would, so every criterion here either
// matches the client's comparison exactly — case-insensitively, whole calendar
// days, values trimmed as matchText trims them — or is refused outright.
//
// A criterion whose value the client itself cannot parse is refused rather than
// dropped: dropping it would send a wider filter, which is harmless, but
// refusing keeps both halves reading the same criterion list.
func (f *nodeFilter) pushdown() (gosmo.ObjectFilter, bool) {
	var out gosmo.ObjectFilter
	if !f.active() {
		return out, true
	}
	for _, c := range f.criteria {
		value := strings.TrimSpace(c.value)
		switch c.prop.kind {
		case filterDate:
			day, err := parseFilterDate(value)
			if err != nil {
				return gosmo.ObjectFilter{}, false
			}
			op, ok := pushdownDateOp(c.op)
			if !ok {
				return gosmo.ObjectFilter{}, false
			}
			out.Created = append(out.Created, gosmo.DateCriterion{Op: op, Day: day})
		case filterBool:
			b, err := parseFilterBool(value)
			if err != nil {
				return gosmo.ObjectFilter{}, false
			}
			if c.op == opNotEquals {
				b = !b
			}
			if c.prop.id != fpMemoryOptimized {
				return gosmo.ObjectFilter{}, false
			}
			out.MemoryOptimized = &b
		default:
			op, ok := pushdownTextOp(c.op)
			if !ok {
				return gosmo.ObjectFilter{}, false
			}
			crit := gosmo.TextCriterion{Op: op, Value: value}
			if c.prop.id == fpSchema {
				out.Schema = append(out.Schema, crit)
			} else {
				out.Name = append(out.Name, crit)
			}
		}
	}
	return out, true
}

func pushdownTextOp(op filterOp) (gosmo.TextOp, bool) {
	switch op {
	case opContains:
		return gosmo.TextContains, true
	case opNotContains:
		return gosmo.TextNotContains, true
	case opEquals:
		return gosmo.TextEquals, true
	case opNotEquals:
		return gosmo.TextNotEquals, true
	}
	return 0, false
}

// pushdownDateOp maps the date operators. opEquals and opOn are the same
// comparison here as in matchDate — two spellings for one meaning.
func pushdownDateOp(op filterOp) (gosmo.DateOp, bool) {
	switch op {
	case opEquals, opOn:
		return gosmo.DateOn, true
	case opBefore:
		return gosmo.DateBefore, true
	case opAfter:
		return gosmo.DateAfter, true
	}
	return 0, false
}

// serverFilter is what a loader passes to a *FilteredContext listing: f's
// translation, or an empty filter when it can't be pushed down, in which case
// the whole folder is read and filterChildren/filterObjects narrow it.
func serverFilter(f *nodeFilter) gosmo.ObjectFilter {
	out, ok := f.pushdown()
	if !ok {
		return gosmo.ObjectFilter{}
	}
	return out
}
