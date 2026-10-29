package cli

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/cli/agent"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/version"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current tree")

// stableVersion pins version.Version for the test, so the golden does not
// follow the checkout.
func stableVersion(t *testing.T) {
	t.Helper()
	old := version.Version
	version.Version = "test"
	t.Cleanup(func() { version.Version = old })
}

// TestAgentSchemaGolden pins the whole catalog: a change to any command's
// path, summary, argument, option, default or example shows up here, and
// -update rewrites it once the change is meant.
func TestAgentSchemaGolden(t *testing.T) {
	stableVersion(t)
	r := runMain(t, "agent", "schema")
	if r.exit != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.exit, r.stderr)
	}
	path := filepath.Join("testdata", "agent_schema.json.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(r.stdout), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if r.stdout != string(want) {
		t.Errorf("agent schema differs from %s (go test -update once the change is meant):\n%s", path, r.stdout)
	}
}

// TestAgentSchemaListsEveryCommand is §13's "a test fails if a command is
// missing from the catalog": every non-hidden command of the tree is a group
// or a command of the schema, with every option it accepts.
func TestAgentSchemaListsEveryCommand(t *testing.T) {
	tree := commandTree()
	var s agent.Schema
	if err := json.Unmarshal([]byte(runMain(t, "agent", "schema").stdout), &s); err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, g := range s.Groups {
		groups[g.Name] = true
	}
	cmds := map[string]agent.Command{}
	for _, c := range s.Commands {
		cmds[c.Path] = c
	}
	for _, c := range tree.Commands {
		if c.Hidden {
			continue
		}
		if len(tree.Children(c.Path)) > 0 {
			if !groups[c.Name()] {
				t.Errorf("group %q missing from the schema", c.Name())
			}
			continue
		}
		got, ok := cmds[c.Name()]
		if !ok {
			t.Errorf("command %q missing from the schema", c.Name())
			continue
		}
		var names []string
		for _, o := range got.Options {
			names = append(names, o.Name)
		}
		for _, o := range c.Options {
			if !slices.Contains(names, "--"+o.Name) {
				t.Errorf("%q: option --%s missing", c.Name(), o.Name)
			}
		}
		if len(got.Arguments) != len(c.Args) || len(got.Examples) != len(c.Examples) {
			t.Errorf("%q: %d arguments, %d examples; tree has %d, %d", c.Name(),
				len(got.Arguments), len(got.Examples), len(c.Args), len(c.Examples))
		}
	}
	if len(s.Globals) != len(tree.Globals) {
		t.Errorf("%d globals, tree has %d", len(s.Globals), len(tree.Globals))
	}
}

// TestAgentSchemaIsHelpJSON: help as JSON is the schema of the same topic,
// however the help was asked for.
func TestAgentSchemaIsHelpJSON(t *testing.T) {
	for _, tc := range []struct{ schema, help []string }{
		{[]string{"agent", "schema"}, []string{"help", "-o", "json"}},
		{[]string{"agent", "schema"}, []string{"--help", "-o", "json"}},
		{[]string{"agent", "schema", "-o", "json"}, []string{"-o", "json", "-h"}},
		{[]string{"agent", "schema", "object"}, []string{"help", "object", "-o", "json"}},
		{[]string{"agent", "schema", "object", "list"}, []string{"object", "list", "--help", "-o", "json"}},
		{[]string{"agent", "schema", "version"}, []string{"help", "version", "-o", "json"}},
	} {
		s, h := runMain(t, tc.schema...), runMain(t, tc.help...)
		if s.exit != 0 || h.exit != 0 || s.stdout != h.stdout {
			t.Errorf("%v (exit %d) and %v (exit %d) differ:\n%s\n---\n%s", tc.schema, s.exit, tc.help, h.exit, s.stdout, h.stdout)
		}
	}
}

func TestAgentSchemaTopic(t *testing.T) {
	var s agent.Schema
	if err := json.Unmarshal([]byte(runMain(t, "agent", "schema", "object").stdout), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Groups) != 1 || s.Groups[0].Name != "object" || len(s.Commands) == 0 {
		t.Fatalf("groups %v, %d commands", s.Groups, len(s.Commands))
	}
	for _, c := range s.Commands {
		if c.Path[:len("object ")] != "object " {
			t.Errorf("%q is not under object", c.Path)
		}
	}
	if len(s.Globals) == 0 || len(s.ExitCodes) == 0 || len(s.Formats) == 0 {
		t.Error("a topic still carries the globals, exit codes and formats")
	}
}

// TestAgentSchemaLeavesOutHidden: a hidden command, and everything under a
// hidden group, is parsed and runnable but not advertised.
func TestAgentSchemaLeavesOutHidden(t *testing.T) {
	tree := groupTree(nil)
	tree.Commands = append(tree.Commands,
		&spec.Command{Path: []string{"grp", "secret"}, Summary: "Hidden", Class: spec.ClassRead, Hidden: true},
		&spec.Command{Path: []string{"hid"}, Summary: "A hidden group", Class: spec.ClassRead, Hidden: true},
		&spec.Command{Path: []string{"hid", "cmd"}, Summary: "Under a hidden group", Class: spec.ClassRead},
	)
	var s agent.Schema
	r := runTree(t, context.Background(), tree, "agent", "schema")
	if err := json.Unmarshal([]byte(r.stdout), &s); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, g := range s.Groups {
		paths = append(paths, g.Name)
	}
	for _, c := range s.Commands {
		paths = append(paths, c.Path)
	}
	if !slices.Contains(paths, "grp cmd") {
		t.Errorf("grp cmd missing: %v", paths)
	}
	for _, p := range []string{"grp secret", "hid", "hid cmd"} {
		if slices.Contains(paths, p) {
			t.Errorf("%q is hidden but listed", p)
		}
	}
}
