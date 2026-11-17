package main

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pi-bmc/nanokvm-app/api/redfish"
	"github.com/pi-bmc/nanokvm-app/pkg/config"
	"github.com/pi-bmc/nanokvm-app/pkg/device/hostsensor"
	"github.com/pi-bmc/nanokvm-app/pkg/device/oled"
	"github.com/pi-bmc/nanokvm-app/pkg/platform/sysinfo"
)

// displaySvc drives the front-panel OLED. Built in initialize() and stopped in
// disposeAll(); nil only before initialize() has run.
var displaySvc *oled.Service

// newDisplay builds the status display over the composition root's own
// controllers. It lives here rather than in pkg/device/oled because the
// display is the one consumer that reads across every subsystem at once —
// power, network, the Redfish host reports, the sensor channel, the video hub
// — and the composition root is the only place allowed to know all of them.
// The display package sees only the oled.Status it is handed.
func newDisplay(cfg *config.Config) *oled.Service {
	return oled.New(cfg.Display, func(ctx context.Context) oled.Status {
		return displayStatus(ctx, cfg)
	}, rootLog.With("component", "display"))
}

// displayStatus samples everything the display shows. It runs once a second
// on the display's goroutine, so every source here is one that is cheap to
// ask: cached state, an in-memory snapshot, or a single small file.
func displayStatus(ctx context.Context, cfg *config.Config) oled.Status {
	hostname, _ := os.Hostname()
	reported, _ := redfish.HostReported()

	st := oled.Status{
		Hostname: hostname,
		Host: oled.Host{
			Power:        displayPower(ctx),
			BootProgress: reported.BootProgress,
			Model:        reported.Model,
		},
		Net:     displayNet(cfg.Network.Eth0),
		Sensors: displaySensors(),
		BMC:     displayBMC(),
	}

	if videoHub != nil {
		v := videoHub.State()
		st.Video = oled.Video{
			Viewers: videoHub.Sessions(),
			Signal:  v.Ready,
			Width:   v.Width,
			Height:  v.Height,
		}
	}
	return st
}

// displayPower maps the power controller's answer onto the display's three
// states. An error is "unknown", not "off": on a board whose sense line did
// not resolve, the BMC has no opinion and the display must not invent one.
func displayPower(ctx context.Context) oled.PowerState {
	on, err := powerCtrl.State(ctx)
	switch {
	case err != nil:
		return oled.PowerUnknown
	case on:
		return oled.PowerOn
	default:
		return oled.PowerOff
	}
}

// displayNet reads the uplink straight from the kernel rather than from the
// network manager's own view of what it configured: the display's job is to
// show the address that actually answers, including one somebody set by hand.
func displayNet(eth config.InterfaceConfig) oled.Net {
	n := oled.Net{Interface: eth.Name, Mode: eth.Mode}

	iface, err := net.InterfaceByName(eth.Name)
	if err != nil {
		return n
	}
	n.MAC = iface.HardwareAddr.String()
	// FlagRunning is the kernel's IFF_RUNNING: carrier, not merely admin-up.
	n.Link = iface.Flags&net.FlagRunning != 0

	if addrs, err := iface.Addrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				n.Addr = ipn.IP.String()
				break
			}
		}
	}

	if n.Link {
		n.SpeedMbps = linkSpeedMbps(eth.Name)
	}
	return n
}

// linkSpeedMbps reads the negotiated speed from sysfs, or 0 when the driver
// does not report one. The file reads as -1 (or fails with EINVAL) while the
// link is down, which is the same answer.
func linkSpeedMbps(iface string) int {
	raw, err := os.ReadFile("/sys/class/net/" + iface + "/speed")
	if err != nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// displaySensors reads the host's self-telemetry through the board-agnostic
// seam, so a board with no channel registered reports Present=false and the
// display leaves the line out rather than showing a dash for a sensor that
// does not exist.
func displaySensors() oled.Sensors {
	src, ok := hostsensor.Get()
	if !ok {
		return oled.Sensors{}
	}

	r, ok := src.Latest()
	if !ok {
		// A channel that has never produced a reading — the host has
		// not booted far enough to push one — is, for the display,
		// the same as one that has stopped.
		return oled.Sensors{Present: true, Stale: true}
	}

	s := oled.Sensors{
		Present:    true,
		Stale:      r.Stale,
		TempC:      r.TempC,
		TempValid:  r.TempValid,
		TempWarnC:  src.Thresholds().TempWarnC,
		FanDutyPct: r.FanDutyPct,
		FanValid:   r.FanValid,
	}
	if r.ThrottleValid {
		s.UnderVoltage = r.Condition(hostsensor.ConditionUnderVoltage)
		s.Throttled = r.Condition(hostsensor.ConditionThrottled)
		s.FreqCapped = r.Condition(hostsensor.ConditionFreqCapped)
		s.SoftTempLimit = r.Condition(hostsensor.ConditionSoftTempLimit)
	}
	return s
}

// displayBMC reports the controller's own version, uptime and load. The load
// figures come from the resource sampler that is already running for the
// overview graphs, so the display adds no sampling of its own.
func displayBMC() oled.BMC {
	u := sysinfo.LatestUsage()
	b := oled.BMC{
		Version:    version,
		CPUPercent: u.CPUPercent,
		CPUValid:   u.CPUValid,
		MemPercent: u.MemPercent,
		MemValid:   u.MemValid,
	}

	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		b.Uptime = time.Duration(si.Uptime) * time.Second
	}
	return b
}
