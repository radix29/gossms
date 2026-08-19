package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// scripting.go is Object Explorer's "Script <Noun> as ▸" cascade: one table
// of which verbs each node type offers and how each is generated, plus the
// three destinations every verb can be sent to. A node type absent from the
// table offers no Script item at all — that is how a folder, or an object
// gosmo can't script, stays out of the menu instead of showing an item that
// fails when clicked.
//
// Every generator runs on a background goroutine and takes a nodeData by
// value, not the *explorerNode it came off, for the same reason objectOp's
// do: the UI goroutine writes node.data (see applyNodeFilter).

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

// scriptFn is a Scripter method bound to the node being scripted — the part
// of a DDL verb that differs per object family.
type scriptFn func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error)

// serverScriptFn is scriptFn for the two principals that belong to no
// database, and so are scripted by a ServerScripter.
type serverScriptFn func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error)

var (
	scriptTable scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptTableContext(ctx, n.Schema, n.Name)
	}
	scriptView scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptViewContext(ctx, n.Schema, n.Name)
	}
	scriptProc scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptStoredProcedureContext(ctx, n.Schema, n.Name)
	}
	scriptFunc scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptFunctionContext(ctx, n.Schema, n.Name)
	}
	scriptTrigger scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptTriggerContext(ctx, n.Schema, n.Name)
	}
	scriptSeq scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSequenceContext(ctx, n.Schema, n.Name)
	}
	scriptSynonym scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSynonymContext(ctx, n.Schema, n.Name)
	}
	scriptSchema scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSchemaContext(ctx, n.Name)
	}
	scriptUser scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptUserContext(ctx, n.Name)
	}
	scriptDBRole scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDatabaseRoleContext(ctx, n.Name)
	}
	scriptDB scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) { return s.ScriptDatabase() }

	// The table-scoped families name their table in TableName; Schema/Name
	// point at the index or constraint itself.
	scriptIndex scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptIndexContext(ctx, n.Schema, n.TableName, n.Name)
	}
	scriptCheck scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptCheckConstraintContext(ctx, n.Schema, n.TableName, n.Name)
	}
	scriptFK scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptForeignKeyContext(ctx, n.Schema, n.TableName, n.Name)
	}

	scriptPartFunc scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptPartitionFunctionContext(ctx, n.Name)
	}
	scriptPartScheme scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptPartitionSchemeContext(ctx, n.Name)
	}
	scriptSecPolicy scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSecurityPolicyContext(ctx, n.Schema, n.Name)
	}
	scriptCMK scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptColumnMasterKeyContext(ctx, n.Name)
	}
	scriptCEK scriptFn = func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptColumnEncryptionKeyContext(ctx, n.Name)
	}

	scriptLogin serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptLoginContext(ctx, n.Name)
	}
	scriptServerRole serverScriptFn = func(s *gosmo.ServerScripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptServerRoleContext(ctx, n.Name)
	}
)

// scriptables is the per-type table. Verb order follows SSMS: CREATE, ALTER,
// DROP, DROP And CREATE, then the DML templates.
var scriptables = map[NodeType]scriptable{
	// A database scripts only as CREATE — gosmo's ScriptDatabase emits the
	// CREATE plus its recovery/compatibility settings and has no DROP form,
	// and dropping a database from a generated script is Delete's job.
	NodeDatabase: {"Database", []scriptVerb{{"CREATE To", ddl(gosmo.ScriptCreate, scriptDB)}}},

	NodeTable: {"Table", append(ddlVerbs(scriptTable, false), rowVerbs...)},
	NodeView:  {"View", append(ddlVerbs(scriptView, true), rowVerbs...)},
	NodeStoredProcedure: {"Stored Procedure", append(ddlVerbs(scriptProc, true),
		scriptVerb{"EXECUTE To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
			return s.ScriptExecuteContext(ctx, n.Schema, n.Name)
		})})},
	NodeFunction: {"Function", append(ddlVerbs(scriptFunc, true),
		scriptVerb{"SELECT To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
			return s.ScriptFunctionCallContext(ctx, n.Schema, n.Name, n.FuncType)
		})})},
	NodeTrigger: {"Trigger", ddlVerbs(scriptTrigger, true)},

	NodeIndex:      {"Index", ddlVerbs(scriptIndex, false)},
	NodeKey:        {"Key", ddlVerbs(scriptIndex, false)},
	NodeCheck:      {"Constraint", ddlVerbs(scriptCheck, false)},
	NodeForeignKey: {"Foreign Key", ddlVerbs(scriptFK, false)},
	NodeSequence:   {"Sequence", ddlVerbs(scriptSeq, false)},
	NodeSynonym:    {"Synonym", ddlVerbs(scriptSynonym, false)},

	NodeSchema:       {"Schema", ddlVerbs(scriptSchema, false)},
	NodeUser:         {"User", ddlVerbs(scriptUser, false)},
	NodeDatabaseRole: {"Database Role", ddlVerbs(scriptDBRole, false)},

	NodePartitionFunction:   {"Partition Function", ddlVerbs(scriptPartFunc, false)},
	NodePartitionScheme:     {"Partition Scheme", ddlVerbs(scriptPartScheme, false)},
	NodeSecurityPolicy:      {"Security Policy", ddlVerbs(scriptSecPolicy, false)},
	NodeColumnMasterKey:     {"Column Master Key", ddlVerbs(scriptCMK, false)},
	NodeColumnEncryptionKey: {"Column Encryption Key", ddlVerbs(scriptCEK, false)},

	NodeLogin:      {"Login", serverDDLVerbs(scriptLogin)},
	NodeServerRole: {"Server Role", serverDDLVerbs(scriptServerRole)},
}

