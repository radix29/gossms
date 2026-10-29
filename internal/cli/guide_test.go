package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/agent"
)

// guideRuns is every guide the command line can print: each topic and the
// whole guide, brief and full.
func guideRuns(t *testing.T) map[string]runResult {
	t.Helper()
	runs := map[string]runResult{}
	for _, topic := range append([]string{""}, agent.Topics(commandTree())...) {
		for _, brief := range []bool{false, true} {
			argv := []string{"agent", "guide"}
			if topic != "" {
				argv = append(argv, "--topic", topic)
			}
			if brief {
				argv = append(argv, "--brief")
			}
			r := runMain(t, argv...)
			if r.exit != 0 || r.stderr != "" {
				t.Fatalf("%v: exit %d\n%s%s", argv, r.exit, r.stdout, r.stderr)
			}
			runs[strings.Join(argv, " ")] = r
		}
	}
	return runs
}

// TestAgentGuideMentionsParse is 3.h's drift guard: every backticked
// `gossms …` in every rendered guide, and every line of a code block that
// starts "gossms ", names a command the tree has and parses — so the
// hand-written rules and recipes cannot point at a command that does not
// exist.
func TestAgentGuideMentionsParse(t *testing.T) {
	tree := commandTree()
	lines := 0
	for name, r := range guideRuns(t) {
		checkMentions(t, tree, name, r.stdout)
		inCode := false
		for _, line := range strings.Split(r.stdout, "\n") {
			if strings.HasPrefix(line, "```") {
				inCode = !inCode
				continue
			}
			if inCode && strings.HasPrefix(line, "gossms ") {
				lines++
				if err := checkCommandLine(tree, line); err != "" {
					t.Errorf("%s: code line %q %s", name, line, err)
				}
			}
		}
	}
	if lines < 20 {
		t.Errorf("only %d code lines checked: the scan is not reading the code blocks", lines)
	}
}

func TestAgentGuideBriefFitsASystemPrompt(t *testing.T) {
	// §8.1: about 1,500 tokens.
	if r := runMain(t, "agent", "guide", "--brief"); len(r.stdout) >= 6000 {
		t.Errorf("brief guide is %d bytes, want under 6000", len(r.stdout))
	}
}

func TestAgentGuideSections(t *testing.T) {
	headings := func(md string) []string {
		var hs []string
		for _, l := range strings.Split(md, "\n") {
			if h, ok := strings.CutPrefix(l, "## "); ok {
				hs = append(hs, h)
			}
		}
		return hs
	}
	full := runMain(t, "agent", "guide")
	want := []string{"What gossms is", "Rules you must follow", "How to call it", "Output contract", "Exit codes", "Commands", "Recipes"}
	if got := headings(full.stdout); !slices.Equal(got, want) {
		t.Errorf("full guide sections = %q, want %q", got, want)
	}
	if !strings.HasPrefix(full.stdout, "# gossms — guide for AI agents\n\ngossms ") || !strings.Contains(full.stdout, " · CLI schema 1.0\n") {
		t.Errorf("guide does not start with its title and version line:\n%.200s", full.stdout)
	}
	brief := runMain(t, "agent", "guide", "--brief")
	if got := headings(brief.stdout); !slices.Equal(got, want[:6]) {
		t.Errorf("brief guide sections = %q, want %q", got, want[:6])
	}
	if strings.Contains(brief.stdout, "<!--") || strings.Contains(full.stdout, "<!--") {
		t.Error("a <!-- full --> marker leaked into the guide")
	}
	if !strings.Contains(full.stdout, "@args.txt") || strings.Contains(brief.stdout, "@args.txt") {
		t.Error("the full-only text of a section is not split from its brief text")
	}

	// The full command index is every runnable, non-hidden command; the
	// brief one is a row per group naming each of them, a sub-group's
	// commands included.
	tree := commandTree()
	for _, c := range tree.Commands {
		if c.Hidden || len(tree.Children(c.Path)) > 0 {
			continue
		}
		if !strings.Contains(full.stdout, "| "+c.Name()+" | ") {
			t.Errorf("full command index lacks %q", c.Name())
		}
		row := ""
		for l := range strings.Lines(brief.stdout) {
			if strings.HasPrefix(l, "| "+c.Path[0]+" | ") {
				row = l
			}
		}
		if name := strings.Join(c.Path[1:], " "); row == "" || name != "" && !strings.Contains(row, " "+name+",") && !strings.Contains(row, " "+name+" |") {
			t.Errorf("brief command index lacks %q (row %q)", c.Name(), row)
		}
	}
	if strings.Contains(brief.stdout, "| server config |") || strings.Contains(brief.stdout, "| Command | Class |") {
		t.Error("the brief command index is not one row per group")
	}
	// A sub-group's commands are in its group's topic.
	server := runMain(t, "agent", "guide", "--topic", "server")
	for _, name := range []string{"server config list", "server config get"} {
		if !strings.Contains(server.stdout, "\n### "+name+"\n") {
			t.Errorf("--topic server lacks %q", name)
		}
	}
	if strings.Contains(server.stdout, "\n### server config\n") {
		t.Error("--topic server lists the server config sub-group as a command")
	}
	// A group topic is each of the group's commands, with its examples.
	obj := runMain(t, "agent", "guide", "--topic", "object")
	for _, c := range commandTree().Children([]string{"object"}) {
		if !strings.Contains(obj.stdout, "\n### "+c.Name()+"\n") || !strings.Contains(obj.stdout, c.Examples[0]+"\n") {
			t.Errorf("--topic object lacks %q or its example", c.Name())
		}
	}
	if got := headings(obj.stdout); !slices.Equal(got, []string{"The object commands"}) {
		t.Errorf("--topic object sections = %q", got)
	}
	if got := headings(runMain(t, "agent", "guide", "--topic", "rules").stdout); !slices.Equal(got, []string{"Rules you must follow"}) {
		t.Errorf("--topic rules sections = %q", got)
	}
}

