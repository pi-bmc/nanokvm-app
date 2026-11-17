package oled

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden frames in testdata")

// art renders a frame as text, one character per pixel, so a golden file is
// readable in a diff and a failure shows the picture that was drawn.
func art(f *Frame) string {
	var b strings.Builder
	for y := range Height {
		for x := range Width {
			if f.Lit(x, y) {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestGoldenFrames pins the screens pixel for pixel. The goldens were checked
// by eye against TestPreview's PNGs; regenerate with
//
//	go test ./pkg/device/oled -run TestGoldenFrames -update
//
// and look at the previews again before committing the result.
func TestGoldenFrames(t *testing.T) {
	upright, flipped := Layout{}, Layout{BandAtBottom: true}

	frames := map[string]Frame{
		"network":      Render(sampleStatus(), PageNetwork, upright),
		"host":         Render(sampleStatus(), PageHost, upright),
		"bmc":          Render(sampleStatus(), PageBMC, upright),
		"alerts":       Render(alertStatus(), PageAlerts, upright),
		"host-off":     Render(offStatus(), PageHost, upright),
		"splash-on":    RenderSplash(sampleStatus(), "POWER ON", upright),
		"network-flip": Render(sampleStatus(), PageNetwork, flipped),
	}

	for name, f := range frames {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", name+".txt")
			got := art(&f)

			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("frame differs from %s; got:\n%s", path, got)
			}
		})
	}
}

// allScreens is every kind of frame the display can show, for the layout
// invariants below. The statuses are the extremes: everything populated,
// everything wrong, and the widest strings each line can carry.
func allScreens(lay Layout) map[string]Frame {
	wide := sampleStatus()
	wide.Hostname = "a-very-long-bmc-hostname.example"
	wide.Host.Power = PowerOff
	wide.Host.Model = "Some Vendor Extremely Long Model Name"
	wide.Net.Addr = "192.168.100.200"
	wide.Net.SpeedMbps = 1000
	wide.Net.Mode = "static"
	wide.BMC.Version = "v12.34.56-123-gdeadbeef-dirty"
	wide.BMC.Uptime = 999 * 24 * time.Hour

	out := map[string]Frame{
		"splash-on":  RenderSplash(sampleStatus(), "POWER ON", lay),
		"splash-off": RenderSplash(offStatus(), "POWER OFF", lay),
	}
	for name, st := range map[string]Status{
		"sample": sampleStatus(), "alert": alertStatus(), "off": offStatus(), "wide": wide, "zero": {},
	} {
		for _, p := range []Page{PageNetwork, PageHost, PageBMC, PageAlerts} {
			out[name+"/"+pageName(p)] = Render(st, p, lay)
		}
	}
	return out
}

func pageName(p Page) string {
	switch p {
	case PageNetwork:
		return "network"
	case PageHost:
		return "host"
	case PageBMC:
		return "bmc"
	case PageAlerts:
		return "alerts"
	}
	return "?"
}

// The yellow and blue zones are separate pieces of glass with a gap between
// them. A row lit on either side of that boundary means a glyph is touching
// the gap — or has been split across the two colours.
func TestNothingTouchesTheColourBoundary(t *testing.T) {
	for _, lay := range []Layout{{}, {BandAtBottom: true}} {
		// The two rows that face each other across the gap.
		a, b := bandH-1, bandH
		if lay.BandAtBottom {
			a, b = bodyH-1, bodyH
		}

		for name, f := range allScreens(lay) {
			for _, o := range orbit {
				s := f.shift(o.X, o.Y)
				for x := range Width {
					if s.Lit(x, a) || s.Lit(x, b) {
						t.Errorf("%s (bandAtBottom=%v, orbit %v): pixel on the boundary at x=%d",
							name, lay.BandAtBottom, o, x)
						break
					}
				}
			}
		}
	}
}

// The orbit moves the image by up to a pixel right and down. Every layout has
// to leave that margin free, or the orbit shaves the edge off it.
func TestOrbitNeverClipsAScreen(t *testing.T) {
	for _, lay := range []Layout{{}, {BandAtBottom: true}} {
		for name, f := range allScreens(lay) {
			want := lit(&f)
			for _, o := range orbit {
				s := f.shift(o.X, o.Y)
				if got := lit(&s); got != want {
					t.Errorf("%s (bandAtBottom=%v): orbit %v leaves %d of %d pixels",
						name, lay.BandAtBottom, o, got, want)
				}
			}
		}
	}
}

// Whatever the status, the header must stay in the band and the page in the
// body: flipping the layout swaps the two regions and nothing else.
func TestHeaderAndBodyStayInTheirOwnZones(t *testing.T) {
	st := sampleStatus()

	headerOnly := Render(st, Page(-1), Layout{})
	for y := bandH; y < Height; y++ {
		for x := range Width {
			if headerOnly.Lit(x, y) {
				t.Fatalf("header drew outside the band at (%d,%d)", x, y)
			}
		}
	}
	if lit(&headerOnly) == 0 {
		t.Fatal("header drew nothing")
	}

	full := Render(st, PageNetwork, Layout{})
	flip := Render(st, PageNetwork, Layout{BandAtBottom: true})
	for y := range Height {
		// Band rows map to the bottom 16; body rows move up by 16.
		fy := y + bodyH
		if y >= bandH {
			fy = y - bandH
		}
		for x := range Width {
			if full.Lit(x, y) != flip.Lit(x, fy) {
				t.Fatalf("flipped layout is not the upright one with the zones swapped, at (%d,%d)", x, y)
			}
		}
	}
}

func TestPagesPutAlertsFirstOnlyWhenThereAreSome(t *testing.T) {
	if got := Pages(sampleStatus()); slices.Contains(got, PageAlerts) {
		t.Errorf("healthy host has an alerts page in rotation: %v", got)
	}
	got := Pages(alertStatus())
	if len(got) == 0 || got[0] != PageAlerts {
		t.Errorf("alerts are not first in rotation: %v", got)
	}
}

func TestAlerts(t *testing.T) {
	st := alertStatus()
	want := []string{"Under-voltage", "Hot: 82.1°C", "Throttled"}
	if got := st.alerts(); !slices.Equal(got, want) {
		t.Errorf("alerts = %q, want %q", got, want)
	}

	// The record a powered-off host leaves behind is history, and so is
	// one from a host that has stopped reporting.
	off := alertStatus()
	off.Host.Power = PowerOff
	if got := off.alerts(); len(got) != 0 {
		t.Errorf("powered-off host raises alerts: %q", got)
	}
	stale := alertStatus()
	stale.Sensors.Stale = true
	if got := stale.alerts(); len(got) != 0 {
		t.Errorf("stale sensors raise alerts: %q", got)
	}

	// No warn threshold means no opinion on "hot", not "always hot".
	noWarn := sampleStatus()
	noWarn.Sensors.TempWarnC = 0
	noWarn.Sensors.TempC = 200
	if got := noWarn.alerts(); len(got) != 0 {
		t.Errorf("temperature alert without a threshold: %q", got)
	}
}

func TestNetworkLines(t *testing.T) {
	base := sampleStatus().Net

	tests := []struct {
		name string
		mut  func(*Net)
		want []string
	}{
		{"up", func(*Net) {}, []string{"10.0.197.82", "eth0 100M dhcp", "48:da:35:6f:f2:e0"}},
		{"no carrier", func(n *Net) { n.Link = false }, []string{"No link", "eth0 down", "48:da:35:6f:f2:e0"}},
		{"dhcp pending", func(n *Net) { n.Addr = "" }, []string{"DHCP...", "eth0 100M dhcp", "48:da:35:6f:f2:e0"}},
		{"static unset", func(n *Net) { n.Addr, n.Mode = "", "static" }, []string{"No address", "eth0 100M static", "48:da:35:6f:f2:e0"}},
		{"speed unknown", func(n *Net) { n.SpeedMbps = 0 }, []string{"10.0.197.82", "eth0 dhcp", "48:da:35:6f:f2:e0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := base
			tt.mut(&n)
			if got := networkLines(n); !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	// The widest link line a real board produces must fit without clipping.
	if l := "eth0 1000M static"; len(l) > Cols {
		t.Errorf("%q is %d columns, more than the panel's %d", l, len(l), Cols)
	}
}

func TestHostLines(t *testing.T) {
	st := sampleStatus()
	want := []string{"Host: OS running", "48.2°C  fan 62%", "HDMI 1920x1080"}
	if got := hostLines(st); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	// Someone watching the console outranks the video mode.
	st.Video.Viewers = 2
	if got := hostLines(st)[2]; got != "KVM: 2 viewers" {
		t.Errorf("line 3 = %q, want the viewer count", got)
	}
	st.Video.Viewers = 1
	if got := hostLines(st)[2]; got != "KVM: 1 viewer" {
		t.Errorf("line 3 = %q", got)
	}

	// A powered-off host shows no sensor line (its last record is stale
	// history) and the model takes the free space.
	if got, want := hostLines(offStatus()), []string{"Host: off", "Raspberry Pi 5"}; !slices.Equal(got, want) {
		t.Errorf("off: got %q, want %q", got, want)
	}

	// The sensor record outlives the host by up to its staleness window:
	// for that long a host that has just been switched off still has a
	// perfectly valid-looking temperature on file. Power state wins.
	justOff := sampleStatus()
	justOff.Host.Power = PowerOff
	justOff.Video = Video{}
	if got, want := hostLines(justOff), []string{"Host: off", "Raspberry Pi 5"}; !slices.Equal(got, want) {
		t.Errorf("just powered off: got %q, want %q", got, want)
	}

	// On a board whose sense line is not wired, say so.
	unknown := Status{}
	if got := hostLines(unknown); !slices.Equal(got, []string{"Host: unknown"}) {
		t.Errorf("unknown: got %q", got)
	}
}

func TestHostPhase(t *testing.T) {
	tests := map[string]string{
		"":                                      "on",
		"None":                                  "on",
		"OEM":                                   "on",
		"PrimaryProcessorInitializationStarted": "CPU init",
		"MemoryInitializationStarted":           "memory init",
		"BusInitializationStarted":              "bus init",
		"PCIResourceConfigStarted":              "PCI config",
		"SystemHardwareInitializationComplete":  "firmware",
		"SetupEntered":                          "in setup",
		"OSBootStarted":                         "OS booting",
		"OSRunning":                             "OS running",
	}
	for progress, want := range tests {
		if got := hostPhase(Host{Power: PowerOn, BootProgress: progress}); got != want {
			t.Errorf("hostPhase(on, %q) = %q, want %q", progress, got, want)
		}
		if len("Host: "+want) > Cols {
			t.Errorf("%q does not fit the line", want)
		}
	}

	// BootProgress is persisted; a host that is off still carries its last.
	if got := hostPhase(Host{Power: PowerOff, BootProgress: "OSRunning"}); got != "off" {
		t.Errorf("powered-off host reports %q", got)
	}
}

func TestBMCLines(t *testing.T) {
	want := []string{"BMC v2.3.16", "Up 6d 2h 14m", "CPU 12%  Mem 41%"}
	if got := bmcLines(sampleStatus().BMC); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	// Before the resource sampler's first reading there is no load line,
	// rather than a 0% that is not a measurement.
	if got := bmcLines(BMC{}); !slices.Equal(got, []string{"BMC dev", "Up 0m"}) {
		t.Errorf("zero BMC: got %q", got)
	}
}

func TestFormatUptime(t *testing.T) {
	tests := map[time.Duration]string{
		0:                            "0m",
		59 * time.Second:             "0m",
		14 * time.Minute:             "14m",
		2*time.Hour + 14*time.Minute: "2h 14m",
		24 * time.Hour:               "1d 0h 0m",
		6*24*time.Hour + 2*time.Hour + 14*time.Minute: "6d 2h 14m",
	}
	for d, want := range tests {
		if got := formatUptime(d); got != want {
			t.Errorf("formatUptime(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("hostname", 4); got != "host" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("ok", 4); got != "ok" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("48.2°C", 5); got != "48.2°" {
		t.Errorf("clip counts bytes, not characters: %q", got)
	}
	if got := clip("x", 0); got != "" {
		t.Errorf("clip to zero = %q", got)
	}
}
