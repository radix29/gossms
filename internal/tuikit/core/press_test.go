package core

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func TestPressLatch(t *testing.T) {
	ev := func(b tcell.ButtonMask) *tcell.EventMouse { return tcell.NewEventMouse(1, 1, b, tcell.ModNone) }
	var l PressLatch

	if l.Held() {
		t.Fatal("the zero value holds a press")
	}
	if l.Fresh(ev(tcell.Button2)) || l.Fresh(ev(tcell.WheelDown)) || l.Held() {
		t.Fatal("a non-Button1 event was claimed")
	}
	if !l.Fresh(ev(tcell.Button1)) {
		t.Fatal("the first press was not fresh")
	}
	if l.Fresh(ev(tcell.Button1)) {
		t.Fatal("a resend of the held press was fresh")
	}
	if l.Release(ev(tcell.WheelUp)) || !l.Held() {
		t.Fatal("a wheel tick mid-gesture released the latch")
	}
	if !l.Release(ev(tcell.ButtonNone)) || l.Held() {
		t.Fatal("ButtonNone did not release the latch")
	}
	if !l.Fresh(ev(tcell.Button1)) {
		t.Fatal("the press after a release was not fresh")
	}
	l.Clear()
	if !l.Fresh(ev(tcell.Button1)) {
		t.Fatal("the press after Clear was not fresh")
	}
}
