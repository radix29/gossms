package tui

import (
	"cmp"
	"context"
	"fmt"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/scripting"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// scripting.go is Object Explorer's "Script <Noun> as ▸" cascade: which node
// types offer it, the menu labels, and the three destinations every verb can be
// sent to. What each family can be scripted at, and how, is internal/scripting's
// table, shared with the command line's script commands; this file maps a
// NodeType onto one of its families and adapts the generators to a nodeData. A
// node type absent from scriptFamilies offers no Script item, which keeps a
// folder, or an object gosmo can't script, out of the menu instead of an item
// that fails when clicked.
//
// Every generator runs on a background goroutine and takes a nodeData by value,
// not the *explorerNode, as objectOp's do: the UI goroutine writes node.data
// (see applyNodeFilter).

// scriptGen produces one object's script text.
type scriptGen func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error)

// scriptVerb is one row of the "Script <Noun> as ▸" submenu.
type scriptVerb struct {
	label string
	gen   scriptGen
}

// scriptable is what a node type offers: the noun the menu names it by and
// the verbs it can be scripted at.
type scriptable struct {
	noun  string
	verbs []scriptVerb
}

// scriptFamily is a node type's scripting family and the noun its menu item
// names it by ("Script <noun> as").
type scriptFamily struct {
	family scripting.Family
	noun   string
}

// scriptFamilies maps each scriptable node type to its family. The verbs each
// offers, and why, are documented on internal/scripting's table.
var scriptFamilies = map[NodeType]scriptFamily{
	NodeDatabase: {scripting.Database, "Database"},

	NodeTable:           {scripting.Table, "Table"},
	NodeView:            {scripting.View, "View"},
	NodeStoredProcedure: {scripting.StoredProcedure, "Stored Procedure"},
	NodeFunction:        {scripting.Function, "Function"},
	NodeTrigger:         {scripting.Trigger, "Trigger"},

	NodeIndex:      {scripting.Index, "Index"},
	NodeKey:        {scripting.Key, "Key"},
	NodeCheck:      {scripting.Check, "Constraint"},
	NodeForeignKey: {scripting.ForeignKey, "Foreign Key"},
	NodeStatistic:  {scripting.Statistic, "Statistics"},
	NodeSequence:   {scripting.Sequence, "Sequence"},
	NodeSynonym:    {scripting.Synonym, "Synonym"},

	NodeSchema:       {scripting.Schema, "Schema"},
	NodeUser:         {scripting.User, "User"},
	NodeDatabaseRole: {scripting.DatabaseRole, "Database Role"},

	NodePartitionFunction:          {scripting.PartitionFunction, "Partition Function"},
	NodePartitionScheme:            {scripting.PartitionScheme, "Partition Scheme"},
	NodeSecurityPolicy:             {scripting.SecurityPolicy, "Security Policy"},
	NodeColumnMasterKey:            {scripting.ColumnMasterKey, "Column Master Key"},
	NodeColumnEncryptionKey:        {scripting.ColumnEncryptionKey, "Column Encryption Key"},
	NodeDatabaseAuditSpecification: {scripting.DatabaseAuditSpecification, "Database Audit Specification"},
	NodeDatabaseTrigger:            {scripting.DatabaseTrigger, "Database Trigger"},
	NodeDatabaseScopedCredential:   {scripting.DatabaseScopedCredential, "Database Scoped Credential"},
	NodeCertificate:                {scripting.Certificate, "Certificate"},
	NodeAsymmetricKey:              {scripting.AsymmetricKey, "Asymmetric Key"},
	NodeSymmetricKey:               {scripting.SymmetricKey, "Symmetric Key"},

	// System Data Types is deliberately absent: a built-in type is not something
	// a script creates, and SSMS offers nothing for one either.
	NodeUserDefinedDataType:  {scripting.UserDefinedDataType, "User-Defined Data Type"},
	NodeUserDefinedTableType: {scripting.UserDefinedTableType, "User-Defined Table Type"},
	NodeUserDefinedType:      {scripting.UserDefinedType, "User-Defined Type"},
	NodeXMLSchemaCollection:  {scripting.XMLSchemaCollection, "XML Schema Collection"},
	NodeRule:                 {scripting.Rule, "Rule"},
	NodeDefault:              {scripting.Default, "Default"},
	NodeAssembly:             {scripting.Assembly, "Assembly"},
	NodePlanGuide:            {scripting.PlanGuide, "Plan Guide"},
	NodeExternalDataSource:   {scripting.ExternalDataSource, "External Data Source"},
	NodeExternalFileFormat:   {scripting.ExternalFileFormat, "External File Format"},
	NodeExternalLibrary:      {scripting.ExternalLibrary, "External Library"},

	NodeMessageType:          {scripting.MessageType, "Message Type"},
	NodeContract:             {scripting.Contract, "Contract"},
	NodeBrokerQueue:          {scripting.BrokerQueue, "Queue"},
	NodeBrokerService:        {scripting.BrokerService, "Service"},
	NodeRoute:                {scripting.Route, "Route"},
	NodeRemoteServiceBinding: {scripting.RemoteServiceBinding, "Remote Service Binding"},
	NodeBrokerPriority:       {scripting.BrokerPriority, "Broker Priority"},

	NodeFullTextCatalog:    {scripting.FullTextCatalog, "Full-Text Catalog"},
	NodeFullTextStoplist:   {scripting.FullTextStoplist, "Full-Text Stoplist"},
	NodeSearchPropertyList: {scripting.SearchPropertyList, "Search Property List"},

	NodeLogin:                    {scripting.Login, "Login"},
	NodeServerRole:               {scripting.ServerRole, "Server Role"},
	NodeCredential:               {scripting.Credential, "Credential"},
	NodeAudit:                    {scripting.Audit, "Audit"},
	NodeServerAuditSpecification: {scripting.ServerAuditSpecification, "Server Audit Specification"},
	NodeBackupDevice:             {scripting.BackupDevice, "Backup Device"},
	NodeServerTrigger:            {scripting.ServerTrigger, "Server Trigger"},
	NodeEndpoint:                 {scripting.Endpoint, "Endpoint"},
	// "Session", as SSMS words it.
	NodeEventSession: {scripting.EventSession, "Session"},

	NodeResourceGovernor:     {scripting.ResourceGovernor, "Resource Governor"},
	NodeResourcePool:         {scripting.ResourcePool, "Resource Pool"},
	NodeWorkloadGroup:        {scripting.WorkloadGroup, "Workload Group"},
	NodeExternalResourcePool: {scripting.ExternalResourcePool, "External Resource Pool"},

	NodeDatabaseMail: {scripting.DatabaseMail, "Database Mail"},
}

