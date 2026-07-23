package usbgadget

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pi-bmc/nanokvm-app/server/config"
)

// withBootDir points the package's bootDir at a fresh temp dir for the duration
// of the test and returns the dir. It restores bootDir on cleanup.
func withBootDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := bootDir
	bootDir = dir
	t.Cleanup(func() { bootDir = prev })
	return dir
}

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// base returns a config as it would arrive with only identity defaults applied.
func base() config.UsbGadget {
	return config.UsbGadget{VendorID: "0x3346", ProductID: "0x1009"}
}

func TestComputeMigrationFreshDevice(t *testing.T) {
	withBootDir(t) // empty /boot

	cfg, st := computeMigration(base())

	if !cfg.Enabled || !cfg.HID || !cfg.WakeupOnWrite || !cfg.BindUDC {
		t.Errorf("fresh device: expected all-on defaults, got %+v", cfg)
	}
	if cfg.BIOSMode {
		t.Error("fresh device: BIOSMode should be false")
	}
	if cfg.VendorID != "0x3346" || cfg.ProductID != "0x1009" {
		t.Errorf("fresh device: IDs changed unexpectedly: %s/%s", cfg.VendorID, cfg.ProductID)
	}
	if st.Ethernet != EthernetOff {
		t.Errorf("fresh device: ethernet = %q, want off", st.Ethernet)
	}
	if !st.Disk {
		t.Error("fresh device: disk should default on")
	}
}

func TestComputeMigrationLegacyECM(t *testing.T) {
	dir := withBootDir(t)
	writeFile(t, dir, "usb.vid", "0x1234\n")
	writeFile(t, dir, "usb.pid", "0x5678\n")
	touch(t, dir, "BIOS")
	touch(t, dir, "disable_hid")
	touch(t, dir, "usb.notwakeup")
	touch(t, dir, "udc.disable")
	touch(t, dir, "usb.ecm0")

	cfg, st := computeMigration(base())

	if cfg.VendorID != "0x1234" || cfg.ProductID != "0x5678" {
		t.Errorf("IDs = %s/%s, want 0x1234/0x5678", cfg.VendorID, cfg.ProductID)
	}
	if cfg.HID {
		t.Error("disable_hid present ⇒ HID should be false")
	}
	if !cfg.BIOSMode {
		t.Error("BIOS present ⇒ BIOSMode should be true")
	}
	if cfg.WakeupOnWrite {
		t.Error("usb.notwakeup present ⇒ WakeupOnWrite should be false")
	}
	if cfg.BindUDC {
		t.Error("udc.disable present ⇒ BindUDC should be false")
	}
	if st.Ethernet != EthernetECM {
		t.Errorf("ethernet = %q, want ecm", st.Ethernet)
	}
	if !cfg.Enabled {
		t.Error("Enabled should always be true after migration")
	}
}

func TestComputeMigrationNCMWinsOverECM(t *testing.T) {
	dir := withBootDir(t)
	touch(t, dir, "usb.ncm")
	touch(t, dir, "usb.ecm0")

	_, st := computeMigration(base())
	if st.Ethernet != EthernetNCM {
		t.Errorf("ethernet = %q, want ncm (ncm takes precedence)", st.Ethernet)
	}
}

func TestComputeMigrationDiskAlwaysOn(t *testing.T) {
	// Even with no usb.disk0 flag, the disk must migrate ON so the firmware
	// boot image on lun.0 remains visible to the host.
	withBootDir(t)
	_, st := computeMigration(base())
	if !st.Disk {
		t.Error("disk must default on regardless of /boot/usb.disk0")
	}
}
