package redfish

import (
	"testing"

	"github.com/stmcginnis/gofish/schemas"

	"github.com/pi-bmc/nanokvm-app/server/service/efivars"
	"github.com/pi-bmc/nanokvm-app/server/service/firmware"
	"github.com/pi-bmc/nanokvm-app/server/service/smbios"
)

// readBoot converts an efivars.BootTarget straight into a schemas.BootSource,
// and setBootOverride converts back. That is only sound while every efivars
// target is spelled exactly like a gofish BootSource — otherwise we'd emit an
// enum value no client can read. Pin the invariant here rather than discover
// it when someone adds a target.
func TestEFIBootTargetsAreValidBootSources(t *testing.T) {
	for _, target := range []efivars.BootTarget{
		efivars.TargetPxe,
		efivars.TargetHdd,
		efivars.TargetCd,
		efivars.TargetUefiHttp,
	} {
		if !bootSourceSupported(schemas.BootSource(target)) {
			t.Errorf("efivars.BootTarget %q is not a supported schemas.BootSource; "+
				"readBoot would emit an invalid enum", target)
		}
	}

	// The reverse direction: everything we accept must round-trip to a target
	// efivars understands (BiosSetup and None are handled before conversion).
	for _, src := range supportedBootSources {
		if src == schemas.NoneBootSource || src == schemas.BiosSetupBootSource {
			continue
		}
		switch efivars.BootTarget(src) {
		case efivars.TargetPxe, efivars.TargetHdd, efivars.TargetCd, efivars.TargetUefiHttp:
		default:
			t.Errorf("supported BootSource %q has no matching efivars.BootTarget", src)
		}
	}
}

// The env fallback path maps through firmware.UBootToRedfish; its values must
// also be valid BootSources for the same reason.
func TestUBootToRedfishValuesAreValidBootSources(t *testing.T) {
	for ubootTarget, redfishName := range firmware.UBootToRedfish {
		if !bootSourceSupported(schemas.BootSource(redfishName)) {
			t.Errorf("UBootToRedfish[%q] = %q, which is not a supported BootSource",
				ubootTarget, redfishName)
		}
	}
	// And every source we advertise must be settable via the env fallback.
	for _, src := range supportedBootSources {
		if _, ok := firmware.RedfishToUBoot[string(src)]; !ok {
			t.Errorf("supported BootSource %q missing from firmware.RedfishToUBoot; "+
				"the env fallback would silently set an empty boot_targets", src)
		}
	}
}

// set() implements the overlay: SMBIOS layers over the env without blanking
// fields it doesn't carry.
func TestSetOnlyOverwritesWithNonEmpty(t *testing.T) {
	got := "from-env"
	set(&got, "")
	if got != "from-env" {
		t.Errorf("empty value clobbered the field: got %q", got)
	}
	set(&got, "from-smbios")
	if got != "from-smbios" {
		t.Errorf("non-empty value did not overlay: got %q", got)
	}
}

func TestOemNanoKVMIsCreatedOnceAndTyped(t *testing.T) {
	var sys ComputerSystem

	a := oemNanoKVM(&sys)
	a["MACAddress"] = "d8:3a:dd:00:00:01"
	b := oemNanoKVM(&sys)
	b["InventorySource"] = "SMBIOS"

	if len(sys.Oem) != 1 {
		t.Fatalf("Oem has %d blocks, want 1", len(sys.Oem))
	}
	block, ok := sys.Oem["NanoKVM"].(map[string]any)
	if !ok {
		t.Fatalf("Oem.NanoKVM is %T", sys.Oem["NanoKVM"])
	}
	// Both writes must land in the same block.
	if block["MACAddress"] != "d8:3a:dd:00:00:01" || block["InventorySource"] != "SMBIOS" {
		t.Errorf("block lost a value: %v", block)
	}
	if block["@odata.type"] != "#NanoKVM.v1_0_0.ComputerSystem" {
		t.Errorf("Oem block missing @odata.type, got %v", block["@odata.type"])
	}
}

