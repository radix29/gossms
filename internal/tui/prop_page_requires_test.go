package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// propPageSets is every Properties dialog's page set, named as the user would
// name the dialog. It exists so the tests below can ask the same question of
// all of them at once — "does this page say what its writes need?" — which is
// the only form in which withRequires's promise is enforceable. A dialog absent
// from here is checked by nothing, so propPageConstructors pins the list.
func propPageSets(sc *db.ServerConn, d *PropDialog) map[string][]propPage {
	return map[string][]propPage{
		"Server":                     serverPropPages(sc),
		"Database":                   databasePropPages(sc, "HealthClinic"),
		"Login":                      loginPropPages(d, sc, "sa"),
		"Server Role":                serverRolePropPages(sc, "sysadmin"),
		"Credential":                 credentialPropPages(sc, "app_cred"),
		"Backup Device":              backupDevicePropPages(sc, "NightlyDev"),
		"Audit":                      auditPropPages(sc, "HIPAA"),
		"Server Audit Specification": serverAuditSpecificationPropPages(sc, "HIPAA_spec"),
		"Server Trigger":             serverTriggerPropPages(sc, "ddl_audit"),
		"Endpoint":                   endpointPropPages(sc, "AGEP"),
		"User":                       userPropPages(d, sc, "HealthClinic", "dbo"),
		"Database Role":              rolePropPages(d, sc, "HealthClinic", "db_owner"),
		"Schema":                     schemaPropPages(sc, "HealthClinic", "dbo"),
		"Table":                      tablePropPages(sc, "HealthClinic", "dbo", "Patient"),
		"Index":                      indexPropPages(d, sc, "HealthClinic", "dbo", "Patient", "IX_Patient"),
		"Statistic":                  statisticPropPages(d, sc, "HealthClinic", "dbo", "Patient", "ST_Patient"),
		"Key":                        keyPropPages(d, sc, "HealthClinic", "dbo", "Patient", "PK_Patient"),
		"Foreign Key":                fkPropPages(sc, "HealthClinic", "dbo", "Patient", "FK_Patient"),
		"Partition Function":         partitionFunctionPropPages(sc, "HealthClinic", "pf"),
		"Partition Scheme":           partitionSchemePropPages(sc, "HealthClinic", "ps"),
		"Security Policy":            securityPolicyPropPages(sc, "HealthClinic", "dbo", "pol"),
		"Column Master Key":          columnMasterKeyPropPages(sc, "HealthClinic", "CMK1"),
		"Column Encryption Key":      columnEncryptionKeyPropPages(sc, "HealthClinic", "CEK1"),
		"Availability Group":         agPropPages(sc, "ag1"),
		"AG Listener":                agListenerPropPages(sc, "ag1", "listener"),
		"Job":                        jobPropPages(d, sc, "nightly"),
		"Alert":                      alertPropPages(sc, "alert1"),
		"Operator":                   operatorPropPages(sc, "oncall"),
		"Schedule":                   schedulePropPages(sc, "nightly"),
	}
}

// pagesThatOnlyRead names every Properties page allowed to arrive without
// requires, as "<dialog>/<page>". A page belongs here only because it has no
// apply at all — showing "this login cannot change these settings" above a page
// with nothing to change sends the reader after a permission that would not
// help them.
//
// The one entry that is not a read-only page is Login/User Mapping, and the
// reason is written where its pages are assembled.
var pagesThatOnlyRead = []string{
	"Server/General",
	"Login/User Mapping",
	"Login/Effective Permissions",
	"User/Effective Permissions",
	"Table/General", "Table/Columns", "Table/Storage",
	"Index/General", "Index/Storage", "Index/Filter", "Index/Fragmentation",
	"Statistic/General", "Statistic/Columns", "Statistic/Filter",
	"Statistic/Details", "Statistic/Histogram", "Statistic/Density Vector",
	"Key/Storage", "Key/Fragmentation",
	"Foreign Key/General",
	"Partition Function/General", "Partition Scheme/General",
	"Security Policy/General",
	"Column Master Key/General", "Column Encryption Key/General",
	"Backup Device/General", "Backup Device/Media Contents",
	"Server Trigger/General", "Server Trigger/Definition",
	"Endpoint/General", "Endpoint/Type Properties",
	"Job/Targets", "Job/History",
	"Operator/Notifications",
	"Schedule/Jobs",
}

