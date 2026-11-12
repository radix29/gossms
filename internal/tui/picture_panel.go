package tui

import (
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gotuipic"
	_ "github.com/radix29/gotuipic/formats" // bmp, tiff, webp beside the core's png, jpeg, gif

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// PicturePanel shows one binary cell that holds a picture, rendered as
// terminal art by gotuipic: what "Show Value" opens for a varbinary/image
// cell whose bytes start like a PNG, JPEG, GIF, BMP, TIFF or WebP
// (sniffPictureHex), as an XML cell opens in a highlighted query panel.
//
// The picture is decoded and flattened once, off the UI goroutine
// (decodePicture); every rendering after that — the panel resized, a zoom
// step, the mode switched — is gotuipic.Convert of the flattened image, which
// skips the decode and the per-pixel conversion and costs only the resize.
// The rendered art is cached against everything it depends on (pictureKey),
// so an ordinary frame draws cells and renders nothing.
//
// Like a value panel it has no connection: it shows bytes already fetched.
type PicturePanel struct {
	app    *App
	title  string
	rect   core.Rect
	active bool

	// toolRect is the toolbar row and viewRect the picture's area below it,
	// scrollbars included while zoomed (layout).
	toolRect, viewRect core.Rect

	// Filled by the decode: data is the original bytes, kept for Save;
	// format is gotuipic's name for them ("png", "jpeg", ...).
	loading bool
	errText string
	data    []byte
	format  string
	flat    *image.NRGBA

	// ascii renders with gotuipic.ModeASCII instead of the default sextants,
	// for a terminal font without the U+1FB00 block (Config.PictureASCII).
	ascii bool
	// zoomCols is the art's width in cells while zoomed; 0 fits the view.
	zoomCols int
	// scrollX/scrollY is the art cell at the view's top-left while zoomed.
	scrollX, scrollY int

	art    *gotuipic.Art
	glyphs []string // art.Cells' runes as strings, built once per rendering
	artKey pictureKey

	tools    controls.ToolRow
	dragZone core.Gesture[pictureZone]
	// dragFrom is the last pointer position of a pan drag, the mouseDragging
	// latch for the view: set on the press, cleared on release.
	dragging   bool
	dragFrom   [2]int
	vbarDrag   bool
	hbarDrag   bool
	renderFail string

	// filePath is where Save last wrote the picture, so a second Ctrl+S
	// writes there without prompting.
	filePath string
}

// pictureKey is what a rendering depends on; a draw whose key matches the
// cached art's renders nothing.
type pictureKey struct {
	ascii      bool
	cols, w, h int
}

// pictureZone is what claimed a Button1 gesture in the panel.
type pictureZone int

const (
	pzNone pictureZone = iota
	pzToolbar
	pzView
	pzVBar
	pzHBar
)

// Toolbar cell indexes, in buildPictureTools' order.
const (
	picToolFit = iota
	picToolActual
	picToolZoomIn
	picToolZoomOut
	picToolMode
	picToolSave
)

// pictureZoomStep is one zoom step's factor on the art's width.
const pictureZoomStep = 1.25

// pictureMinCols is the narrowest a zoom-out goes.
const pictureMinCols = 4

// openPicturePanel shows a binary cell's picture in a new PicturePanel titled
// after its column plus the format's extension, as openValuePanel titles an
// XML cell. Reports whether it took the value: one that doesn't sniff as a
// picture is left to the grid's popup.
func (a *App) openPicturePanel(column, value string) bool {
	format, ok := sniffPictureHex(value)
	if !ok {
		return false
	}
	title := strings.ToUpper(format)
	if column != "" {
		title = column + pictureExt(format)
	}
	pp := NewPicturePanel(a, title, value)
	a.panels.SetActiveIndex(a.panels.AddPanel(pp))
	a.focusPanels()
	a.setStatus("Opened " + title)
	return true
}

// NewPicturePanel creates a panel for the picture whose bytes hexValue spells
// as the grid shows a binary cell ("0x…"), and starts decoding it.
func NewPicturePanel(app *App, title, hexValue string) *PicturePanel {
	pp := new(PicturePanel{app: app, title: title, loading: true})
	if app.cfg != nil {
		pp.ascii = app.cfg.PictureASCII
	}
	pp.buildTools()
	app.safego("decode picture", func() {
		pic, err := decodePicture(hexValue)
		app.postAndWake(func() { pp.setDecoded(pic, err) })
	})
	return pp
}

// decodedPicture is decodePicture's result.
type decodedPicture struct {
	data   []byte
	format string
	flat   *image.NRGBA
}

// decodePicture turns a binary cell's 0x… text back into bytes, decodes them
// under gotuipic's size guards and flattens the image for repeated rendering.
// It runs off the UI goroutine: a varbinary(max) picture is megabytes of hex.
func decodePicture(hexValue string) (decodedPicture, error) {
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(hexValue), "0x"), "0X")
	data, err := hex.DecodeString(digits)
	if err != nil {
		return decodedPicture{}, fmt.Errorf("not binary data: %w", err)
	}
	img, format, err := gotuipic.DecodeBytes(data)
	if err != nil {
		return decodedPicture{}, err
	}
	flat, err := gotuipic.Flatten(img)
	if err != nil {
		return decodedPicture{}, err
	}
	return decodedPicture{data: data, format: format, flat: flat}, nil
}

