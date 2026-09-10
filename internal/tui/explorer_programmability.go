package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The Programmability families added in Phase 3: Types (five sub-folders),
// Assemblies, Rules, Defaults and Plan Guides. explorer_objects.go keeps
// tables and their sub-objects; the module families that predate this file
// (stored procedures, functions, sequences, synonyms) stayed there rather
// than being moved, since moving working code buys nothing.

// loadTypesChildren returns the five type families SSMS files under
// Programmability > Types, in SSMS's order. A static loader: each folder
// reads for itself, so expanding Types costs no query.
func loadTypesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbName := node.data.DBName
	return []*explorerNode{
		l.node("System Data Types", NodeSystemDataTypes, "", "", dbName),
		l.node("User-Defined Data Types", NodeUserDefinedDataTypes, "", "", dbName),
		l.node("User-Defined Table Types", NodeUserDefinedTableTypes, "", "", dbName),
		l.node("User-Defined Types", NodeUserDefinedTypes, "", "", dbName),
		l.node("XML Schema Collections", NodeXmlSchemaCollections, "", "", dbName),
	}, nil
}

// loadSystemDataTypesChildren lists the built-in types the instance ships.
// They carry no schema (sys.types names them unqualified for the caller's
// purposes here) and are marked IsSystem, which is what keeps Delete and
// Rename off their context menu.
func loadSystemDataTypesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.SystemDataType, error) { return dbObj.SystemDataTypesContext(l.ctx) },
		func(t *gosmo.SystemDataType) *explorerNode {
			n := l.node(t.Name, NodeSystemDataType, "", t.Name, node.data.DBName)
			n.data.IsSystem = true
			return n
		})
}

// loadUserDefinedDataTypesChildren lists the alias types. The label carries
// the base type the way loadColumnsChildren's does — an alias type is its
// base type plus a name, and the name alone says nothing about it.
func loadUserDefinedDataTypesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.UserDefinedDataType, error) { return dbObj.UserDefinedDataTypesContext(l.ctx) },
		func(t *gosmo.UserDefinedDataType) *explorerNode {
			nullWord := "null"
			if !t.IsNullable {
				nullWord = "not null"
			}
			base := formatDataTypeLen(t.BaseType, t.MaxLength, t.Precision, t.Scale)
			label := t.Schema + "." + t.Name + " (" + base + ", " + nullWord + ")"
			return l.node(label, NodeUserDefinedDataType, t.Schema, t.Name, node.data.DBName)
		})
}

func loadUserDefinedTableTypesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.UserDefinedTableType, error) { return dbObj.UserDefinedTableTypesContext(l.ctx) },
		func(t *gosmo.UserDefinedTableType) *explorerNode {
			n := l.node(t.Schema+"."+t.Name, NodeUserDefinedTableType, t.Schema, t.Name, node.data.DBName)
			n.data.IsMemoryOptimized = t.IsMemoryOptimized
			return n
		})
}

// loadUserDefinedTypesChildren lists the CLR types — SSMS's "User-Defined
// Types" folder, which is the assembly-backed family and not the alias types
// one folder above it.
func loadUserDefinedTypesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.ClrType, error) { return dbObj.ClrTypesContext(l.ctx) },
		func(t *gosmo.ClrType) *explorerNode {
			return l.node(t.Schema+"."+t.Name, NodeUserDefinedType, t.Schema, t.Name, node.data.DBName)
		})
}

func loadXmlSchemaCollectionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.XmlSchemaCollection, error) { return dbObj.XmlSchemaCollectionsContext(l.ctx) },
		func(c *gosmo.XmlSchemaCollection) *explorerNode {
			n := l.node(c.Schema+"."+c.Name, NodeXmlSchemaCollection, c.Schema, c.Name, node.data.DBName)
			n.data.CreateDate = c.CreateDate
			return n
		})
}