// rowVerbs are the four row-level templates a table or view offers.
var rowVerbs = []scriptVerb{
	{"SELECT To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptSelectContext(ctx, n.Schema, n.Name)
	})},
	{"INSERT To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptInsertContext(ctx, n.Schema, n.Name)
	})},
	{"UPDATE To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptUpdateContext(ctx, n.Schema, n.Name)
	})},
	{"DELETE To", dml(func(s *gosmo.Scripter, ctx context.Context, n nodeData) (string, error) {
		return s.ScriptDeleteContext(ctx, n.Schema, n.Name)
	})},
}

// ddlVerbs is the standard DDL set one Scripter method covers, with ALTER
// included only for the module objects that have an ALTER form.
func ddlVerbs(f scriptFn, alter bool) []scriptVerb {
	verbs := []scriptVerb{{"CREATE To", ddl(gosmo.ScriptCreate, f)}}
	if alter {
		verbs = append(verbs, scriptVerb{"ALTER To", ddl(gosmo.ScriptAlter, f)})
	}
	return append(verbs,
		scriptVerb{"DROP To", ddl(gosmo.ScriptDrop, f)},
		scriptVerb{"DROP And CREATE To", ddl(gosmo.ScriptDropAndCreate, f)})
}

// serverDDLVerbs is ddlVerbs for a server-level principal.
func serverDDLVerbs(f serverScriptFn) []scriptVerb {
	return []scriptVerb{
		{"CREATE To", serverDDL(gosmo.ScriptCreate, f)},
		{"DROP To", serverDDL(gosmo.ScriptDrop, f)},
		{"DROP And CREATE To", serverDDL(gosmo.ScriptDropAndCreate, f)},
	}
}

// ddl binds a Scripter method to one verb.
func ddl(v gosmo.ScriptVerb, f scriptFn) scriptGen {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		d, err := sc.Server.DatabaseByNameContext(ctx, n.DBName)
		if err != nil {
			return "", err
		}
		opts := gosmo.DefaultScriptOptions()
		opts.Verb = v
		return f(gosmo.NewScripter(d, opts), ctx, n)
	}
}

// dml binds one of the row-level templates, which have no verb of their own.
func dml(f scriptFn) scriptGen { return ddl(gosmo.ScriptCreate, f) }

// serverDDL is ddl for a ServerScripter.
func serverDDL(v gosmo.ScriptVerb, f serverScriptFn) scriptGen {
	return func(ctx context.Context, sc *db.ServerConn, n nodeData) (string, error) {
		opts := gosmo.DefaultScriptOptions()
		opts.Verb = v
		return f(gosmo.NewServerScripter(sc.Server, opts), ctx, n)
	}
}

// scriptMenuItems is the "Script <Noun> as ▸" cascade for a node, or nil when
// its type has nothing to script. Spliced into every node's menu by
// contextMenuItemsForNode, so no per-type branch mentions scripting.
func (a *App) scriptMenuItems(node *explorerNode) []controls.MenuItem {
	s, ok := scriptables[node.data.Type]
	if !ok {
		return nil
	}
	// A system object's definition lives in the resource database, which
	// sys.sql_modules in a user database doesn't expose — scripting sys.objects
	// or sys.sp_executesql can only fail, so the item isn't offered. A system
	// database is the exception: its CREATE script is assembled from metadata
	// gossms can read, exactly as a user database's is.
	if node.data.IsSystem && node.data.Type != NodeDatabase {
		return nil
	}
	sc := resolveConn(node)
	verbs := make([]controls.MenuItem, 0, len(s.verbs))
	for _, v := range s.verbs {
		verbs = append(verbs, controls.MenuItem{Label: v.label, Sub: a.scriptDestinations(sc, node.data, v)})
	}
	return []controls.MenuItem{{Label: "Script " + s.noun + " as", Sub: verbs}}
}

// scriptDestinations is the third level of the cascade — where the generated
// script goes. Each generates first and only then commits to the
// destination, so a failure reports itself without having prompted for a
// file or overwritten the clipboard.
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
	a.setStatus("Scripting " + n.Name + "...")
	// safegoRepair, not safego: the status line is latched to "Scripting..."
	// before the goroutine starts and only the posted callback clears it, so a
	// panic would leave the app claiming to still be working.
	a.safegoRepair("scripting an object", func() { a.setStatus("") }, func() {
		ctx, cancel := context.WithTimeout(sc.Context(), childFetchTimeout)
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
// LF-separated UTF-8, the shape a script gossms generated itself has — there
// is no source file whose encoding could be preserved (see writeQueryFile).
func (a *App) saveScriptAs(n nodeData, text string) {
	a.fileDialog.ShowSave("Script To File", scriptFileName(n), func(path string) {
		if err := fileutil.WriteAtomic(path, []byte(text), 0o644); err != nil {
			a.setStatus(fmt.Sprintf("Save failed: %v", err))
			return
		}
		a.setStatus("Saved to " + path)
	})
}

// scriptFileName is the name the save prompt starts on — the object's, the
// way SSMS proposes one.
func scriptFileName(n nodeData) string {
	if n.Schema != "" {
		return n.Schema + "." + n.Name + ".sql"
	}
	return n.Name + ".sql"
}
