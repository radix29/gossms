package cli

import (
	"fmt"
	"strings"

	"github.com/clipperhouse/displaywidth"

	"github.com/radix29/gossms/internal/cli/agent"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/version"
)

// agentLine is the first line of the top-level help: the pointer for AI
// agents (docs/cli.md § Help).
const agentLine = "AI agents: run `gossms agent guide` for complete usage instructions."

// showHelp writes the help for cmd (nil: the top level) to stdout: plain text
// for people, outside the envelope. invoked is the command line's command, for
// the envelope of a refusal.
//
// Help as JSON is the command schema (`agent schema`) of the same topic; the
// other formats are refused.
func showHelp(env Env, tree *spec.Tree, format output.Format, invoked string, cmd *spec.Command) int {
	if format == output.FormatJSON {
		var topic []string
		if cmd != nil {
			topic = cmd.Path
		}
		return writeSchema(env, tree, topic)
	}
	if format != output.FormatText {
		return finish(env, &output.Result{Command: invoked, Errors: []*output.Error{output.NewError(output.CodeUsageError,
			"help is available as text or json only", "Run the same command without --output, or with -o json for the command schema.")}}, output.DefaultOptions(format), nil)
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

// showSchema is `agent schema [<group> [<command>]]`: the catalog as JSON,
// outside the envelope, for text (the default) or json; any other format is
// refused rather than ignored.
func showSchema(env Env, tree *spec.Tree, format output.Format, o output.Options, inv *spec.Invocation) int {
	refuse := func(e *output.Error) int {
		return finish(env, &output.Result{Command: inv.Command.Name(), Errors: []*output.Error{e}}, o, nil)
	}
	if format != output.FormatText && format != output.FormatJSON {
		return refuse(output.NewError(output.CodeUsageError, "agent schema is printed as json only",
			"Run the same command without --output, or with -o json."))
	}
	if len(inv.Args) > 0 && tree.Lookup(inv.Args) == nil {
		return refuse(unknownTopic(tree, inv.Command, inv.Args))
	}
	return writeSchema(env, tree, inv.Args)
}

// showGuide is `agent guide [--brief] [--topic <t>]`: the manual, outside
// the envelope. Markdown unless --output is given (D6): -o text is the same
// content as plain lines, -o markdown markdown, -o json the sections as a
// JSON document; the row formats are refused.
func showGuide(env Env, tree *spec.Tree, format output.Format, o output.Options, inv *spec.Invocation) int {
	if !inv.Given["output"] {
		format = output.FormatMarkdown
	}
	g := agent.BuildGuide(tree, version.Version, inv.Values["topic"].(string), inv.Values["brief"].(bool))
	var err error
	switch format {
	case output.FormatMarkdown:
		_, err = fmt.Fprint(env.Stdout, g.Markdown())
	case output.FormatText:
		_, err = fmt.Fprint(env.Stdout, g.Text())
	case output.FormatJSON:
		err = g.WriteJSON(env.Stdout)
	default:
		return finish(env, &output.Result{Command: inv.Command.Name(), Errors: []*output.Error{output.NewError(output.CodeUsageError,
			"agent guide is printed as markdown, text or json only", "Run the same command without --output.")}}, o, nil)
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "gossms: %s: writing output: %v\n", output.CodeGeneralError, err)
		return 1
	}
	return 0
}

// writeSchema prints the catalog of the commands under topic (nil: all).
func writeSchema(env Env, tree *spec.Tree, topic []string) int {
	if err := agent.Build(tree, version.Version, topic).Write(env.Stdout); err != nil {
		fmt.Fprintf(env.Stderr, "gossms: %s: writing output: %v\n", output.CodeGeneralError, err)
		return 1
	}
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
			if len(o.Enum) > 0 {
				left += " <" + strings.Join(o.Enum, "|") + ">,..."
			} else {
				left += " <a,b,...>"
			}
		default:
			left += " <value>"
		}
		help := o.Help
		if o.Default != "" && o.Type != spec.TypeBool {
			help += " (default: " + o.Default + ")"
		}
		if o.Type == spec.TypeRepeat {
			help += " (repeatable)"
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

// guideCommand is the tree's `agent guide` command, which Main answers
// itself: the guide is printed outside the envelope.
func guideCommand(tree *spec.Tree) *spec.Command { return tree.Lookup([]string{"agent", "guide"}) }

// schemaCommand is the tree's `agent schema` command, which Main answers
// itself: the catalog is printed outside the envelope.
func schemaCommand(tree *spec.Tree) *spec.Command { return tree.Lookup([]string{"agent", "schema"}) }

// unknownTopic is the usage error for `gossms help <words>` (or `agent schema
// <words>`, the invoked command) naming no command.
func unknownTopic(tree *spec.Tree, invoked *spec.Command, words []string) *output.Error {
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
	hint := fmt.Sprintf("Run `gossms %s` for the command list.", invoked.Name())
	if s := args.Suggest(word, names); s != "" {
		hint = fmt.Sprintf("Did you mean `gossms %s %s`?", invoked.Name(), strings.Join(append(append([]string{}, parent...), s), " "))
	}
	return output.NewError(output.CodeUsageError,
		fmt.Sprintf("no %s topic %q", invoked.Name(), strings.Join(words, " ")), hint)
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
