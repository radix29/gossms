package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_synonym_dialog.go is the New Synonym dialog (a database's Synonyms
// folder), built on newObjectDialog. Three pages, as SSMS has them: General
// (the synonym's name and schema, then the base object's server, database,
// schema and name), Permissions (object-scoped GRANT/DENY with no existing
// entries) and Extended Properties. The last two run after the CREATE, against
// the typed name. A synonym has no ALTER, so there is no Properties dialog to
// pair with this one.

// nsynPrefetch is everything the dialog reads before it opens.
type nsynPrefetch struct {
	// existing holds every synonym as sequenceKey spells it, under the
	// database's collation. Only synonyms: any other object of the same name
	// collides too, and the server names that one (Msg 2714).
	existing *nameSet
	schemas  []string
	// principals are what the Permissions page may grant to: every user and role.
	principals []permPrincipal
	// collation is the database's, for the Extended Properties page's names.
	collation string
}

func fetchNewSynonymPrefetch(ctx context.Context, sc *db.ServerConn, dbName string) (*nsynPrefetch, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, err
	}
	schemas, err := d.Schemas(ctx)
	if err != nil {
		return nil, err
	}
	syns, err := d.Synonyms(ctx)
	if err != nil {
		return nil, err
	}
	principals, err := loadDatabasePrincipals(ctx, d)
	if err != nil {
		return nil, err
	}
	pf := &nsynPrefetch{existing: newNameSet(databaseCollation(d)), collation: databaseCollation(d),
		principals: principals.grantees()}
	// sys and INFORMATION_SCHEMA refuse CREATE SYNONYM as they refuse CREATE
	// SEQUENCE (Msg 2760, sa included), so the sequence picker's rule holds.
	pf.schemas = sequenceSchemas(databaseCollation(d), schemas)
	for _, s := range syns {
		pf.existing.Add(sequenceKey(s.Schema, s.Name))
	}
	return pf, nil
}

// NewSynonymDialog is the New Synonym dialog.
type NewSynonymDialog struct {
	dbFolderDialog[nsynPrefetch]
}

// NewNewSynonymDialog creates the dialog and wires its callbacks.
func NewNewSynonymDialog(app *App) *NewSynonymDialog {
	d := &NewSynonymDialog{}
	d.initInFolder(app, newObjectConfig[nsynPrefetch]{
		title: "New Synonym",
		noun:  "Synonym",
		pages: []string{"General", "Permissions", "Extended Properties"},
		fetch: func(ctx context.Context, sc *db.ServerConn) (*nsynPrefetch, error) {
			return fetchNewSynonymPrefetch(ctx, sc, d.dbName)
		},
		build: d.buildPages,
	})
	return d
}

// nsynFields are the General page's rows, which the preflight reads.
type nsynFields struct {
	name, server, database, baseSchema, object *propsheet.TextRow
	schema                                     *propsheet.SelectRow
}

func (d *NewSynonymDialog) buildPages(pf *nsynPrefetch) {
	sc := d.sc
	dbName := d.dbName

	schema := 0
	for i, s := range pf.schemas {
		if s == "dbo" {
			schema = i
		}
	}
	f := nsynFields{
		name:       propsheet.Text("Synonym name", "", 30),
		schema:     propsheet.Select("Synonym schema", pf.schemas, schema),
		server:     propsheet.Text("Server name", "", 30),
		database:   propsheet.Text("Database name", dbName, 30),
		baseSchema: propsheet.Text("Schema", "dbo", 30),
		object:     propsheet.Text("Object name", "", 30),
	}
	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Synonym"),
		f.name,
		f.schema,
		propsheet.Static("Database", dbName),
		propsheet.Section("Base object"),
		f.server,
		f.database,
		f.baseSchema,
		f.object,
		propsheet.Note("A blank server means this one, and a blank database this database. "+
			"The base object need not exist yet: it is resolved when the synonym is used."),
		propsheet.Note("Creating a synonym takes CREATE SYNONYM on the database and ALTER on its schema."),
	)

	var req gosmo.CreateSynonymRequest
	d.objectName = func() string { return strings.TrimSpace(f.name.Value()) }
	d.preflight = func() error {
		r, err := newSynonymRequest(f, dbName, pf)
		if err != nil {
			return err
		}
		if !gate.Allows(sc, dbName, gate.CreateSynonym) {
			return fmt.Errorf("creating a synonym needs CREATE SYNONYM on database %s", dbName)
		}
		if !gate.AllowsOn(sc, dbName, r.Schema, "", gate.AlterOnSchema) {
			return fmt.Errorf("creating a synonym in schema %s needs ALTER on it", r.Schema)
		}
		req = r
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		// DatabaseRef, not DatabaseByName: the CREATE addresses the database by name,
		// and the by-name read would not work under Script Changes.
		_, err := sc.Server.DatabaseRef(dbName).CreateSynonym(ctx, req)
		return err
	}

	// The securable is read when apply runs, from the request the preflight built:
	// the name is only known once typed. Permissions go through DatabaseRef for the
	// same reason as the CREATE.
	var noEntries []permEntry
	d.forms[1], d.applyFns[1] = buildPermissionsMatrix(pf.principals, gosmo.SecurableSynonym.PermissionNames(), noEntries,
		func(ctx context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, permission, principal string) error {
			sec := gosmo.Securable{Class: gosmo.SecurableSynonym, Schema: req.Schema, Name: req.Name}
			return securablePermApply(sc.Server.DatabaseRef(dbName), sec)(ctx, verb, opts, permission, principal)
		})
	d.forms[2], d.applyFns[2] = buildExtendedPropertiesForm(sc, dbName, pf.collation, func() gosmo.ExtendedPropertyLevel {
		return synonymPropertyLevel(req.Schema, req.Name)
	}, nil)
}

