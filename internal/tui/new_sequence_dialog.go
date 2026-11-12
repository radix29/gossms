package tui

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_sequence_dialog.go is the New Sequence dialog (a database's Sequences
// folder), built on newObjectDialog. Three pages, as SSMS has them: General
// (name, schema, type and every CREATE SEQUENCE option), Permissions
// (object-scoped GRANT/DENY, Sequence Properties' matrix with no existing
// entries) and Extended Properties. The last two run after the CREATE, against
// the typed name.

// nseqTypes are the types CREATE SEQUENCE takes: the integer types, and
// decimal/numeric at scale 0. A user-defined alias of one is accepted too, and
// not offered.
var nseqTypes = []gosmo.DataType{
	gosmo.DataTypeTinyInt, gosmo.DataTypeSmallInt, gosmo.DataTypeInt, gosmo.DataTypeBigInt,
	gosmo.DataTypeDecimal, gosmo.DataTypeNumeric,
}

// The Cache picker's three CACHE forms.
const (
	nseqCacheDefault = "Default size"
	nseqCacheNone    = "No cache"
	nseqCacheSize    = "Cache size"
)

var nseqCacheItems = []string{nseqCacheDefault, nseqCacheNone, nseqCacheSize}

// schemaObjectPrefetch is everything the New Sequence and New Synonym dialogs
// read before they open: the two differ only in which family fills existing.
type schemaObjectPrefetch struct {
	// existing holds every object of the dialog's own family as sequenceKey
	// spells it, under the database's collation. Only that family: an object
	// of any other kind with the same name collides too, and the server names
	// that one (Msg 2714).
	existing *nameSet
	schemas  []string
	// principals are what the Permissions page may grant to: every user and role.
	principals []permPrincipal
	// collation is the database's, for the Extended Properties page's names.
	collation string
}

// sequenceKey is one nameSet entry for a schema-scoped name. The NUL cannot
// occur in an identifier, so two pairs never share a key.
func sequenceKey(schema, name string) string { return schema + "\x00" + name }

// schemaObjectKeys adapts a Database list read into fetchSchemaObjectPrefetch's
// keys: each object's sequenceKey, from the schema and name of returns.
func schemaObjectKeys[T any](list func(*gosmo.Database, context.Context) ([]T, error),
	names func(T) (schema, name string)) func(context.Context, *gosmo.Database) ([]string, error) {
	return func(ctx context.Context, d *gosmo.Database) ([]string, error) {
		objs, err := list(d, ctx)
		if err != nil {
			return nil, err
		}
		keys := make([]string, len(objs))
		for i, o := range objs {
			keys[i] = sequenceKey(names(o))
		}
		return keys, nil
	}
}

// newObjectPermissionsPage is a New dialog's Permissions page for an object of
// class: nothing granted yet, and the securable's schema and name read from
// target when apply runs — they are only known once typed. Permissions go
// through DatabaseRef for the same reason as the CREATE: the GRANTs need
// nothing but the database's name. schema is "" for a schema itself.
func newObjectPermissionsPage(sc *db.ServerConn, dbName string, principals []permPrincipal,
	class gosmo.SecurableClass, target func() (schema, name string)) (*propsheet.Form, propApply) {
	return buildPermissionsMatrix(principals, class.PermissionNames(), nil,
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, permission, principal string) error {
			schema, name := target()
			sec := gosmo.Securable{Class: class, Schema: schema, Name: name}
			return securablePermApply(sc.Server.DatabaseRef(dbName), sec)(ctx, verb, opts, permission, principal)
		})
}