// setDecoded takes the decode's result, on the UI goroutine.
func (pp *PicturePanel) setDecoded(pic decodedPicture, err error) {
	pp.loading = false
	if err != nil {
		pp.errText = "Could not decode the picture: " + err.Error()
		return
	}
	pp.data, pp.format, pp.flat = pic.data, pic.format, pic.flat
}

// Title returns the panel's tab title (Panel interface).
func (pp *PicturePanel) Title() string { return pp.title }

// SetActive marks the panel focused (title bar colour).
func (pp *PicturePanel) SetActive(v bool) { pp.active = v }

// SetBounds positions the panel: title row, toolbar row, then the picture.
func (pp *PicturePanel) SetBounds(x, y, w, h int) {
	pp.rect = core.Rect{X: x, Y: y, W: w, H: h}
	pp.toolRect = core.Rect{X: x, Y: y + 1, W: w, H: min(1, max(h-1, 0))}
	pp.viewRect = core.Rect{X: x, Y: y + 2, W: w, H: max(h-2, 0)}
	pp.clampScroll()
}

// ready reports whether there is a picture to render.
func (pp *PicturePanel) ready() bool { return pp.flat != nil }

// zoomed reports whether the art has its own width rather than fitting the
// view; only then can it overflow, so only then are scrollbars reserved.
func (pp *PicturePanel) zoomed() bool { return pp.zoomCols > 0 }

// artArea is where the art itself is drawn: the view, less the scrollbar
// column and row while zoomed.
func (pp *PicturePanel) artArea() core.Rect {
	r := pp.viewRect
	if pp.zoomed() {
		r.W, r.H = max(r.W-1, 0), max(r.H-1, 0)
	}
	return r
}

// cellPixels is how many image pixels one cell covers in the current mode
// (gotuipic: sextant 2x3, ASCII 1x2), for 1:1 and the zoom percentage.
func (pp *PicturePanel) cellPixels() (w, h int) {
	if pp.ascii {
		return 1, 2
	}
	return 2, 3
}

// mode is the gotuipic mode the panel renders in.
func (pp *PicturePanel) mode() gotuipic.Mode {
	if pp.ascii {
		return gotuipic.ModeASCII
	}
	return gotuipic.ModeSextant
}

