package tui

import (
	"context"
	"testing"
)

// A saved Properties change must drop the Details pane's cached view of what
// it edited — the node the dialog was opened over, its parent folder and its
// children — or the pane keeps listing pre-edit values until ⟳ (B8: a symmetric
// key's removed password still shown after OK). Script Changes writes nothing,
// so it must leave the cache alone.
func TestPropDialogApplyStalesTheDetailsItEdited(t *testing.T) {
	folder := &explorerNode{label: "Symmetric Keys"}
	key := &explorerNode{label: "k1", parent: folder}
	child := &explorerNode{label: "child", parent: key}
	sibling := &explorerNode{label: "k2", parent: folder}
	all := []*explorerNode{folder, key, child, sibling}

	setup := func() (*App, *PropDialog) {
		a := newTestApp()
		a.detailBrowser = NewDetailBrowser("Details")
		for _, n := range all {
			a.detailBrowser.cache[n] = &detailResult{}
		}
		d := newSheetDialog(t, []propPage{{title: "Encryption"}},
			map[int]propApply{0: func(context.Context) error { return nil }}, nil)
		d.app = a
		d.ctx = context.Background()
		d.detailNode = key
		return a, d
	}

	a, d := setup()
	d.runApply(true)
	drainUntil(t, a, func() bool { return !d.Applying() }, "the apply to report back")
	for _, n := range []*explorerNode{folder, key, child} {
		if _, ok := a.detailBrowser.cache[n]; ok {
			t.Errorf("%s: still cached after Apply", n.label)
		}
	}
	if _, ok := a.detailBrowser.cache[sibling]; !ok {
		t.Error("an unrelated sibling's cache entry was dropped")
	}

	a, d = setup()
	d.runScript()
	drainUntil(t, a, func() bool { return !d.Applying() }, "the script run to report back")
	for _, n := range all {
		if _, ok := a.detailBrowser.cache[n]; !ok {
			t.Errorf("%s: cache dropped by Script Changes, which writes nothing", n.label)
		}
	}
}
