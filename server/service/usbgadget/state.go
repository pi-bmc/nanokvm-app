package usbgadget

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/pi-bmc/nanokvm-app/server/config"
)

// State holds the runtime function toggles that persist across reboots,
// independent of the (identity/settings) config. Persisted as JSON at
// cfg.StatePath on the /data partition.
type State struct {
	Ethernet string `json:"ethernet"` // "off" | "ecm" | "ncm"
	Disk     bool   `json:"disk"`     // whether mass_storage.disk0 is linked into c.1
}

func defaultState() State {
	return State{Ethernet: EthernetOff, Disk: true}
}

// loadState reads the persisted state. ok is false when the file is absent —
// that absence is the first-run sentinel that triggers migrateFromBoot.
func loadState(path string) (State, bool) {
	if path == "" {
		return defaultState(), false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultState(), false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		log.Warnf("usbgadget: corrupt state file %s: %v", path, err)
		return defaultState(), false
	}
	if s.Ethernet == "" {
		s.Ethernet = EthernetOff
	}
	return s, true
}

// saveState atomically persists the state to path (tmp write + rename).
func saveState(path string, s State) error {
	if path == "" {
		return fmt.Errorf("state path not configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// bootFlag reports whether a /boot flag file exists.
func bootFlag(name string) bool {
	_, err := os.Stat(filepath.Join(bootDir, name))
	return err == nil
}

// bootValue reads and trims a /boot value file, returning "" when absent/empty.
func bootValue(name string) string {
	data, err := os.ReadFile(filepath.Join(bootDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// computeMigration derives the migrated UsbGadget config and the initial
// runtime State from the legacy /boot flag files, given the current config. It
// is pure apart from reading bootDir, which keeps it unit-testable.
func computeMigration(cur config.UsbGadget) (config.UsbGadget, State) {
	// The gadget was historically always built (S03usbdev ran unconditionally
	// at boot), so Enabled=true.
	cur.Enabled = true
	if vid := bootValue("usb.vid"); vid != "" {
		cur.VendorID = vid
	}
	if pid := bootValue("usb.pid"); pid != "" {
		cur.ProductID = pid
	}
	cur.HID = !bootFlag("disable_hid")
	cur.BIOSMode = bootFlag("BIOS")
	cur.WakeupOnWrite = !bootFlag("usb.notwakeup")
	cur.BindUDC = !bootFlag("udc.disable")

	st := defaultState()
	// usb.ncm takes precedence over usb.ecm0, matching the old script's if/else.
	switch {
	case bootFlag("usb.ncm"):
		st.Ethernet = EthernetNCM
	case bootFlag("usb.ecm0"):
		st.Ethernet = EthernetECM
	default:
		st.Ethernet = EthernetOff
	}
	// The mass-storage disk is always enabled: the firmware boot image is
	// presented on its lun.0 and a default-off disk would break host boot. This
	// is a deliberate departure from the literal legacy reading
	// (disk = exists(/boot/usb.disk0)); the previous /boot/usb.disk0 UI toggle
	// only ever gated whether the host saw the disk, never the BMC's need for it.
	st.Disk = true

	return cur, st
}

// migrateFromBoot performs the one-time migration from the legacy /boot flag
// files to the server config + runtime state. It runs only on the first boot
// after this feature ships, detected by the absence of the state file, and is
// the authoritative seeder for existing devices' identity and toggles.
//
// It leaves the /boot flag files in place (a rollback to the old image still
// reads them) but they no longer have any effect under this server. Caller
// holds g.mu.
func (g *Gadget) migrateFromBoot() error {
	cfg := config.GetInstance()
	statePath := cfg.UsbGadget.StatePath

	// Already migrated? The state file is the sentinel.
	if _, ok := loadState(statePath); ok {
		return nil
	}

	newCfg, st := computeMigration(cfg.UsbGadget)
	cfg.UsbGadget = newCfg

	config.Save()
	if err := saveState(statePath, st); err != nil {
		return fmt.Errorf("write initial state: %w", err)
	}
	log.Infof("usbgadget: migrated from /boot flags (vid=%s pid=%s hid=%v bios=%v wakeup=%v bindUDC=%v ethernet=%s)",
		newCfg.VendorID, newCfg.ProductID, newCfg.HID,
		newCfg.BIOSMode, newCfg.WakeupOnWrite, newCfg.BindUDC, st.Ethernet)
	return nil
}
