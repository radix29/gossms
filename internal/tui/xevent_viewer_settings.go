package tui

import (
	"fmt"
	"maps"
	"slices"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_settings.go is the Extended Events viewer's Settings menu:
// the display — hidden columns, filter, grouping and aggregation — saved
// under a name and applied to any viewer later, SSMS's Save/Open Display
// Settings (.viewsetting files) kept in config.XEventViewSettings instead of
// files.

// currentViewSetting is the viewer's display as it would be saved.
func (v *XEventViewer) currentViewSetting() config.XEventViewSetting {
	var s config.XEventViewSetting
	s.Hidden = slices.Sorted(maps.Keys(v.hiddenCols))
	s.Filter = v.filter.String()
	for _, c := range v.groupBy {
		s.GroupBy = append(s.GroupBy, c.Key())
	}
	for _, a := range v.aggs {
		s.Aggregates = append(s.Aggregates, a.Key())
	}
	return s
}

// applyViewSetting puts s in force. The hidden columns become this session's
// too, as a Columns change would. A filter that no longer parses, a column or
// an aggregate this build can't read, is left out and said.
func (v *XEventViewer) applyViewSetting(name string, s config.XEventViewSetting) {
	var problems []string
	v.hiddenCols = map[string]bool{}
	for _, k := range s.Hidden {
		v.hiddenCols[k] = true
	}
	f, err := xevent.ParseFilter(s.Filter)
	if err != nil {
		problems = append(problems, "filter ("+err.Error()+")")
		f = nil
	}
	v.filter = f
	var by []xevent.Column
	for _, k := range s.GroupBy {
		if c, ok := xevent.ParseColumnKey(k); ok {
			by = append(by, c)
		} else {
			problems = append(problems, "grouping column "+k)
		}
	}
	var aggs []xevent.Aggregate
	for _, k := range s.Aggregates {
		if a, ok := xevent.ParseAggregateKey(k); ok {
			aggs = append(aggs, a)
		} else {
			problems = append(problems, "aggregate "+k)
		}
	}
	v.aggs = aggs
	v.setGrouping(by)
	saveXEHiddenColumns(v.app, v.columnsKey(), v.hiddenCols)
	if len(problems) > 0 {
		v.app.setStatus(fmt.Sprintf("Applied settings %q, without: %v", name, problems))
		return
	}
	v.app.setStatus(fmt.Sprintf("Applied settings %q", name))
}

// saveViewSettingAs asks for a name and saves the display under it, asking
// before replacing a setting of that name.
func (v *XEventViewer) saveViewSettingAs() {
	v.app.promptDialog.ShowPrompt("Save Settings",
		"Save the columns shown, the filter, the grouping and the aggregation under a name, to apply to any Extended Events viewer later.",
		"Name:", v.session, func(name string) {
			s := v.currentViewSetting()
			if _, exists := v.app.cfg.XEventViewSettings[name]; exists {
				v.app.confirmDialog.ShowConfirm("Save Settings", fmt.Sprintf("Replace the saved settings %q?", name), func(yes bool) {
					if yes {
						v.storeViewSetting(name, &s)
					}
				})
				return
			}
			v.storeViewSetting(name, &s)
		})
}

// storeViewSetting writes s under name to config, or removes name when s is
// nil. The map is replaced, never written into — see
// config.Config.XEventViewSettings.
func (v *XEventViewer) storeViewSetting(name string, s *config.XEventViewSetting) {
	cfg := v.app.cfg
	if cfg == nil {
		return
	}
	next := maps.Clone(cfg.XEventViewSettings)
	if next == nil {
		next = map[string]config.XEventViewSetting{}
	}
	verb := "Saved"
	if s == nil {
		delete(next, name)
		verb = "Deleted"
	} else {
		next[name] = *s
	}
	if len(next) == 0 {
		next = nil
	}
	cfg.XEventViewSettings = next
	if err := cfg.Save(); err != nil {
		v.app.setStatus("Settings not saved: " + err.Error())
		return
	}
	v.app.setStatus(fmt.Sprintf("%s settings %q", verb, name))
}

// showSettingsMenu pops Save Settings As, and Apply and Delete cascades over
// the saved names.
func (v *XEventViewer) showSettingsMenu() {
	var names []string
	if v.app.cfg != nil {
		names = slices.Sorted(maps.Keys(v.app.cfg.XEventViewSettings))
	}
	var apply, del []controls.MenuItem
	for _, name := range names {
		apply = append(apply, controls.MenuItem{Label: name, Action: func() {
			v.applyViewSetting(name, v.app.cfg.XEventViewSettings[name])
		}})
		del = append(del, controls.MenuItem{Label: name, Action: func() {
			v.app.confirmDialog.ShowConfirm("Delete Settings", fmt.Sprintf("Delete the saved settings %q?", name), func(yes bool) {
				if yes {
					v.storeViewSetting(name, nil)
				}
			})
		}})
	}
	saved := func() bool { return len(names) > 0 }
	v.popMenu(xeToolSettings, []controls.MenuItem{
		{Label: "Save Settings As...", Action: v.saveViewSettingAs},
		{Divider: true},
		{Label: "Apply Settings", Sub: apply, Enabled: saved, Note: "none saved"},
		{Label: "Delete Settings", Sub: del, Enabled: saved, Note: "none saved"},
	})
}
