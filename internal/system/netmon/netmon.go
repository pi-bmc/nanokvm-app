// Package netmon monitors network interface state for the kvm_system daemon.
// It is a Go port of the C++ system_state network monitoring logic from
// support/sg2002/kvm_system/main/lib/system_state/system_state.cpp.
package netmon

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// NICState represents the link-level state of a network interface.
type NICState int

const (
	NICStateNoExist NICState = -2
	NICStateUnknown NICState = -1
	NICStateDown    NICState = 0
	NICStateUp      NICState = 1
	NICStateRunning NICState = 2
)

const (
	ethInterface  = "eth0"
	wlanInterface = "wlan0"

	udcStatePath = "/sys/class/udc/4340000.usb/state"
	stopPingPath = "/etc/kvm/stop_ping"
	gatewayPath  = "/etc/kvm/gateway"
)

// InterfaceState holds the current state of a single network interface.
type InterfaceState struct {
	State   NICState
	IPAddr  string
	Gateway string
}

// NetworkState is the composite network state updated by the Monitor.
type NetworkState struct {
	mu       sync.RWMutex
	Eth      InterfaceState
	WiFi     InterfaceState
	USBState int8 // 0=not attached, 1=configured, -1=unknown
	PingOK   bool // whether ping to any gateway succeeded last cycle
}

// GetNICState returns the link-level state of the named interface.
// Uses the Go net package rather than raw ioctls.
func GetNICState(ifname string) NICState {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return NICStateNoExist
	}
	if iface.Flags&net.FlagUp == 0 {
		return NICStateDown
	}
	if iface.Flags&net.FlagRunning != 0 {
		return NICStateRunning
	}
	return NICStateUp
}

// GetIPAddr returns the first IPv4 address on the named interface, or "".
func GetIPAddr(ifname string) string {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	return ""
}

// GetGateway returns the default gateway IP for the named interface.
// It first checks the /etc/kvm/gateway override file (matching the C++
// behaviour for eth0), then falls back to parsing `ip route`.
func GetGateway(ifname string) string {
	// For eth0, honour the optional static gateway file.
	if ifname == ethInterface {
		if gw := readGatewayFile(); gw != "" {
			return gw
		}
	}
	return getGatewayFromRoute(ifname)
}

// readGatewayFile reads /etc/kvm/gateway if it exists.
func readGatewayFile() string {
	data, err := os.ReadFile(gatewayPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// getGatewayFromRoute parses `ip route` output for the default gateway.
// Equivalent to: ip route | grep '^default' | grep '<ifname>' | awk '{print $3}'
func getGatewayFromRoute(ifname string) string {
	out, err := exec.Command("ip", "route").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "default") {
			continue
		}
		if !strings.Contains(line, ifname) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			return fields[2]
		}
	}
	return ""
}

// PingGateway sends a single ping through the named interface and returns
// true when the gateway is reachable.
func PingGateway(ifname, gateway string) bool {
	if gateway == "" {
		return false
	}
	cmd := exec.Command("ping", "-I", ifname, "-w", "1", "-c", "1", gateway)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// WiFiExists reports whether a wlan0 interface is present on the system.
func WiFiExists() bool {
	return GetNICState(wlanInterface) != NICStateNoExist
}

// GetUSBState reads the USB gadget UDC state file.
// Returns 0 ("not attached"), 1 ("configured"), or -1 (unknown/error).
func GetUSBState() int8 {
	data, err := os.ReadFile(udcStatePath)
	if err != nil {
		return -1
	}
	s := bytes.TrimSpace(data)
	if len(s) == 0 {
		return -1
	}
	switch s[0] {
	case 'n': // "not attached"
		return 0
	case 'c': // "configured"
		return 1
	default:
		return -1
	}
}

// GetPingAllowed returns true when the stop-ping sentinel file does NOT exist,
// meaning the monitor is allowed to run gateway pings.
func GetPingAllowed() bool {
	_, err := os.Stat(stopPingPath)
	return os.IsNotExist(err)
}

// Monitor periodically polls network state and exposes a thread-safe snapshot.
type Monitor struct {
	state    NetworkState
	interval time.Duration
}

// NewMonitor creates a Monitor with a 1-second poll interval.
func NewMonitor() *Monitor {
	return &Monitor{
		interval: time.Second,
		state: NetworkState{
			Eth:  InterfaceState{State: NICStateUnknown},
			WiFi: InterfaceState{State: NICStateUnknown},
		},
	}
}

// State returns a snapshot of the current network state.
func (m *Monitor) State() NetworkState {
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()
	return NetworkState{
		Eth:      m.state.Eth,
		WiFi:     m.state.WiFi,
		USBState: m.state.USBState,
		PingOK:   m.state.PingOK,
	}
}

// Run starts the polling loop. It blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	log.Info("netmon: starting network monitor")
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		m.poll()

		select {
		case <-ctx.Done():
			log.Info("netmon: stopping network monitor")
			return
		case <-ticker.C:
		}
	}
}

// poll performs a single monitoring cycle.
func (m *Monitor) poll() {
	pingAllowed := GetPingAllowed()

	eth := pollInterface(ethInterface, pingAllowed)
	wifi := pollWiFi(pingAllowed)
	usb := GetUSBState()

	pingOK := false
	if eth.State == NICStateRunning && eth.Gateway != "" {
		pingOK = PingGateway(ethInterface, eth.Gateway)
	}
	if !pingOK && wifi.State == NICStateRunning && wifi.Gateway != "" {
		pingOK = PingGateway(wlanInterface, wifi.Gateway)
	}

	m.state.mu.Lock()
	m.state.Eth = eth
	m.state.WiFi = wifi
	m.state.USBState = usb
	m.state.PingOK = pingOK
	m.state.mu.Unlock()
}

// pollInterface gathers state for a generic interface.
func pollInterface(ifname string, pingAllowed bool) InterfaceState {
	st := InterfaceState{State: GetNICState(ifname)}
	if st.State < NICStateRunning {
		return st
	}
	st.IPAddr = GetIPAddr(ifname)
	st.Gateway = GetGateway(ifname)
	return st
}

// pollWiFi gathers WiFi state, returning NoExist early if the adapter is absent.
func pollWiFi(pingAllowed bool) InterfaceState {
	if !WiFiExists() {
		return InterfaceState{State: NICStateNoExist}
	}
	return pollInterface(wlanInterface, pingAllowed)
}
