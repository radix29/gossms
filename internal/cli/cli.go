// Package cli is gossms's command-line mode: `gossms <command> …` runs one
// command and exits, and never starts the TUI. cmd/gossms dispatches here for
// any argument other than the version flags.
//
// The package does no os.* I/O of its own: everything arrives through Env, so
// every test runs in-process.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// Env is the process environment Main runs against.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// ReadFile reads an @file response file (os.ReadFile). Nil refuses them.
	ReadFile func(string) ([]byte, error)
}

// Main runs the command line args (os.Args[1:]) and returns the process exit
// code: the result goes to stdout in the --output format, and an error also
// gets one line on stderr.
func Main(ctx context.Context, argv []string, env Env) int {
	return run(ctx, commandTree(), argv, env)
}

// run is Main over any tree, so tests can add commands (a panicking handler).
func run(ctx context.Context, tree *spec.Tree, argv []string, env Env) int {
	// Packages below cmd log.Printf, and log's default output is stderr: that
	// chatter would land between the result and the summary line. The command
	// line never opens the TUI's log file (two processes would share it).
	verbose := hasFlag(argv, "--verbose")
	if verbose {
		log.SetOutput(env.Stderr)
	} else {
		log.SetOutput(io.Discard)
	}

	// Scanned before parsing, so a usage error is rendered as JSON when asked.
	format := scanOutput(argv)
	inv, err := args.Parse(tree, argv, args.Options{ReadFile: env.ReadFile, Getenv: env.Getenv})
	if err != nil {
		perr, _ := errors.AsType[*args.Error](err)
		if perr != nil && perr.Kind == args.KindNoCommand && hasFlag(argv, "--help", "-h") {
			return showHelp(env, tree, format, "", nil)
		}
		res := &output.Result{Errors: []*output.Error{output.NewError(output.CodeUsageError, err.Error(), "")}}
		if perr != nil {
			res.Errors[0].Hint = perr.Hint
			if perr.Command != nil {
				res.Command = perr.Command.Name()
			}
		}
		return finish(env, res, output.DefaultOptions(format), nil)
	}
	inv.Stdin, inv.Getenv, inv.ReadFile = env.Stdin, env.Getenv, env.ReadFile
	format = output.Format(inv.Values["output"].(string))
	cmd := inv.Command
	o, oerr := renderOptions(inv)
	if oerr != nil {
		o = output.DefaultOptions(format)
	}
	o.Quiet = inv.Values["quiet"].(bool)

	if inv.Values[args.HelpOption].(bool) {
		return showHelp(env, tree, format, cmd.Name(), cmd)
	}
	if cmd == helpCommand(tree) {
		if len(inv.Args) == 0 {
			return showHelp(env, tree, format, cmd.Name(), nil)
		}
		target := tree.Lookup(inv.Args)
		if target == nil {
			return finish(env, &output.Result{Command: cmd.Name(), Errors: []*output.Error{unknownTopic(tree, cmd, inv.Args)}}, o, nil)
		}
		return showHelp(env, tree, format, cmd.Name(), target)
	}
	if cmd == guideCommand(tree) {
		return showGuide(ctx, env, tree, format, o, inv)
	}
	if cmd == schemaCommand(tree) {
		return showSchema(env, tree, format, o, inv)
	}
	if cmd.Run == nil {
		e := output.NewError(output.CodeGeneralError, cmd.Name()+": not implemented", "")
		if subs := tree.Children(cmd.Path); len(subs) > 0 {
			e = output.NewError(output.CodeUsageError, cmd.Name()+": a command is required",
				fmt.Sprintf("Commands: %s. Run `gossms help %s` for usage.", commandNames(subs), cmd.Name()))
		}
		return finish(env, &output.Result{Command: cmd.Name(), Errors: []*output.Error{e}}, o, nil)
	}
	if oerr != nil {
		return finish(env, &output.Result{Command: cmd.Name(), Errors: []*output.Error{oerr.(*output.Error)}}, o, nil)
	}
	// The Sink exists before the handler runs: an --out-file that cannot be
	// written fails before anything connects.
	sink, err := output.NewSink(env.Stdout, o)
	if err != nil {
		return finish(env, &output.Result{Command: cmd.Name(), Errors: []*output.Error{err.(*output.Error)}}, o, nil)
	}
	inv.Sink = sink

	// Ctrl+C (os.Interrupt is Ctrl+C on Windows too) cancels the context; the
	// first one only, so a second still kills a handler that ignores it.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	context.AfterFunc(ctx, stop)

	start := time.Now()
	res, err := call(ctx, inv, verbose, env.Stderr, inv.Command.Run)
	if res == nil {
		res = &output.Result{}
	}
	res.Command = cmd.Name()
	res.Duration = time.Since(start)
	switch {
	case ctx.Err() != nil:
		// Whatever the handler made of it: a cancelled query surfaces as a
		// driver or SQL error, and the exit code must still say "cancelled".
		res.Errors = []*output.Error{output.NewError(output.CodeCancelled, "cancelled by interrupt", "")}
	case err != nil:
		e, ok := errors.AsType[*output.Error](err)
		if !ok {
			e = output.NewError(output.CodeGeneralError, err.Error(), "")
		}
		res.Errors = append(res.Errors, e)
	}
	return finish(env, res, o, sink)
}

