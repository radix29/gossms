package tui

import (
	"database/sql"
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// QueryPanel holds a SQL editor on top and a results grid below, separated
// by a draggable/keyboard-resizable horizontal Splitter. It implements the
// tuikit/layout.Panel interface so it can be hosted by a layout.PanelManager.
type QueryPanel struct {
	rect     core.Rect
	title    string
	editor   *controls.Editor
	results  *controls.DataGrid
	splitter *layout.Splitter
	active   bool
	connIdx  int // -1 = none
	app      *App
}

// NewQueryPanel creates a new query panel bound to the given App (for
// connection lookup and status updates) and titled accordingly.
func NewQueryPanel(app *App, title string) *QueryPanel {
	return new(QueryPanel{
		app:      app,
		title:    title,
		connIdx:  -1,
		editor:   controls.NewEditor(controls.SQLHighlighter(theme.Active())),
		results:  controls.NewDataGrid(),
		splitter: layout.NewHorizontalSplitter("─── Results ─── (drag or Ctrl+Up/Down to resize)"),
	})
}

// Title returns the panel title (Panel interface).
func (p *QueryPanel) Title() string     { return p.title }
func (p *QueryPanel) SetTitle(t string) { p.title = t }

// SetBounds positions the panel and lays out the editor/splitter/results.
func (p *QueryPanel) SetBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	// Row 0 is the title bar; the splitter manages everything below it.
	p.splitter.SetBounds(x, y+1, w, h-1)
	p.layoutChildren()
}

func (p *QueryPanel) layoutChildren() {
	top := p.splitter.FirstRect()
	bottom := p.splitter.SecondRect()
	p.editor.SetBounds(top.X, top.Y, top.W, top.H)
	p.results.SetBounds(bottom.X, bottom.Y, bottom.W, bottom.H)
}

// SetActive marks this panel as focused.
func (p *QueryPanel) SetActive(v bool) {
	p.active = v
	p.editor.SetActive(v)
}

// Draw renders the title bar, editor, splitter, and results grid.
func (p *QueryPanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	if p.active {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
	}
	core.FillRect(s, core.Rect{X: p.rect.X, Y: p.rect.Y, W: p.rect.W, H: 1}, ' ', titleStyle)
	connLabel := ""
	if p.connIdx >= 0 && p.connIdx < len(p.app.connections) {
		connLabel = " [" + p.app.connections[p.connIdx].Opts.Server + "]"
	}
	core.DrawTextClipped(s, p.rect.X+1, p.rect.Y, p.rect.W-2, titleStyle, p.title+connLabel)

	p.editor.Draw(s)
	p.splitter.Draw(s)
	p.results.Draw(s)
}

// Execute runs the query text against the connected server, in a goroutine.
func (p *QueryPanel) Execute() {
	query := p.editor.Text()
	if query == "" {
		p.results.SetStatus("No query to execute")
		return
	}
	if p.connIdx < 0 || p.connIdx >= len(p.app.connections) {
		p.results.SetStatus("Not connected — use File > Connect")
		p.results.SetData([]string{"Message"}, [][]string{{"No active connection"}})
		return
	}
	sc := p.app.connections[p.connIdx]
	p.results.SetStatus("Executing...")
	p.app.setStatus("Executing query...")

	go func() {
		cols, rows, err := execQuery(sc.Server.DB(), query)
		p.app.postEvent(func() {
			if err != nil {
				p.results.SetError(err)
				p.app.setStatus(fmt.Sprintf("Error: %v", err))
			} else {
				p.results.SetData(cols, rows)
				p.app.setStatus(fmt.Sprintf("Query returned %d rows", len(rows)))
			}
			p.app.screen.EventQ() <- tcell.NewEventInterrupt(nil)
		})
	}()
}

// HandleKey routes keys to the splitter (Ctrl+Up/Down resize), F5 execute,
// or the editor.
func (p *QueryPanel) HandleKey(ev *tcell.EventKey) bool {
	if p.splitter.HandleKey(ev) {
		p.layoutChildren()
		return true
	}
	if ev.Key() == tcell.KeyF5 {
		p.Execute()
		return true
	}
	return p.editor.HandleKey(ev)
}

// HandleMouse routes mouse events to the splitter (drag), editor, or results.
func (p *QueryPanel) HandleMouse(ev *tcell.EventMouse) bool {
	mx, _ := ev.Position()
	// Always forward release events so an in-progress splitter drag can
	// terminate even if the cursor has moved outside this panel's column.
	if ev.Buttons() == tcell.ButtonNone {
		if p.splitter.HandleMouse(ev) {
			p.layoutChildren()
		}
		return true
	}
	if mx < p.rect.X || mx >= p.rect.X+p.rect.W {
		return false
	}
	if p.splitter.HandleMouse(ev) {
		p.layoutChildren()
		return true
	}
	if p.editor.HandleMouse(ev) {
		return true
	}
	return p.results.HandleMouse(ev)
}

// execQuery runs a T-SQL query and returns columns + rows.
func execQuery(db *sql.DB, query string) ([]string, [][]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var result [][]string
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				row[i] = "NULL"
			} else {
				row[i] = fmt.Sprintf("%v", v)
			}
		}
		result = append(result, row)
	}
	return cols, result, rows.Err()
}
