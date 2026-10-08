package tui

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
)

func TestDetectClipboardMethodLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this test exercises the linux/default detection branch")
	}
	t.Setenv("WAYLAND_DISPLAY", "")

	lookPath := func(available ...string) func(string) (string, error) {
		set := make(map[string]bool, len(available))
		for _, a := range available {
			set[a] = true
		}
		return func(name string) (string, error) {
			if set[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		}
	}

	if m := detectClipboardMethod(lookPath()); m != nil {
		t.Fatal("expected nil when no clipboard tool is available")
	}

	if m := detectClipboardMethod(lookPath("xsel")); m == nil {
		t.Fatal("expected a method when xsel is available")
	}

	if m := detectClipboardMethod(lookPath("xsel", "xclip")); m == nil {
		t.Fatal("expected a method when xclip and xsel are both available")
	}
}

func TestDetectClipboardMethodPrefersWaylandWhenDisplaySet(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this test exercises the linux/default detection branch")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")

	lookPath := func(name string) (string, error) {
		switch name {
		case "wl-copy", "wl-paste", "xclip":
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	m := detectClipboardMethod(lookPath)
	if m == nil {
		t.Fatal("expected a method when wl-copy/wl-paste are available")
	}
}

// TestUTF16LEWithBOMEncodesNonASCII pins the clip.exe input: "ö" typed on
// Windows pasted back as "├╢" while the UTF-8 bytes went to clip, which read
// them in the OEM code page.
func TestUTF16LEWithBOMEncodesNonASCII(t *testing.T) {
	got := []byte(utf16LEWithBOM("öä a\n😀"))
	want := []byte{
		0xFF, 0xFE, // BOM
		0xF6, 0x00, // ö
		0xE4, 0x00, // ä
		0x20, 0x00, // space
		0x61, 0x00, // a
		0x0A, 0x00, // \n
		0x3D, 0xD8, 0x00, 0xDE, // 😀 as a surrogate pair
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("utf16LEWithBOM = % X, want % X", got, want)
	}
	if got := []byte(utf16LEWithBOM("")); !bytes.Equal(got, []byte{0xFF, 0xFE}) {
		t.Fatalf("empty text = % X, want just the BOM", got)
	}
}

// TestTrimPowerShellOutputKeepsTheTextsOwnNewlines is K6: PowerShell appends
// one line terminator, and only that one is PowerShell's. TrimRight also ate
// the newlines a copied block ended with.
func TestTrimPowerShellOutputKeepsTheTextsOwnNewlines(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"\uFEFFSELECT 1\r\n", "SELECT 1"},
		{"SELECT 1\r\n\r\n", "SELECT 1\r\n"},
		{"a\nb\n\n", "a\nb\n"},
		{"no terminator", "no terminator"},
		{"\r\n", ""},
	} {
		if got := trimPowerShellOutput(c.in); got != c.want {
			t.Errorf("trimPowerShellOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