// fetchSchemaObjectPrefetch reads a schemaObjectPrefetch; keys lists the
// dialog's family as sequenceKey entries (d.Sequences for New Sequence,
// d.Synonyms for New Synonym).
func fetchSchemaObjectPrefetch(ctx context.Context, sc *db.ServerConn, dbName string,
	keys func(context.Context, *gosmo.Database) ([]string, error)) (*schemaObjectPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	schemas, err := d.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := keys(ctx, d)
	if err != nil {
		return nil, err
	}
	principals, err := loadDatabasePrincipals(ctx, d)
	if err != nil {
		return nil, err
	}
	collation := databaseCollation(d)
	pf := &schemaObjectPrefetch{existing: newNameSet(collation, existing...), collation: collation,
		principals: principals.grantees()}
	// sys and INFORMATION_SCHEMA refuse CREATE SYNONYM as they refuse CREATE
	// SEQUENCE (Msg 2760, sa included), so one picker rule serves both.
	pf.schemas = sequenceSchemas(collation, schemas)
	return pf, nil
}

// sequenceSchemas is what the schema picker offers: every schema but sys and
// INFORMATION_SCHEMA, which refuse CREATE SEQUENCE whoever runs it (Msg 2760,
// sa included). The db_* role schemas are ordinary schemas.
func sequenceSchemas(collation string, schemas []*gosmo.Schema) []string {
	noCreate := newNameSet(collation, nschemaUnownable...)
	var out []string
	for _, s := range schemas {
		if !noCreate.Has(s.Name) {
			out = append(out, s.Name)
		}
	}
	return out
}

// NewSequenceDialog is the New Sequence dialog.
type NewSequenceDialog struct {
	dbFolderDialog[schemaObjectPrefetch]
}

// NewNewSequenceDialog creates the dialog and wires its callbacks.
func NewNewSequenceDialog(app *App) *NewSequenceDialog {
	d := &NewSequenceDialog{}
	d.initInFolder(app, newObjectConfig[schemaObjectPrefetch]{
		title: "New Sequence",
		noun:  "Sequence",
		pages: []string{"General", "Permissions", "Extended Properties"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*schemaObjectPrefetch, error) {
			return fetchSchemaObjectPrefetch(ctx, sc, d.dbName, schemaObjectKeys((*gosmo.Database).Sequences,
				func(s *gosmo.Sequence) (string, string) { return s.Schema, s.Name }))
		},
		build: d.buildPages,
	})
	return d
}

// nseqFields are the General page's rows, which the preflight reads.
type nseqFields struct {
	name, start, increment, min, max, precision, cacheSize *propsheet.TextRow
	schema, dataType, cache                                *propsheet.SelectRow
	noMin, noMax, cycle                                    *propsheet.CheckRow
}

