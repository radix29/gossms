package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/scripting"
)

// The script commands' options.
const (
	optAs         = "as"
	optScriptFile = "script-file"
	optObjects    = "objects"
	optInclPerms  = "include-permissions"
)

// scriptVerbs is --as's values, in SSMS's order, and the verb each names.
var scriptVerbs = []struct {
	name string
	verb scripting.Verb
}{
	{"create", scripting.Create},
	{"alter", scripting.Alter},
	{"drop", scripting.Drop},
	{"drop-and-create", scripting.DropAndCreate},
	{"select", scripting.Select},
	{"insert", scripting.Insert},
	{"update", scripting.Update},
	{"delete", scripting.Delete},
	{"execute", scripting.Execute},
}

// scriptVerbNames is the --as names of vs that the command line offers, in
// vs's order. A verb with no --as name (an index's REBUILD) is left out.
func scriptVerbNames(vs []scripting.Verb) []string {
	var out []string
	for _, v := range vs {
		for _, sv := range scriptVerbs {
			if sv.verb == v {
				out = append(out, sv.name)
			}
		}
	}
	return out
}

// scriptVerbNamed is the verb --as name names.
func scriptVerbNamed(name string) scripting.Verb {
	for _, sv := range scriptVerbs {
		if sv.name == name {
			return sv.verb
		}
	}
	return 0
}

// scriptDBVerbs is script db's --as: the verbs Script Database As offers.
var scriptDBVerbs = scriptVerbNames(scripting.Verbs(scripting.Database))

// objectFamily is the Script As family of a sys.objects type: the
// schema-scoped objects Object Explorer scripts. A constraint, an index or a
// rule is not one (D6), and false.
func objectFamily(typ string) (scripting.Family, bool) {
	switch typ {
	case "U":
		return scripting.Table, true
	case "V":
		return scripting.View, true
	case "P", "PC":
		return scripting.StoredProcedure, true
	case "FN", "IF", "TF", "FS", "FT":
		return scripting.Function, true
	case "TR", "TA":
		return scripting.Trigger, true
	case "SO":
		return scripting.Sequence, true
	case "SN":
		return scripting.Synonym, true
	}
	return 0, false
}

// ScriptCommands is the script group: its node and its commands.
func ScriptCommands(d Deps) []*spec.Command {
	scriptFile := spec.Option{Name: optScriptFile, Type: spec.TypeString,
		Help: "Also write the script to this file (UTF-8, LF), as Script As ▸ File... does; --force replaces one"}
	return []*spec.Command{
		{
			Path:    []string{"script"},
			Summary: "Generate T-SQL scripts, as Object Explorer's Script As does",
			Description: "The script is generated, never run: each command reads the catalog and answers the text " +
				"SSMS's Script As would put in a query window, with its default options (headers, schema-qualified " +
				"names, IF NOT EXISTS guards, ANSI_PADDING, extended properties). script db --objects scripts a whole " +
				"database's objects in dependency order; the wizard's per-object choices are not offered.",
			Class: spec.ClassRead,
		},
		{
			Path:    []string{"script", "object"},
			Summary: "Script a table, view, procedure, function, trigger, sequence or synonym as CREATE, ALTER, DROP or a DML template",
			Description: "The object's Script As text, the same the TUI's Script <Object> as ▸ … ▸ File... writes: " +
				"it opens with USE [database], then the statement. --as picks the verb; each kind offers only what " +
				"Object Explorer does — every kind create, drop and drop-and-create; alter a view, procedure, " +
				"function or trigger; select, insert, update and delete a table or view; execute a procedure; " +
				"select a function. A verb the kind lacks is exit 2, naming the kind's verbs. A record — object, " +
				"type, verb — and the script: text prints it under an === SCRIPT (n lines) === marker, line breaks " +
				"kept, and --max-col-width does not cut it; JSON gives it as one string. A constraint, index or " +
				"other object of a table is not scripted alone (its table's CREATE carries it), nor is an object " +
				"SQL Server ships. Scripting reads definitions, which need VIEW DEFINITION: without it a module, " +
				"or a table with a default, check, computed column or filtered index, is exit 9. An encrypted " +
				"module has no definition to script: create, alter and drop-and-create are exit 11 (drop works). " +
				"--script-file writes the script as File... does.",
			Args: []spec.Arg{{Name: "object", Help: "The object: schema.name, or a name alone when one schema has it",
				Format: "schema.name", Required: true}},
			Options: append(ConnectOptions(),
				spec.Option{Name: optAs, Type: spec.TypeEnum, Enum: scriptVerbNames(scriptVerbsAll()), Default: "create",
					Help: "The verb: what the script does with the object"},
				scriptFile,
			),
			Class: spec.ClassRead,
			Run:   d.scriptObject,
			Examples: []string{
				"gossms script object -p dev Sales.SalesOrderHeader",
				"gossms script object -p dev dbo.uspGetBillOfMaterials --as alter -o json",
				"gossms script object -p dev Sales.vSalesPerson --as drop-and-create --script-file vSalesPerson.sql",
			},
		},
		{
			Path:    []string{"script", "db"},
			Summary: "Script a database as CREATE, DROP or DROP And CREATE",
			Description: "The database's own Script Database As text — files, filegroups, options, owner — not " +
				"the objects in it. The script runs in master and names the database itself, so it has no USE " +
				"[database] of its own. A record — database, verb — and the script, as for script object; " +
				"--script-file writes it as Script Database As ▸ File... does. --objects scripts instead every " +
				"user object in an order a replay into an empty database accepts (schemas, types, tables without " +
				"their foreign keys and edge constraints, functions, views, procedures and synonyms by dependency, " +
				"then foreign keys, edge constraints and triggers); --include-permissions adds the owner, roles, users, memberships and permissions. " +
				"The header lists what is not scripted and its warnings (each dependency cycle broken, a degraded " +
				"dependency order), which are also the result's warnings; an encrypted module is a comment, " +
				"listed in the record as notes, and so is everything that needs it.",
			Args: []spec.Arg{{Name: "name", Help: "The database", Required: true}},
			Options: append(ConnectOptions(),
				spec.Option{Name: optAs, Type: spec.TypeEnum, Enum: scriptDBVerbs, Default: "create",
					Help: "The verb: what the script does with the database"},
				spec.Option{Name: optObjects, Type: spec.TypeBool,
					Help: "Script the objects in the database, in dependency order, instead of the database itself (--as create only)"},
				spec.Option{Name: optInclPerms, Type: spec.TypeBool,
					Help: "With --objects: also the owner, roles, users, role memberships and every GRANT/DENY"},
				scriptFile,
			),
			Class: spec.ClassRead,
			Run:   d.scriptDB,
			Examples: []string{
				"gossms script db -p dev Sales",
				"gossms script db -p dev Sales --as drop-and-create --script-file Sales.sql",
				"gossms script db -p dev Sales --objects --include-permissions --script-file Sales-objects.sql",
			},
		},
	}
}

