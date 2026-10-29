package cmd

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/output"
)

func TestParseObjectName(t *testing.T) {
	for _, tt := range []struct {
		in           string
		schema, name string
	}{
		{"Customer", "", "Customer"},
		{"Sales.Customer", "Sales", "Customer"},
		{" Sales . Customer ", "Sales", "Customer"},
		{"Sales.[Order.Detail]", "Sales", "Order.Detail"},
		{"[a]]b].x", "a]b", "x"},
		{`"Sales"."Order ""x"""`, "Sales", `Order "x"`},
		{"[Sales].[Customer]", "Sales", "Customer"},
		{"app.Sales.Customer", "Sales", "Customer"},
		{"[app].dbo.t", "dbo", "t"},
		{"név.ő", "név", "ő"},
		{"dbo.[1x]", "dbo", "1x"},
	} {
		n, err := parseObjectName(tt.in, "app")
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if n.Schema != tt.schema || n.Name != tt.name {
			t.Errorf("%q: got %+v, want %q.%q", tt.in, n, tt.schema, tt.name)
		}
	}
}

func TestParseObjectNameRefuses(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"", "empty"},
		{"  ", "empty"},
		{"a.", "ends in a dot"},
		{".a", "part is empty"},
		{"app..t", "part is empty"},
		{"a b", "separated by dots"},
		{"[a", "unclosed"},
		{"a.[]", "part is empty"},
		{"a.b.c.d", "4 parts"},
		{"other.dbo.t", "names database [other]"},
		{"APP.dbo.t", "names database [APP]"}, // compared exactly
		{"dbo.t;DROP TABLE x", `unexpected ";"`},
		{"dbo.t--x", "unexpected"},
		{"dbo.'t'", "unexpected"},
		{"1x", "unexpected"},
		{"my-table", `unexpected "-"`},
	} {
		_, err := parseObjectName(tt.in, "app")
		e, ok := err.(*output.Error)
		if !ok || e.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", tt.in, err)
			continue
		}
		if !strings.Contains(e.Message, tt.want) {
			t.Errorf("%q: message %q, want it to contain %q", tt.in, e.Message, tt.want)
		}
	}
}

// Whatever the input, the parser answers a name or a usage error, and a name
// it answers round-trips through its own bracketed form.
func FuzzParseObjectName(f *testing.F) {
	for _, s := range []string{"Sales.Customer", "[a]]b].x", `"a""b".c`, "app.dbo.t", "a..b", "[", "dbo.t--", "/*x*/t"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := parseObjectName(s, "app")
		if err != nil {
			if e, ok := err.(*output.Error); !ok || e.Code != output.CodeUsageError {
				t.Fatalf("%q: err = %v, want a usage error", s, err)
			}
			return
		}
		if n.Name == "" {
			t.Fatalf("%q: empty name in %+v", s, n)
		}
		again, err := parseObjectName(n.String(), "app")
		if err != nil || again != n {
			t.Fatalf("%q: %+v -> %q -> %+v, %v", s, n, n.String(), again, err)
		}
	})
}
