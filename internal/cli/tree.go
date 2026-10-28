package cli

import "github.com/radix29/gossms/internal/cli/spec"

// commandTree assembles the command line. An option or command is here only
// once it works: connection and safety globals arrive with the phases that
// implement them, and --output lists only the formats a renderer exists for.
//
// The handlers are wired in phase 0.f; until then Run is nil.
func commandTree() *spec.Tree {
	return &spec.Tree{
		Globals: []spec.Option{
			{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: []string{"text", "json"}, Default: "text",
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
		Commands: []*spec.Command{
			{
				Path:        []string{"version"},
				Summary:     "Show the gossms version and the command-line schema version",
				Description: "Prints the build metadata `gossms --version` shows, plus the schema version of this command line's output.",
				Class:       spec.ClassRead,
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
		},
	}
}
