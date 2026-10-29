package cli

import (
	"context"

	"github.com/radix29/gossms/internal/cli/agent"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/version"
)

// commandTree assembles the command line. An option or command is here only
// once it works: connection and safety globals arrive with the phases that
// implement them, and --output lists only the formats a renderer exists for.
//
// help, agent guide and agent schema have no Run: Main answers them (and
// every --help) itself, since help, the guide and the catalog are printed
// outside the envelope. The guide's environment section runs agent
// context's.
func commandTree() *spec.Tree {
	return newCommandTree(cmd.Deps{LoadConfig: config.LoadReadOnly, Connect: db.Connect, OpenSession: cmd.OpenQuerySession,
		Catalog: cmd.ServerCatalog})
}

// newCommandTree is commandTree over d, so tests can stand in for the config
// directory and the server.
func newCommandTree(d cmd.Deps) *spec.Tree {
	tree := &spec.Tree{
		Globals: []spec.Option{
			{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: output.Formats, Default: "text",
				Help: "Output format", Global: true},
			{Name: "quiet", Short: "q", Type: spec.TypeBool,
				Help: "Results only, no envelope header", Global: true},
			// No short alias: -v is --version, handled before the command line.
			{Name: "verbose", Type: spec.TypeBool,
				Help: "Diagnostic logging to stderr", Global: true},
			// Accepted so scripts can pass it today; nothing is coloured yet.
			{Name: "no-color", Type: spec.TypeBool,
				Help: "Never colour the output", Global: true},
			{Name: "help", Short: "h", Type: spec.TypeBool,
				Help: "Show help for the command", Global: true},
		},
	}
	tree.Globals = append(tree.Globals, outputOptions()...)
	tree.Commands = []*spec.Command{
		{
			Path:        []string{"version"},
			Summary:     "Show the gossms version and the command-line schema version",
			Description: "Prints the build metadata `gossms --version` shows, plus the schema version of this command line's output.",
			Class:       spec.ClassRead,
			Run:         runVersion,
			Examples: []string{
				"gossms version",
				"gossms version -o json",
			},
		},
		{
			Path:        []string{"help"},
			Summary:     "Show help for gossms, a command group or a command",
			Description: "With no argument, lists the command groups. With a group, lists its commands; with a group and a command, shows that command's usage, options and examples, as `<command> --help` does.",
			Args: []spec.Arg{
				{Name: "group", Help: "A command group, or a depth-1 command such as version"},
				{Name: "command", Help: "A command in the group"},
			},
			Class: spec.ClassRead,
			Examples: []string{
				"gossms help",
				"gossms help version",
			},
		},
		{
			Path:    []string{"agent"},
			Summary: "Describe gossms to an AI agent",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"agent", "guide"},
			Summary: "Print the usage manual for AI agents: rules, call pattern, output contract, exit codes, commands, recipes",
			Description: "What an agent reads before its first call: what gossms is, the rules it must follow, how to " +
				"call it, the output contract, the exit codes, the command index and recipes. The command index and the " +
				"group topics are generated from the command tree; the rules and recipes are hand-written. It is " +
				"printed outside the envelope: markdown by default, plain lines with -o text, and a JSON document " +
				"of the same sections with -o json; any other format is refused. --brief is the condensed version " +
				"for a system prompt. With a profile (or --server) it connects and adds \"Your environment\", the " +
				"answer `gossms agent context` gives for the same connection, as the last section.",
			// The connection options add `agent context`'s answer as the last
			// section, "Your environment".
			Options: append([]spec.Option{
				{Name: "brief", Type: spec.TypeBool,
					Help: "The condensed guide for a system prompt: no recipes, the short form of each section"},
				// Enum filled in below, once the groups are in the tree.
				{Name: "topic", Type: spec.TypeEnum,
					Help: "One section, or one command group's commands in full detail"},
			}, cmd.ConnectOptions()...),
			Class: spec.ClassRead,
			Examples: []string{
				"gossms agent guide",
				"gossms agent guide --brief -p dev",
				"gossms agent guide --topic rules",
				"gossms agent guide --topic object -o json",
			},
		},
		{
			Path:    []string{"agent", "schema"},
			Summary: "Print the JSON catalog of every command, argument, option, exit code and output format",
			Description: "The machine-readable catalog an agent builds tool definitions from and checks a call " +
				"against: each command's path, summary, operation class, arguments, options (type, enum, typed " +
				"default, environment variable) and examples, plus the global options, the exit codes and the " +
				"--output formats. It is JSON whatever --output says (text or json; any other format is refused), " +
				"printed outside the envelope, and the same document `gossms help -o json` prints. A group, or a " +
				"group and a command, narrows it to the commands under them.",
			Args: []spec.Arg{
				{Name: "group", Help: "A command group, or a depth-1 command such as version"},
				{Name: "command", Help: "A command in the group"},
			},
			Class: spec.ClassRead,
			Examples: []string{
				"gossms agent schema",
				"gossms agent schema object",
				"gossms agent schema query run",
			},
		},
	}
	tree.Commands = append(tree.Commands, cmd.AgentContextCommand(d))
	tree.Commands = append(tree.Commands, cmd.ProfileCommands(d)...)
	tree.Commands = append(tree.Commands, cmd.ServerCommands(d)...)
	tree.Commands = append(tree.Commands, cmd.DBCommands(d)...)
	tree.Commands = append(tree.Commands, cmd.ObjectCommands(d)...)
	tree.Commands = append(tree.Commands, cmd.QueryCommands(d)...)
	guideCommand(tree).Options[1].Enum = agent.Topics(tree)
	return tree
}

// runVersion is `gossms version`: the fields `gossms --version` prints, keyed
// for JSON, plus the envelope's schema version.
func runVersion(context.Context, *spec.Invocation) (*output.Result, error) {
	var fields []output.Field
	for _, f := range version.Fields() {
		fields = append(fields, output.Field{Key: f.Key, Value: f.Value})
	}
	fields = append(fields, output.Field{Key: "cliSchema", Value: output.SchemaVersion})
	return &output.Result{Fields: fields}, nil
}
