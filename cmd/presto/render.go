package main

// render.go — spectrogram image renderer for the analyze subcommand.
// Uses golang.org/x/image for anti-aliased TrueType font rendering
// with Go's built-in fonts (no external TTF files needed).

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// --- Fonts ---

var (
	labelFace font.Face // axis labels, info text
	titleFace font.Face // title, larger
)

func init() {
	const labelSize = 16
	const titleSize = 18

	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		log.Fatal(err)
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		log.Fatal(err)
	}
	opts := &opentype.FaceOptions{
		Size:    labelSize,
		DPI:     72,
		Hinting: font.HintingFull,
	}
	labelFace, err = opentype.NewFace(regular, opts)
	if err != nil {
		log.Fatal(err)
	}
	opts.Size = titleSize
	titleFace, err = opentype.NewFace(bold, opts)
	if err != nil {
		log.Fatal(err)
	}
}

// textWidth returns the pixel width of a string in the given font face.
func textWidth(face font.Face, s string) int {
	w := font.MeasureString(face, s)
	return w.Ceil()
}

// drawLabel renders anti-aliased text at (x, y) where (x, y) is the
// **top-left** corner of the text bounding box. Internally adds the
// font ascent to convert to the baseline coordinate that x/image
// expects. This makes positioning intuitive: (x, y) is where the text
// visually starts, like image.Draw or any normal graphics API.
func drawLabel(img *image.RGBA, face font.Face, x, y int, s string, c color.RGBA) {
	ascent := face.Metrics().Ascent.Ceil()
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, y+ascent),
	}
	d.DrawString(s)
}

// drawLabelRight renders text right-aligned so the right edge of the
// text sits at x.
func drawLabelRight(img *image.RGBA, face font.Face, x, y int, s string, c color.RGBA) {
	w := textWidth(face, s)
	drawLabel(img, face, x-w, y, s, c)
}

// drawLabelCenter renders text horizontally centered at x.
func drawLabelCenter(img *image.RGBA, face font.Face, x, y int, s string, c color.RGBA) {
	w := textWidth(face, s)
	drawLabel(img, face, x-w/2, y, s, c)
}

// drawLabelRightVCenter draws text right-aligned at x, vertically centered
// on y. Uses font.BoundString for exact glyph bounds so the visual midpoint
// of the rendered glyphs sits on y — not an approximation from ascent/descent
// metrics which overestimate for characters without descenders (digits, caps).
func drawLabelRightVCenter(img *image.RGBA, face font.Face, x, y int, s string, c color.RGBA) {
	bounds, advance := font.BoundString(face, s)
	// bounds.Min.Y is negative (top above baseline), bounds.Max.Y is
	// positive or zero (bottom below baseline). The visual midpoint
	// relative to the baseline is halfway between them.
	midY := (bounds.Min.Y + bounds.Max.Y) / 2
	baseline := fixed.I(y) - midY
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x) - advance, Y: baseline},
	}
	d.DrawString(s)
}

// --- Layout ---

type layout struct {
	PixW, PixH int
	MaxBin     int
	ColorBarW  int
	Pad        int

	PlotW, PlotH     int
	MarginL, MarginR int
	MarginT, MarginB int
	ImgW, ImgH       int
	PlotX0, PlotY0   int
	CbX              int
	FontH            int // full glyph height (ascent + descent)
	NumFrames        int
}

func defaultLayout() layout {
	return layout{
		PixW: 5, PixH: 4,
		MaxBin:    140,
		ColorBarW: 16,
		Pad:       10,
	}
}

func (l *layout) compute(numFrames int) {
	l.NumFrames = numFrames
	m := labelFace.Metrics()
	l.FontH = m.Ascent.Ceil() + m.Descent.Ceil()

	l.PlotW = numFrames * l.PixW
	l.PlotH = l.MaxBin * l.PixH

	// Left margin: widest Y label ("6000") + tick + gap
	l.MarginL = textWidth(labelFace, "6000") + l.Pad*2 + 5
	// Right margin: gap + color bar + gap + "loud" label + gap
	l.MarginR = l.Pad + l.ColorBarW + l.Pad + textWidth(labelFace, "loud") + l.Pad
	// Top: title line + gap
	l.MarginT = l.FontH + l.Pad*3
	// Bottom: tick + time label + gap
	l.MarginB = 5 + l.FontH + l.Pad*2

	l.ImgW = l.MarginL + l.PlotW + l.MarginR
	l.ImgH = l.MarginT + l.PlotH + l.MarginB
	l.PlotX0 = l.MarginL
	l.PlotY0 = l.MarginT
	l.CbX = l.PlotX0 + l.PlotW + l.Pad
}

