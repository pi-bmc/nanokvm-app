package oled

import (
	"image"
	"testing"
)

func lit(f *Frame) int {
	n := 0
	for y := range Height {
		for x := range Width {
			if f.Lit(x, y) {
				n++
			}
		}
	}
	return n
}

func TestSetAndLitUseThePanelLayout(t *testing.T) {
	var f Frame
	f.Set(5, 20, true)

	if !f.Lit(5, 20) {
		t.Fatal("pixel did not read back")
	}
	// Page 2 (rows 16..23), column 5, bit 4 — the SSD1306's own layout, so
	// the frame can go to the driver untouched.
	if got := f[2*Width+5]; got != 1<<4 {
		t.Errorf("byte = %#02x, want bit 4 set", got)
	}

	f.Set(5, 20, false)
	if lit(&f) != 0 {
		t.Error("clearing the pixel left something lit")
	}
}

func TestDrawingOffPanelIsClipped(t *testing.T) {
	var f Frame
	for _, p := range []image.Point{{-1, 0}, {0, -1}, {Width, 0}, {0, Height}} {
		f.Set(p.X, p.Y, true)
		if f.Lit(p.X, p.Y) {
			t.Errorf("off-panel pixel %v reads as lit", p)
		}
	}
	f.Fill(image.Rect(-10, -10, Width+10, Height+10), true)
	if got := lit(&f); got != Width*Height {
		t.Errorf("clipped Fill lit %d pixels, want the whole panel (%d)", got, Width*Height)
	}

	// Text running off the right edge must not wrap or panic.
	var g Frame
	g.Text(Width-10, 0, "WWWW")
	g.Text(0, Height-4, "WWWW")
}

func TestTextAdvancesOneCellPerCharacter(t *testing.T) {
	var f Frame
	if end := f.Text(3, 0, "abc"); end != 3+3*glyphW {
		t.Errorf("Text returned x=%d, want %d", end, 3+3*glyphW)
	}
	if lit(&f) == 0 {
		t.Error("Text drew nothing")
	}
	if got := TextWidth("10.0.197.82"); got != 11*glyphW {
		t.Errorf("TextWidth = %d, want %d", got, 11*glyphW)
	}
}

func TestTextStaysInsideItsCell(t *testing.T) {
	var f Frame
	f.Text(10, 20, "Wg|_")
	for y := range Height {
		for x := range Width {
			if !f.Lit(x, y) {
				continue
			}
			if x < 10 || x >= 10+4*glyphW || y < 20 || y >= 20+glyphH {
				t.Fatalf("pixel (%d,%d) is outside the text's cells", x, y)
			}
		}
	}
}

func TestScaledTextIsTheSameShapeEnlarged(t *testing.T) {
	var small, big Frame
	small.Text(0, 0, "ON")
	big.TextScaled(0, 0, 2, "ON")

	for y := range glyphH {
		for x := range 2 * glyphW {
			want := small.Lit(x, y)
			for dy := range 2 {
				for dx := range 2 {
					if big.Lit(2*x+dx, 2*y+dy) != want {
						t.Fatalf("scaled pixel (%d,%d) does not match source (%d,%d)", 2*x+dx, 2*y+dy, x, y)
					}
				}
			}
		}
	}
}

// Operator-chosen text (a hostname) can carry anything; what the font cannot
// draw must come out as a visible placeholder of the normal width, and the
// one non-ASCII character the display itself uses must draw.
func TestUnprintableRunesAndDegreeSign(t *testing.T) {
	var a, b Frame
	a.Text(0, 0, "é\x01")
	b.Text(0, 0, "??")
	if a != b {
		t.Error("non-ASCII and control characters did not render as '?'")
	}

	var d, blank Frame
	if end := d.Text(0, 0, string(degree)); end != glyphW {
		t.Errorf("degree sign advanced %d, want one cell (%d)", end, glyphW)
	}
	if d == blank {
		t.Error("degree sign drew nothing")
	}
	var q Frame
	q.Text(0, 0, "?")
	if d == q {
		t.Error("degree sign rendered as the '?' placeholder")
	}
}

func TestShiftMovesEveryPixel(t *testing.T) {
	var f Frame
	f.Set(0, 0, true)
	f.Set(10, 20, true)

	s := f.shift(1, 1)
	if !s.Lit(1, 1) || !s.Lit(11, 21) || lit(&s) != 2 {
		t.Error("shift(1,1) did not move both pixels by one")
	}
	if same := f.shift(0, 0); same != f {
		t.Error("shift(0,0) changed the frame")
	}
}