// applySMBIOSInfo must project the SMBIOS memory tables onto the standard
// ComputerSystem.MemorySummary, and route the detail with no standard property
// (module type/speed, ECC, sockets, per-module list, slots) to Oem.
func TestApplySMBIOSInfoMemory(t *testing.T) {
	var sys ComputerSystem
	info := &smbios.Info{
		MemoryTotalMB:         16384,
		MemoryErrorCorrection: "Single-bit ECC",
		MemorySlots:           1,
		Memory: []smbios.MemoryModule{{
			Locator:      "P0",
			SizeMB:       16384,
			Type:         "LPDDR4",
			FormFactor:   "Row of chips",
			SpeedMTs:     4267,
			Manufacturer: "Micron",
			PartNumber:   "MT53E2G32",
		}},
		Slots: []string{"PCIe"},
	}

	applySMBIOSInfo(&sys, info)

	if sys.MemorySummary == nil {
		t.Fatal("MemorySummary not set")
	}
	if sys.MemorySummary.TotalSystemMemoryGiB == nil {
		t.Fatal("TotalSystemMemoryGiB nil")
	}
	if got := *sys.MemorySummary.TotalSystemMemoryGiB; got != 16 {
		t.Errorf("TotalSystemMemoryGiB = %v, want 16", got)
	}
	if sys.MemorySummary.MemoryMirroring != schemas.NoneMemoryMirroring {
		t.Errorf("MemoryMirroring = %q, want None", sys.MemorySummary.MemoryMirroring)
	}
	if sys.MemorySummary.Status == nil ||
		sys.MemorySummary.Status.State != schemas.EnabledState ||
		sys.MemorySummary.Status.Health != schemas.OKHealth {
		t.Errorf("Status = %+v, want Enabled/OK", sys.MemorySummary.Status)
	}

	oem := oemNanoKVM(&sys)
	for key, want := range map[string]any{
		"MemoryErrorCorrection": "Single-bit ECC",
		"MemorySlots":           1,
		"MemoryType":            "LPDDR4",
		"MemorySpeedMTs":        4267,
	} {
		if oem[key] != want {
			t.Errorf("Oem[%q] = %v, want %v", key, oem[key], want)
		}
	}
	if _, ok := oem["MemoryDevices"].([]smbios.MemoryModule); !ok {
		t.Errorf("Oem[MemoryDevices] is %T, want []smbios.MemoryModule", oem["MemoryDevices"])
	}
	if slots, ok := oem["Slots"].([]string); !ok || len(slots) != 1 || slots[0] != "PCIe" {
		t.Errorf("Oem[Slots] = %v, want [PCIe]", oem["Slots"])
	}
}

// A board that carries no memory tables (older firmware, blank region) must not
// invent a MemorySummary or any memory Oem keys.
func TestApplySMBIOSInfoNoMemory(t *testing.T) {
	var sys ComputerSystem
	applySMBIOSInfo(&sys, &smbios.Info{Manufacturer: "Raspberry Pi"})

	if sys.MemorySummary != nil {
		t.Errorf("MemorySummary = %+v, want nil", sys.MemorySummary)
	}
	oem := oemNanoKVM(&sys)
	for _, key := range []string{"MemoryType", "MemorySlots", "MemoryDevices", "Slots"} {
		if _, present := oem[key]; present {
			t.Errorf("Oem[%q] present with no memory tables", key)
		}
	}
}

func TestResetTypeSupported(t *testing.T) {
	for _, ok := range supportedResetTypes {
		if !resetTypeSupported(ok) {
			t.Errorf("%q should be supported", ok)
		}
	}
	// Types gofish defines but power.Controller cannot service.
	for _, bad := range []schemas.ResetType{
		schemas.NmiResetType,
		schemas.PushPowerButtonResetType,
		schemas.GracefulRestartResetType,
		"", "Bogus",
	} {
		if resetTypeSupported(bad) {
			t.Errorf("%q should not be supported", bad)
		}
	}
}

func TestBootSourceSupported(t *testing.T) {
	if !bootSourceSupported(schemas.PxeBootSource) {
		t.Error("Pxe should be supported")
	}
	for _, bad := range []schemas.BootSource{
		schemas.FloppyBootSource,
		schemas.UefiShellBootSource,
		schemas.SDCardBootSource,
		"", "Bogus",
	} {
		if bootSourceSupported(bad) {
			t.Errorf("%q should not be supported", bad)
		}
	}
}