// call runs handler on inv, recovering a panic into GENERAL_ERROR. The stack
// goes to stderr only under --verbose: it is for a bug report, not for the
// agent reading stdout.
func call(ctx context.Context, inv *spec.Invocation, verbose bool, stderr io.Writer,
	handler func(context.Context, *spec.Invocation) (*output.Result, error)) (res *output.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			hint := "Re-run with --verbose for the stack trace."
			if verbose {
				fmt.Fprintf(stderr, "panic: %v\n%s", r, debug.Stack())
				hint = ""
			}
			res, err = nil, output.NewError(output.CodeGeneralError, fmt.Sprintf("internal error: %v", r), hint)
		}
	}()
	return handler(ctx, inv)
}

// finish completes the Sink (made from o when the command had none) with the
// sets the handler built itself, sets the result's status from its errors and
// renders it to stdout. stderr gets one line for an error — or, for csv and
// tsv, whose stdout is rows only, the whole summary. It returns the exit
// code: the first error's, else 0.
func finish(env Env, res *output.Result, o output.Options, sink *output.Sink) int {
	if sink == nil {
		o.OutFile = ""
		sink, _ = output.NewSink(env.Stdout, o)
	}
	sink.Add(res.Sets)
	if err := sink.Finish(res, len(res.Errors) > 0); err != nil {
		e, ok := errors.AsType[*output.Error](err)
		if !ok {
			e = output.NewError(output.CodeGeneralError, err.Error(), "")
		}
		res.Errors = append(res.Errors, e)
	}
	exit := 0
	if len(res.Errors) > 0 {
		e := res.Errors[0]
		exit = e.Exit()
		if res.Status == "" || res.Status == output.StatusOK {
			res.Status = output.StatusError
			switch e.Code {
			case output.CodeCancelled:
				res.Status = output.StatusCancelled
			case output.CodeBlocked:
				res.Status = output.StatusBlocked
			}
		}
	} else if res.Status == "" {
		res.Status = output.StatusOK
	}
	if err := output.Render(env.Stdout, res, o); err != nil {
		fmt.Fprintf(env.Stderr, "gossms: %s: writing output: %v\n", output.CodeGeneralError, err)
		return max(exit, 1)
	}
	switch {
	case o.Format.RowsOnly():
		output.RenderSummary(env.Stderr, res, o)
	case len(res.Errors) > 0:
		fmt.Fprintf(env.Stderr, "gossms: %s: %s\n", res.Errors[0].Code, res.Errors[0].Message)
	}
	return exit
}

// hasFlag reports whether any of names appears as a token before "--". It is
// for decisions made before (or without) a successful parse.
func hasFlag(argv []string, names ...string) bool {
	for _, tok := range argv {
		if tok == "--" {
			return false
		}
		if slices.Contains(names, tok) {
			return true
		}
	}
	return false
}

// scanOutput is the -o/--output value the raw command line asks for, when it
// names an implemented format; text otherwise. It lets a parse error come out
// in the format the caller will parse.
func scanOutput(argv []string) output.Format {
	format := output.FormatText
	for i, tok := range argv {
		if tok == "--" {
			break
		}
		var v string
		switch tok {
		case "-o", "--output":
			if i+1 < len(argv) {
				v = argv[i+1]
			}
		default:
			for _, p := range []string{"-o=", "--output="} {
				if after, ok := strings.CutPrefix(tok, p); ok {
					v = after
				}
			}
		}
		if slices.Contains(output.Formats, v) {
			format = output.Format(v)
		}
	}
	return format
}