// loadAssembliesChildren lists the database's CLR assemblies. The ones SQL
// Server ships (Microsoft.SqlServer.Types and friends, present in every
// database) are listed too, as SSMS lists them, but marked IsSystem so
// Delete and Rename stay off their menu.
func loadAssembliesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Assembly, error) { return dbObj.AssembliesContext(l.ctx) },
		func(a *gosmo.Assembly) *explorerNode {
			n := l.node(a.Name, NodeAssembly, "", a.Name, node.data.DBName)
			n.data.CreateDate = a.CreateDate
			n.data.IsSystem = !a.IsUserDefined
			return n
		})
}

func loadRulesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Rule, error) { return dbObj.RulesContext(l.ctx) },
		func(r *gosmo.Rule) *explorerNode {
			n := l.node(r.Schema+"."+r.Name, NodeRule, r.Schema, r.Name, node.data.DBName)
			n.data.CreateDate = r.CreateDate
			return n
		})
}

// loadDefaultsChildren lists standalone CREATE DEFAULT objects. gosmo's
// Defaults read is the one that carries the parent_object_id = 0 predicate,
// so a table's DF_… constraints do not land here — see gosmo's
// rule_default.go.
func loadDefaultsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.Default, error) { return dbObj.DefaultsContext(l.ctx) },
		func(df *gosmo.Default) *explorerNode {
			n := l.node(df.Schema+"."+df.Name, NodeDefault, df.Schema, df.Name, node.data.DBName)
			n.data.CreateDate = df.CreateDate
			return n
		})
}

// loadPlanGuidesChildren lists the database's plan guides, each labelled with
// its state — the same "(Disabled)" suffix the trigger and security-policy
// folders use, and for the same reason: a disabled plan guide shapes no plan
// and nothing else in the row says so.
func loadPlanGuidesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(func() ([]*gosmo.PlanGuide, error) { return dbObj.PlanGuidesContext(l.ctx) },
		func(g *gosmo.PlanGuide) *explorerNode {
			label := g.Name
			if g.IsDisabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodePlanGuide, "", g.Name, node.data.DBName)
			n.data.CreateDate = g.CreateDate
			n.data.IsEnabled = !g.IsDisabled
			n.data.ScopeSchema, n.data.ScopeName = g.ScopeSchema, g.ScopeName
			return n
		})
}

// The context menus for this family's nodes, looked up through nodeMenus
// (explorer_loaders.go). Each opens the read-only Properties its props file
// builds; the plan guide's is the one that can write, and it does so from the
// page rather than from a menu item.
//
// NodeSystemDataType has no entry: a built-in type has no Properties in SSMS
// either, and nothing about `int` to show.

func userDefinedDataTypeMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showUserDefinedDataTypePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func userDefinedTableTypeMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showUserDefinedTableTypePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func userDefinedTypeMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showClrTypePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func xmlSchemaCollectionMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showXmlSchemaCollectionPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func assemblyMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showAssemblyPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func ruleMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showRulePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func defaultObjectMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showDefaultPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

func planGuideMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	// The one family here with a command beyond Properties: a disabled
	// guide shapes no plan and is invisible except for the label suffix,
	// so Enable/Disable is what makes the folder worth having. The right
	// is sp_control_plan_guide's own, and depends on the guide's scope —
	// see planGuideRights.
	planGuideToggle := "Disable"
	if !node.data.IsEnabled {
		planGuideToggle = "Enable"
	}
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		gateOn(controls.MenuItem{Label: planGuideToggle, Action: func() { a.togglePlanGuide(sc, node) }},
			sc, node.data.DBName, node.data.ScopeSchema, node.data.ScopeName,
			planGuideRights(node.data.ScopeName)...),
		{Divider: true},
		refresh,
		{Label: "Properties...", Action: func() {
			a.showPlanGuidePropertiesFor(sc, node.data.DBName, node.data.Name,
				node.data.ScopeSchema, node.data.ScopeName)
		}},
	}
}