// scriptVerbLabels is each verb's submenu label, as SSMS words it.
var scriptVerbLabels = map[scripting.Verb]string{
	scripting.Create:           "CREATE To",
	scripting.Alter:            "ALTER To",
	scripting.Drop:             "DROP To",
	scripting.DropAndCreate:    "DROP And CREATE To",
	scripting.Select:           "SELECT To",
	scripting.Insert:           "INSERT To",
	scripting.Update:           "UPDATE To",
	scripting.Delete:           "DELETE To",
	scripting.Execute:          "EXECUTE To",
	scripting.Rebuild:          "REBUILD To",
	scripting.Reorganize:       "REORGANIZE To",
	scripting.UpdateStatistics: "UPDATE STATISTICS To",
}

// scriptables is the per-type table the menu reads: scriptFamilies with each
// family's verbs labelled and bound to a nodeData.
var scriptables = func() map[NodeType]scriptable {
	m := make(map[NodeType]scriptable, len(scriptFamilies))
	for t, f := range scriptFamilies {
		var verbs []scriptVerb
		for _, v := range scripting.Verbs(f.family) {
			verbs = append(verbs, scriptVerbFor(f.family, v))
		}
		m[t] = scriptable{f.noun, verbs}
	}
	return m
}()

// scriptVerbFor is family f's verb v as a submenu row.
func scriptVerbFor(f scripting.Family, v scripting.Verb) scriptVerb {
	gen := scripting.For(f, v)
	return scriptVerb{scriptVerbLabels[v], func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		return gen(ctx, sc.Server, scriptTarget(n))
	}}
}

// scriptTarget is the object a node names, as internal/scripting addresses it.
func scriptTarget(n nodeData) scripting.Target {
	return scripting.Target{DBName: n.DBName, Schema: n.Schema, Name: n.Name, TableName: n.TableName, FuncType: n.FuncType}
}

// scriptReadRights gate a family's whole Script cascade on the right that makes
// what the script is built from visible, so a login without it isn't offered
// an item that fails in the status line. A family absent here is not gated: its
// node is listed only to a login that can read it.
//
// Resource Governor's node is the exception: always listed, but its
// configuration row is invisible without VIEW ANY DEFINITION (no row, no
// error), which VIEW SERVER STATE does not imply.
var scriptReadRights = map[NodeType][]gate.Right{
	NodeResourceGovernor: {gate.ViewAnyDefinition},
}

