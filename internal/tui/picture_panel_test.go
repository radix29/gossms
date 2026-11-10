package tui

import (
	"bytes"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// testPicture is a 120x80 gradient: wider than tall, so fit and zoom
// geometry differ on the two axes.
func testPicture() image.Image {
	im := image.NewNRGBA(image.Rect(0, 0, 120, 80))
	for y := range 80 {
		for x := range 120 {
			im.SetNRGBA(x, y, color.NRGBA{uint8(x * 2), uint8(y * 3), 128, 255})
		}
	}
	return im
}

// cellHex spells bytes as the grid shows a binary cell (query.appendValue).
func cellHex(b []byte) string { return "0x" + strings.ToUpper(hex.EncodeToString(b)) }

func encodePicture(t *testing.T, format string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, testPicture())
	case "jpeg":
		err = jpeg.Encode(&buf, testPicture(), nil)
	case "gif":
		err = gif.Encode(&buf, testPicture(), nil)
	case "bmp":
		err = bmp.Encode(&buf, testPicture())
	case "tiff":
		err = tiff.Encode(&buf, testPicture(), nil)
	default:
		t.Fatalf("no encoder for %s", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Every format the panel claims must both sniff and decode: a sniff without a
// registered decoder opens a panel that can only say it failed.
func TestEverySniffedFormatDecodes(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "gif", "bmp", "tiff"} {
		value := cellHex(encodePicture(t, format))
		got, ok := sniffPictureHex(value)
		if !ok || got != format {
			t.Errorf("%s: sniffed %q, %v", format, got, ok)
			continue
		}
		pic, err := decodePicture(value)
		if err != nil {
			t.Errorf("%s: decode: %v", format, err)
			continue
		}
		if pic.format != format || pic.flat.Bounds().Dx() != 120 || pic.flat.Bounds().Dy() != 80 {
			t.Errorf("%s: decoded %q %v", format, pic.format, pic.flat.Bounds())
		}
	}
	// WebP has no encoder in x/image; its magic is checked alone.
	webp := []byte("RIFF\x10\x00\x00\x00WEBPVP8 ")
	if got, ok := sniffPicture(webp); !ok || got != "webp" {
		t.Errorf("webp header sniffed %q, %v", got, ok)
	}
}

func TestSniffPictureHexRefusesNonPictures(t *testing.T) {
	for _, v := range []string{
		"",
		"0x",
		"0x00010203",
		"0x424D",                     // "BM" and nothing to back it up
		"0x424D0102030405060708090A", // "BM" with a non-zero reserved field
		"89504E470D0A1A0A",           // a PNG's digits without the 0x
		"0x89504E47ZZ",               // not hex
		"[dbo]",
	} {
		if format, ok := sniffPictureHex(v); ok {
			t.Errorf("sniffPictureHex(%q) = %q, want no picture", v, format)
		}
	}
}

// The column type decides whether a cell is sniffed at all: a picture's hex in
// an nvarchar is text, and an untyped set still sees XML.
func TestClassifyCellKindPictures(t *testing.T) {
	pngHex := cellHex(encodePicture(t, "png"))
	for _, tc := range []struct {
		sqlType, value string
		want           cellValueKind
	}{
		{"varbinary(max)", pngHex, cellPicture},
		{"image", pngHex, cellPicture},
		{"binary(16)", "0x00112233", cellPlain},
		{"", pngHex, cellPicture},
		{"nvarchar(max)", pngHex, cellPlain},
		{"", "<a/>", cellXML},
		{"varbinary(10)", "0x3C612F3E", cellPlain}, // "<a/>" as bytes is still bytes
	} {
		if got := classifyCellKind(tc.sqlType, tc.value); got != tc.want {
			t.Errorf("classifyCellKind(%q, %.20q) = %v, want %v", tc.sqlType, tc.value, got, tc.want)
		}
	}
}

func TestDecodePictureReportsGarbage(t *testing.T) {
	if _, err := decodePicture("0x89504E470D0A1A0A0000"); err == nil {
		t.Error("a truncated PNG decoded")
	}
	if _, err := decodePicture("0x89504E47G"); err == nil {
		t.Error("odd, non-hex digits decoded")
	}
}

// readyPicturePanel is a panel with a decoded picture, laid out at w x h.
func readyPicturePanel(t *testing.T, a *App, w, h int) *PicturePanel {
	t.Helper()
	pic, err := decodePicture(cellHex(encodePicture(t, "png")))
	if err != nil {
		t.Fatal(err)
	}
	pp := new(PicturePanel{app: a, title: "Data.png"})
	pp.buildTools()
	pp.setDecoded(pic, nil)
	pp.SetBounds(0, 0, w, h)
	return pp
}

func TestPicturePanelFitsTheView(t *testing.T) {
	pp := readyPicturePanel(t, newTestApp(), 80, 30)
	art := pp.render()
	area := pp.artArea()
	if art == nil || art.Cols > area.W || art.Rows > area.H {
		t.Fatalf("fit art %v does not fit %dx%d", art, area.W, area.H)
	}
	if art.Cols != area.W && art.Rows != area.H {
		t.Errorf("fit art %dx%d touches neither side of %dx%d", art.Cols, art.Rows, area.W, area.H)
	}
	// A resize re-fits; an unchanged frame reuses the art.
	if pp.render() != art {
		t.Error("an unchanged frame rendered again")
	}
	pp.SetBounds(0, 0, 40, 30)
	if again := pp.render(); again == art || again.Cols > 40 {
		t.Errorf("resized art %dx%d, want a new rendering at most 40 wide", again.Cols, again.Rows)
	}
}

func TestPicturePanelZoomAndPan(t *testing.T) {
	pp := readyPicturePanel(t, newTestApp(), 40, 20)
	fitCols := pp.render().Cols

	pp.zoomBy(1)
	if !pp.zoomed() || pp.render().Cols <= fitCols {
		t.Fatalf("zoom in: cols %d, fit was %d", pp.render().Cols, fitCols)
	}
	// 1:1 in sextant mode is one image pixel per subpixel, two per column.
	pp.zoomActual()
	if got := pp.render().Cols; got != 60 {
		t.Errorf("1:1 sextant art is %d columns, want 120/2 = 60", got)
	}
	if pp.zoomPercent() != 100 {
		t.Errorf("1:1 is %d%%", pp.zoomPercent())
	}
	// Panning stops at the art's edges.
	pp.scrollBy(1000, 1000)
	art, area := pp.render(), pp.artArea()
	if pp.scrollX != art.Cols-area.W || pp.scrollY != art.Rows-area.H {
		t.Errorf("scrolled to %d,%d, want the far corner %d,%d", pp.scrollX, pp.scrollY, art.Cols-area.W, art.Rows-area.H)
	}
	pp.scrollBy(-1000, -1000)
	if pp.scrollX != 0 || pp.scrollY != 0 {
		t.Errorf("scrolled to %d,%d, want 0,0", pp.scrollX, pp.scrollY)
	}
	// Zoom never passes its cap, and Fit goes back.
	for range 100 {
		pp.zoomBy(1)
	}
	if pp.zoomCols != pp.maxZoomCols() {
		t.Errorf("zoomed to %d, cap is %d", pp.zoomCols, pp.maxZoomCols())
	}
	pp.runTool(picToolFit)
	if pp.zoomed() || pp.scrollX != 0 || pp.scrollY != 0 {
		t.Error("Fit left the panel zoomed or scrolled")
	}
}

// The mode switch re-renders in the other mode and is remembered in config.
func TestPicturePanelModeSwitch(t *testing.T) {
	a := newTestApp()
	pp := readyPicturePanel(t, a, 40, 20)
	before := pp.render()
	pp.runTool(picToolMode)
	after := pp.render()
	if !pp.ascii || after == before || after.Mode().String() != "ascii" {
		t.Fatalf("mode switch: ascii=%v, art mode %v", pp.ascii, after.Mode())
	}
	if !a.cfg.PictureASCII {
		t.Error("the mode switch was not stored in Config.PictureASCII")
	}
	if !strings.Contains(pp.tools.Cells[picToolMode].Label, "ASCII") {
		t.Errorf("mode cell says %q", pp.tools.Cells[picToolMode].Label)
	}
	// A new panel opens in the remembered mode.
	if !NewPicturePanel(a, "x", "0x00").ascii {
		t.Error("a new panel ignored Config.PictureASCII")
	}
}

// Save writes the cell's own bytes, not a re-encoding of what is shown.
func TestSavePicturePanelWritesTheOriginalBytes(t *testing.T) {
	a := newTestApp()
	pp := readyPicturePanel(t, a, 40, 20)
	pp.filePath = filepath.Join(t.TempDir(), "out.png")
	a.savePicturePanel(pp, false)
	got, err := os.ReadFile(pp.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encodePicture(t, "png")) {
		t.Errorf("saved %d bytes that differ from the cell's %d", len(got), len(pp.data))
	}
}

func TestPicturePanelToolsWaitForTheDecode(t *testing.T) {
	pp := new(PicturePanel{app: newTestApp(), loading: true})
	pp.buildTools()
	for i := range pp.tools.Cells {
		if want := i != picToolMode; pp.toolDisabled(i) != want {
			t.Errorf("cell %d disabled=%v before the decode, want %v", i, !want, want)
		}
	}
	pp.setDecoded(decodedPicture{}, os.ErrInvalid)
	if pp.loading || pp.errText == "" || pp.render() != nil {
		t.Error("a failed decode left the panel loading, silent or rendering")
	}
}

// Show Value on a picture cell opens a picture panel, and the decode lands.
func TestOpenCellValuePanelOpensAPicture(t *testing.T) {
	a := newTestApp()
	if !a.openCellValuePanel("varbinary(max)", "Data", cellHex(encodePicture(t, "jpeg"))) {
		t.Fatal("a JPEG cell was not claimed")
	}
	pp, ok := a.panels.PanelAt(0).(*PicturePanel)
	if !ok {
		t.Fatalf("panel is %T, want *PicturePanel", a.panels.PanelAt(0))
	}
	if pp.Title() != "Data.jpg" {
		t.Errorf("title %q, want Data.jpg", pp.Title())
	}
	drainUntil(t, a, func() bool { return !pp.loading }, "the picture decode")
	if !pp.ready() || pp.format != "jpeg" {
		t.Errorf("decode: ready=%v format=%q err=%q", pp.ready(), pp.format, pp.errText)
	}
	if !a.canSaveActivePanel() {
		t.Error("File > Save is off for a picture panel")
	}
	if a.openCellValuePanel("varbinary(max)", "Data", "0x0102") {
		t.Error("a non-picture binary cell was claimed; it belongs to the popup")
	}
}
