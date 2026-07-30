package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tui"
)

func main() {
	if path, err := config.LogFilePath(); err == nil {
		// 0600, matching the config file and encryption key alongside it —
		// the log records server names, login names, and error text.
		logFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			log.SetOutput(logFile)
			defer logFile.Close()
		}
	}

	app := tui.NewApp()
	if err := run(app); err != nil {
		log.Fatalf("gossms error: %v", err)
	}
}

// run wraps app.Run so a panic on the UI goroutine is reported usefully
// instead of vanishing.
//
// App.Run's own `defer screen.Fini()` restores the terminal during a panic
// unwind, but the trace itself is written to stderr — which is still the
// alternate screen at that point, so it scrolls away with it and the user
// sees a silently exited program. Recovering here, after Fini has already
// run, puts the trace in the log file and a short line on the restored
// screen. Background goroutines can't be covered from here at all; they go
// through App.safego/recoverPanic instead.
func run(app *tui.App) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			log.Printf("panic: %v\n%s", r, stack)
			fmt.Fprintf(os.Stderr, "gossms panicked: %v\n", r)
			if path, perr := config.LogFilePath(); perr == nil {
				fmt.Fprintf(os.Stderr, "A stack trace was written to %s\n", path)
			}
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return app.Run()
}
