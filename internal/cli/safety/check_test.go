package safety

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/spec"
)

func TestCheckClass(t *testing.T) {
	for _, tt := range []struct {
		script         string
		allow, ceiling spec.Class
		blocked        bool
		allowed        spec.Class
		hint           string // a substring of the hint
	}{
		{"SELECT 1", read, "", false, "", ""},
		{"UPDATE t SET a = 1", read, "", true, read, "--allow write"},
		{"UPDATE t SET a = 1", write, "", false, "", ""},
		{"UPDATE t SET a = 1", admin, "", false, "", ""},
		{"DROP TABLE t", write, "", true, write, "--allow ddl"},
		{"KILL 52", ddl, admin, true, ddl, "--allow admin"},
		// The ceiling refuses what --allow would let through, and says so.
		{"UPDATE t SET a = 1", admin, read, true, read, "ceiling (max_allow) is read"},
		{"UPDATE t SET a = 1", write, write, false, "", ""},
		// Both: --allow is too low and so is the ceiling.
		{"DROP TABLE t", read, write, true, read, "above the profile's ceiling (max_allow write)"},
	} {
		b := Check(Classify(tt.script), Policy{Allow: tt.allow, Ceiling: tt.ceiling})
		if (b != nil) != tt.blocked {
			t.Errorf("%q allow %s ceiling %q: blocked = %v, want %v", tt.script, tt.allow, tt.ceiling, b != nil, tt.blocked)
			continue
		}
		if b == nil {
			continue
		}
		if b.Allowed != tt.allowed || b.Detected != Classify(tt.script).Class {
			t.Errorf("%q: detected %s allowed %s, want allowed %s", tt.script, b.Detected, b.Allowed, tt.allowed)
		}
		if !strings.Contains(b.Hint, tt.hint) {
			t.Errorf("%q: hint %q, want it to contain %q", tt.script, b.Hint, tt.hint)
		}
	}
}

// A refusal names only the statements above the allowed class, in order.
func TestCheckNamesTheOffendingStatements(t *testing.T) {
	sc := Classify("SELECT 1\nUPDATE Sales.Customer SET a = 1\nSELECT 2\nGO\nDELETE dbo.t\nSELECT 1\nDISABLE TRIGGER tr ON t")
	b := Check(sc, Policy{Allow: read})
	if b == nil {
		t.Fatal("not blocked")
	}
	want := "UPDATE Sales.Customer (line 2); DELETE dbo.t (line 5); SELECT (line 6): keyword DISABLE (line 7)"
	if got := b.Describe(); got != want {
		t.Errorf("Describe() = %q\nwant         %q", got, want)
	}
	if b.Detected != ddl {
		t.Errorf("detected %s, want ddl", b.Detected)
	}
	// Under --allow write only the ddl statement is named.
	if got := Check(sc, Policy{Allow: write}).Describe(); got != "SELECT (line 6): keyword DISABLE (line 7)" {
		t.Errorf("under write: %q", got)
	}
}

// The modes the command line owns are refused whatever --allow says.
func TestCheckModeOptions(t *testing.T) {
	for _, s := range []string{"SET PARSEONLY ON\nSELECT 1", "SET SHOWPLAN_XML ON", "SET NOEXEC ON"} {
		b := Check(Classify(s), Policy{Allow: admin})
		if b == nil || !strings.Contains(b.Message, "PARSEONLY") || len(b.Statements) != 1 {
			t.Errorf("%q: %+v", s, b)
		}
	}
	if b := Check(Classify("SET NOEXEC OFF"), Policy{Allow: read}); b != nil {
		t.Errorf("SET NOEXEC OFF blocked: %s", b.Message)
	}
}

// §6.4: a wrapping transaction refuses transaction control and what SQL Server
// refuses inside one; only when there is one.
func TestCheckTransaction(t *testing.T) {
	for _, tt := range []struct {
		script string
		want   string // substring of the message; empty: not blocked
	}{
		{"UPDATE t SET a = 1", ""},
		{"BEGIN TRAN\nUPDATE t SET a = 1\nCOMMIT", "controls transactions"},
		{"SET IMPLICIT_TRANSACTIONS ON", "controls transactions"},
		{"ALTER DATABASE d SET RECOVERY SIMPLE", "inside a transaction"},
		{"BACKUP LOG d TO DISK = 'x'", "inside a transaction"},
	} {
		b := Check(Classify(tt.script), Policy{Allow: admin, Transaction: true})
		if got := ""; b != nil {
			got = b.Message
			if tt.want == "" || !strings.Contains(got, tt.want) {
				t.Errorf("%q: blocked %q, want %q", tt.script, got, tt.want)
			}
		} else if tt.want != "" {
			t.Errorf("%q: not blocked, want %q", tt.script, tt.want)
		}
		if b := Check(Classify(tt.script), Policy{Allow: admin}); b != nil {
			t.Errorf("%q blocked without a transaction: %s", tt.script, b.Message)
		}
	}
}

// D4: --max-rows-affected refuses a script whose row counts can be invisible.
func TestCheckMaxRowsAffectedBlindSpots(t *testing.T) {
	for _, tt := range []struct {
		script  string
		blocked bool
	}{
		{"UPDATE t SET a = 1", false},
		{"EXEC sp_who2\nUPDATE t SET a = 1", false},
		{"SET NOCOUNT ON\nUPDATE t SET a = 1", true},
		{"EXEC dbo.p", true},
		{"EXEC (@sql)", true},
		{"UPDATE t SET a = 1 OUTPUT inserted.a", true},
	} {
		b := Check(Classify(tt.script), Policy{Allow: admin, Transaction: true, MaxRowsAffected: true})
		if (b != nil) != tt.blocked {
			t.Errorf("%q: blocked = %v, want %v", tt.script, b != nil, tt.blocked)
		}
		if b != nil && !strings.Contains(b.Message, "--max-rows-affected") {
			t.Errorf("%q: message %q", tt.script, b.Message)
		}
	}
}

// The class check comes first: a write script under read is refused for its
// class, not for a flag.
func TestCheckOrder(t *testing.T) {
	b := Check(Classify("SET NOCOUNT ON\nBEGIN TRAN\nUPDATE t SET a = 1"), Policy{Allow: read, Transaction: true, MaxRowsAffected: true})
	if b == nil || !strings.Contains(b.Message, "allowed class") {
		t.Fatalf("%+v", b)
	}
}
