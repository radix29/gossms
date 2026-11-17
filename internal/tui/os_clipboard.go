package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OS-native clipboard, shelled out to the platform's clipboard tool (on
// Windows, the Win32 API: os_clipboard_windows.go). This is the primary
// clipboard path; clipboard.go falls back to tcell's OSC 52 terminal
// clipboard when no native tool is available (e.g. a bare SSH session) or an
// invocation fails.

// clipboardMethod holds the platform-specific copy/paste implementations
// chosen by detectClipboardMethod. nil means no native tool was found.
type clipboardMethod struct {
	copy  func(text string) bool
	paste func() (string, bool)
}

var (
	clipboardOnce sync.Once
	clipboardMeth *clipboardMethod
)

// win32Clipboard is the Windows method, set by os_clipboard_windows.go's init
// and nil in every other build.
var win32Clipboard *clipboardMethod

// resolveClipboardMethod detects and caches which native clipboard tool to
// use, once per process.
func resolveClipboardMethod() *clipboardMethod {
	clipboardOnce.Do(func() {
		clipboardMeth = detectClipboardMethod(exec.LookPath)
	})
	return clipboardMeth
}

// detectClipboardMethod picks a clipboardMethod for runtime.GOOS using lookPath
// to probe for tools. lookPath is a parameter so it is unit-testable without
// real clipboard binaries.
func detectClipboardMethod(lookPath func(string) (string, error)) *clipboardMethod {
	found := func(name string) bool {
		_, err := lookPath(name)
		return err == nil
	}
	switch runtime.GOOS {
	case "darwin":
		if found("pbcopy") && found("pbpaste") {
			return &clipboardMethod{
				copy:  func(text string) bool { return runClipboardCmd(text, "pbcopy") },
				paste: func() (string, bool) { return runClipboardOutCmd("pbpaste") },
			}
		}
	case "windows":
		// The Win32 API, not a tool: see os_clipboard_windows.go.
		return win32Clipboard
	default: // linux and other Unix-likes
		if os.Getenv("WAYLAND_DISPLAY") != "" && found("wl-copy") && found("wl-paste") {
			return &clipboardMethod{
				copy:  func(text string) bool { return runClipboardCmd(text, "wl-copy") },
				paste: func() (string, bool) { return runClipboardOutCmd("wl-paste", "--no-newline") },
			}
		}
		if found("xclip") {
			return &clipboardMethod{
				copy:  func(text string) bool { return runClipboardCmd(text, "xclip", "-selection", "clipboard") },
				paste: func() (string, bool) { return runClipboardOutCmd("xclip", "-selection", "clipboard", "-o") },
			}
		}
		if found("xsel") {
			return &clipboardMethod{
				copy:  func(text string) bool { return runClipboardCmd(text, "xsel", "--clipboard", "--input") },
				paste: func() (string, bool) { return runClipboardOutCmd("xsel", "--clipboard", "--output") },
			}
		}
	}
	return nil
}

// clipboardTimeout bounds a clipboard tool's run.
const clipboardTimeout = 2 * time.Second

// runClipboardCmd runs name(args...) with text piped to stdin, for a copy
// command. Returns false if the tool isn't available or the invocation fails.
func runClipboardCmd(text string, name string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// runClipboardOutCmd runs name(args...) and returns its stdout verbatim, for a
// paste command. ok=false means the tool isn't available or failed, not a
// legitimately empty clipboard (ok=true, text==""). Output is byte-for-byte (no
// newline trimming) since xclip/xsel/pbpaste/wl-paste (--no-newline) emit the
// clipboard's exact bytes.
func runClipboardOutCmd(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// osClipboardWrite writes text to the native OS clipboard via the first
// available platform tool. Returns false if none is available or this
// invocation failed; callers fall back to OSC 52.
func osClipboardWrite(text string) bool {
	m := resolveClipboardMethod()
	if m == nil {
		return false
	}
	return m.copy(text)
}

// osClipboardRead reads the native OS clipboard synchronously. ok=false means no
// tool is available or this invocation failed; callers fall back to the async
// GetClipboard()/EventClipboard path.
func osClipboardRead() (string, bool) {
	m := resolveClipboardMethod()
	if m == nil {
		return "", false
	}
	return m.paste()
}