func (d *NewSequenceDialog) buildPages(pf *schemaObjectPrefetch) {
	sc := d.sc
	dbName := d.dbName

	typeItems := make([]string, len(nseqTypes))
	for i, t := range nseqTypes {
		typeItems[i] = string(t)
	}
	schema := 0
	for i, s := range pf.schemas {
		if s == "dbo" {
			schema = i
		}
	}
	var f nseqFields
	// The value rows' range is the type picked, read when a row validates. A
	// precision the Precision row refuses falls back to the bare name's 18; that
	// row reports its own error.
	typeRange := func() (lo, hi *big.Int, ok bool) {
		p := 0
		if f.precision.Enabled() {
			if n, err := f.precision.IntValue(); err == nil {
				p = int(n)
			}
		}
		return sequenceRange(gosmo.DataType(f.dataType.Value()), p)
	}
	f = nseqFields{
		name:      propsheet.Text("Sequence name", "", 30),
		schema:    propsheet.Select("Sequence schema", pf.schemas, schema),
		dataType:  propsheet.Select("Data type", typeItems, 3), // bigint, the server's default
		precision: propsheet.Int("Precision", 18, 1, 38, ""),
		start:     sequenceValueRow("Start value", "1", typeRange),
		increment: sequenceValueRow("Increment by", "1", typeRange),
		noMin:     propsheet.Check("No minimum (the type's)", true),
		min:       sequenceValueRow("Minimum value", "1", typeRange),
		noMax:     propsheet.Check("No maximum (the type's)", true),
		max:       sequenceValueRow("Maximum value", strconv.FormatInt(math.MaxInt64, 10), typeRange),
		cycle:     propsheet.Check("Cycle", false),
		cache:     propsheet.Select("Cache", nseqCacheItems, 0),
		cacheSize: propsheet.Int("Cache size", 50, 1, math.MaxInt32, ""),
	}
	sync := func() {
		dt := gosmo.DataType(f.dataType.Value())
		f.precision.SetEnabled(dt == gosmo.DataTypeDecimal || dt == gosmo.DataTypeNumeric)
		f.min.SetEnabled(!f.noMin.Checked())
		f.max.SetEnabled(!f.noMax.Checked())
		f.cacheSize.SetEnabled(f.cache.Value() == nseqCacheSize)
	}
	f.dataType.SetOnChange(func(string) { sync() })
	f.noMin.SetOnChange(func(bool) { sync() })
	f.noMax.SetOnChange(func(bool) { sync() })
	f.cache.SetOnChange(func(string) { sync() })
	sync()

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Sequence"),
		f.name,
		f.schema,
		propsheet.Static("Database", dbName),
		propsheet.Section("Type"),
		f.dataType,
		f.precision,
		propsheet.Section("Values"),
		f.start,
		f.increment,
		f.noMin, f.min,
		f.noMax, f.max,
		f.cycle,
		propsheet.Section("Cache"),
		f.cache,
		f.cacheSize,
		propsheet.Note("Creating a sequence in a schema takes CREATE SEQUENCE or ALTER on that schema."),
	)

	var req gosmo.CreateSequenceRequest
	d.objectName = func() string { return strings.TrimSpace(f.name.Value()) }
	d.preflight = func() error {
		r, err := newSequenceRequest(f, dbName, pf)
		if err != nil {
			return err
		}
		if !gate.AllowsOn(sc, dbName, r.Schema, "", gate.CreateSequence) {
			return fmt.Errorf("creating a sequence in schema %s needs CREATE SEQUENCE or ALTER on it", r.Schema)
		}
		req = r
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// DatabaseRef, not DatabaseByName: the CREATE addresses the database by name,
		// and the by-name read would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).CreateSequence(ctx, req)
		return err
	}

	// The securable is read when apply runs, from the request the preflight built.
	d.forms[1], d.applyFns[1] = newObjectPermissionsPage(sc, dbName, pf.principals, gosmo.SecurableSequence,
		func() (string, string) { return req.Schema, req.Name })
	d.forms[2], d.applyFns[2] = buildExtendedPropertiesForm(sc, dbName, pf.collation, func() gosmo.ExtendedPropertyLevel {
		return sequencePropertyLevel(req.Schema, req.Name)
	}, nil)
}

// sequenceValueRow is one value row: wide enough for the 39 characters of a
// signed decimal(38, 0) value (propsheet.Int's twelve showed only a tail).
func sequenceValueRow(label, v string, typeRange func() (lo, hi *big.Int, ok bool)) *propsheet.TextRow {
	r := propsheet.Text(label, v, 40)
	sequenceValue(r, false, typeRange)
	return r
}

// sequencePropertyLevel is where a sequence's extended properties live.
func sequencePropertyLevel(schema, name string) gosmo.ExtendedPropertyLevel {
	return gosmo.ExtendedPropertyLevel{
		Level0Type: "SCHEMA", Level0Name: schema,
		Level1Type: "SEQUENCE", Level1Name: name,
	}
}