// scriptVerbsAll is every verb a family script object covers offers, in
// SSMS's order: --as's enum.
func scriptVerbsAll() []scripting.Verb {
	var out []scripting.Verb
	for _, sv := range scriptVerbs {
		for _, f := range []scripting.Family{scripting.Table, scripting.View, scripting.StoredProcedure,
			scripting.Function, scripting.Trigger, scripting.Sequence, scripting.Synonym} {
			if scripting.For(f, sv.verb) != nil {
				out = append(out, sv.verb)
				break
			}
		}
	}
	return out
}

func (d Deps) scriptObject(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	file, err := writableFile(inv, optScriptFile)
	if err != nil {
		return nil, err
	}
	verbName := inv.Values[optAs].(string)
	res := &output.Result{}
	_, dbase, o, done, err := d.lookupObject(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer done()

	name := gosmo.QualifiedName(o.Schema, o.Name)
	kind := kindOfDesc(o.TypeDesc)
	f, ok := objectFamily(o.Type)
	switch {
	case !ok:
		hint := "script object covers tables, views, procedures, functions, triggers, sequences and synonyms."
		if o.ParentName != "" {
			hint = "Script its table, whose CREATE carries it: gossms script object " +
				gosmo.QualifiedName(o.ParentSchema, o.ParentName) + "."
		}
		return res, usageError(fmt.Sprintf("%s is a %s, which script object does not script", name, kind), hint)
	case !scripting.Offered(f, o.IsMSShipped):
		return res, usageError(fmt.Sprintf("%s is a system object: its definition is not scripted", name),
			"Object Explorer offers no Script As for an object SQL Server ships; gossms object definition may show its text.")
	}
	v := scriptVerbNamed(verbName)
	if scripting.For(f, v) == nil {
		return res, usageError(fmt.Sprintf("--as %s: a %s offers %s", verbName, kind,
			strings.Join(scriptVerbNames(scripting.Verbs(f)), ", ")), "")
	}
	t := scripting.Target{DBName: dbase.Name, Schema: o.Schema, Name: o.Name}
	if f == scripting.Function {
		t.FuncType = gosmo.FunctionType(o.Type)
	}
	text, err := scripting.Script(ctx, dbase.Server(), f, v, t)
	if err != nil {
		return res, scriptError(err, name, false)
	}
	res.Fields = []output.Field{
		{Key: "object", Value: name},
		{Key: "type", Value: kind},
		{Key: "verb", Value: verbName},
		{Key: "script", Value: text, Verbatim: true},
	}
	writeScriptFile(res, file, text)
	return res, nil
}

func (d Deps) scriptDB(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	file, err := writableFile(inv, optScriptFile)
	if err != nil {
		return nil, err
	}
	verbName := inv.Values[optAs].(string)
	objects, _ := inv.Values[optObjects].(bool)
	perms, _ := inv.Values[optInclPerms].(bool)
	switch {
	case perms && !objects:
		return nil, usageError("--include-permissions needs --objects", "Add --objects to script the database's objects with their permissions.")
	case objects && inv.Given[optAs] && verbName != "create":
		return nil, usageError("--objects scripts CREATE only; --as "+verbName+" is not offered with it", "")
	}
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	dbase, err := namedDatabase(ctx, cat, inv.Args[0], res)
	if err != nil {
		return res, err
	}
	if objects {
		r, err := scripting.DatabaseObjects(ctx, dbase.Server(), dbase.Name, perms, nil)
		if err != nil {
			return res, scriptError(err, gosmo.QuoteName(dbase.Name), true)
		}
		res.Fields = []output.Field{
			{Key: "database", Value: dbase.Name},
			{Key: "verb", Value: "create"},
			{Key: "objects", Value: strconv.Itoa(r.Objects)},
		}
		if len(r.Notes) > 0 {
			res.Fields = append(res.Fields, output.Field{Key: "notes", Value: strings.Join(r.Notes, "; ")})
		}
		// Also the script's header comments; the envelope's warnings let a
		// reader of the record see the order degraded without parsing it.
		res.Warnings = append(res.Warnings, r.Warnings...)
		res.Fields = append(res.Fields, output.Field{Key: "script", Value: r.Text, Verbatim: true})
		writeScriptFile(res, file, r.Text)
		return res, nil
	}
	text, err := scripting.Script(ctx, dbase.Server(), scripting.Database, scriptVerbNamed(verbName), scripting.Target{DBName: dbase.Name})
	if err != nil {
		return res, scriptError(err, gosmo.QuoteName(dbase.Name), false)
	}
	res.Fields = []output.Field{
		{Key: "database", Value: dbase.Name},
		{Key: "verb", Value: verbName},
		{Key: "script", Value: text, Verbatim: true},
	}
	writeScriptFile(res, file, text)
	return res, nil
}

// scriptError is readError for a script of name, with the hint the common
// refusals call for. objects is a script of name's objects (script db
// --objects), not of name itself: a refusal there is of one of many catalog
// reads, which the message names, so readError's own hint stands.
func scriptError(err error, name string, objects bool) *output.Error {
	e := readError(err)
	switch {
	case objects:
		// gosmo reads sys.sql_expression_dependencies there only with VIEW
		// DEFINITION on the database, and falls back to sys.sql_dependencies
		// when refused, so the DROP hint below named the wrong operation.
	case e.Code == output.CodePermissionDenied && strings.Contains(err.Error(), "sql_expression_dependencies"):
		// A table's DROP reads what is schema-bound to it there, and that
		// read, not the definition, is what was refused.
		e.Hint = "Scripting a DROP of " + name + " reads what is schema-bound to it, which takes " + dependenciesSelect
	case e.Code == output.CodePermissionDenied:
		e.Hint = "Scripting " + name + " reads its definition, which needs VIEW DEFINITION on it (or on its schema or " +
			"the database); a SELECT or EXECUTE grant alone does not give it."
	case errors.Is(err, gosmo.ErrUnsupported) && strings.Contains(err.Error(), "encrypted"):
		// gosmo's refusal for a module WITH ENCRYPTION, which wraps
		// ErrUnsupported alone (a hidden one also wraps ErrDefinitionHidden).
		e.Hint = name + " was created WITH ENCRYPTION: its text cannot be read back, so only --as drop scripts it."
	}
	return e
}

// writeScriptFile writes text to file, when given, as the TUI's Script As ▸
// File... does (scripting.go, saveScriptAs): the text as it is, UTF-8 with
// LF line ends, replaced atomically.
func writeScriptFile(res *output.Result, file, text string) {
	if file == "" {
		return
	}
	if err := fileutil.WriteAtomic(file, []byte(text), 0o644); err != nil {
		res.Errors = append(res.Errors, output.NewError(output.CodeGeneralError,
			fmt.Sprintf("writing --script-file %s: %v", file, err), ""))
		return
	}
	res.Fields = append(res.Fields, output.Field{Key: "scriptFile", Value: file})
}
