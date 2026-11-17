// Package oled puts the BMC's view of the managed machine on a 128x64 SSD1306
// status display: is the host on, how do I reach this BMC, is anything wrong.
//
// It is written for the two-colour panels whose top 16 rows are yellow and
// the remaining 48 blue. The colours are a property of the glass, not of the
// data — the controller is 1-bit — so the layout is what makes use of them:
// a fixed header (hostname and the host's power state) owns the yellow band
// and never moves, and the blue area below it pages through the detail. A
// single-colour panel shows the same layout, just without the tint.
//
// The package is three layers that do not know about each other's concerns:
//
//   - ssd1306 (subpackage) moves a framebuffer to the glass;
//   - Render turns a Status snapshot into a Frame, and is a pure function, so
//     every page can be tested and previewed without a display;
//   - Service samples the Status once a second, decides which page is due,
//     and owns the panel's lifecycle — including it being absent, which on a
//     board with a bare pin header is the normal case.
//
// Status is plain data on purpose. What the display shows comes from half a
// dozen subsystems (power, network, Redfish host reports, the sensor
// channel, the video hub); the composition root gathers it and this package
// never imports any of them.
package oled

import "time"

// PowerState is the managed host's power state as the BMC senses it.
type PowerState int

const (
	// PowerUnknown means the sense line could not be read — an unwired
	// header, or a board whose GPIO lines did not resolve. It is shown as
	// such rather than as "off": a BMC that cannot tell must not claim to.
	PowerUnknown PowerState = iota
	PowerOff
	PowerOn
)

// Status is everything the display can show, sampled at one instant.
type Status struct {
	// Hostname is the BMC's own name — what an operator types to reach it.
	Hostname string

	Host    Host
	Net     Net
	Sensors Sensors
	Video   Video
	BMC     BMC
}

// Host is the managed machine.
type Host struct {
	Power PowerState
	// BootProgress is the host firmware's last Redfish BootProgress
	// LastState ("OSRunning", "SetupEntered", ...), or "" when it has
	// never reported one. Only meaningful while the host is on: the value
	// is persisted, so a powered-off host still carries its last one.
	BootProgress string
	// Model is the host's self-reported model string, or "".
	Model string
}

// Net is the BMC's management uplink.
type Net struct {
	// Interface is the kernel name of the uplink ("eth0").
	Interface string
	// Addr is its IPv4 address, or "" while it has none.
	Addr string
	// MAC is its hardware address in the usual colon-separated form.
	MAC string
	// Link reports carrier.
	Link bool
	// SpeedMbps is the negotiated speed, or 0 when unknown.
	SpeedMbps int
	// Mode is how the address is assigned: "dhcp" or "static".
	Mode string
}

// Sensors is what the host reports about itself over the board's telemetry
// channel. The validity flags are separate because the channel can be
// present and quiet, and a carried-forward temperature must not be shown as
// a current one.
type Sensors struct {
	// Present is false on a board with no host-telemetry channel at all.
	Present bool
	// Stale means the host has stopped reporting; the values are its last.
	Stale bool

	TempC     float64
	TempValid bool
	// TempWarnC is where the source considers the host to be running hot,
	// or 0 when it has no opinion.
	TempWarnC float64

	FanDutyPct float64
	FanValid   bool

	// The host firmware's live power-health conditions.
	UnderVoltage  bool
	Throttled     bool
	FreqCapped    bool
	SoftTempLimit bool
}

// Video is the HDMI capture path.
type Video struct {
	// Viewers is how many browsers are attached to the remote console.
	// Worth showing on the machine itself: it is the one indication at the
	// rack that someone else is looking at this host's screen.
	Viewers int
	// Signal reports a locked HDMI input; Width and Height are its mode.
	Signal bool
	Width  int
	Height int
}

// BMC is the management controller itself.
type BMC struct {
	Version string
	Uptime  time.Duration

	CPUPercent float64
	CPUValid   bool
	MemPercent float64
	MemValid   bool
}

// alerts lists what is wrong right now, most serious first, each short
// enough for one display line behind its marker.
//
// Sensor conditions only count while the host is on and still reporting. A
// powered-off host leaves its last record behind — see pkg/device/bmcsensor
// — and a throttle flag from before it was switched off is history, not an
// alert.
func (s Status) alerts() []string {
	sn := s.Sensors
	if !sn.Present || sn.Stale || s.Host.Power == PowerOff {
		return nil
	}

	var out []string
	if sn.UnderVoltage {
		out = append(out, "Under-voltage")
	}
	if sn.TempValid && sn.TempWarnC > 0 && sn.TempC >= sn.TempWarnC {
		out = append(out, "Hot: "+formatTemp(sn.TempC))
	}
	if sn.Throttled {
		out = append(out, "Throttled")
	}
	if sn.SoftTempLimit {
		out = append(out, "Soft temp limit")
	}
	if sn.FreqCapped {
		out = append(out, "Freq capped")
	}
	return out
}
