package agent

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The hand-written sections (docs/cli.md § Agent guide). Text before a line
// "<!-- full -->" is in the brief guide too; text after it only in the full
// one.
//
//go:embed guide/*.md
var guideFiles embed.FS

const fullMarker = "<!-- full -->"

// Section is one section of the guide. Body is markdown, without the
// section's own heading.
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Guide is `agent guide`'s document: what Markdown, Text and WriteJSON print.
type Guide struct {
	SchemaVersion string    `json:"schemaVersion"`
	GossmsVersion string    `json:"gossmsVersion"`
	Title         string    `json:"title"`
	Brief         bool      `json:"brief"`
	Topic         *string   `json:"topic"`
	Sections      []Section `json:"sections"`
}

// sectionIDs is the guide's fixed section order (§8.2); each is also a
// --topic. The environment section (Environment) follows them, only when
// the guide was given a connection.
var sectionIDs = []string{"overview", "rules", "calling", "output", "exit-codes", "commands", "recipes"}

var sectionTitles = map[string]string{
	"overview":   "What gossms is",
	"rules":      "Rules you must follow",
	"calling":    "How to call it",
	"output":     "Output contract",
	"exit-codes": "Exit codes",
	"commands":   "Commands",
	"recipes":    "Recipes",
}

// Topics is every --topic value: the sections in order, then the tree's
// command groups.
func Topics(tree *spec.Tree) []string {
	topics := slices.Clone(sectionIDs)
	for _, c := range tree.Commands {
		if len(c.Path) == 1 && !c.Hidden && len(tree.Children(c.Path)) > 0 {
			topics = append(topics, c.Path[0])
		}
	}
	return topics
}

// BuildGuide is the guide for topic ("" for the whole guide), brief or full.
// Brief leaves out the recipes and every section's full-only text. Topic is
// one of Topics(tree); a group topic is the full detail of its commands,
// generated from the tree.
func BuildGuide(tree *spec.Tree, version, topic string, brief bool) *Guide {
	g := &Guide{SchemaVersion: output.SchemaVersion, GossmsVersion: version, Brief: brief, Sections: []Section{}}
	g.Title = "gossms — guide for AI agents"
	switch {
	case topic != "":
		g.Topic = &topic
		g.Title += ": " + topic
	case brief:
		g.Title += " (brief)"
	}
	if slices.Contains(sectionIDs, topic) || topic == "" {
		for _, id := range sectionIDs {
			if (topic == "" && !(brief && id == "recipes")) || id == topic {
				g.Sections = append(g.Sections, Section{ID: id, Title: sectionTitles[id], Body: sectionBody(tree, id, brief)})
			}
		}
		return g
	}
	if group := tree.Lookup([]string{topic}); group != nil {
		g.Sections = append(g.Sections, Section{ID: topic, Title: "The " + topic + " commands", Body: groupBody(tree, group, brief)})
	}
	return g
}

func sectionBody(tree *spec.Tree, id string, brief bool) string {
	switch id {
	case "exit-codes":
		return exitCodesBody(brief)
	case "commands":
		return commandsBody(tree, brief)
	}
	b, err := guideFiles.ReadFile("guide/" + id + ".md")
	if err != nil {
		panic(err) // every hand-written section is embedded; a test reads them all
	}
	text := string(b)
	if before, _, ok := strings.Cut(text, fullMarker+"\n"); ok {
		if brief {
			text = before
		} else {
			text = strings.Replace(text, fullMarker+"\n", "", 1)
		}
	}
	return strings.TrimSpace(text)
}

