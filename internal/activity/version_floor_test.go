package activity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// C3: CREATE OR ALTER arrived in SQL Server 2016 SP1 (13.0.4001), and it is
// the only reason goSSMS's supported floor is SP1 rather than 2016 RTM — every
// other statement either runs on RTM or is gated on the connected instance's
// version. Each unconditional site raises that floor for one more operation,
// so the inventory is pinned here rather than rediscovered by a user on an old
// server. README.md § Supported SQL Server versions states the same three.
//
// String literals only, via the parser: several files' comments name the
// keywords while their code does not emit them, and a source grep cannot tell
// the two apart.
func TestOnlyKnownSitesEmitCreateOrAlter(t *testing.T) {
	// Repo-relative. gosmo's own site (procedure.go) is pinned by gosmo's
	// TestOnlyKnownSitesEmitCreateOrAlter; this covers goSSMS's half.
	allowed := map[string]bool{
		"internal/activity/block.go":       true,
		"internal/activity/whoisactive.go": true,
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// todo/ is scratch notes and SQL, not source.
			if name := d.Name(); name == ".git" || name == "todo" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if !strings.Contains(strings.ToUpper(lit.Value), "CREATE OR ALTER") {
				return true
			}
			found[rel] = true
			if !allowed[rel] {
				t.Errorf("%s: a SQL literal emits CREATE OR ALTER, which SQL Server 2016 RTM "+
					"cannot parse — every such site raises the supported floor, so add it here "+
					"and to README.md deliberately", rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no source files walked; the root is wrong and this test proves nothing")
	}
	for rel := range allowed {
		if !found[rel] {
			t.Errorf("%s no longer emits CREATE OR ALTER; drop it from allowed, and if it was "+
				"the last one here the floor is gosmo's alone", rel)
		}
	}
}