// synonymPropertyLevel is where a synonym's extended properties live.
func synonymPropertyLevel(schema, name string) gosmo.ExtendedPropertyLevel {
	return gosmo.ExtendedPropertyLevel{
		Level0Type: "SCHEMA", Level0Name: schema,
		Level1Type: "SYNONYM", Level1Name: name,
	}
}

// newSynonymRequest reads the General page into a request, refusing, naming
// the field, what the CREATE would be refused for.
func newSynonymRequest(f nsynFields, dbName string, pf *nsynPrefetch) (gosmo.CreateSynonymRequest, error) {
	req := gosmo.CreateSynonymRequest{
		Schema: f.schema.Value(),
		Name:   strings.TrimSpace(f.name.Value()),
	}
	switch {
	case req.Name == "":
		return req, fmt.Errorf("synonym name is required")
	case req.Schema == "":
		return req, fmt.Errorf("pick a schema")
	case pf.existing.Has(sequenceKey(req.Schema, req.Name)):
		return req, fmt.Errorf("a synonym named %s already exists in %s", gosmo.QualifiedName(req.Schema, req.Name), dbName)
	}
	base, err := synonymBaseObject(f.server.Value(), f.database.Value(), f.baseSchema.Value(), f.object.Value())
	if err != nil {
		return req, err
	}
	req.BaseObject = base
	return req, nil
}

// synonymBaseObject is the FOR clause: each part bracket-quoted on its own,
// as gosmo's CreateSynonymRequest.BaseObject wants it, and the blank leading
// parts left out. A part may be left blank only from the left — the server
// reads [db]..[t] as the default schema, which gosmo's name check refuses and
// which would name a different object for each user.
func synonymBaseObject(server, database, schema, object string) (string, error) {
	server, database = strings.TrimSpace(server), strings.TrimSpace(database)
	schema, object = strings.TrimSpace(schema), strings.TrimSpace(object)
	switch {
	case object == "":
		return "", fmt.Errorf("the base object's name is required")
	case schema == "":
		return "", fmt.Errorf("the base object's schema is required")
	case server != "" && database == "":
		return "", fmt.Errorf("a base object on server %s needs its database name", server)
	}
	var parts []string
	for _, p := range []string{server, database, schema, object} {
		if p != "" {
			parts = append(parts, gosmo.QuoteName(p))
		}
	}
	return strings.Join(parts, "."), nil
}

// synonymsMenuItems is the Synonyms folder's menu. CREATE SYNONYM needs the
// database permission and ALTER on the target schema, so New Synonym is offered
// while the first is held and any schema permits the second, and the dialog
// refuses the schema picked if that one does not.
func synonymsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.ItemInAnySchema(controls.MenuItem{Label: "New Synonym...",
			Action: func() { a.showNewSynonymDialog(sc, node) }},
			sc, node.data.DBName, gate.AlterOnSchema, gate.CreateSynonym),
	)
}

// showNewSynonymDialog is the Synonyms folder's entry point.
func (a *App) showNewSynonymDialog(sc *db.ServerConn, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.newSynonymDialog.show(sc, node)
}
