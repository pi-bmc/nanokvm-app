package oled

import (
	"image"
	"unicode/utf8"

	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/pi-bmc/nanokvm-app/pkg/device/oled/ssd1306"
)

// Panel geometry, re-exported so callers of this package do not need the
// driver's.
const (
	Width  = ssd1306.Width
	Height = ssd1306.Height
)

// The one typeface: X11's "fixed" 7x13, via x/image. A cell is 7 pixels
// wide, so a line holds 18 characters with two pixels to spare, and its 13
// rows fit a 16-pixel band with room for the burn-in orbit (see shift).
const (
	glyphW = 7
	glyphH = 13
	// Cols is how many characters fit across the panel.
	Cols = Width / glyphW
)

// Frame is a 1-bit Width x Height image held in the SSD1306's own memory
// layout — 8 pages of Width column bytes, LSB at the top — so a finished
// frame goes to the driver without conversion.
//
// The zero Frame is all dark. Drawing outside the panel is silently clipped:
// every caller here is positioning text from measured widths, and a label one
// pixel too long should lose that pixel, not panic a management controller.
type Frame [ssd1306.BufferSize]byte

// Set lights or darkens one pixel.
func (f *Frame) Set(x, y int, on bool) {
	if x < 0 || x >= Width || y < 0 || y >= Height {
		return
	}
	bit := byte(1) << (y & 7)
	if on {
		f[(y>>3)*Width+x] |= bit
	} else {
		f[(y>>3)*Width+x] &^= bit
	}
}

// Lit reports whether a pixel is on. Off-panel coordinates read as dark.
func (f *Frame) Lit(x, y int) bool {
	if x < 0 || x >= Width || y < 0 || y >= Height {
		return false
	}
	return f[(y>>3)*Width+x]&(1<<(y&7)) != 0
}

// Fill sets every pixel in r.
func (f *Frame) Fill(r image.Rectangle, on bool) {
	r = r.Intersect(image.Rect(0, 0, Width, Height))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			f.Set(x, y, on)
		}
	}
}

// Invert flips every pixel in r, turning light-on-dark text drawn there into
// dark-on-light.
func (f *Frame) Invert(r image.Rectangle) {
	r = r.Intersect(image.Rect(0, 0, Width, Height))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			f.Set(x, y, !f.Lit(x, y))
		}
	}
}

// Text draws s with its top-left corner at (x, y) and returns the x just past
// the last character.
func (f *Frame) Text(x, y int, s string) int {
	return f.TextScaled(x, y, 1, s)
}

// TextScaled draws s with every font pixel enlarged to a scale x scale block.
// Integer scaling keeps the strokes crisp on a 1-bit panel, where any
// smoothing would have nothing to render itself with.
func (f *Frame) TextScaled(x, y, scale int, s string) int {
	face := basicfont.Face7x13
	for _, r := range s {
		if r == degree {
			f.degreeSign(x, y, scale)
			x += glyphW * scale
			continue
		}

		// Ask for the glyph at the origin and place it ourselves: the
		// face answers in its own coordinates (dot on the baseline), and
		// only the offsets within the cell matter here.
		dr, mask, maskp, _, ok := face.Glyph(fixed.P(0, face.Ascent), printable(r))
		if !ok {
			x += glyphW * scale
			continue
		}
		for gy := dr.Min.Y; gy < dr.Max.Y; gy++ {
			for gx := dr.Min.X; gx < dr.Max.X; gx++ {
				_, _, _, a := mask.At(maskp.X+gx-dr.Min.X, maskp.Y+gy-dr.Min.Y).RGBA()
				if a == 0 {
					continue
				}
				f.Fill(image.Rect(
					x+gx*scale, y+gy*scale,
					x+(gx+1)*scale, y+(gy+1)*scale,
				), true)
			}
		}
		x += glyphW * scale
	}
	return x
}

// degree is the one non-ASCII character the display needs. The 7x13 face has
// no glyph for it, so it is drawn by hand.
const degree = '°'

// degreeSign draws a small ring in the top-left of a character cell, sitting
// at cap height so it reads as part of the "C" that follows it.
func (f *Frame) degreeSign(x, y, scale int) {
	for _, p := range [...]image.Point{
		{1, 2},
		{2, 2},
		{0, 3},
		{3, 3},
		{0, 4},
		{3, 4},
		{1, 5},
		{2, 5},
	} {
		f.Fill(image.Rect(
			x+p.X*scale, y+p.Y*scale,
			x+(p.X+1)*scale, y+(p.Y+1)*scale,
		), true)
	}
}

// printable maps a rune onto one the 7x13 face can draw. The face is ASCII
// only; anything else — a hostname is the one place operator-chosen text
// reaches the panel — becomes '?' rather than the face's hollow replacement
// box, which at this size reads as a display fault.
func printable(r rune) rune {
	if r < ' ' || r > '~' {
		return '?'
	}
	return r
}

// TextWidth is how many pixels s occupies at scale 1.
func TextWidth(s string) int {
	return utf8.RuneCountInString(s) * glyphW
}

// shift returns the frame moved right and down by (dx, dy), with the vacated
// edge left dark and whatever crosses the far edge dropped.
//
// It is how the renderer orbits the whole image by a pixel to spread OLED
// wear: layouts are drawn once at the origin and moved afterwards, so no page
// has to know the orbit exists — only to leave the margin it moves into.
func (f *Frame) shift(dx, dy int) Frame {
	if dx == 0 && dy == 0 {
		return *f
	}
	var out Frame
	for y := range Height {
		for x := range Width {
			if f.Lit(x, y) {
				out.Set(x+dx, y+dy, true)
			}
		}
	}
	return out
}
