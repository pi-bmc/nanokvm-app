package oled

import (
	"fmt"
	"image"
	"strings"
	"time"
)

// Vertical geometry of a yellow/blue panel: a 16-row band of one colour and
// a 48-row area of the other, with a physical gap of a pixel or two between
// them on the glass. Nothing is ever drawn across that boundary — a glyph
// straddling it would be cut in half by the gap and change colour midway.
const (
	bandH = 16
	bodyH = Height - bandH
	// lineH is the pitch of a text line. Three of them fill the body
	// exactly, and the band is one.
	lineH = 16
	// lineInset is the dark row left above each line's glyph cell. With a
	// 13-row cell in a 16-row line that leaves two below, which is the
	// room the burn-in orbit moves into (see orbit).
	lineInset = 1
	// bodyLines is how many text lines a page has to work with.
	bodyLines = bodyH / lineH
	// leftEdge and rightEdge bound the columns the layouts use (rightEdge
	// exclusive). That is 126 columns — exactly 18 character cells — set
	// one in from the left so no stroke sits on the bezel, with the last
	// column kept dark for the orbit's one-pixel move to the right.
	leftEdge  = 1
	rightEdge = leftEdge + Cols*glyphW
)

// Layout says where on the glass the band is.
type Layout struct {
	// BandAtBottom puts the header in the last 16 rows instead of the
	// first. The yellow band is part of the glass, at the edge nearest the
	// module's pin header; turning the module over (Options.Flip) turns
	// the band over with it, and the header has to follow so that it
	// stays the part drawn in yellow.
	BandAtBottom bool
}

func (l Layout) bandTop() int {
	if l.BandAtBottom {
		return bodyH
	}
	return 0
}

func (l Layout) bodyTop() int {
	if l.BandAtBottom {
		return 0
	}
	return bandH
}

// Page is one screenful of the body area.
type Page int

const (
	// PageNetwork is how to reach this BMC: address, link, MAC.
	PageNetwork Page = iota
	// PageHost is the managed machine: boot phase, temperature, console.
	PageHost
	// PageBMC is the controller itself: version, uptime, load.
	PageBMC
	// PageAlerts lists what is wrong. It is only in the rotation while
	// there is something to list.
	PageAlerts
)

// Pages is the rotation for st, in display order. Alerts go first when there
// are any: they are the reason to look at the display at all, and the
// rotation comes back round to them soonest from there.
func Pages(st Status) []Page {
	if len(st.alerts()) > 0 {
		return []Page{PageAlerts, PageNetwork, PageHost, PageBMC}
	}
	return []Page{PageNetwork, PageHost, PageBMC}
}

// Render draws st with the given page in the body area.
func Render(st Status, page Page, lay Layout) Frame {
	var f Frame
	drawHeader(&f, lay.bandTop(), st)

	var lines []string
	switch page {
	case PageNetwork:
		lines = networkLines(st.Net)
	case PageHost:
		lines = hostLines(st)
	case PageBMC:
		lines = bmcLines(st.BMC)
	case PageAlerts:
		lines = alertLines(st.alerts())
	}

	top := lay.bodyTop()
	for i, line := range lines {
		if i >= bodyLines {
			break
		}
		f.Text(leftEdge, top+i*lineH+lineInset, clip(line, Cols))
	}
	return f
}

// RenderSplash draws the header with one short message filling the body at
// double size. It is what a power transition looks like: the change is the
// thing someone standing at the machine just caused, and it should be
// readable from further away than the detail pages are.
func RenderSplash(st Status, msg string, lay Layout) Frame {
	const scale = 2

	var f Frame
	drawHeader(&f, lay.bandTop(), st)

	msg = clip(msg, Cols/scale)
	w := TextWidth(msg) * scale
	x := leftEdge + (rightEdge-leftEdge-w)/2
	y := lay.bodyTop() + (bodyH-glyphH*scale)/2
	f.TextScaled(x, y, scale, msg)
	return f
}

// drawHeader fills the band: the BMC's hostname on the left, the host's power
// state on the right, and between them a marker while anything is wrong.
//
// The power state is a lamp and a word: a solid disc for on, a ring for off.
// The shape is legible before the two letters are, and neither depends on
// dark-on-light text — on an OLED the lit surround blooms over one-pixel
// dark strokes, so inverse video is kept to the single-stroke alert mark.
func drawHeader(f *Frame, top int, st Status) {
	y := top + lineInset

	label, lamp := "--", bitmap("")
	switch st.Host.Power {
	case PowerOn:
		label, lamp = "ON", lampOn
	case PowerOff:
		label, lamp = "OFF", lampOff
	case PowerUnknown:
	}

	// A glyph cell ends in a blank spacing column; let that one hang past
	// the edge so the last stroke of the word is what lines up with it.
	x := rightEdge - TextWidth(label) + 1
	f.Text(x, y, label)
	if lamp != "" {
		x -= lampGap + lamp.width()
		f.draw(x, y+capTop, lamp)
	}

	nameEnd := x - headerGap
	if len(st.alerts()) > 0 {
		mark := image.Rect(nameEnd-glyphW-2, y, nameEnd, y+glyphH)
		f.Text(mark.Min.X+2, y, "!")
		f.Invert(mark)
		nameEnd = mark.Min.X - headerGap
	}

	name := st.Hostname
	if name == "" {
		name = "BMC"
	}
	f.Text(leftEdge, y, clip(name, (nameEnd-leftEdge)/glyphW))
}

// Header spacing.
const (
	// capTop is the row within a glyph cell where capital letters start;
	// they are nine rows tall, which is what the lamps are sized to.
	capTop = 2
	// lampGap separates the lamp from its word, headerGap the hostname
	// from whatever sits to its right.
	lampGap   = 3
	headerGap = 4
)

