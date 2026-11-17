package oled

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sampleStatus is a host that is up and healthy, with every optional field
// populated — the busiest the pages get.
func sampleStatus() Status {
	return Status{
		Hostname: "licheervnano",
		Host: Host{
			Power:        PowerOn,
			BootProgress: "OSRunning",
			Model:        "Raspberry Pi 5",
		},
		Net: Net{
			Interface: "eth0",
			Addr:      "10.0.197.82",
			MAC:       "48:da:35:6f:f2:e0",
			Link:      true,
			SpeedMbps: 100,
			Mode:      "dhcp",
		},
		Sensors: Sensors{
			Present:    true,
			TempC:      48.2,
			TempValid:  true,
			TempWarnC:  80,
			FanDutyPct: 62,
			FanValid:   true,
		},
		Video: Video{Signal: true, Width: 1920, Height: 1080},
		BMC: BMC{
			Version:    "v2.3.16",
			Uptime:     6*24*time.Hour + 2*time.Hour + 14*time.Minute,
			CPUPercent: 12,
			CPUValid:   true,
			MemPercent: 41,
			MemValid:   true,
		},
	}
}

// alertStatus is sampleStatus with the host in trouble.
func alertStatus() Status {
	st := sampleStatus()
	st.Sensors.TempC = 82.1
	st.Sensors.UnderVoltage = true
	st.Sensors.Throttled = true
	return st
}

// offStatus is a powered-off host on a board with no telemetry channel.
func offStatus() Status {
	st := sampleStatus()
	st.Host.Power = PowerOff
	st.Sensors = Sensors{}
	st.Video = Video{}
	return st
}

// TestPreview writes every screen as an enlarged, tinted PNG when
// OLED_PREVIEW_DIR is set, and is skipped otherwise:
//
//	OLED_PREVIEW_DIR=/some/dir go test ./pkg/device/oled -run TestPreview
//
// It exists because the display is the one output of this program that
// cannot be judged from a test assertion. The tint follows the glass of a
// yellow/blue module — including the dark gap between the two zones, which
// is what a layout has to respect — so the image is close to what the panel
// shows.
func TestPreview(t *testing.T) {
	dir := os.Getenv("OLED_PREVIEW_DIR")
	if dir == "" {
		t.Skip("OLED_PREVIEW_DIR not set")
	}

	upright, flipped := Layout{}, Layout{BandAtBottom: true}
	screens := map[string]struct {
		frame Frame
		lay   Layout
	}{
		"network":       {Render(sampleStatus(), PageNetwork, upright), upright},
		"host":          {Render(sampleStatus(), PageHost, upright), upright},
		"bmc":           {Render(sampleStatus(), PageBMC, upright), upright},
		"alerts":        {Render(alertStatus(), PageAlerts, upright), upright},
		"host-off":      {Render(offStatus(), PageHost, upright), upright},
		"splash-on":     {RenderSplash(sampleStatus(), "POWER ON", upright), upright},
		"splash-off":    {RenderSplash(offStatus(), "POWER OFF", upright), upright},
		"network-flip":  {Render(sampleStatus(), PageNetwork, flipped), flipped},
		"network-orbit": {orbited(Render(sampleStatus(), PageNetwork, upright), 2), upright},
	}

	for name, s := range screens {
		path := filepath.Join(dir, name+".png")
		if err := writePreview(path, &s.frame, s.lay); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("wrote %s", path)
	}
}

func orbited(f Frame, step int) Frame {
	o := orbit[step%len(orbit)]
	return f.shift(o.X, o.Y)
}

// writePreview renders f at 4x with the panel's colours.
func writePreview(path string, f *Frame, lay Layout) error {
	const scale, gap = 4, 6

	yellow := color.RGBA{R: 0xFF, G: 0xD8, B: 0x1F, A: 0xFF}
	blue := color.RGBA{R: 0x3B, G: 0xA8, B: 0xFF, A: 0xFF}
	off := color.RGBA{R: 0x06, G: 0x08, B: 0x0C, A: 0xFF}

	img := image.NewRGBA(image.Rect(0, 0, Width*scale, Height*scale+gap))
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			img.SetRGBA(x, y, off)
		}
	}

	bandTop := lay.bandTop()
	for y := range Height {
		inBand := y >= bandTop && y < bandTop+bandH
		// The gap sits between the zones, wherever the band is.
		oy := y * scale
		if (bandTop == 0 && !inBand) || (bandTop != 0 && inBand) {
			oy += gap
		}
		c := blue
		if inBand {
			c = yellow
		}
		for x := range Width {
			if !f.Lit(x, y) {
				continue
			}
			// Leave a one-pixel grid between dots, as the glass does.
			for dy := range scale - 1 {
				for dx := range scale - 1 {
					img.SetRGBA(x*scale+dx, oy+dy, c)
				}
			}
		}
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(out, img); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
