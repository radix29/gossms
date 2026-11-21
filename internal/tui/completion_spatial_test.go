package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

func TestSQLSpatialCompletion(t *testing.T) {
	for _, tt := range []struct {
		sql, name string
		want      bool
	}{
		{"SELECT geom|", "geometry::Point", true},
		{"SELECT geography::STG|", "STGeomFromText", true},
		{"SELECT geometry::stg|", "STGeomFromText", true},
		{"SELECT geometry :: /* keep */ STG|", "STGeomFromText", true},
		{"SELECT geometry::ST|", "STArea", false},
		{"SELECT geometry::uni| FROM dbo.Customers", "UnionAggregate", true},
		{"SELECT * FROM dbo.Customers WHERE geometry::uni|", "UnionAggregate", false},
		{"SELECT c.Geom.ST| FROM dbo.Customers c", "STArea", true},
		{"SELECT dbo.Customers.Geom.STAr| FROM dbo.Customers c", "STArea", false},
		{"SELECT Geom.ST| FROM dbo.Customers", "STArea", true},
		{"SELECT c.Location.ST| FROM dbo.Customers c", "STDistance", true},
		{"SELECT c.Location.ST| FROM dbo.Customers c", "STCentroid", false},
		{"SELECT c.Geom.ST| FROM dbo.Customers c", "STCentroid", true},
		{"SELECT geometry::Point(1, 2, 0).ST|", "STArea", true},
		{"SELECT c.Geom.STBuffer(5).ST| FROM dbo.Customers c", "STArea", true},
		{"SELECT c.Location.STAsText().ST| FROM dbo.Customers c", "STArea", false},
		{"SELECT c.Geom.STArea().ST| FROM dbo.Customers c", "STUFF", false},
		{"SELECT c.Geom.ST| FROM dbo.Customers c", "STSrid", true},
		{"SELECT c.Location.L| FROM dbo.Customers c", "Lat", true},
		{"SELECT c.Geom.L| FROM dbo.Customers c", "Lat", false},
		{"SELECT c.Name.ST| FROM dbo.Customers c", "STArea", false},
		{"SELECT Unknown.ST| FROM dbo.Customers", "STArea", false},
		{"SELECT Geom.ST| FROM dbo.Customers Geom", "STArea", false},
		{"DECLARE @g geometry; SELECT @g.ST|", "STArea", true},
		{"DECLARE @g AS geography, @x int = 1; SELECT @g.ST|", "STDistance", true},
		{"DECLARE @x int = ABS(1), @g AS geometry; SELECT @g.STAr|", "STArea", true},
		{"DECLARE @x int = CASE WHEN 1=1 THEN 1 ELSE 0 END, @g AS geometry; SELECT @g.STAr|", "STArea", true},
		{"DECLARE @g int; SELECT @g.ST|", "STArea", false},
		{"DECLARE @t table ([@g] geometry); DECLARE @g int; SELECT @g.STAr|", "STArea", false},
		{"DECLARE @g int; SELECT [@g].STAr| FROM dbo.Customers", "STArea", true},
		{"DECLARE @g geometry;\nGO\nSELECT @g.ST|", "STArea", false},
		{"DECLARE @t TABLE (Shape geometry); SELECT t.Shape.ST| FROM @t t", "STArea", false},
		{"WITH c AS (SELECT Geom AS Shape FROM dbo.Customers) SELECT c.Shape.ST| FROM c", "STArea", false},
		{"SELECT * FROM geometry::STG|", "STGeomFromText", false},
		{"SELECT * FROM dbo.Customers c CROSS APPLY c.Geom.ST|", "STArea", false},
		{"SELECT * FROM dbo.Customers c CROSS APPLY dbo.fnOrdersFor(c.Geom.STAr|) f", "STArea", true},
		{"SELECT * FROM dbo.Customers c CROSS APPLY dbo.fnOrdersFor(ABS(c.Geom.STAr|)) f", "STArea", true},
		{"SELECT * FROM dbo.Customers c CROSS APPLY STRING_SPLIT(CONVERT(varchar(255),c.Geom.STAsTe|()), ',') s", "STAsText", true},
		{"SELECT * FROM dbo.Customers c JOIN STRING_SPLIT(CONVERT(varchar(255),geometry::Poi|(1,2,0)), ',') s ON 1=1", "Point", true},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders (c.Geom.STAr|) o ON c.Id = o.CustomerId", "STArea", false},
		{"SELECT * FROM dbo.Customers c JOIN dbo.Orders (INDEX(c.Geom.STAr|)) o ON c.Id = o.CustomerId", "STArea", false},
		{"SELECT * FROM dbo.Customers c CROSS APPLY dbo.fnOrdersFor(1) f(c.Geom.STAr|)", "STArea", false},
	} {
		for _, fabric := range []bool{false, true} {
			t.Run(tt.sql+tt.name+map[bool]string{false: "/SQL", true: "/Fabric"}[fabric], func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				inv := qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)]
				inv.catalog.Objects[0].Columns = append(inv.catalog.Objects[0].Columns,
					gosmo.CatalogColumn{Name: "Geom", DataType: "geometry"},
					gosmo.CatalogColumn{Name: "Location", DataType: "geography"},
					gosmo.CatalogColumn{Name: "@g", DataType: "geometry"},
				)
				if fabric {
					qp.conn.Server = fabricConn(t).Server
				}
				lines, row, col := linesAndCursor(t, tt.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				want := tt.want
				if got := containsLabel(items, tt.name); got != want {
					t.Fatalf("%s present=%v, want %v in %v", tt.name, got, want, labels(items))
				}
			})
		}
	}
}

func TestSQLSpatialCompletionAccepts(t *testing.T) {
	for _, tt := range []struct{ before, typed, after, want string }{
		{"SELECT ", "geometry::stgeomfromt", "", "SELECT geometry::STGeomFromText(|)"},
		{"DECLARE @g geometry; SELECT @g.", "starea", "", "DECLARE @g geometry; SELECT @g.STArea()|"},
		{"DECLARE @g geometry; SELECT @g.", "starea", "()", "DECLARE @g geometry; SELECT @g.STArea()|"},
		{"DECLARE @g geometry; SELECT @g.", "starea", " /* keep */ (\n)", "DECLARE @g geometry; SELECT @g.STArea /* keep */ (\n)|"},
		{"DECLARE @g geometry; SELECT @g.STBuffer(5).", "starea", "", "DECLARE @g geometry; SELECT @g.STBuffer(5).STArea()|"},
		{"DECLARE @g geometry; SELECT @g.", "stsrid", "", "DECLARE @g geometry; SELECT @g.STSrid|"},
		{"DECLARE @g geometry; SELECT @g.", "stdist", " /* keep */ (@other)", "DECLARE @g geometry; SELECT @g.STDistance /* keep */ (|@other)"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			qp.editor.SetText(tt.before + tt.after)
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
			for range []rune(tt.after) {
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
			}
			for _, r := range tt.typed {
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
			}
			if !qp.editor.OverlayActive() {
				t.Fatal("spatial completion popup did not open")
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
			if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "", 1); got != want {
				t.Fatalf("accepted text=%q, want %q", got, want)
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
			if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "x", 1); got != want {
				t.Fatalf("cursor text=%q, want %q", got, want)
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModNone))
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModNone))
			if got, want := qp.editor.Text(), tt.before+tt.typed+tt.after; got != want {
				t.Fatalf("undo text=%q, want %q", got, want)
			}
		})
	}
}
