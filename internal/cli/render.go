package cli

import (
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The output options' names (docs/cli.md § Output options).
const (
	optMaxRows        = "max-rows"
	optMaxColWidth    = "max-col-width"
	optMaxOutputBytes = "max-output-bytes"
	optNullText       = "null-text"
	optHeaders        = "headers"
	optMessages       = "messages"
	optOutFile        = "out-file"
	optForce          = "force"
)

// outputOptions is the global options that shape a result, beyond --output
// and --quiet. They are global because every command's answer goes through
// the same Sink and renderers: `profile list --max-rows 5` is cut like a
// query's result.
func outputOptions() []spec.Option {
	return []spec.Option{
		{Name: optMaxRows, Type: spec.TypeInt, Default: strconv.Itoa(output.DefaultMaxRows), Global: true,
			Help: "Rows kept per result set on stdout; 0 keeps all (in memory: prefer --out-file or -o jsonl for large results)"},
		{Name: optMaxColWidth, Type: spec.TypeInt, Default: strconv.Itoa(output.DefaultMaxColWidth), Global: true,
			Help: "Characters kept per cell in text, markdown, json and jsonl, marked …[+N chars]; 0 keeps all"},
		{Name: optMaxOutputBytes, Type: spec.TypeInt, Default: strconv.Itoa(output.DefaultMaxOutputBytes), Global: true,
			Help: "Cap on stdout in bytes; whole rows are dropped to fit; 0 is no cap"},
		{Name: optNullText, Type: spec.TypeString, Default: output.DefaultNullText, Global: true,
			Help: "How text and markdown show NULL"},
		{Name: optHeaders, Type: spec.TypeBool, Default: "true", Global: true,
			Help: "Column headers in text, csv and tsv; --no-headers leaves them out"},
		{Name: optMessages, Type: spec.TypeBool, Default: "true", Global: true,
			Help: "PRINT and informational messages; --no-messages leaves them out"},
		{Name: optOutFile, Type: spec.TypeString, Global: true,
			Help: "Write every row to this file, its format by extension (.csv .tsv .jsonl .json .md .txt); stdout gets the summary"},
		{Name: optForce, Type: spec.TypeBool, Global: true,
			Help: "Replace an existing --out-file or --plan-file"},
	}
}

// renderOptions is inv's output options. A negative limit is a usage error.
func renderOptions(inv *spec.Invocation) (output.Options, error) {
	o := output.Options{
		Format:         output.Format(inv.Values["output"].(string)),
		Quiet:          inv.Values["quiet"].(bool),
		MaxRows:        inv.Values[optMaxRows].(int),
		MaxColWidth:    inv.Values[optMaxColWidth].(int),
		MaxOutputBytes: int64(inv.Values[optMaxOutputBytes].(int)),
		NullText:       inv.Values[optNullText].(string),
		NoHeaders:      !inv.Values[optHeaders].(bool),
		NoMessages:     !inv.Values[optMessages].(bool),
		OutFile:        inv.Values[optOutFile].(string),
		Force:          inv.Values[optForce].(bool),
	}
	for _, n := range []string{optMaxRows, optMaxColWidth, optMaxOutputBytes} {
		if v := inv.Values[n].(int); v < 0 {
			return o, output.NewError(output.CodeUsageError, "--"+n+": must be 0 or more", "0 means no limit.")
		}
	}
	if inv.Given[optOutFile] && o.OutFile == "" {
		return o, output.NewError(output.CodeUsageError, "--out-file: empty path", "")
	}
	if o.Force && o.OutFile == "" {
		targets := []string{"--" + optOutFile}
		for _, n := range forceFileOptions {
			if inv.Given[n] {
				return o, nil
			}
			if inv.Command != nil && slices.ContainsFunc(inv.Command.Options, func(op spec.Option) bool { return op.Name == n }) {
				targets = append(targets, "--"+n)
			}
		}
		return o, output.NewError(output.CodeUsageError, "--force: only with "+strings.Join(targets, " or "), "")
	}
	return o, nil
}

// forceFileOptions is the command options, besides --out-file, that name a
// file the command writes and that --force lets replace an existing file.
var forceFileOptions = []string{"plan-file"}
