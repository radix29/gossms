package safety

import (
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/sqltext"
)

// execProc classifies a procedure call whose target starts at toks[i] (after
// EXEC, or at an implicit EXEC's first token).
func execProc(st *Statement, toks []token, i int) {
	st.Leader = "EXEC"
	c, obj, reason, flags := execTarget(toks, i)
	st.Class, st.Object, st.Reason, st.Flags = c, obj, reason, st.Flags|flags
}

// execTarget classifies the call whose target starts at toks[i]: the name's
// allow-lists, or write and FlagDynamic when the target is not a name.
func execTarget(toks []token, i int) (c spec.Class, obj, reason string, flags Flag) {
	// EXEC @rc = dbo.p — the return status variable.
	if i+1 < len(toks) && strings.HasPrefix(toks[i].raw, "@") && toks[i+1].punct("=") {
		i += 2
	}
	if i >= len(toks) {
		return spec.ClassWrite, "", "a call the classifier cannot read", FlagDynamic
	}
	if t := toks[i]; t.punct("(") || t.kind == sqltext.KindString || strings.HasPrefix(t.raw, "@") {
		return spec.ClassWrite, "", "dynamic SQL is not classified", FlagDynamic
	}
	parts, end := nameParts(toks, i)
	if len(parts) == 0 {
		return spec.ClassWrite, "", "a call the classifier cannot read", FlagDynamic
	}
	obj = objectName(toks, i)
	proc := parts[len(parts)-1]
	dbName := ""
	if len(parts) >= 3 {
		dbName = parts[len(parts)-3]
	}
	switch {
	case proc == "sp_executesql":
		return spec.ClassWrite, obj, "dynamic SQL is not classified", FlagDynamic
	case readProcedure(parts):
		if proc == "sp_spaceused" && spaceusedUpdates(toks, end, toks[i].depth) {
			return spec.ClassAdmin, obj, "sp_spaceused @updateusage runs DBCC UPDATEUSAGE", FlagExec
		}
		return spec.ClassRead, obj, "", 0
	case readSystemProcs[proc] && strings.HasPrefix(proc, "xp_"):
		// dbo.xp_msver outside master is the database's own procedure, not
		// administration: ahead of the xp_ admin rule below.
		return spec.ClassWrite, obj, "procedure " + obj + " is not on the read allow-list: " + proc +
			" is read only unqualified, as sys." + proc + " or as master.dbo." + proc, FlagExec
	case proc == "sp_rename" && argNotLiteral(toks, end, toks[i].depth, "@OBJTYPE", 3, "NULL", "'COLUMN'",
		"'INDEX'", "'OBJECT'", "'STATISTICS'", "'USERDATATYPE'"):
		return spec.ClassAdmin, obj, "sp_rename @objtype DATABASE renames a database", FlagExec
	case proc == "sp_settriggerorder" && argNotLiteral(toks, end, toks[i].depth, "@NAMESPACE", 4, "NULL", "'DATABASE'"):
		return spec.ClassAdmin, obj, "sp_settriggerorder @namespace SERVER orders a server trigger", FlagExec
	case ddlProcs[proc] != "":
		return spec.ClassDDL, obj, "procedure " + obj + " " + ddlProcs[proc], FlagExec
	case readMsdbProcs[proc] && dbName != "msdb":
		return spec.ClassWrite, obj, "procedure " + obj + " is read only as msdb.dbo." + proc +
			": elsewhere the name is the current database's own procedure", FlagExec
	case msdbProcs[proc]:
		// However qualified: after USE msdb the bare name is msdb's own.
		return spec.ClassAdmin, obj, "procedure " + obj + " is an msdb procedure, administration", FlagExec
	case adminProcs[proc] || strings.HasPrefix(proc, "xp_") || strings.HasPrefix(proc, "sp_trace_") ||
		strings.HasPrefix(proc, "sp_oa") || dbName == "msdb" && strings.HasPrefix(proc, "sp_"):
		return spec.ClassAdmin, obj, "procedure " + obj + " is administration", FlagExec
	}
	return spec.ClassWrite, obj, "procedure " + obj + " is not on the read allow-list", FlagExec
}

// readProcedure reports whether the dotted call name parts (nameParts) is a
// procedure on a read allow-list where the server resolves it: a system sp_
// one (readSystemProcs) with no schema, or dbo or sys; a system xp_ one with
// no schema, sys, or master.dbo; an Agent one (readMsdbProcs) in msdb's dbo
// schema. A linked server's four-part name is
// judged by its last three parts.
func readProcedure(parts []string) bool {
	proc := parts[len(parts)-1]
	schema, db := "", ""
	if len(parts) >= 2 {
		schema = parts[len(parts)-2]
	}
	if len(parts) >= 3 {
		db = parts[len(parts)-3]
	}
	switch {
	case readSystemProcs[proc] && strings.HasPrefix(proc, "xp_"):
		// The server resolves sp_ names to the system procedure even under
		// dbo, but not xp_ ones: dbo.xp_msver in a user database runs that
		// database's own procedure. master is the exception — a user
		// master.dbo.xp_fixeddrives exists side by side with the system one,
		// and the call still runs the system one (docs/decisions.md).
		return schema == "" || schema == "sys" || schema == "dbo" && db == "master"
	case readSystemProcs[proc]:
		return schema == "" || schema == "dbo" || schema == "sys"
	case readMsdbProcs[proc]:
		return db == "msdb" && (schema == "" || schema == "dbo")
	}
	return false
}

// spaceusedUpdates reports whether sp_spaceused's arguments, starting at
// toks[i] at parenthesis depth depth, ask for @updateusage: a named
// @updateusage, or the second positional argument, whose value is anything
// but the literal 'false' (N'false', any case). A variable there counts: the
// classifier cannot see its value.
func spaceusedUpdates(toks []token, i, depth int) bool {
	return argNotLiteral(toks, i, depth, "@UPDATEUSAGE", 2, "'FALSE'")
}

// argNotLiteral reports whether a procedure call's arguments, starting at
// toks[i] at parenthesis depth depth, pass the parameter name (upper-cased,
// with its @) — named, or as positional argument pos — with a value other than
// one of the literals in safe: upper-cased as written, a string's quotes kept
// and its N prefix dropped ("'FALSE'", "NULL"). A variable or an expression
// there counts as other: the classifier cannot see its value. An omitted
// argument is not passed.
func argNotLiteral(toks []token, i, depth int, name string, pos int, safe ...string) bool {
	n := 0
	for i < len(toks) && toks[i].depth >= depth {
		// One argument: up to the next comma at this depth.
		j := i
		for j < len(toks) && toks[j].depth >= depth && !(toks[j].depth == depth && toks[j].punct(",")) {
			j++
		}
		arg := toks[i:j]
		named := len(arg) >= 2 && arg[0].kind == sqltext.KindWord && strings.HasPrefix(arg[0].raw, "@") && arg[1].punct("=")
		var value []token
		switch {
		case named && arg[0].word == name:
			value = arg[2:]
		case !named:
			if n++; n == pos {
				value = arg
			}
		}
		if value != nil && !slices.Contains(safe, literalText(value)) {
			return true
		}
		i = j + 1
	}
	return false
}

// literalText is value as one upper-cased literal, a string's N prefix
// dropped: 'FALSE' for N'false', NULL for null.
func literalText(value []token) string {
	var b strings.Builder
	for _, t := range value {
		b.WriteString(t.raw)
	}
	v := strings.ToUpper(b.String())
	if strings.HasPrefix(v, "N'") {
		v = v[1:]
	}
	return v
}
