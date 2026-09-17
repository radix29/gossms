package tui

import (
	"context"
	"testing"
	"time"
)

// Object Dependencies and the About box share one PropertiesDialog instance, so
// repurposing it has to supersede the dependency fetch in flight: without it
// the fetch lands later and overwrites the About box with dependency rows, and
// its two catalog reads go on holding a pool connection nobody is waiting on.
func TestShowGenericPropertiesSupersedesTheDependencyFetch(t *testing.T) {
	for _, c := range []struct {
		name string
		show func(d *PropertiesDialog)
	}{
		{"ShowGenericProperties", func(d *PropertiesDialog) {
			d.ShowGenericProperties("About", []PropertyRow{{Key: "Version", Value: "v0"}})
		}},
		{"ShowGenericPropertiesSized", func(d *PropertiesDialog) {
			d.ShowGenericPropertiesSized("About", []PropertyRow{{Key: "Version", Value: "v0"}}, 60, 24)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := NewPropertiesDialog(newTestApp())

			// Stand in for ShowDependencies' fetch, which starts its run the
			// same way.
			ctx, token := d.run.BeginTimeout(context.Background(), time.Minute)
			c.show(d)

			if ctx.Err() == nil {
				t.Error("the dependency fetch's reads were not cancelled; they keep a pool connection until they time out")
			}
			if d.run.Current(token) {
				t.Error("the dependency fetch's token is still current; its rows would overwrite what is shown now")
			}
		})
	}
}