// statisticUpdateVerb is the UPDATE STATISTICS script behind a statistic node's
// own "Update Statistics" item. A full scan, the same read Statistics
// Properties' Details page runs.
var statisticUpdateVerb = scriptVerbFor(scripting.Statistic, scripting.UpdateStatistics)

// scriptMenuItems is the "Script <Noun> as ▸" cascade for a node, or nil when
// its type has nothing to script. Spliced into every node's menu by
// contextMenuItemsForNode, so no per-type branch mentions scripting.
func (a *App) scriptMenuItems(node *explorerNode) []controls.MenuItem {
	s, ok := scriptables[node.data.Type]
	if !ok {
		return nil
	}
	// A system object is not offered, a system database excepted; see
	// scripting.Offered.
	if !scripting.Offered(scriptFamilies[node.data.Type].family, node.data.IsSystem) {
		return nil
	}
	sc := resolveConn(node)
	verbs := make([]controls.MenuItem, 0, len(s.verbs))
	for _, v := range s.verbs {
		item := controls.MenuItem{Label: v.label, Sub: a.scriptDestinations(sc, node.data, v)}
		if editionRefusesScriptVerb(node.data.Type, v.label) {
			item = gateAzure(item, sc)
		}
		verbs = append(verbs, item)
	}
	item := controls.MenuItem{Label: "Script " + s.noun + " as", Sub: verbs}
	if rights := scriptReadRights[node.data.Type]; len(rights) > 0 {
		item = gate.Item(item, sc, "", rights...)
	}
	return []controls.MenuItem{item}
}

// scriptDestinations is the third cascade level: where the generated script
// goes. Each generates first and only then commits to the destination, so a
// failure reports itself without having prompted for a file or overwritten the
// clipboard.
func (a *App) scriptDestinations(sc *db.ServerConn, n nodeData, v scriptVerb) []controls.MenuItem {
	return []controls.MenuItem{
		{Label: "New Query Editor Window", Action: func() {
			a.generateScript(sc, n, v, func(text string) { a.openQueryWithText(sc, n.DBName, text) })
		}},
		{Label: "File...", Action: func() {
			a.generateScript(sc, n, v, func(text string) { a.saveScriptAs(n, text) })
		}},
		{Label: "Clipboard", Action: func() {
			a.generateScript(sc, n, v, func(text string) { a.copyWithStatus(text) })
		}},
	}
}

// generateScript runs one verb's generator in the background and hands the
// text to then on the UI goroutine.
func (a *App) generateScript(sc *db.ServerConn, n nodeData, v scriptVerb, then func(string)) {
	if !a.requireConn(sc) {
		return
	}
	a.setStatus("Scripting " + cmp.Or(n.Name, scriptables[n.Type].noun) + "...")
	// safegoRepair, not safego: the status line is latched to "Scripting..." before
	// the goroutine starts and only the posted callback clears it, so a panic would
	// leave the app claiming to still be working.
	a.safegoRepair("scripting an object", func() { a.setStatus("") }, func() {
		ctx, cancel := context.WithTimeout(sc.Server.Context(), childFetchTimeout)
		defer cancel()
		text, err := v.gen(ctx, sc, n)
		a.postAndWake(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Script error: %v", err))
				return
			}
			a.setStatus("")
			then(text)
		})
	})
}

// saveScriptAs prompts for a path and writes the generated script to it.
// LF-separated UTF-8, the shape a gossms-generated script has; there is no
// source file whose encoding could be preserved (see writeQueryFile).
func (a *App) saveScriptAs(n nodeData, text string) {
	a.fileDialog.ShowSave("Script To File", scriptFileName(n), func(path string) {
		if err := fileutil.WriteAtomic(path, []byte(text), 0o644); err != nil {
			a.setStatus(fmt.Sprintf("Save failed: %v", err))
			return
		}
		a.setStatus("Saved to " + path)
	})
}

// scriptFileName is the name the save prompt starts on: the object's, as SSMS
// proposes one.
//
// A singleton node has no name (Resource Governor, Database Mail) and is
// proposed under its noun, not as ".sql".
func scriptFileName(n nodeData) string {
	switch {
	case n.Schema != "":
		return core.SafeFileName(n.Schema) + "." + core.SafeFileName(n.Name) + ".sql"
	case n.Name == "":
		return scriptables[n.Type].noun + ".sql"
	}
	return core.SafeFileName(n.Name) + ".sql"
}