// allPropPageKeys is every page in every dialog, as "<dialog>/<page>".
func allPropPageKeys(t *testing.T) (keys []string, pages map[string]propPage) {
	t.Helper()
	sc, _ := newFakeConn(t)
	pages = map[string]propPage{}
	for dialog, set := range propPageSets(sc, &PropDialog{}) {
		for _, p := range set {
			key := dialog + "/" + p.title
			keys = append(keys, key)
			pages[key] = p
		}
	}
	return keys, pages
}

// TestEveryWritablePropPageDeclaresItsRights is what makes withRequires's
// docstring true. A page that can write and says nothing about what permits it
// opens fully editable for a login that will be refused on Apply — which is
// exactly the case the read-only banner was built for.
func TestEveryWritablePropPageDeclaresItsRights(t *testing.T) {
	keys, pages := allPropPageKeys(t)
	for _, key := range keys {
		listed := slices.Contains(pagesThatOnlyRead, key)
		switch {
		case len(pages[key].requires) == 0 && !listed:
			t.Errorf("%s declares no rights: wrap it in withRequires, or add it to pagesThatOnlyRead if it has no apply", key)
		case len(pages[key].requires) > 0 && listed:
			t.Errorf("%s is in pagesThatOnlyRead but declares rights — drop it from the list", key)
		}
	}
}

// TestPagesThatOnlyReadHasNoStaleEntries. A renamed or deleted page leaves its
// exemption behind, and the exemption is what the test above trusts.
func TestPagesThatOnlyReadHasNoStaleEntries(t *testing.T) {
	keys, _ := allPropPageKeys(t)
	for _, key := range pagesThatOnlyRead {
		if !slices.Contains(keys, key) {
			t.Errorf("pagesThatOnlyRead names %q, which no dialog builds", key)
		}
	}
}

// TestAnObjectScopedPageNamesItsSecurable. objectWriteRights()'s last two
// entries are asked about a schema and an object, and answer for nobody when
// the page did not say which — the principal granted ALTER on one table reads 0
// everywhere else, so the page they can write would come up read-only. Wrapping
// such a page in plain withRequires compiles and looks right.
func TestAnObjectScopedPageNamesItsSecurable(t *testing.T) {
	keys, pages := allPropPageKeys(t)
	for _, key := range keys {
		p := pages[key]
		for _, r := range p.requires {
			if (r.object || r.schema) && p.requiresSchema == "" {
				t.Errorf("%s requires %q at object/schema scope but names no schema — use withRequiresOn", key, r.name)
				break
			}
			if r.object && p.requiresObject == "" {
				t.Errorf("%s requires %q on the object itself but names no object — use withRequiresOn", key, r.name)
				break
			}
		}
	}
}

// propPageConstructors names every function in this package returning a
// []propPage for a Properties dialog. The test below reads the package back and
// fails when the two disagree — a new dialog absent from propPageSets is
// checked by neither test above, which is the silent failure this file exists
// to prevent.
var propPageConstructors = []string{
	"agListenerPropPages", "agPropPages", "alertPropPages",
	"columnEncryptionKeyPropPages", "columnMasterKeyPropPages",
	"databasePropPages", "fkPropPages", "indexPropPages", "jobPropPages",
	"keyPropPages", "loginPropPages", "operatorPropPages",
	"partitionFunctionPropPages", "partitionSchemePropPages", "rolePropPages",
	"credentialPropPages", "backupDevicePropPages", "serverTriggerPropPages", "endpointPropPages",
	"auditPropPages", "serverAuditSpecificationPropPages",
	"schedulePropPages", "schemaPropPages", "securityPolicyPropPages",
	"serverPropPages", "serverRolePropPages", "statisticPropPages",
	"tablePropPages", "userPropPages",
}

func TestEveryPropPagesConstructorIsListed(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var found []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			arr, ok := fn.Type.Results.List[0].Type.(*ast.ArrayType)
			if !ok {
				continue
			}
			if id, ok := arr.Elt.(*ast.Ident); ok && id.Name == "propPage" {
				found = append(found, fn.Name.Name)
			}
		}
	}
	slices.Sort(found)
	want := slices.Clone(propPageConstructors)
	slices.Sort(want)
	if !slices.Equal(found, want) {
		t.Errorf("functions returning []propPage:\n  found  %v\n  listed %v", found, want)
	}
	if n := len(propPageSets(nil, nil)); n != len(propPageConstructors) {
		t.Errorf("propPageSets covers %d dialogs, but %d constructors exist", n, len(propPageConstructors))
	}
}