// --- Viridis colormap ---

type rgb struct{ r, g, b uint8 }

var viridisCtrl = [8]rgb{
	{68, 1, 84}, {72, 35, 116}, {64, 67, 135}, {52, 94, 141},
	{33, 144, 140}, {53, 183, 121}, {143, 215, 68}, {253, 231, 37},
}

func viridis(t float64) color.RGBA {
	t = max(0, min(1, t))
	idx := t * float64(len(viridisCtrl)-1)
	lo := int(idx)
	hi := min(lo+1, len(viridisCtrl)-1)
	frac := idx - float64(lo)
	a, b := viridisCtrl[lo], viridisCtrl[hi]
	return color.RGBA{
		lerpU8(a.r, b.r, frac), lerpU8(a.g, b.g, frac), lerpU8(a.b, b.b, frac), 255,
	}
}

func lerpU8(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t)
}

// --- Drawing primitives ---

func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for dy := range h {
		for dx := range w {
			img.SetRGBA(x+dx, y+dy, c)
		}
	}
}

func drawHLine(img *image.RGBA, x, y, w int, c color.RGBA) {
	for dx := range w {
		img.SetRGBA(x+dx, y, c)
	}
}

func drawVLine(img *image.RGBA, x, y, h int, c color.RGBA) {
	for dy := range h {
		img.SetRGBA(x, y+dy, c)
	}
}

func drawDashedH(img *image.RGBA, x, y, w int, c color.RGBA) {
	for dx := 0; dx < w; dx += 3 {
		img.SetRGBA(x+dx, y, c)
	}
}

func drawDashedV(img *image.RGBA, x, y, h int, c color.RGBA) {
	for dy := 0; dy < h; dy += 3 {
		img.SetRGBA(x, y+dy, c)
	}
}

func drawRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	drawHLine(img, x, y, w, c)
	drawHLine(img, x, y+h, w+1, c)
	drawVLine(img, x, y, h, c)
	drawVLine(img, x+w, y, h, c)
}

func blendColor(bg, fg color.RGBA) color.RGBA {
	a := float64(fg.A) / 255
	return color.RGBA{
		uint8(float64(bg.R)*(1-a) + float64(fg.R)*a),
		uint8(float64(bg.G)*(1-a) + float64(fg.G)*a),
		uint8(float64(bg.B)*(1-a) + float64(fg.B)*a),
		255,
	}
}

// --- Helpers ---

func logScale(spect [][]float64, maxBin int) ([][]float64, float64) {
	n := len(spect)
	out := make([][]float64, n)
	maxVal := 0.0
	for t := range n {
		out[t] = make([]float64, maxBin)
		for f := range maxBin {
			if f < len(spect[t]) {
				v := math.Log1p(spect[t][f] * 200)
				out[t][f] = v
				if v > maxVal {
					maxVal = v
				}
			}
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}
	return out, maxVal
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

func savePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatalf("encode %s: %v", path, err)
	}
	info, _ := f.Stat()
	fmt.Printf("  %s (%d KB)\n", path, info.Size()/1024)
}

// --- Color palette ---

type palette struct {
	bg, border, grid, label, dim, title color.RGBA
	peak, peakGlow                      color.RGBA
	boxBg, boxBorder, boxText           color.RGBA
}

func colors() palette {
	return palette{
		bg:        color.RGBA{20, 20, 28, 255},
		border:    color.RGBA{100, 100, 115, 255},
		grid:      color.RGBA{60, 60, 75, 255},
		label:     color.RGBA{190, 190, 205, 255},
		dim:       color.RGBA{130, 130, 145, 255},
		title:     color.RGBA{220, 220, 235, 255},
		peak:      color.RGBA{255, 70, 70, 255},
		peakGlow:  color.RGBA{255, 70, 70, 80},
		boxBg:     color.RGBA{10, 10, 22, 210},
		boxBorder: color.RGBA{90, 90, 110, 255},
		boxText:   color.RGBA{230, 230, 245, 255},
	}
}

// --- Composite drawing ---

// spectrogramParams holds the signal-specific values needed to annotate
// a spectrogram with axis labels and ticks.
type spectrogramParams struct {
	hzPerBin     float64
	framesPerSec float64
	duration     float64
	title        string
}

