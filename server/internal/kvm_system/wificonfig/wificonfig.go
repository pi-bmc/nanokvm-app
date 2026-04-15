// Package wificonfig manages the WiFi AP configuration flow for NanoKVM.
// It is a Go port of the C++ WiFi config logic from
// support/sg2002/kvm_system/main/lib/system_ctrl/system_ctrl.cpp.
//
// The state machine drives a captive-AP workflow:
//
//	State 0 – generate WPA2 passphrase, start AP mode
//	State 1 – wait for a station to associate
//	State 2 – wait for SSID/password submission via the web UI
//	State 3 – restart WiFi in station mode and check connectivity
package wificonfig

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// ConfigState represents the current step in the WiFi configuration flow.
type ConfigState int

const (
	StateIdle        ConfigState = -1 // not configuring
	StateStarting    ConfigState = 0  // generate passphrase, start AP
	StateWaitConnect ConfigState = 1  // wait for station association
	StateWaitCreds   ConfigState = 2  // wait for SSID/password submission
	StateTryConnect  ConfigState = 3  // restart WiFi, verify connection
)

const (
	apPassPath       = "/kvmapp/kvm/ap.pass"
	apSSIDPath       = "/kvmapp/kvm/ap.ssid"
	tryConnectPath   = "/kvmapp/kvm/wifi_try_connect"
	hostapdConfPath  = "/etc/hostapd.conf"
	udhcpdConfPath   = "/etc/udhcpd.wlan0.conf"
	wifiInitScript   = "/etc/init.d/S30wifi"
	kvmPasswordPath  = "/etc/kvm/pwd"
	wlanInterface    = "wlan0"
	defaultAPSSID    = "NanoKVM"
)

const hostapdConfTemplate = `ctrl_interface=/var/run/hostapd
ctrl_interface_group=0
ssid=NanoKVM
hw_mode=g
channel=1
beacon_int=100
dtim_period=2
max_num_sta=255
rts_threshold=-1
fragm_threshold=-1
macaddr_acl=0
auth_algs=3
wpa=2
wpa_passphrase=%s
ieee80211n=1
`

const udhcpdConf = `start 10.10.10.100
end 10.10.10.200
interface wlan0
pidfile /var/run/udhcpd.wlan0.pid
lease_file /var/lib/misc/udhcpd.wlan0.leases
option subnet 255.255.255.0
option lease 864000
`

// WiFiConfig drives the AP-based WiFi configuration state machine.
type WiFiConfig struct {
	mu         sync.Mutex
	State      ConfigState
	APPassword string
}

// New returns a WiFiConfig in the idle state.
func New() *WiFiConfig {
	return &WiFiConfig{
		State: StateIdle,
	}
}

// Start begins the WiFi configuration process. It transitions from idle
// to the starting state so the next call to Process will generate a
// passphrase and bring up the AP.
func (w *WiFiConfig) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.State != StateIdle {
		return fmt.Errorf("wificonfig: already in progress (state %d)", w.State)
	}
	log.Info("wificonfig: starting WiFi configuration")
	w.State = StateStarting
	return nil
}

// Process advances the state machine by one step. It should be called
// periodically (roughly every 1 s). The returned ConfigState reflects
// the state after the step completes.
func (w *WiFiConfig) Process() (ConfigState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.State {
	case StateIdle:
		// Nothing to do.

	case StateStarting:
		pass := generatePassphrase()
		w.APPassword = pass

		if err := os.WriteFile(apSSIDPath, []byte(defaultAPSSID+"\n"), 0644); err != nil {
			return w.State, fmt.Errorf("wificonfig: write ap.ssid: %w", err)
		}
		if err := os.WriteFile(apPassPath, []byte(pass+"\n"), 0644); err != nil {
			return w.State, fmt.Errorf("wificonfig: write ap.pass: %w", err)
		}
		if err := writeHostapdConf(pass); err != nil {
			return w.State, fmt.Errorf("wificonfig: write hostapd.conf: %w", err)
		}
		if err := writeUdhcpdConf(); err != nil {
			return w.State, fmt.Errorf("wificonfig: write udhcpd.conf: %w", err)
		}

		if err := runCommand(wifiInitScript, "ap"); err != nil {
			return w.State, fmt.Errorf("wificonfig: start AP: %w", err)
		}

		log.WithField("password", pass).Info("wificonfig: AP started")
		w.State = StateWaitConnect

	case StateWaitConnect:
		if StaConnected() {
			log.Info("wificonfig: station connected to AP")
			w.State = StateWaitCreds
		}

	case StateWaitCreds:
		if SSIDPassOK() {
			log.Info("wificonfig: credentials submitted, restarting WiFi")
			os.Remove(tryConnectPath)
			if err := RestartWiFi(); err != nil {
				return w.State, fmt.Errorf("wificonfig: restart WiFi: %w", err)
			}
			w.State = StateTryConnect
		}

	case StateTryConnect:
		if WiFiConnected() {
			log.Info("wificonfig: WiFi connected successfully")
			w.State = StateIdle
		} else {
			log.Warn("wificonfig: WiFi connection failed, restarting AP")
			w.State = StateStarting
		}
	}

	return w.State, nil
}

