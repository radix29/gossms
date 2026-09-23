package main

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"

	gosmoversion "github.com/radix29/gosmo/version"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tui"
	"github.com/radix29/gossms/internal/version"
)

func main() {
	// Handled first, without the flag package: gossms takes no other arguments,
	// and everything below opens a file or a tcell screen. `brew test`, CI and
	// bug reports run without a TTY, where App.Run cannot start.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-version", "-v":
			printVersion()
			return
		}
	}

	if logFile, err := config.OpenLogFile(); err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	app := tui.NewApp()
	if err := run(app); err != nil {
		log.Fatalf("gossms error: %v", err)
	}
}

// printVersion writes the build metadata Help > About shows. Keep in step with
// newAboutRows in internal/tui/menu.go.
func printVersion() {
	fmt.Printf("%s %s\n", version.Name, version.Version)
	fmt.Printf("Commit:   %s\n", version.Commit)
	fmt.Printf("Built:    %s\n", version.Date)
	fmt.Printf("Platform: %s\n", version.Runtime())
	fmt.Printf("License:  %s\n", version.License)
	fmt.Printf("gosmo:    %s\n", gosmoversion.Version)
}

// run reports a panic on the UI goroutine instead of letting it vanish:
// App.Run's deferred screen.Fini restores the terminal, but the trace goes to
// stderr while still on the alternate screen and scrolls away with it.
// Recovering here, after Fini, puts the trace in the log and a short line on
// the restored screen. Every query panel with unsaved text is then written to
// config.RecoveredDir by App.EmergencySave, and the files named. Background
// goroutines use App.safego/recoverPanic instead.
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
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return app.Run()
}
