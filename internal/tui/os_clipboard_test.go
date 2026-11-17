package tui

import (
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
