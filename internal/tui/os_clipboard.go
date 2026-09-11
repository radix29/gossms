package tui

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// ---------------------------------------------------------------------------
// OS-native clipboard, shelled out to the platform's clipboard tool. This
// is the primary clipboard path — see clipboard.go, which falls back to
// tcell's OSC 52 terminal clipboard when no native tool is available (e.g.
// a bare SSH session) or a particular invocation fails.
// ---------------------------------------------------------------------------

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

// resolveClipboardMethod detects and caches which native clipboard tool to
// use, once per process.
func resolveClipboardMethod() *clipboardMethod {
	clipboardOnce.Do(func() {
		clipboardMeth = detectClipboardMethod(exec.LookPath)
	})
	return clipboardMeth
}

// detectClipboardMethod picks a clipboardMethod for runtime.GOOS using
// lookPath to probe for available tools. Takes lookPath as a parameter so
// it's unit-testable without requiring real clipboard binaries to be
// installed.
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
		if found("clip") {
			return &clipboardMethod{
				// clip.exe reads plain stdin in the console's OEM code
				// page, not UTF-8: "ö" (C3 B6) landed on the clipboard as
				// "├╢" under CP437. A UTF-16LE stream with a BOM is the one
				// input it takes as Unicode, whatever the code page.
				copy: func(text string) bool { return runClipboardCmd(utf16LEWithBOM(text), "clip") },
				paste: func() (string, bool) {
					// PowerShell writes redirected stdout in
					// [Console]::OutputEncoding, the OEM code page, so
					// Get-Clipboard alone turns "ö" into a lone 0x94 byte.
					// Switch it to BOM-less UTF-8 first. Its pipeline output
					// also reliably appends a trailing line terminator even
					// with -Raw, unlike xclip/xsel/pbpaste/wl-paste's
					// exact-bytes output — trim it here rather than in the
					// shared helper.
					out, ok := runClipboardOutCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", windowsPasteScript)
					if !ok {
						return "", false
					}
					return strings.TrimRight(strings.TrimPrefix(out, "\uFEFF"), "\r\n"), true
				},
			}
		}
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

// windowsPasteScript reads the clipboard with PowerShell's stdout switched
// to BOM-less UTF-8 — see the Windows paste in detectClipboardMethod.
const windowsPasteScript = "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false; Get-Clipboard -Raw"

// utf16LEWithBOM encodes text as UTF-16LE preceded by a byte-order mark,
// the form clip.exe takes as Unicode. The result is raw bytes carried in a
// string, for runClipboardCmd's stdin.
func utf16LEWithBOM(text string) string {
	units := utf16.Encode([]rune(text))
	b := make([]byte, 0, 2+2*len(units))
	b = append(b, 0xFF, 0xFE)
	for _, u := range units {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return string(b)
}

// runClipboardCmd runs name(args...) with text piped to stdin, for a copy
// command. Returns false if the tool isn't available or the invocation
// fails.
func runClipboardCmd(text string, name string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// runClipboardOutCmd runs name(args...) and returns its stdout verbatim,
// for a paste command. ok=false means the tool isn't available or the
// invocation failed — not the same as a legitimately empty clipboard,
// which is ok=true with text=="". Output is returned byte-for-byte (no
// trailing-newline trimming here) since xclip/xsel/pbpaste/wl-paste
// (with --no-newline) all emit the clipboard's exact bytes; a caller
// whose tool doesn't (PowerShell) trims it itself.
func runClipboardOutCmd(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// osClipboardWrite writes text to the native OS clipboard via the first
// available platform tool. Returns false if no tool is available or this
// particular invocation failed — callers fall back to the OSC 52 path.
func osClipboardWrite(text string) bool {
	m := resolveClipboardMethod()
	if m == nil {
		return false
	}
	return m.copy(text)
}

// osClipboardRead reads the native OS clipboard synchronously. ok=false
// means no tool is available or this invocation failed — callers fall
// back to the async GetClipboard()/EventClipboard path.
func osClipboardRead() (string, bool) {
	m := resolveClipboardMethod()
	if m == nil {
		return "", false
	}
	return m.paste()
}