// newSequenceRequest reads the General page into a request, refusing, naming
// the field, what the CREATE would be refused for.
func newSequenceRequest(f nseqFields, dbName string, pf *schemaObjectPrefetch) (gosmo.CreateSequenceRequest, error) {
	req := gosmo.CreateSequenceRequest{
		Schema:   f.schema.Value(),
		Name:     strings.TrimSpace(f.name.Value()),
		DataType: gosmo.DataType(f.dataType.Value()),
		Cycle:    f.cycle.Checked(),
	}
	switch {
	case req.Name == "":
		return req, fmt.Errorf("sequence name is required")
	case req.Schema == "":
		return req, fmt.Errorf("pick a schema")
	case pf.existing.Has(sequenceKey(req.Schema, req.Name)):
		return req, fmt.Errorf("a sequence named %s already exists in %s", gosmo.QualifiedName(req.Schema, req.Name), dbName)
	}
	valid := func(r *propsheet.TextRow) error {
		if err := r.Validate(); err != nil {
			// A value row's message names its field already (sequenceValue);
			// propsheet.Int's does not.
			if label := strings.ToLower(r.Label()); !strings.HasPrefix(err.Error(), label) {
				return fmt.Errorf("%s %v", label, err)
			}
			return err
		}
		return nil
	}
	intNum := func(r *propsheet.TextRow) (int, error) {
		if err := valid(r); err != nil {
			return 0, err
		}
		n, err := r.IntValue()
		return int(n), err
	}
	// num validates a value row (a whole number in the type's range) and returns
	// its digits, the form gosmo takes, with the parsed value for the
	// comparisons below.
	num := func(r *propsheet.TextRow) (string, *big.Int, error) {
		if err := valid(r); err != nil {
			return "", nil, err
		}
		n, _ := sequenceNumber(r)
		return strings.TrimSpace(r.Value()), n, nil
	}
	var err error
	if f.precision.Enabled() {
		if req.Precision, err = intNum(f.precision); err != nil {
			return req, err
		}
	}
	var start, inc, lo, hi *big.Int
	if req.StartValue, start, err = num(f.start); err != nil {
		return req, err
	}
	if req.Increment, inc, err = num(f.increment); err != nil {
		return req, err
	}
	if inc.Sign() == 0 {
		return req, fmt.Errorf("increment by cannot be 0")
	}
	if !f.noMin.Checked() {
		if req.MinValue, lo, err = num(f.min); err != nil {
			return req, err
		}
	}
	if !f.noMax.Checked() {
		if req.MaxValue, hi, err = num(f.max); err != nil {
			return req, err
		}
	}
	if lo != nil && hi != nil && lo.Cmp(hi) >= 0 {
		return req, fmt.Errorf("the minimum value must be below the maximum")
	}
	if lo != nil && start.Cmp(lo) < 0 || hi != nil && start.Cmp(hi) > 0 {
		return req, fmt.Errorf("the start value must lie between the minimum and maximum values")
	}
	switch f.cache.Value() {
	case nseqCacheNone:
		req.Cache = new(0)
	case nseqCacheSize:
		n, err := intNum(f.cacheSize)
		if err != nil {
			return req, err
		}
		req.Cache = new(n)
	}
	return req, nil
}

// sequencesMenuItems is the Sequences folder's menu. CREATE SEQUENCE is granted
// per schema, so New Sequence is offered while any schema permits it and the
// dialog refuses the schema picked if that one does not (gate.CreateSequence).
func sequencesMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.ItemInAnySchema(controls.MenuItem{Label: "New Sequence...",
			Action: func() { a.showNewSequenceDialog(sc, node) }},
			sc, node.data.DBName, gate.CreateSequence),
	)
}

// showNewSequenceDialog is the Sequences folder's entry point.
func (a *App) showNewSequenceDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newSequenceDialog.show(sc, node)
}

// formatSequenceType renders a sequence's type as CREATE SEQUENCE spells it:
// decimal and numeric with their precision (the scale is always 0), an alias
// type schema-qualified.
func formatSequenceType(s *gosmo.Sequence) string {
	t := string(s.DataType)
	if s.DataType == gosmo.DataTypeDecimal || s.DataType == gosmo.DataTypeNumeric {
		t += "(" + strconv.Itoa(s.Precision) + ", 0)"
	}
	if s.DataTypeSchema != "" && s.DataTypeSchema != "sys" {
		t = gosmo.QualifiedName(s.DataTypeSchema, t)
	}
	return t
}
