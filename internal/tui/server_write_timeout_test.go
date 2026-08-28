package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// TestServerWriteContextOutlivesAFolderRead.
//
// Object Explorer's Delete, Rename and Move-to-schema, the database
// online/offline toggle, the Agent enable/delete/start/stop actions and every
// Always On operation used to share childFetchTimeout with every read in the
// package, and 30s is a folder listing's budget, not a write's. A drop or
// rename waits on locks another session holds; SET OFFLINE WITH ROLLBACK
// IMMEDIATE waits for everything it killed to roll back; SET ONLINE waits for
// recovery; a failover waits for the target to catch up. Minutes, routinely. On
// the read budget the statement was abandoned mid-flight, and gosmo's MULTI_USER
// repair then ran on a context that had already expired.
//
// Asserted as a deadline rather than by comparing the two constants, because
// the mutant worth killing is serverWriteContext quietly going back to
// childFetchTimeout — which leaves serverWriteTimeout declared and unused, and
// a constants-only comparison still passing.
func TestServerWriteContextOutlivesAFolderRead(t *testing.T) {
	if serverWriteTimeout <= childFetchTimeout {
		t.Fatalf("serverWriteTimeout is %v, not longer than childFetchTimeout's %v", serverWriteTimeout, childFetchTimeout)
	}

	// A nil connection: ServerConn.Context() falls back to context.Background()
	// for exactly this, so the budget can be measured without a server.
	ctx, cancel := serverWriteContext((*db.ServerConn)(nil))
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("serverWriteContext returned a context with no deadline; a dead connection would leave the status line pending forever")
	}
	got := time.Until(deadline)
	if got <= childFetchTimeout {
		t.Errorf("serverWriteContext's budget is %v, no more than the read timeout %v — a write is not a read", got, childFetchTimeout)
	}
	if delta := serverWriteTimeout - got; delta < 0 || delta > time.Second {
		t.Errorf("serverWriteContext's budget is %v, want serverWriteTimeout (%v)", got, serverWriteTimeout)
	}
}

// readTimeoutSites is every reference to childFetchTimeout the package is meant
// to have, by file. The declaration itself counts as one, which is why
// app_explorer_data.go is one higher than its two reads.
//
// Every entry is a *read*, with one deliberate exception noted below. Writes go
// through serverWriteContext, and nothing at run time tells the two budgets
// apart: the wrong one only shows as a write abandoned part-done, against a
// server slow enough to reach it. The habit that put the read's timeout on the
// writes is a keystroke away from returning on the next action added, and
// several of these files hold reads and writes side by side.
//
// So: a new read raises its file's number here, on purpose and in the same
// commit. A new write does not appear here at all — it wants
// serverWriteContext.
//
// The exception is restore_dialog_ops.go's seventh reference, the
// SET MULTI_USER after a failed restore. That one is a *repair* and is on the
// short budget deliberately, for the reason gosmo's Server.restoreMultiUser
// gives for its own 10s: the caller still holds the single-user slot, so the
// ALTER has nothing to wait for, and a repair that hangs is worse than one that
// gives up.
var readTimeoutSites = map[string]int{
	"alwayson_menu.go":     2,
	"app_connections.go":   1,
	"app_explorer_data.go": 3,
	"backup_dialog.go":     1,
	// 2: the node-detail fetch, and re-reading a Query Store query's text for
	// "Show Value" — see DetailBrowser.showQueryStoreValue.
	"detail_browser.go":           2,
	"detail_browser_backfill.go":  1,
	"detail_browser_databases.go": 1,
	"detail_browser_logins.go":    1,
	"detail_browser_server.go":    1,
	"detail_browser_tables.go":    1,
	"explorer_object_ops.go":      1,
	"properties_dialog.go":        1,
	"restore_dialog_ops.go":       7,
	"scripting.go":                1,
}

// TestOnlyReadsUseTheReadTimeout pins readTimeoutSites against the package.
func TestOnlyReadsUseTheReadTimeout(t *testing.T) {
	got := map[string]int{}
	fset := token.NewFileSet()

	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		// Identifiers inside comments are not in the AST, so the doc comments
		// naming childFetchTimeout don't count — only real references do.
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "childFetchTimeout" {
				got[name]++
			}
			return true
		})
	}

	for name, want := range readTimeoutSites {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("readTimeoutSites names %s, which no longer exists", name)
			continue
		}
		if got[name] != want {
			t.Errorf("%s references childFetchTimeout %d times, want %d\n"+
				"a new write wants serverWriteContext; a new read wants this number raised on purpose",
				name, got[name], want)
		}
	}
	for name, n := range got {
		if _, ok := readTimeoutSites[name]; !ok {
			t.Errorf("%s references childFetchTimeout %d times and is not in readTimeoutSites\n"+
				"if that is a write it wants serverWriteContext; if it is a read, add the file here",
				name, n)
		}
	}
}
