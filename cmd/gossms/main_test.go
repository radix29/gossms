package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	gosmoversion "github.com/radix29/gosmo/version"

	"github.com/radix29/gossms/internal/version"
)

// TestWatchdogLeavesTheExitToMainOnceTheLoopReturned is K13: the watchdog's
// exit raced main's FlushSaves. Once the loop has returned, the watchdog must
// not exit, however long main's flush takes.
func TestWatchdogLeavesTheExitToMainOnceTheLoopReturned(t *testing.T) {
	sigs := make(chan os.Signal, 1)
	returned := make(chan struct{})
	exited := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		watchdog(sigs, returned, func(os.Signal) { close(returned) }, 20*time.Millisecond,
			func() { exited <- struct{}{} })
		close(done)
	}()
	sigs <- syscall.SIGTERM
	<-done
	select {
	case <-exited:
		t.Fatal("the watchdog exited although the loop had returned")
	case <-time.After(60 * time.Millisecond):
	}
}

// TestWatchdogExitsWhenTheLoopIsWedged: a loop that never returns after the
// save still ends the process.
func TestWatchdogExitsWhenTheLoopIsWedged(t *testing.T) {
	sigs := make(chan os.Signal, 1)
	exited := make(chan struct{})
	go watchdog(sigs, make(chan struct{}), func(os.Signal) {}, 10*time.Millisecond,
		func() { close(exited) })
	sigs <- syscall.SIGHUP
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("the watchdog did not exit for a wedged loop")
	}
}

// TestPrintVersionFormat pins `gossms --version` to its pre-version.Fields
// bytes: release.yml's brew test and users' scripts read it.
func TestPrintVersionFormat(t *testing.T) {
	want := fmt.Sprintf("gossms %s\nCommit:   %s\nBuilt:    %s\nPlatform: %s\nLicense:  %s\ngosmo:    %s\n",
		version.Version, version.Commit, version.Date, version.Runtime(), version.License, gosmoversion.Version)
	var b strings.Builder
	printVersion(&b)
	if got := b.String(); got != want {
		t.Errorf("printVersion:\n%s\nwant:\n%s", got, want)
	}
}

// TestSelectMode pins the mode rule (docs/cli.md § Modes): no arguments is the
// TUI, but only with both stdin and stdout on a terminal; any argument but the
// version flags is the command line, terminal or not.
func TestSelectMode(t *testing.T) {
	const in, out = 10, 11
	defer func(f func(int) bool) { isTerminal = f }(isTerminal)
	for _, tc := range []struct {
		name          string
		args          []string
		inTTY, outTTY bool
		want          mode
	}{
		{"bare on a terminal", nil, true, true, modeTUI},
		{"bare, stdout piped", nil, true, false, modeNoTerminal},
		{"bare, stdin redirected", nil, false, true, modeNoTerminal},
		{"bare, both redirected", nil, false, false, modeNoTerminal},
		{"--version", []string{"--version"}, false, false, modeVersion},
		{"-version", []string{"-version"}, true, true, modeVersion},
		{"-v", []string{"-v", "extra"}, true, true, modeVersion},
		{"a command", []string{"version"}, true, true, modeCLI},
		{"a command, piped", []string{"version", "-o", "json"}, false, false, modeCLI},
		{"--help", []string{"--help"}, true, true, modeCLI},
		{"options only", []string{"-p", "x"}, true, true, modeCLI},
		{"empty argument", []string{""}, true, true, modeCLI},
	} {
		isTerminal = func(fd int) bool {
			switch fd {
			case in:
				return tc.inTTY
			case out:
				return tc.outTTY
			}
			t.Fatalf("%s: isTerminal(%d), want stdin %d or stdout %d", tc.name, fd, in, out)
			return false
		}
		if got := selectMode(tc.args, in, out); got != tc.want {
			t.Errorf("%s: selectMode(%q) = %d, want %d", tc.name, tc.args, got, tc.want)
		}
	}
}
