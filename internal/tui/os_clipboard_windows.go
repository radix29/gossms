package tui

import (
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// The Windows clipboard, through the Win32 API rather than a shelled-out tool.
// Both tools tried cost something a user hit: clip.exe keeps the UTF-16 BOM it
// needs for Unicode input on the clipboard as a U+FEFF, which a terminal paste
// carried into the editor and SQL Server rejected; PowerShell's Set-Clipboard /
// Get-Clipboard set the text exactly but take about a second to start, per copy
// and per paste. CF_UNICODETEXT is UTF-16 with no BOM and no guessing.
//
// The one file outside os_clipboard.go/version.go that is platform-specific:
// syscall.NewLazyDLL exists only in Windows builds, so this can't be a
// runtime.GOOS branch. It plugs in through win32Clipboard, leaving no stub to
// keep in step on other platforms.

func init() {
	win32Clipboard = &clipboardMethod{copy: win32ClipboardWrite, paste: win32ClipboardRead}
}

// user32 and kernel32 are KnownDLLs, always loaded from the system directory,
// so the plain lookup can't be hijacked by a DLL beside the executable.
var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard          = user32.NewProc("OpenClipboard")
	procCloseClipboard         = user32.NewProc("CloseClipboard")
	procEmptyClipboard         = user32.NewProc("EmptyClipboard")
	procSetClipboardData       = user32.NewProc("SetClipboardData")
	procGetClipboardData       = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvail = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalAlloc            = kernel32.NewProc("GlobalAlloc")
	procGlobalFree             = kernel32.NewProc("GlobalFree")
	procGlobalLock             = kernel32.NewProc("GlobalLock")
	procGlobalUnlock           = kernel32.NewProc("GlobalUnlock")
	procGlobalSize             = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory          = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// openClipboardTimeout bounds the wait for another process to close the
// clipboard: it is one global lock, and clipboard managers and remote-desktop
// clients hold it briefly after every change.
const openClipboardTimeout = time.Second

// withClipboard runs fn with the clipboard open, on one locked OS thread:
// Windows ties the open clipboard to the thread that opened it, and the Go
// scheduler could otherwise move this goroutine between OpenClipboard and
// CloseClipboard. false means the clipboard stayed busy.
func withClipboard(fn func() bool) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	deadline := time.Now().Add(openClipboardTimeout)
	for {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			break
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer procCloseClipboard.Call()
	return fn()
}

// win32ClipboardWrite puts text on the clipboard as CF_UNICODETEXT. Empty text
// leaves the clipboard empty, as the copy tools did.
func win32ClipboardWrite(text string) bool {
	units := append(utf16.Encode([]rune(text)), 0)
	size := uintptr(len(units)) * 2
	return withClipboard(func() bool {
		if r, _, _ := procEmptyClipboard.Call(); r == 0 {
			return false
		}
		if text == "" {
			return true
		}
		h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
		if h == 0 {
			return false
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			procGlobalFree.Call(h)
			return false
		}
		// RtlMoveMemory rather than a Go slice over p: turning a uintptr from a
		// syscall back into an unsafe.Pointer is what vet's unsafeptr check
		// forbids, and passing a Go pointer as a Call argument is what it allows.
		procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&units[0])), size)
		procGlobalUnlock.Call(h)
		// On success the system owns h; on failure it is still ours to free.
		if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
			procGlobalFree.Call(h)
			return false
		}
		return true
	})
}

// win32ClipboardRead returns the clipboard's text. A clipboard holding no text
// (empty, or only an image) reads as "", true: a legitimately empty paste, not
// a failure to fall back from.
func win32ClipboardRead() (string, bool) {
	var text string
	ok := withClipboard(func() bool {
		if r, _, _ := procIsClipboardFormatAvail.Call(cfUnicodeText); r == 0 {
			return true
		}
		h, _, _ := procGetClipboardData.Call(cfUnicodeText)
		if h == 0 {
			return false
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			return false
		}
		defer procGlobalUnlock.Call(h)
		size, _, _ := procGlobalSize.Call(h)
		units := make([]uint16, size/2)
		if len(units) > 0 {
			procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&units[0])), p, uintptr(len(units))*2)
		}
		// The block can be larger than the text: it ends at the first NUL.
		for i, u := range units {
			if u == 0 {
				units = units[:i]
				break
			}
		}
		text = string(utf16.Decode(units))
		return true
	})
	return text, ok
}