// render returns the art for the current state, rendering only when the
// state has changed since the cached art (pictureKey). nil while there is
// nothing to show or the area is empty.
func (pp *PicturePanel) render() *gotuipic.Art {
	area := pp.artArea()
	if !pp.ready() || area.W <= 0 || area.H <= 0 {
		return nil
	}
	key := pictureKey{ascii: pp.ascii, cols: pp.zoomCols}
	if !pp.zoomed() {
		key.w, key.h = area.W, area.H
	}
	if pp.art != nil && key == pp.artKey {
		return pp.art
	}
	opts := []gotuipic.Option{gotuipic.WithMode(pp.mode())}
	if pp.zoomed() {
		opts = append(opts, gotuipic.WithWidth(pp.zoomCols))
	} else {
		opts = append(opts, gotuipic.WithWidth(area.W), gotuipic.WithHeight(area.H))
	}
	art, err := gotuipic.Convert(pp.flat, opts...)
	if err != nil {
		pp.renderFail = err.Error()
		pp.art, pp.glyphs = nil, nil
		return nil
	}
	pp.renderFail = ""
	pp.art, pp.artKey = art, key
	// core.PutRune allocates for any rune outside ASCII and the box block, and a
	// sextant is neither: built once here rather than per cell per frame.
	pp.glyphs = make([]string, len(art.Cells))
	for i, c := range art.Cells {
		pp.glyphs[i] = string(c.Rune)
	}
	pp.clampScroll()
	return art
}

// maxZoomCols is the widest a zoom-in goes: four times the picture's own
// pixels per subpixel is already blocky, and gotuipic refuses more than
// DefaultMaxCells cells in all.
func (pp *PicturePanel) maxZoomCols() int {
	if !pp.ready() {
		return pictureMinCols
	}
	b := pp.flat.Bounds()
	pw, _ := pp.cellPixels()
	byPixels := 4 * max(b.Dx()/pw, 1)
	// rows ≈ cols · h/w · 0.5 (gotuipic's default cell aspect), so the cell
	// budget allows cols² ≤ budget · 2w/h.
	byCells := int(math.Sqrt(float64(gotuipic.DefaultMaxCells) * 2 * float64(b.Dx()) / float64(b.Dy())))
	return max(min(byPixels, byCells), pictureMinCols)
}

// currentCols is the art's width now: its zoom width, or what fitting gave.
func (pp *PicturePanel) currentCols() int {
	if pp.zoomed() {
		return pp.zoomCols
	}
	if art := pp.render(); art != nil {
		return art.Cols
	}
	return pictureMinCols
}

// setZoom moves to an art width of cols (0 = fit), keeping the point at the
// view's centre where it was.
func (pp *PicturePanel) setZoom(cols int) {
	if !pp.ready() {
		return
	}
	if cols != 0 {
		cols = core.Clamp(cols, pictureMinCols, pp.maxZoomCols())
	}
	if cols == pp.zoomCols {
		return
	}
	// The centre as a fraction of the art, before and after.
	fx, fy := 0.5, 0.5
	area := pp.artArea()
	if old := pp.render(); old != nil && pp.zoomed() {
		fx = (float64(pp.scrollX) + float64(area.W)/2) / float64(old.Cols)
		fy = (float64(pp.scrollY) + float64(area.H)/2) / float64(old.Rows)
	}
	pp.zoomCols = cols
	art := pp.render()
	if art == nil || !pp.zoomed() {
		pp.scrollX, pp.scrollY = 0, 0
		return
	}
	area = pp.artArea()
	pp.scrollX = int(fx*float64(art.Cols) - float64(area.W)/2)
	pp.scrollY = int(fy*float64(art.Rows) - float64(area.H)/2)
	pp.clampScroll()
}

// zoomBy steps the zoom in (dir > 0) or out, by at least one column.
func (pp *PicturePanel) zoomBy(dir int) {
	cur := pp.currentCols()
	next := int(math.Round(float64(cur) * pictureZoomStep))
	if dir < 0 {
		next = int(math.Round(float64(cur) / pictureZoomStep))
		next = min(next, cur-1)
	} else {
		next = max(next, cur+1)
	}
	pp.setZoom(next)
}

// zoomActual shows one image pixel per subpixel: the picture's own size.
func (pp *PicturePanel) zoomActual() {
	if !pp.ready() {
		return
	}
	pw, _ := pp.cellPixels()
	pp.setZoom((pp.flat.Bounds().Dx() + pw - 1) / pw)
}

