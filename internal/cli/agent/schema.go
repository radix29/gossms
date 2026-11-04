// Package agent is the command line's self-description for AI agents: `agent
// schema`, the machine-readable catalog (docs/cli.md § Agent schema), and
// `agent guide`, the manual (§ Agent guide). The catalog, and every part of
// the guide but its hand-written rules and prose (guide/*.md), are generated
// from the command tree and the output package's tables, so neither can
// drift from the commands it describes.
package agent

import (
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// Schema is the catalog `agent schema` and `help -o json` print. Struct field
// order is output order; every key is always present (null or [] when
// empty), as in the envelope.
type Schema struct {
	SchemaVersion string     `json:"schemaVersion"`
	GossmsVersion string     `json:"gossmsVersion"`
	Formats       []string   `json:"formats"`
	ExitCodes     []ExitCode `json:"exitCodes"`
	Globals       []Option   `json:"globals"`
	Groups        []Group    `json:"groups"`
	Commands      []Command  `json:"commands"`
}

// ExitCode is one row of output.ExitCodes.
type ExitCode struct {
	Exit    int         `json:"exit"`
	Code    output.Code `json:"code"`
	Meaning string      `json:"meaning"`
}

// Group is a group node: a word that takes a command after it.
type Group struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// Command is one runnable command.
type Command struct {
	Path           string     `json:"path"`
	Summary        string     `json:"summary"`
	Description    *string    `json:"description"`
	OperationClass spec.Class `json:"operationClass"`
	Arguments      []Arg      `json:"arguments"`
	Options        []Option   `json:"options"`
	Examples       []string   `json:"examples"`
}

// Arg is one positional argument, in order.
type Arg struct {
	Name     string  `json:"name"`
	Help     string  `json:"help"`
	Format   *string `json:"format"`
	Required bool    `json:"required"`
	Repeat   bool    `json:"repeat"`
}

// Option is one option, spelled as typed: "--max-rows", "-o".
type Option struct {
	Name  string          `json:"name"`
	Short *string         `json:"short"`
	Type  spec.OptionType `json:"type"`
	Enum  []string        `json:"enum"`
	// Default is typed by Type: a bool, a number, a string, or an array of
	// strings for a list; null when the option has none.
	Default any `json:"default"`
	// Negation is a bool's --no-<name> spelling; null for every other type.
	Negation *string `json:"negation"`
	Env      *string `json:"env"`
	Help     string  `json:"help"`
}

// Build is the catalog of tree's commands under topic (nil: every command),
// leaving out hidden commands and everything under a hidden group. version
// is gossms's own (version.Version), so an agent can tell a cached catalog
// is stale.
func Build(tree *spec.Tree, version string, topic []string) *Schema {
	s := &Schema{
		SchemaVersion: output.SchemaVersion,
		GossmsVersion: version,
		Formats:       slices.Clone(output.Formats),
		ExitCodes:     []ExitCode{},
		Globals:       options(tree.Globals),
		Groups:        []Group{},
		Commands:      []Command{},
	}
	for _, e := range output.ExitCodes {
		s.ExitCodes = append(s.ExitCodes, ExitCode(e))
	}
	for _, c := range tree.Commands {
		if hidden(tree, c) || len(c.Path) < len(topic) || !slices.Equal(c.Path[:len(topic)], topic) {
			continue
		}
		if len(tree.Children(c.Path)) > 0 {
			s.Groups = append(s.Groups, Group{Name: c.Name(), Summary: c.Summary})
			continue
		}
		cmd := Command{
			Path:           c.Name(),
			Summary:        c.Summary,
			Description:    nullable(c.Description),
			OperationClass: c.Class,
			Arguments:      []Arg{},
			Options:        options(c.Options),
			Examples:       slices.Clone(c.Examples),
		}
		if cmd.Examples == nil {
			cmd.Examples = []string{}
		}
		for _, a := range c.Args {
			cmd.Arguments = append(cmd.Arguments, Arg{Name: a.Name, Help: a.Help, Format: nullable(a.Format),
				Required: a.Required, Repeat: a.Repeat})
		}
		s.Commands = append(s.Commands, cmd)
	}
	return s
}

// Write prints s as indented JSON with one trailing newline, as the envelope
// is.
func (s *Schema) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// hidden reports whether c or the group above it is hidden.
func hidden(tree *spec.Tree, c *spec.Command) bool {
	if c.Hidden {
		return true
	}
	if len(c.Path) > 1 {
		if g := tree.Lookup(c.Path[:1]); g != nil && g.Hidden {
			return true
		}
	}
	return false
}

func options(opts []spec.Option) []Option {
	out := []Option{}
	for _, o := range opts {
		opt := Option{
			Name:    "--" + o.Name,
			Type:    o.Type,
			Enum:    slices.Clone(o.Enum),
			Default: typedDefault(o),
			Env:     nullable(o.Env),
			Help:    o.Help,
		}
		if o.Short != "" {
			opt.Short = new("-" + o.Short)
		}
		if opt.Enum == nil {
			opt.Enum = []string{}
		}
		if o.Type == spec.TypeBool {
			opt.Negation = new("--no-" + o.Name)
		}
		out = append(out, opt)
	}
	return out
}

// typedDefault is o.Default as the JSON value of o's type. The parser applies
// a default by the same path as a typed value, so the spelling is valid for
// the type; one that is not stays a string rather than being lost.
func typedDefault(o spec.Option) any {
	switch o.Type {
	case spec.TypeBool:
		return o.Default == "true"
	case spec.TypeInt:
		if n, err := strconv.Atoi(o.Default); err == nil {
			return n
		}
	case spec.TypeList:
		if o.Default != "" {
			return strings.Split(o.Default, ",")
		}
	}
	if o.Default == "" {
		return nil
	}
	return o.Default
}

// nullable is nil for "", so JSON says null rather than an empty string.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