// WebProcess handles the web-based WiFi configuration shortcut.
// When credentials are submitted via the web UI (while the AP flow may
// or may not be active), this attempts an immediate connect cycle.
func (w *WiFiConfig) WebProcess() error {
	if !SSIDPassOK() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	log.Info("wificonfig: web config – credentials found, restarting WiFi")
	os.Remove(tryConnectPath)
	if err := RestartWiFi(); err != nil {
		return fmt.Errorf("wificonfig: restart WiFi: %w", err)
	}

	w.State = StateTryConnect

	// Give wpa_supplicant a moment to associate.
	time.Sleep(5 * time.Second)

	if WiFiConnected() {
		log.Info("wificonfig: web config – connected")
		w.State = StateIdle
	} else {
		log.Warn("wificonfig: web config – connection failed, restarting AP")
		w.State = StateStarting
	}
	return nil
}

// ---------------------------------------------------------------------------
// Probes — stateless helpers that inspect system state
// ---------------------------------------------------------------------------

// StaConnected reports whether at least one station is associated with
// the running hostapd instance.
func StaConnected() bool {
	out, err := exec.Command("hostapd_cli", "all", "sta").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "aid")
}

// SSIDPassOK returns true when the web UI has written the sentinel file
// indicating that new WiFi credentials are ready to try.
func SSIDPassOK() bool {
	_, err := os.Stat(tryConnectPath)
	return err == nil
}

// WiFiConnected checks wpa_supplicant on wlan0 and returns true when
// the interface has completed association (wpa_state=COMPLETED).
func WiFiConnected() bool {
	out, err := exec.Command("wpa_cli", "-i", wlanInterface, "status").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "wpa_state=") {
			return strings.TrimPrefix(line, "wpa_state=") == "COMPLETED"
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// System actions
// ---------------------------------------------------------------------------

// ResetPassword resets the root password to "root" and removes the KVM
// password sentinel file, matching the C++ kvm_reset_password behaviour.
func ResetPassword() error {
	cmd := exec.Command("bash")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("wificonfig: open bash stdin: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("wificonfig: start bash: %w", err)
	}

	lines := []string{
		"passwd root",
		"root",
		"root",
		"rm " + kvmPasswordPath,
		"sync",
		"exit",
	}
	for _, l := range lines {
		fmt.Fprintln(stdin, l)
		time.Sleep(50 * time.Millisecond)
	}
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wificonfig: bash: %w", err)
	}
	log.Info("wificonfig: root password reset to default")
	return nil
}

// RestartWiFi restarts the WiFi service via the init script.
func RestartWiFi() error {
	return runCommand(wifiInitScript, "restart")
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// generatePassphrase creates an 8-character WPA2 passphrase consisting
// of 4 pairs of repeated digits where consecutive pairs always differ.
// Example output: "33557799".
func generatePassphrase() string {
	var pass [8]byte
	for i := 0; i < 4; i++ {
		for {
			n, err := rand.Int(rand.Reader, big.NewInt(10))
			if err != nil {
				// Fallback: on the off chance crypto/rand fails,
				// just use a fixed digit. This should never happen.
				n = big.NewInt(int64(i))
			}
			digit := byte(n.Int64()) + '0'
			if i == 0 || digit != pass[(i-1)*2] {
				pass[i*2] = digit
				pass[i*2+1] = digit
				break
			}
		}
	}
	return string(pass[:])
}

func writeHostapdConf(passphrase string) error {
	data := fmt.Sprintf(hostapdConfTemplate, passphrase)
	return os.WriteFile(hostapdConfPath, []byte(data), 0644)
}

func writeUdhcpdConf() error {
	return os.WriteFile(udhcpdConfPath, []byte(udhcpdConf), 0644)
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
