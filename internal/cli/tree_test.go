package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/spec"
)

// TestCommandTreeWellFormed checks the shape rules spec documents but its
// types cannot enforce. (Phase 0.g adds the examples-parse drift guard.)
func TestCommandTreeWellFormed(t *testing.T) {
	tree := commandTree()
	checkOption := func(where string, o spec.Option) {
		if o.Name == "" || strings.HasPrefix(o.Name, "-") {
			t.Errorf("%s: option name %q must be non-empty, without dashes", where, o.Name)
		}
		if len([]rune(o.Short)) > 1 {
			t.Errorf("%s --%s: short name %q is more than one letter", where, o.Name, o.Short)
		}
		if o.Help == "" {
			t.Errorf("%s --%s: no help", where, o.Name)
		}
		if (o.Type == spec.TypeEnum) != (len(o.Enum) > 0) {
			t.Errorf("%s --%s: Enum must be set exactly when Type is enum", where, o.Name)
		}
		if o.Type == spec.TypeEnum && o.Default != "" && !slices.Contains(o.Enum, o.Default) {
			t.Errorf("%s --%s: default %q is not in %v", where, o.Name, o.Default, o.Enum)
		}
	}
	// Long and short names are unique across a command's options and the
	// globals together, since the parser looks them up in that union.
	checkUnique := func(where string, opts []spec.Option) {
		long, short := map[string]bool{}, map[string]bool{}
		for _, o := range opts {
			if long[o.Name] {
				t.Errorf("%s: --%s defined twice", where, o.Name)
			}
			long[o.Name] = true
			if o.Short != "" {
				if short[o.Short] {
					t.Errorf("%s: -%s defined twice", where, o.Short)
				}
				short[o.Short] = true
			}
		}
	}

	for _, o := range tree.Globals {
		checkOption("global", o)
		if !o.Global {
			t.Errorf("global --%s: Global is not set", o.Name)
		}
	}
	checkUnique("globals", tree.Globals)

	paths := map[string]bool{}
	for _, c := range tree.Commands {
		where := strings.Join(c.Path, " ")
		if len(c.Path) < 1 || len(c.Path) > 2 {
			t.Errorf("%q: path depth %d, want 1 or 2", where, len(c.Path))
		}
		if paths[where] {
			t.Errorf("%q: defined twice", where)
		}
		paths[where] = true
		if c.Summary == "" || strings.HasSuffix(c.Summary, ".") {
			t.Errorf("%q: summary %q must be non-empty, with no trailing period", where, c.Summary)
		}
		switch c.Class {
		case spec.ClassRead, spec.ClassWrite, spec.ClassDDL, spec.ClassAdmin:
		default:
			t.Errorf("%q: class %q", where, c.Class)
		}
		for i, a := range c.Args {
			if a.Name == "" || a.Help == "" {
				t.Errorf("%q: argument %d needs a name and help", where, i)
			}
			if a.Repeat && i != len(c.Args)-1 {
				t.Errorf("%q <%s>: only the last argument may repeat", where, a.Name)
			}
			if a.Required && i > 0 && !c.Args[i-1].Required {
				t.Errorf("%q <%s>: a required argument follows an optional one", where, a.Name)
			}
		}
		for _, o := range c.Options {
			checkOption(where, o)
			if o.Global {
				t.Errorf("%q --%s: a command's own option has Global set", where, o.Name)
			}
		}
		checkUnique(where, append(slices.Clone(tree.Globals), c.Options...))
		for _, ex := range c.Examples {
			if !strings.HasPrefix(ex, "gossms "+where) {
				t.Errorf("%q: example %q does not run this command", where, ex)
			}
		}
	}
	// Every depth-2 command sits under a group node.
	for _, c := range tree.Commands {
		if len(c.Path) == 2 && !paths[c.Path[0]] {
			t.Errorf("%q: no group node %q", strings.Join(c.Path, " "), c.Path[0])
		}
	}
}
