package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

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
	loopReturned := watchTermSignals(app)
	err := run(app)
	loopReturned()
	if !errors.Is(err, errPanicked) {
		// Settings and tracked queries are saved in the background; a save
		// still out when the loop returned would die with the process.
		app.FlushSaves(3 * time.Second)
	}
	if err != nil {
		// The log above may be the only other place this goes: an error
		// before the screen starts ("init screen") would otherwise leave the
		// shell with nothing. run has already told stderr about a panic.
		if !errors.Is(err, errPanicked) {
			fmt.Fprintf(os.Stderr, "gossms error: %v\n", err)
		}
		log.Fatalf("gossms error: %v", err)
	}
}

// errPanicked marks run's error for a recovered panic, which run reports to
// stderr itself.
var errPanicked = errors.New("panic")

// watchTermSignals saves every unsaved query panel when the terminal is closed,
// an ssh session drops (SIGHUP) or the process is killed (SIGTERM), rather than
// dying with the default action. Both constants exist on every GOOS; on
// Windows they are simply never delivered.
//
// App.SaveOnSignal quits once it has saved, so Run returns and main exits
// normally. If Run is wedged and never does, the exit below ends the process
// anyway: the text is on disk by then, and a hung process on a closed terminal
// is worse than an unrestored one.
//
// main calls the returned func as soon as Run returns. From then on main owns
// the exit: the watchdog's os.Exit used to fire 2 s after the signal whatever
// main was doing, cutting off its FlushSaves(3s) mid-write.
func watchTermSignals(app *tui.App) (loopReturned func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM)
	returned := make(chan struct{})
	go watchdog(sigs, returned, func(sig os.Signal) { app.SaveOnSignal(sig, 2*time.Second) },
		2*time.Second, func() { os.Exit(1) })
	return sync.OnceFunc(func() { close(returned) })
}

// watchdog is watchTermSignals' goroutine, with the save and the exit injected
// for a test. After a signal it saves, then exits only if returned is still open
// once grace has passed.
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
			err = fmt.Errorf("%w: %v", errPanicked, r)
		}
	}()
	return app.Run()
}
