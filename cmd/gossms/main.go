package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/radix29/gossms/internal/cli"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tui"
	"github.com/radix29/gossms/internal/version"
)

func main() {
	// Decided before anything opens a file or a tcell screen: `brew test` and CI
	// run without a TTY, where App.Run cannot start, and the command line must
	// never open the TUI's log file (two processes would share it).
	switch selectMode(os.Args[1:], int(os.Stdin.Fd()), int(os.Stdout.Fd())) {
	case modeVersion:
		printVersion(os.Stdout)
		return
	case modeCLI:
		os.Exit(cli.Main(context.Background(), os.Args[1:],
			cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, ReadFile: os.ReadFile}))
	case modeNoTerminal:
		fmt.Fprintln(os.Stderr, noTerminalMessage)
		os.Exit(1)
	}

	if logFile, err := config.OpenLogFile(); err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	app := tui.NewApp()
	loopReturned := watchTermSignals(app)
	err := run(app)
	loopReturned()
	if !errors.Is(err, errPanicked) {
		// Background saves still out when the loop returned would die with the process.
		app.FlushSaves(3 * time.Second)
	}
	if err != nil {
		// An error before the screen starts ("init screen") must still reach the
		// shell. run already reported a panic.
		if !errors.Is(err, errPanicked) {
			fmt.Fprintf(os.Stderr, "gossms error: %v\n", err)
		}
		log.Fatalf("gossms error: %v", err)
	}
}

// mode is what main runs for a given command line.
type mode int

const (
	modeTUI        mode = iota // bare gossms on a terminal
	modeVersion                // --version, -version, -v: the shipped version block
	modeCLI                    // any other argument: internal/cli
	modeNoTerminal             // bare gossms with stdin or stdout not a terminal
)

// noTerminalMessage is bare gossms's refusal when stdio is redirected; it
// points an agent at its manual (docs/cli.md § Modes).
const noTerminalMessage = "gossms: the interactive UI needs a terminal on stdin and stdout; " +
	"for scripted use run `gossms agent guide`"

// isTerminal is term.IsTerminal, a variable so TestSelectMode can decide
// without a tty.
var isTerminal = term.IsTerminal

// selectMode is the mode rule: no arguments means the TUI, any argument the
// command line, the version flags being the one shipped exception. Bare gossms
// also needs both stdin and stdout on a terminal: with them piped inside a
// terminal session tcell would still open /dev/tty (CONIN$ on Windows) and the
// TUI would start and wait for keys no one sends.
func selectMode(args []string, stdin, stdout int) mode {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-version", "-v":
			return modeVersion
		}
		return modeCLI
	}
	if !isTerminal(stdin) || !isTerminal(stdout) {
		return modeNoTerminal
	}
	return modeTUI
}

// errPanicked marks run's error for a recovered panic, which run reports to
// stderr itself.
var errPanicked = errors.New("panic")

// watchTermSignals saves every unsaved query panel when the terminal closes,
// ssh drops (SIGHUP) or the process is killed (SIGTERM). Both constants exist
// on every GOOS; on Windows they are never delivered.
//
// App.SaveOnSignal quits once saved, so Run returns and main exits normally.
// If Run is wedged, the exit below ends the process anyway: the text is on disk
// by then, and a hung process on a closed terminal is worse than an unrestored
// one.
//
// main calls the returned func as soon as Run returns and then owns the exit: a
// watchdog os.Exit would cut off main's FlushSaves(3s) mid-write.
func watchTermSignals(app *tui.App) (loopReturned func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM)
	returned := make(chan struct{})
	go watchdog(sigs, returned, func(sig os.Signal) { app.SaveOnSignal(sig, 2*time.Second) },
		2*time.Second, func() { os.Exit(1) })
	return sync.OnceFunc(func() { close(returned) })
}

// watchdog is watchTermSignals' goroutine, with save and exit injected for
// tests. After a signal it saves, then exits unless returned closes within grace.
func watchdog(sigs <-chan os.Signal, returned <-chan struct{}, save func(os.Signal), grace time.Duration, exit func()) {
	var sig os.Signal
	select {
	case sig = <-sigs:
	case <-returned:
		return
	}
	save(sig)
	select {
	case <-returned:
	case <-time.After(grace):
		log.Printf("exiting on %v: the event loop did not return", sig)
		exit()
	}
}

// printVersion writes version.Fields as `gossms --version` always has: the
// name line, then aligned "Label: value" lines. The bytes are a contract (the
// Homebrew test in release.yml matches them), so TestPrintVersionFormat pins them.
func printVersion(w io.Writer) {
	fields := version.Fields()
	fmt.Fprintf(w, "%s %s\n", version.Name, fields[0].Value)
	for _, f := range fields[1:] {
		fmt.Fprintf(w, "%-10s%s\n", f.Label+":", f.Value)
	}
}

// run reports a UI-goroutine panic instead of letting it vanish: a trace
// written on the alternate screen scrolls away with it, so recover after
// App.Run's deferred screen.Fini and log it plus a short line on the restored
// screen. Query panels with unsaved text go to config.RecoveredDir via
// App.EmergencySave and the files are named. Background goroutines use
// App.safego/recoverPanic.
func run(app *tui.App) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			log.Printf("panic: %v\n%s", r, stack)
			fmt.Fprintf(os.Stderr, "gossms panicked: %v\n", r)
			if path, perr := config.LogFilePath(); perr == nil {
				fmt.Fprintf(os.Stderr, "A stack trace was written to %s\n", path)
			}
			saved, failed := app.EmergencySave()
			for _, path := range saved {
				log.Printf("unsaved query recovered to %s", path)
				fmt.Fprintf(os.Stderr, "Unsaved query recovered to %s\n", path)
			}
			for _, ferr := range failed {
				log.Printf("unsaved query not recovered: %v", ferr)
				fmt.Fprintf(os.Stderr, "Unsaved query NOT recovered: %v\n", ferr)
			}
			err = fmt.Errorf("%w: %v", errPanicked, r)
		}
	}()
	return app.Run()
}