func TestAgentGuideFormats(t *testing.T) {
	md := runMain(t, "agent", "guide")
	if r := runMain(t, "agent", "guide", "-o", "markdown"); r.stdout != md.stdout {
		t.Error("-o markdown differs from the default")
	}
	text := runMain(t, "agent", "guide", "-o", "text")
	if text.exit != 0 || !strings.HasPrefix(text.stdout, "gossms — guide for AI agents\n====") {
		t.Fatalf("-o text: exit %d\n%.200s", text.exit, text.stdout)
	}
	for _, mark := range []string{"`", "```", "\n## ", "|---", "| Exit |"} {
		if strings.Contains(text.stdout, mark) {
			t.Errorf("-o text still holds markdown %q", mark)
		}
	}
	if !strings.Contains(text.stdout, "\n130   CANCELLED          Ctrl+C or SIGINT\n") {
		t.Error("-o text: the exit-code table is not aligned columns")
	}

	js := envelope(t, runMain(t, "agent", "guide", "--brief", "-o", "json").stdout)
	if js["brief"] != true || js["topic"] != nil || js["schemaVersion"] != "1.0" {
		t.Errorf("json head: %v", js)
	}
	var ids []string
	for _, s := range js["sections"].([]any) {
		sec := s.(map[string]any)
		ids = append(ids, sec["id"].(string))
		if sec["title"] == "" || sec["body"] == "" {
			t.Errorf("json section %v is empty", sec)
		}
	}
	if want := []string{"overview", "rules", "calling", "output", "exit-codes", "commands"}; !slices.Equal(ids, want) {
		t.Errorf("json sections = %q, want %q", ids, want)
	}

	for _, f := range []string{"jsonl", "csv", "tsv"} {
		if r := runMain(t, "agent", "guide", "-o", f); r.exit != 2 || !strings.Contains(r.stdout+r.stderr, "USAGE_ERROR") {
			t.Errorf("-o %s: exit %d, want 2", f, r.exit)
		}
	}
	if r := runMain(t, "agent", "guide", "--topic", "objct"); r.exit != 2 || !strings.Contains(r.stdout, "Did you mean object?") {
		t.Errorf("--topic objct: exit %d\n%s", r.exit, r.stdout)
	}
}
