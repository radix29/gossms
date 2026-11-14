package textutil

import "testing"

// TestOneLine pins OneLine's contract: one space per break or tab, including
// the ones outside ASCII (NEL, LS, PS), and nothing else touched.
func TestOneLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain  text", "plain  text"},
		{"a\r\nb", "a  b"},
		{"a\tb\vc\fd", "a b c d"},
		{"a\u0085b c d", "a b c d"},
		{"trailing\n", "trailing "},
		{"nbsp kept", "nbsp kept"},
	}
	for _, c := range cases {
		if got := OneLine(c.in); got != c.want {
			t.Errorf("OneLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCollapse pins Collapse's contract: every whitespace run, breaks and NEL
// included, becomes one space, and the ends are trimmed.
func TestCollapse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"  SELECT\r\n\t  1\n", "SELECT 1"},
		{"a\u0085b c", "a b c"},
		{"a  b", "a b"},
		{"\n\t ", ""},
	}
	for _, c := range cases {
		if got := Collapse(c.in); got != c.want {
			t.Errorf("Collapse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAllDigits(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{{"", false}, {"0", true}, {"0123456789", true}, {"12a", false}, {"-1", false}, {"١", false}} {
		if got := AllDigits(c.in); got != c.want {
			t.Errorf("AllDigits(%q) = %v, want %v", c.in, got, c.want)
		}
		if got := AllDigits([]byte(c.in)); got != c.want {
			t.Errorf("AllDigits([]byte(%q)) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNullable(t *testing.T) {
	if Nullable("") != nil {
		t.Error(`Nullable("") is not nil`)
	}
	if p := Nullable("x"); p == nil || *p != "x" {
		t.Errorf(`Nullable("x") = %v`, p)
	}
}