func exitCodesBody(brief bool) string {
	var b strings.Builder
	if brief {
		var parts []string
		for _, e := range output.ExitCodes {
			parts = append(parts, fmt.Sprintf("%d %s", e.Exit, e.Code))
		}
		b.WriteString(strings.Join(parts, " · "))
		return b.String()
	}
	b.WriteString("| Exit | Code | Meaning |\n|---|---|---|\n")
	for _, e := range output.ExitCodes {
		fmt.Fprintf(&b, "| %d | `%s` | %s |\n", e.Exit, e.Code, escapeCell(e.Meaning))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// commandsBody is the command index. The full guide has a row per runnable
// command: its operation class and what it returns. The brief one has a row
// per group, naming its commands, so it stays a system prompt's size as
// commands are added; a command whose class is not read is marked.
func commandsBody(tree *spec.Tree, brief bool) string {
	var b strings.Builder
	if brief {
		b.WriteString("| Group | Commands | Covers |\n|---|---|---|\n")
		for _, g := range tree.Children(nil) {
			if g.Hidden {
				continue
			}
			names := []string{g.Name()}
			if subs := runnable(tree, g.Path); len(subs) > 0 {
				names = names[:0]
				for _, c := range subs {
					name := strings.Join(c.Path[1:], " ")
					if c.Class != spec.ClassRead {
						name += " (" + string(c.Class) + ")"
					}
					names = append(names, name)
				}
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", g.Name(), escapeCell(strings.Join(names, ", ")), escapeCell(g.Summary))
		}
		b.WriteString("\nEvery command is read unless marked; `query run` and `plan actual` are gated by `--allow` on what their script does.\n")
		b.WriteString("Full guide: `gossms agent guide` · one group in detail: `gossms agent guide --topic <group>` · " +
			"every option as JSON: `gossms agent schema`.")
		return b.String()
	}
	b.WriteString("| Command | Class | Returns |\n|---|---|---|\n")
	for _, c := range runnable(tree, nil) {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", c.Name(), c.Class, escapeCell(c.Summary))
	}
	b.WriteString("\nThe class is what the command itself does; `query run` and `plan actual` are gated by `--allow` on what their script does.\n")
	b.WriteString("One group in detail: `gossms agent guide --topic <group>` · every option as JSON: `gossms agent schema`.")
	return b.String()
}

// runnable is every non-hidden command under path, at any depth, that is a
// command rather than a group, in tree order.
func runnable(tree *spec.Tree, path []string) []*spec.Command {
	var out []*spec.Command
	for _, c := range tree.Commands {
		if len(c.Path) > len(path) && slices.Equal(c.Path[:len(path)], path) &&
			!hidden(tree, c) && len(tree.Children(c.Path)) == 0 {
			out = append(out, c)
		}
	}
	return out
}

// groupBody is one group's commands in full: summary, description, usage,
// arguments, options and examples. The connection options every connecting
// command shares are named once, not per command.
func groupBody(tree *spec.Tree, group *spec.Command, brief bool) string {
	conn := connectionOptions(tree)
	var b strings.Builder
	b.WriteString(group.Summary + ".")
	if len(conn) > 0 {
		var names []string
		for _, o := range conn {
			names = append(names, "`"+optionSpelling(o)+"`")
		}
		b.WriteString("\n\nA command that connects also takes the connection options: " + strings.Join(names, ", ") +
			". `gossms <command> --help` describes them.")
	}
	for _, c := range runnable(tree, group.Path) {
		fmt.Fprintf(&b, "\n\n### %s\n\n", c.Name())
		if c.Description != "" && !brief {
			b.WriteString(c.Description)
		} else {
			b.WriteString(c.Summary + ".")
		}
		fmt.Fprintf(&b, "\n\nClass: %s", c.Class)
		if hasOption(c, "profile") || hasOption(c, "server") {
			b.WriteString(" · connects")
		}
		if hasOption(c, "allow") {
			b.WriteString(" · the script is checked against `--allow`")
		}
		b.WriteString("\n\n```text\n" + usageLine(c) + "\n```")
		if !brief {
			if len(c.Args) > 0 {
				b.WriteString("\n")
				for _, a := range c.Args {
					help := a.Help
					if a.Format != "" {
						help += " (" + a.Format + ")"
					}
					fmt.Fprintf(&b, "\n- `<%s>` — %s", a.Name, help)
				}
			}
			var own []spec.Option
			for _, o := range c.Options {
				if !slices.ContainsFunc(conn, func(x spec.Option) bool { return x.Name == o.Name }) {
					own = append(own, o)
				}
			}
			if len(own) > 0 {
				b.WriteString("\n")
				for _, o := range own {
					fmt.Fprintf(&b, "\n- `%s` — %s", optionSpelling(o), optionHelp(o))
				}
			}
		}
		if len(c.Examples) > 0 {
			b.WriteString("\n\n```bash\n" + strings.Join(c.Examples, "\n") + "\n```")
		}
	}
	return b.String()
}

// connectionOptions is the options every command with --profile has: the
// profile and the connection options. Derived from the tree, so a new
// connection option is named without a list to keep.
func connectionOptions(tree *spec.Tree) []spec.Option {
	var common []spec.Option
	first := true
	for _, c := range tree.Commands {
		if !hasOption(c, "profile") {
			continue
		}
		if first {
			common, first = slices.Clone(c.Options), false
			continue
		}
		common = slices.DeleteFunc(common, func(o spec.Option) bool { return !hasOption(c, o.Name) })
	}
	return common
}

func hasOption(c *spec.Command, name string) bool {
	return slices.ContainsFunc(c.Options, func(o spec.Option) bool { return o.Name == name })
}

func usageLine(c *spec.Command) string {
	s := "gossms " + c.Name()
	for _, a := range c.Args {
		w := "<" + a.Name + ">"
		if a.Repeat {
			w += "..."
		}
		if !a.Required {
			w = "[" + w + "]"
		}
		s += " " + w
	}
	if len(c.Options) > 0 {
		s += " [options]"
	}
	return s
}

// optionSpelling is how an option is typed: "-o, --output <a|b>".
func optionSpelling(o spec.Option) string {
	s := "--" + o.Name
	if o.Short != "" {
		s = "-" + o.Short + ", " + s
	}
	switch o.Type {
	case spec.TypeBool:
	case spec.TypeEnum:
		s += " <" + strings.Join(o.Enum, "|") + ">"
	case spec.TypeInt:
		s += " <n>"
	case spec.TypeList:
		if len(o.Enum) > 0 {
			s += " <" + strings.Join(o.Enum, "|") + ">,..."
		} else {
			s += " <a,b,...>"
		}
	default:
		s += " <value>"
	}
	return s
}

func optionHelp(o spec.Option) string {
	h := o.Help
	if o.Default != "" && o.Type != spec.TypeBool {
		h += " (default: " + o.Default + ")"
	}
	if o.Type == spec.TypeRepeat {
		h += " (repeatable)"
	}
	if o.Env != "" {
		h += " [env " + o.Env + "]"
	}
	return h
}

// EnvironmentID is the environment section's id.
const EnvironmentID = "environment"

// briefRows caps each table of the brief guide's environment section.
const briefRows = 15

// Environment is the "Your environment" section: res is `agent context`'s
// answer, its record as a list and each result set as a table, so the
// section says what that command says. A failed answer is its error, so the
// guide still prints with the reason the environment is missing.
func Environment(res *output.Result, profile string, brief bool) Section {
	s := Section{ID: EnvironmentID, Title: "Your environment"}
	if profile != "" {
		s.Title += " (profile: " + profile + ")"
	}
	var b strings.Builder
	if len(res.Errors) > 0 {
		e := res.Errors[0]
		fmt.Fprintf(&b, "Not available: `%s` — %s", e.Code, e.Message)
		if e.Hint != "" {
			b.WriteString("\n\n" + e.Hint)
		}
		s.Body = b.String()
		return s
	}
	for _, f := range res.Fields {
		v := f.Value
		switch {
		case f.Null:
			v = "NULL"
		case v == "":
			v = "(none)"
		}
		fmt.Fprintf(&b, "- %s: %s\n", f.Key, v)
	}
	for _, set := range res.Sets {
		fmt.Fprintf(&b, "\n### %s\n\n", set.Name)
		if len(set.Rows) == 0 {
			b.WriteString("(none)\n")
			continue
		}
		b.WriteString("|")
		for _, c := range set.Columns {
			b.WriteString(" " + c.Name + " |")
		}
		b.WriteString("\n|" + strings.Repeat("---|", len(set.Columns)) + "\n")
		rows := set.Rows
		if brief && len(rows) > briefRows {
			rows = rows[:briefRows]
		}
		for _, r := range rows {
			b.WriteString("|")
			for _, cell := range r {
				v := "NULL"
				if cell != nil {
					v = escapeCell(*cell)
				}
				b.WriteString(" " + v + " |")
			}
			b.WriteString("\n")
		}
		if len(rows) < len(set.Rows) {
			fmt.Fprintf(&b, "\n%d more: `gossms agent context`\n", len(set.Rows)-len(rows))
		}
	}
	if len(res.Warnings) > 0 {
		b.WriteString("\nWarnings:\n\n")
		for _, w := range res.Warnings {
			b.WriteString("- " + w + "\n")
		}
	}
	s.Body = strings.TrimSpace(b.String())
	return s
}

// escapeCell makes s safe inside a markdown table cell.
func escapeCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// Markdown is the guide as markdown: the title, the version line, then each
// section under a level-2 heading (a topic's single section included).
func (g *Guide) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\ngossms %s · CLI schema %s\n", g.Title, g.GossmsVersion, g.SchemaVersion)
	for _, s := range g.Sections {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", s.Title, s.Body)
	}
	return b.String()
}

// Text is the guide as plain lines: the same content with the markdown
// markup taken out (plain.go).
func (g *Guide) Text() string { return Plain(g.Markdown()) }

// WriteJSON prints g as indented JSON, as the schema is.
func (g *Guide) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(g)
}