// renderSpectrogram creates a fully annotated spectrogram image: viridis
// cells, axes with ticks/labels/grid, color bar, and title. Returns the
// image plus the layout and palette so callers can add overlays.
func renderSpectrogram(spect [][]float64, p spectrogramParams) (*image.RGBA, layout, palette) {
	lo := defaultLayout()
	lo.compute(len(spect))

	col := colors()
	img := image.NewRGBA(image.Rect(0, 0, lo.ImgW, lo.ImgH))
	fillRect(img, 0, 0, lo.ImgW, lo.ImgH, col.bg)

	// Spectrogram cells
	logSpec, maxVal := logScale(spect, lo.MaxBin)
	for t := range len(spect) {
		for f := range lo.MaxBin {
			c := viridis(logSpec[t][f] / maxVal)
			fillRect(img,
				lo.PlotX0+t*lo.PixW, lo.PlotY0+(lo.MaxBin-1-f)*lo.PixH,
				lo.PixW, lo.PixH, c)
		}
	}

	// Plot border
	drawRect(img, lo.PlotX0, lo.PlotY0, lo.PlotW, lo.PlotH, col.border)

	// Y axis
	for hz := 0; hz <= int(float64(lo.MaxBin)*p.hzPerBin); hz += 1000 {
		bin := int(float64(hz) / p.hzPerBin)
		if bin >= lo.MaxBin {
			break
		}
		y := lo.PlotY0 + (lo.MaxBin-1-bin)*lo.PixH
		drawLabelRightVCenter(img, labelFace, lo.PlotX0-lo.Pad, y, fmt.Sprintf("%d", hz), col.label)
		drawHLine(img, lo.PlotX0-4, y, 4, col.label)
		if hz > 0 {
			drawDashedH(img, lo.PlotX0+1, y, lo.PlotW-2, col.grid)
		}
	}
	drawLabelRight(img, labelFace, lo.PlotX0-lo.Pad, lo.Pad, "Hz", col.dim)

	// X axis
	for sec := 0.0; sec <= p.duration; sec += 0.5 {
		frame := int(sec * p.framesPerSec)
		if frame >= len(spect) {
			break
		}
		x := lo.PlotX0 + frame*lo.PixW
		drawLabelCenter(img, labelFace, x, lo.PlotY0+lo.PlotH+6, fmt.Sprintf("%.1fs", sec), col.label)
		drawVLine(img, x, lo.PlotY0+lo.PlotH, 4, col.label)
		if sec > 0 {
			drawDashedV(img, x, lo.PlotY0+1, lo.PlotH-2, col.grid)
		}
	}

	// Color bar
	for y := range lo.PlotH {
		t := 1.0 - float64(y)/float64(lo.PlotH)
		c := viridis(t)
		fillRect(img, lo.CbX, lo.PlotY0+y, lo.ColorBarW, 1, c)
	}
	drawRect(img, lo.CbX, lo.PlotY0, lo.ColorBarW, lo.PlotH, col.border)
	drawLabel(img, labelFace, lo.CbX+lo.ColorBarW+4, lo.PlotY0+2, "loud", col.dim)
	drawLabel(img, labelFace, lo.CbX+lo.ColorBarW+4, lo.PlotY0+lo.PlotH-lo.FontH-2, "soft", col.dim)

	// Title
	drawLabelCenter(img, titleFace, lo.PlotX0+lo.PlotW/2, lo.Pad, p.title, col.title)

	return img, lo, col
}

func drawPeakDot(img *image.RGBA, cx, cy, r, px0, py0, pw, ph int, solid, glow color.RGBA) {
	for dy := -r - 1; dy <= r+1; dy++ {
		for dx := -r - 1; dx <= r+1; dx++ {
			d2 := dx*dx + dy*dy
			x, y := cx+dx, cy+dy
			if x < px0 || x >= px0+pw || y < py0 || y >= py0+ph {
				continue
			}
			if d2 <= r*r/2 {
				img.SetRGBA(x, y, solid)
			} else if d2 <= r*r {
				bg := img.RGBAAt(x, y)
				img.SetRGBA(x, y, blendColor(bg, glow))
			}
		}
	}
}

func drawInfoBox(img *image.RGBA, lines []string, lo layout, col palette) {
	lineH := lo.FontH + 4
	boxW := 0
	for _, l := range lines {
		if w := textWidth(labelFace, l) + lo.Pad*3; w > boxW {
			boxW = w
		}
	}
	boxH := len(lines)*lineH + lo.Pad*2
	bx := lo.PlotX0 + lo.PlotW - boxW - lo.Pad*2
	by := lo.PlotY0 + lo.Pad*2

	fillRect(img, bx, by, boxW, boxH, col.boxBg)
	drawRect(img, bx, by, boxW, boxH, col.boxBorder)
	for i, line := range lines {
		drawLabel(img, labelFace, bx+lo.Pad, by+lo.Pad+i*lineH, line, col.boxText)
	}
}