// bitmap is a small image written out as rows of text, '#' for a lit pixel,
// so the shape can be read in the source.
type bitmap string

func (b bitmap) width() int { return strings.IndexByte(string(b), '\n') }

// The power lamps: nine rows, to stand exactly as tall as the capitals
// beside them.
const (
	lampOn bitmap = `..#####..
.#######.
#########
#########
#########
#########
#########
.#######.
..#####..`

	lampOff bitmap = `..#####..
.#.....#.
#.......#
#.......#
#.......#
#.......#
#.......#
.#.....#.
..#####..`
)

// draw lights b's pixels with its top-left corner at (x, y).
func (f *Frame) draw(x, y int, b bitmap) {
	dx, dy := 0, 0
	for _, c := range b {
		if c == '\n' {
			dx, dy = 0, dy+1
			continue
		}
		if c == '#' {
			f.Set(x+dx, y+dy, true)
		}
		dx++
	}
}

func networkLines(n Net) []string {
	iface := n.Interface
	if iface == "" {
		iface = "eth0"
	}

	if !n.Link {
		return []string{"No link", iface + " down", n.MAC}
	}

	addr := n.Addr
	if addr == "" {
		// Carrier but no address is the DHCP wait; say which, since
		// "nothing" and "still asking" call for different responses.
		addr = "No address"
		if n.Mode == "dhcp" {
			addr = "DHCP..."
		}
	}

	// "eth0 1000M static" is 17 of the 18 columns; spelling the unit out
	// would push the mode off the end on exactly the links that have one.
	link := iface
	if n.SpeedMbps > 0 {
		link += fmt.Sprintf(" %dM", n.SpeedMbps)
	}
	if n.Mode != "" {
		link += " " + n.Mode
	}
	return []string{addr, link, n.MAC}
}

func hostLines(st Status) []string {
	lines := []string{"Host: " + hostPhase(st.Host)}

	if s := sensorLine(st); s != "" {
		lines = append(lines, s)
	}

	switch v := st.Video; {
	case v.Viewers == 1:
		lines = append(lines, "KVM: 1 viewer")
	case v.Viewers > 1:
		lines = append(lines, fmt.Sprintf("KVM: %d viewers", v.Viewers))
	case v.Signal && v.Width > 0 && v.Height > 0:
		lines = append(lines, fmt.Sprintf("HDMI %dx%d", v.Width, v.Height))
	}

	// The model fills whatever is left: it never changes, so it is the
	// first thing to give way to something that does.
	if len(lines) < bodyLines && st.Host.Model != "" {
		lines = append(lines, st.Host.Model)
	}
	return lines
}

// hostPhase is the host's state in a couple of words: its power state, and
// while it is on, how far its firmware says boot has got.
func hostPhase(h Host) string {
	switch h.Power {
	case PowerOff:
		return "off"
	case PowerUnknown:
		return "unknown"
	case PowerOn:
	}

	// Redfish BootProgress LastState values (DSP2046), as the host firmware
	// PATCHes them. Anything unrecognised — "None", an OEM string, a value
	// from a newer schema — falls back to plain "on", which is still true.
	switch h.BootProgress {
	case "PrimaryProcessorInitializationStarted", "SecondaryProcessorInitializationStarted":
		return "CPU init"
	case "MemoryInitializationStarted":
		return "memory init"
	case "BusInitializationStarted":
		return "bus init"
	case "PCIResourceConfigStarted":
		return "PCI config"
	case "SystemHardwareInitializationComplete":
		return "firmware"
	case "SetupEntered":
		return "in setup"
	case "OSBootStarted":
		return "OS booting"
	case "OSRunning":
		return "OS running"
	}
	return "on"
}

// sensorLine is the host's temperature and fan, or "" when there is nothing
// current to show — no telemetry channel, a host that is off, or one that has
// stopped reporting. A stale reading is dropped rather than shown: a
// plausible temperature for a machine that is switched off is worse than a
// blank line.
func sensorLine(st Status) string {
	sn := st.Sensors
	if !sn.Present || sn.Stale || st.Host.Power == PowerOff {
		return ""
	}

	var parts []string
	if sn.TempValid {
		parts = append(parts, formatTemp(sn.TempC))
	}
	if sn.FanValid {
		parts = append(parts, fmt.Sprintf("fan %.0f%%", sn.FanDutyPct))
	}
	return strings.Join(parts, "  ")
}

func bmcLines(b BMC) []string {
	version := b.Version
	if version == "" {
		version = "dev"
	}
	lines := []string{"BMC " + version, "Up " + formatUptime(b.Uptime)}

	var load []string
	if b.CPUValid {
		load = append(load, fmt.Sprintf("CPU %.0f%%", b.CPUPercent))
	}
	if b.MemValid {
		load = append(load, fmt.Sprintf("Mem %.0f%%", b.MemPercent))
	}
	if len(load) > 0 {
		lines = append(lines, strings.Join(load, "  "))
	}
	return lines
}

func alertLines(alerts []string) []string {
	lines := make([]string, 0, len(alerts))
	for _, a := range alerts {
		lines = append(lines, "! "+a)
	}
	return lines
}

func formatTemp(c float64) string {
	return fmt.Sprintf("%.1f%cC", c, degree)
}

// formatUptime renders d in its two or three most significant units. Seconds
// are left out on purpose: a value that changes every second would make the
// display redraw every second for a digit nobody reads.
func formatUptime(d time.Duration) string {
	mins := int(d / time.Minute)
	days, hours, mins := mins/(24*60), mins/60%24, mins%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// clip truncates s to at most n characters.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
