// Package cli is gossms's command-line mode: `gossms <command> …` runs one
// command and exits, and never starts the TUI. cmd/gossms dispatches here for
// any argument other than the version flags.
//
// The package does no os.* I/O of its own: everything arrives through Env, so
// every test runs in-process.
package cli

import (
	"context"
	"fmt"
	"io"
)

// Env is the process environment Main runs against.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
}

// Main runs the command line args (os.Args[1:]) and returns the process exit
// code.
//
// Phase 0.b stub: the command tree lands in 0.f, so every invocation is a
// usage error (exit 2) for now.
func Main(ctx context.Context, args []string, env Env) int {
	fmt.Fprintln(env.Stderr, "gossms: the command line is not implemented yet; run gossms with no arguments for the TUI")
	return 2
}
