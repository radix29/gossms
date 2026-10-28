// Package spec is the command line's command tree as data: each command's
// path, help, arguments, options, operation class, examples and handler. The
// parser, --help and (later) `agent schema` all read the same values, so none
// of them can drift from the others.
//
// spec holds types, and lookups over them, and imports no handler package:
// the tree itself is assembled in internal/cli (tree.go), with no init()
// registration, so a command package can import spec without a cycle.
package spec

import (
	"context"
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/cli/output"
)

// Class is a command's operation class — what the safety model gates it on.
// The values are the schema's spelling.
type Class string

const (
	ClassRead  Class = "read"
	ClassWrite Class = "write"
	ClassDDL   Class = "ddl"
	ClassAdmin Class = "admin"
)

// OptionType is how an option's value is parsed. The values are the schema's
// spelling.
type OptionType string

const (
	TypeBool   OptionType = "bool"   // a flag; --no-<name> sets it false
	TypeString OptionType = "string" // any text
	TypeInt    OptionType = "int"    // a base-10 integer
	TypeEnum   OptionType = "enum"   // one of Option.Enum
	TypeList   OptionType = "list"   // repeated and/or comma-separated text
)

// Tree is the whole command line: the global options every command accepts,
// and the commands.
type Tree struct {
	Globals  []Option // each has Global set
	Commands []*Command
}

// Command is one node of the tree. Path is its words after "gossms":
// {"version"} for a depth-1 leaf, {"<group>", "<command>"} for a depth-2 one,
// {"<group>"} for a group node.
//
// A group node has a nil Run; given alone on the command line it is a usage
// error (exit 2) listing the commands under it.
type Command struct {
	Path        []string
	Summary     string // one line, no trailing period: help listings and the schema
	Description string // the paragraph(s) `<command> --help` shows under the usage line
	Args        []Arg
	Options     []Option // the command's own; Tree.Globals are added by the parser
	Class       Class
	Examples    []string // full command lines, each starting "gossms "; every one must parse
	Hidden      bool     // parsed and runnable, but left out of help and the schema
	Run         Handler
}

// Arg is one positional argument, in order.
type Arg struct {
	Name     string // the <name> in the usage line
	Help     string
	Format   string // a hint such as "schema.name"; empty when free text
	Required bool
	Repeat   bool // takes every remaining positional token; last Arg only
}

// Option is one --name option.
type Option struct {
	Name  string // long name without the leading "--"
	Short string // one letter without the leading "-"; empty for none
	Type  OptionType
	Enum  []string // TypeEnum's values, in the order help lists them
	// Default is the value when the option is absent, spelled as on the
	// command line, so the parser applies it by the same path as a typed
	// value; empty means none (false for a bool).
	Default string
	Env     string // environment variable read when the option is absent; empty for none
	Help    string
	Global  bool // a Tree.Globals entry, accepted by every command
}

// Handler runs a parsed command.
type Handler func(ctx context.Context, inv *Invocation) (*output.Result, error)

// Invocation is one parsed command line, as the parser hands it to a Handler.
type Invocation struct {
	Command *Command
	Args    []string // positional arguments, in Command.Args order
	// Values holds every option of Command.Options and Tree.Globals by long
	// name, after defaults and environment variables, typed by Option.Type:
	// bool, string (string and enum), int, []string (list).
	Values map[string]any
}

// Lookup is the command whose Path is path, or nil.
func (t *Tree) Lookup(path []string) *Command {
	for _, c := range t.Commands {
		if slices.Equal(c.Path, path) {
			return c
		}
	}
	return nil
}

// Children is every command one level below path (nil path: the top level),
// in tree order. A command with children is a group node.
func (t *Tree) Children(path []string) []*Command {
	var out []*Command
	for _, c := range t.Commands {
		if len(c.Path) == len(path)+1 && slices.Equal(c.Path[:len(path)], path) {
			out = append(out, c)
		}
	}
	return out
}

// Name is the command's path as typed: "version", "object list".
func (c *Command) Name() string { return strings.Join(c.Path, " ") }
