package tui

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// warningPage is a one-page set whose form warns before applying while its
// "Risky" row is edited, and whose apply counts real and scripted runs.
func warningPage(real, scripted *int) (func() []propPage, *propsheet.Form) {
	form := propsheet.NewForm(propsheet.Select("Risky", []string{"OFF", "ON"}, 0))
	form.SetApplyConfirm(func() string { return "Changing Risky closes connections." })
	return func() []propPage {
		return []propPage{{
			title: "Options",
			load: func(context.Context) (*propsheet.Form, propApply, error) {
				return form, func(ctx context.Context) error {
					if gosmo.Scripting(ctx) {
						*scripted++
					} else {
						*real++
					}
					return nil
				}, nil
			},
		}}
	}, form
}

// An edit whose page registered an apply warning is not written until the
// warning is accepted: No leaves the server untouched and the edit pending,
// Yes applies it. Script Changes writes nothing, so it does not ask.
func TestPropDialogAsksBeforeApplyingAWarnedEdit(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t)
	d := NewPropDialog(a)
	a.propDialog = d
	var real, scripted int
	pages, form := warningPage(&real, &scripted)

	d.show(sc, "", "Properties", "", "", pages)
	waitAndDrain(t, a)
	if d.PageForm(0) != form {
		t.Fatal("the page's form was not installed")
	}
	editSelect(t, form, "Risky", "ON")

	d.runApply(false)
	if !a.confirmDialog.Visible() {
		t.Fatal("Apply wrote a warned edit without asking")
	}
	// No starts nothing, so there is nothing to wait for: had it started the
	// run, the count below would already be racing it, which -race reports.
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if real != 0 {
		t.Fatalf("answering No still applied (%d runs)", real)
	}
	if !form.Dirty() {
		t.Fatal("answering No discarded the edit")
	}

	d.runScript()
	waitAndDrain(t, a)
	if a.confirmDialog.Visible() {
		t.Fatal("Script Changes asked the apply warning")
	}
	if scripted != 1 || real != 0 {
		t.Fatalf("Script Changes: scripted %d, real %d; want 1, 0", scripted, real)
	}

	d.runApply(false)
	answerConfirm(t, a, false)
	waitAndDrain(t, a)
	if real != 1 {
		t.Fatalf("answering Yes applied %d times, want 1", real)
	}
}
