package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// TestEveryGatedRightIsOneGosmoActuallyProbes.
//
// A requiredRight is only ever asked about through Capabilities.Allows or
// DatabaseCapabilities.Allows, and both answer *true* for a name that was
// never probed — the fail-open rule the whole layer is built on. So a name
// that is not in gosmo's Probed* list, or is in the list for the other scope,
// does not fail: it reads back CapabilityUnknown forever and the gate silently
// withholds nothing. Nothing at run time can tell that from a login that holds
// the right, which is why it has to be pinned here.
//
// The db flag chooses the list. HAS_PERMS_BY_NAME(NULL, NULL, 'ALTER') asks
// about the *server* and answers NULL, so a database-scope name declared with
// db:false disappears in exactly the same silent way.
//
// Read out of the source rather than off the vars, because Go cannot enumerate
// package-level variables — and a hand-written list here would go stale the
// first time a right is added, which is the case this test exists for. Same
// reasoning as prop_label_width_test.go.
func TestEveryGatedRightIsOneGosmoActuallyProbes(t *testing.T) {
	rights := parseRequiredRights(t, "permission_gate.go")
	if len(rights) < 15 {
		t.Fatalf("found only %d requiredRight literals; the parser has stopped seeing them", len(rights))
	}
	for _, r := range rights {
		scope, list := "server", gosmo.ProbedServerPermissions
		switch {
		case r.membership:
			// A membership right names a fixed database role, not a
			// permission, and is answered by IS_ROLEMEMBER out of
			// ProbedDatabaseRoles. A role missing from that list reads false
			// forever, which for a membership right means withholding the
			// action from everyone — the opposite failure to the one above,
			// and the louder of the two, but still silent at run time.
			if !slices.Contains(gosmo.ProbedDatabaseRoles, r.name) {
				t.Errorf("%q is declared as a membership right but is not in gosmo's ProbedDatabaseRoles — "+
					"it will read false for every login and withhold the action from all of them", r.name)
			}
			if r.inDB == "" {
				t.Errorf("membership right %q names no database to be a member in", r.name)
			}
			continue
		case r.serverRole:
			// A server-role right names a fixed server role, not a
			// permission, and is answered by IS_SRVROLEMEMBER out of
			// ProbedServerRoles. A role missing from that list reads false
			// forever, which for this kind of right means withholding the
			// action from everyone — membership's failure, at server scope.
			if !slices.Contains(gosmo.ProbedServerRoles, r.name) {
				t.Errorf("%q is declared as a server-role right but is not in gosmo's ProbedServerRoles — "+
					"it will read false for every login and withhold the action from all of them", r.name)
			}
			continue
		case r.schema:
			// Asked of the per-schema probe, which has a list of its own — a
			// name only the database-wide list carries reads unknown for
			// every schema and gates nothing.
			for _, name := range append([]string{r.name}, r.alt...) {
				if !slices.Contains(gosmo.ProbedSchemaPermissions, name) {
					t.Errorf("%q is declared schema-scoped but is not in gosmo's ProbedSchemaPermissions — "+
						"it will read CapabilityUnknown for every schema and gate nothing", name)
				}
			}
			continue
		case r.db:
			scope, list = "database", gosmo.ProbedDatabasePermissions
		}
		for _, name := range append([]string{r.name}, r.alt...) {
			if slices.Contains(list, name) {
				continue
			}
			other := gosmo.ProbedDatabasePermissions
			otherScope := "database"
			if r.db {
				other, otherScope = gosmo.ProbedServerPermissions, "server"
			}
			if slices.Contains(other, name) {
				t.Errorf("%q is declared at %s scope but gosmo probes it at %s scope — "+
					"it will read CapabilityUnknown forever and gate nothing", name, scope, otherScope)
				continue
			}
			t.Errorf("%q is in neither of gosmo's Probed*Permissions lists — "+
				"it will read CapabilityUnknown forever and gate nothing", name)
		}
	}
}

// TestEveryGatedRightNamesARealRole. The role is display-only — it is what
// requiresText tells the user to go and ask for — so a typo cannot break a
// gate. It sends them to an administrator asking for a role that does not
// exist, which is worse than saying nothing.
func TestEveryGatedRightNamesARealRole(t *testing.T) {
	for _, r := range parseRequiredRights(t, "permission_gate.go") {
		if r.role == "" {
			continue
		}
		list := gosmo.ProbedServerRoles
		if r.db {
			list = gosmo.ProbedDatabaseRoles
		}
		if !slices.Contains(list, r.role) {
			t.Errorf("right %q names role %q, which is not a fixed role at that scope", r.name, r.role)
		}
	}
}

// parsedRight is one requiredRight composite literal as written in the source.
type parsedRight struct {
	name       string
	role       string
	db         bool
	schema     bool
	membership bool
	inDB       string
	serverRole bool
	alt        []string
}

// parseRequiredRights returns every `requiredRight{...}` literal in file.
// Fields are read by name, so reordering them in the struct changes nothing
// here; a field given a non-literal value is skipped, and there are none.
func parseRequiredRights(t *testing.T, file string) []parsedRight {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	var out []parsedRight
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "requiredRight" {
			return true
		}
		var r parsedRight
		for _, e := range lit.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "name":
				r.name = stringLit(t, kv.Value)
			case "role":
				r.role = stringLit(t, kv.Value)
			case "db":
				id, ok := kv.Value.(*ast.Ident)
				r.db = ok && id.Name == "true"
			case "schema":
				id, ok := kv.Value.(*ast.Ident)
				r.schema = ok && id.Name == "true"
			case "membership":
				id, ok := kv.Value.(*ast.Ident)
				r.membership = ok && id.Name == "true"
			case "inDB":
				r.inDB = stringLit(t, kv.Value)
			case "serverRole":
				id, ok := kv.Value.(*ast.Ident)
				r.serverRole = ok && id.Name == "true"
			case "alt":
				alts, ok := kv.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, a := range alts.Elts {
					r.alt = append(r.alt, stringLit(t, a))
				}
			}
		}
		if r.name != "" {
			out = append(out, r)
		}
		return true
	})
	return out
}

// stringLit unquotes a string literal, failing on anything else — every field
// these tests read is written as one.
func stringLit(t *testing.T, e ast.Expr) string {
	t.Helper()
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		t.Fatalf("expected a string literal, got %T", e)
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatalf("unquote %s: %v", lit.Value, err)
	}
	return s
}
