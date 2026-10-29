package agent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

func TestBuildOptionShapes(t *testing.T) {
	tree := &spec.Tree{
		Globals: []spec.Option{{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: []string{"text", "json"}, Default: "text", Help: "h", Global: true}},
		Commands: []*spec.Command{{
			Path: []string{"x"}, Summary: "X", Class: spec.ClassRead,
			Args: []spec.Arg{{Name: "name", Help: "h", Format: "schema.name", Required: true}},
			Options: []spec.Option{
				{Name: "on", Type: spec.TypeBool, Default: "true", Help: "h"},
				{Name: "off", Type: spec.TypeBool, Help: "h"},
				{Name: "n", Type: spec.TypeInt, Default: "200", Help: "h"},
				{Name: "none", Type: spec.TypeInt, Help: "h"},
				{Name: "kinds", Type: spec.TypeList, Enum: []string{"a", "b"}, Default: "a,b", Help: "h"},
				{Name: "param", Type: spec.TypeRepeat, Env: "X_PARAM", Help: "h"},
			},
		}},
	}
	var buf bytes.Buffer
	if err := Build(tree, "v1", nil).Write(&buf); err != nil {
		t.Fatal(err)
	}
	var s struct {
		SchemaVersion, GossmsVersion string
		ExitCodes                    []map[string]any
		Globals                      []map[string]any
		Commands                     []struct {
			Description any
			Arguments   []map[string]any
			Options     []map[string]any
			Examples    []string
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != output.SchemaVersion || s.GossmsVersion != "v1" || len(s.ExitCodes) != len(output.ExitCodes) {
		t.Errorf("header %q %q, %d exit codes", s.SchemaVersion, s.GossmsVersion, len(s.ExitCodes))
	}
	if g := s.Globals[0]; g["name"] != "--output" || g["short"] != "-o" || g["default"] != "text" || g["negation"] != nil {
		t.Errorf("global %v", g)
	}
	c := s.Commands[0]
	if c.Description != nil || c.Examples == nil {
		t.Errorf("description %v, examples %v: want null and []", c.Description, c.Examples)
	}
	if a := c.Arguments[0]; a["format"] != "schema.name" || a["required"] != true || a["repeat"] != false {
		t.Errorf("argument %v", a)
	}
	for i, want := range []map[string]any{
		{"default": true, "negation": "--no-on", "short": nil, "enum": []any{}},
		{"default": false, "negation": "--no-off"},
		{"default": float64(200), "negation": nil},
		{"default": nil},
		{"default": []any{"a", "b"}, "enum": []any{"a", "b"}},
		{"default": nil, "env": "X_PARAM"},
	} {
		for k, v := range want {
			if got, ok := c.Options[i][k]; !ok || !reflect.DeepEqual(got, v) {
				t.Errorf("%v: %s = %#v, want %#v", c.Options[i]["name"], k, got, v)
			}
		}
	}
}