// zoomPercent is the art's scale against the picture's own pixels.
func (pp *PicturePanel) zoomPercent() int {
	if !pp.ready() {
		return 0
	}
	pw, _ := pp.cellPixels()
	return int(math.Round(float64(pp.currentCols()*pw) * 100 / float64(pp.flat.Bounds().Dx())))
}

// toggleMode switches sextant and ASCII art, and remembers the choice: a
// terminal font without sextants shows none in any picture.
func (pp *PicturePanel) toggleMode() {
	pp.ascii = !pp.ascii
	if cfg := pp.app.cfg; cfg != nil {
		cfg.PictureASCII = pp.ascii
		pp.app.saveConfig(nil)
	}
	pp.refreshToolLabels()
}

// scrollBy pans the zoomed art.
func (pp *PicturePanel) scrollBy(dx, dy int) {
	pp.scrollX += dx
	pp.scrollY += dy
	pp.clampScroll()
}

// clampScroll keeps the view inside the art; a smaller art is centred and
// never scrolled.
func (pp *PicturePanel) clampScroll() {
	if pp.art == nil || !pp.zoomed() {
		pp.scrollX, pp.scrollY = 0, 0
		return
	}
	area := pp.artArea()
	pp.scrollX = core.Clamp(pp.scrollX, 0, max(pp.art.Cols-area.W, 0))
	pp.scrollY = core.Clamp(pp.scrollY, 0, max(pp.art.Rows-area.H, 0))
}

// pictureColor maps a gotuipic cell colour to a terminal one; alpha 0 means
// "no colour", for which def stands in.
func pictureColor(c color.RGBA, def tcell.Color) tcell.Color {
	if c.A == 0 {
		return def
	}
	return tcell.NewRGBColor(int32(c.R), int32(c.G), int32(c.B))
}

// sniffPictureHex reports the picture format a binary cell's 0x… text starts
// with, reading only the first few bytes' digits — this runs on the UI
// goroutine when Show Value is asked, so its cost must not grow with the
// value. The full decode (decodePicture) is what proves it.
func sniffPictureHex(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if len(v) < 2 || v[0] != '0' || (v[1] != 'x' && v[1] != 'X') {
		return "", false
	}
	digits := v[2:]
	n := min(len(digits), 2*pictureSniffLen) &^ 1
	head, err := hex.DecodeString(digits[:n])
	if err != nil {
		return "", false
	}
	return sniffPicture(head)
}

// pictureSniffLen is how many leading bytes sniffPicture needs at most.
const pictureSniffLen = 18

// sniffPicture names the format whose magic number b starts with, among those
// gotuipic decodes here (the core's PNG, JPEG and GIF; BMP, TIFF and WebP
// from its formats package).
func sniffPicture(b []byte) (string, bool) {
	has := func(prefix string) bool { return strings.HasPrefix(string(b), prefix) }
	switch {
	case has("\x89PNG\r\n\x1a\n"):
		return "png", true
	case has("\xff\xd8\xff"):
		return "jpeg", true
	case has("GIF87a"), has("GIF89a"):
		return "gif", true
	case has("BM") && bmpHeader(b):
		return "bmp", true
	case has("II*\x00"), has("MM\x00*"):
		return "tiff", true
	case has("RIFF") && len(b) >= 12 && string(b[8:12]) == "WEBP":
		return "webp", true
	}
	return "", false
}

// bmpHeader checks what follows a "BM": two bytes alone would claim any
// binary value that happens to start 0x424D, so the reserved fields must be
// zero and the DIB header one of its defined sizes.
func bmpHeader(b []byte) bool {
	if len(b) < 18 || string(b[6:10]) != "\x00\x00\x00\x00" || string(b[15:18]) != "\x00\x00\x00" {
		return false
	}
	switch b[14] {
	case 12, 40, 52, 56, 64, 108, 124:
		return true
	}
	return false
}

// pictureExt is the file extension for a gotuipic format name.
func pictureExt(format string) string {
	switch format {
	case "jpeg":
		return ".jpg"
	case "tiff":
		return ".tif"
	case "":
		return ".bin"
	}
	return "." + format
}
