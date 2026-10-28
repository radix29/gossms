package cli

import (
	"fmt"
	"strings"

	"github.com/clipperhouse/displaywidth"

	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// agentLine is the first line of the top-level help. It names `gossms help`
// until `agent guide` exists (docs/cli.md § Help).
const agentLine = "AI agents: run `gossms help <command>` for any command's usage, options and examples."

// showHelp writes the help for cmd (nil: the top level) to stdout: plain text
// for people, outside the envelope. invoked is the command line's command, for
// the envelope of a refusal.
//
// Help as JSON is the command schema, which arrives with `agent schema`; until
// then it is refused rather than emitted as a partial catalog an agent would
// trust.
func showHelp(env Env, tree *spec.Tree, format output.Format, invoked string, cmd *spec.Command) int {
	if format != output.FormatText {
		return finish(env, &output.Result{Command: invoked, Errors: []*output.Error{output.NewError(output.CodeUsageError,
			"help is available as text only", "Run the same command without --output.")}}, format, false)
	}
	var text string
	switch {
	case cmd == nil:
		text = topHelp(tree)
	case len(tree.Children(cmd.Path)) > 0:
		text = groupHelp(tree, cmd)
	default:
		text = commandHelp(tree, cmd)
	}
	fmt.Fprint(env.Stdout, text)
	return 0
}

func topHelp(tree *spec.Tree) string {
	var b strings.Builder
	b.WriteString(agentLine + "\n\n")
	b.WriteString("Usage:\n")
	b.WriteString("  gossms <command> [arguments] [options]\n")
	b.WriteString("  gossms              with no arguments, starts the interactive UI (needs a terminal)\n\n")
	b.WriteString("Commands:\n")
	writeCommands(&b, tree.Children(nil))
	b.WriteString("\nGlobal options:\n")
	writeOptions(&b, tree.Globals)
	b.WriteString("\nRun `gossms help <command>` for a command's options and examples.\n")
	return b.String()
}

func groupHelp(tree *spec.Tree, group *spec.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: gossms %s <command> [arguments] [options]\n\n", group.Name())
	writeParagraph(&b, group)
	b.WriteString("Commands:\n")
	writeCommands(&b, tree.Children(group.Path))
	fmt.Fprintf(&b, "\nRun `gossms help %s <command>` for a command's options and examples.\n", group.Name())
	return b.String()
}

func commandHelp(tree *spec.Tree, cmd *spec.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: gossms %s", cmd.Name())
	for _, a := range cmd.Args {
		s := "<" + a.Name + ">"
		if a.Repeat {
			s += "..."
		}
		if !a.Required {
			s = "[" + s + "]"
		}
		b.WriteString(" " + s)
	}
	b.WriteString(" [options]\n\n")
	writeParagraph(&b, cmd)
	if len(cmd.Args) > 0 {
		b.WriteString("Arguments:\n")
		rows := make([][2]string, len(cmd.Args))
		for i, a := range cmd.Args {
			help := a.Help
			if a.Format != "" {
				help += " (" + a.Format + ")"
			}
			rows[i] = [2]string{"<" + a.Name + ">", help}
		}
		writeRows(&b, rows)
		b.WriteString("\n")
	}
	if len(cmd.Options) > 0 {
		b.WriteString("Options:\n")
		writeOptions(&b, cmd.Options)
		b.WriteString("\n")
	}
	b.WriteString("Global options:\n")
	writeOptions(&b, tree.Globals)
	if len(cmd.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range cmd.Examples {
			b.WriteString("  " + ex + "\n")
		}
	}
	return b.String()
}

// writeParagraph is the command's Description, else its Summary, and a blank
// line.
func writeParagraph(b *strings.Builder, cmd *spec.Command) {
	text := cmd.Description
	if text == "" {
		text = cmd.Summary + "."
	}
	b.WriteString(text + "\n\n")
}

func writeCommands(b *strings.Builder, cmds []*spec.Command) {
	var rows [][2]string
	for _, c := range cmds {
		if !c.Hidden {
			rows = append(rows, [2]string{c.Path[len(c.Path)-1], c.Summary})
		}
	}
	writeRows(b, rows)
}

func writeOptions(b *strings.Builder, opts []spec.Option) {
	rows := make([][2]string, len(opts))
	for i, o := range opts {
		left := "    "
		if o.Short != "" {
			left = "-" + o.Short + ", "
		}
		left += "--" + o.Name
		switch o.Type {
		case spec.TypeBool:
		case spec.TypeEnum:
			left += " <" + strings.Join(o.Enum, "|") + ">"
		case spec.TypeInt:
			left += " <n>"
		case spec.TypeList:
			left += " <a,b,...>"
		default:
			left += " <value>"
		}
		help := o.Help
		if o.Default != "" && o.Type != spec.TypeBool {
			help += " (default: " + o.Default + ")"
		}
		if o.Env != "" {
			help += " [env " + o.Env + "]"
		}
		rows[i] = [2]string{left, help}
	}
	writeRows(b, rows)
}

// writeRows writes two aligned columns, indented two spaces.
func writeRows(b *strings.Builder, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, displaywidth.String(r[0]))
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", width-displaywidth.String(r[0])+2)
		b.WriteString("  " + r[0] + pad + r[1] + "\n")
	}
}

// helpCommand is the tree's `help` command, which Main answers itself: help is
// text for people, not a result in the envelope.
func helpCommand(tree *spec.Tree) *spec.Command { return tree.Lookup([]string{"help"}) }

// unknownHelpTopic is the usage error for `gossms help <words>` naming no
// command.
func unknownHelpTopic(tree *spec.Tree, words []string) *output.Error {
	// The deepest existing prefix is where the suggestion comes from.
	parent := words[:0]
	for len(parent) < len(words)-1 && tree.Lookup(words[:len(parent)+1]) != nil {
		parent = words[:len(parent)+1]
	}
	word := words[len(parent)]
	var names []string
	for _, c := range tree.Children(parent) {
		names = append(names, c.Path[len(parent)])
	}
	hint := "Run `gossms help` for the command list."
	if s := args.Suggest(word, names); s != "" {
		hint = fmt.Sprintf("Did you mean `gossms help %s`?", strings.Join(append(append([]string{}, parent...), s), " "))
	}
	return output.NewError(output.CodeUsageError,
		fmt.Sprintf("no help topic %q", strings.Join(words, " ")), hint)
}

// commandNames is the last path word of each command, comma-separated.
func commandNames(cmds []*spec.Command) string {
	var names []string
	for _, c := range cmds {
		if !c.Hidden {
			names = append(names, c.Path[len(c.Path)-1])
		}
	}
	return strings.Join(names, ", ")
}
