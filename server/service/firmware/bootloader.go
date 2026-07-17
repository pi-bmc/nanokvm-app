package firmware

// bootloader.go bridges the UEFI variables U-Boot publishes over I2C
// (server/service/bootloader) into the BMC's EEPROM handling.
//
// U-Boot no longer dumps pieeprom.bin to the FAT, so the live bootloader
// config and the rpi-eeprom git version now come from those variables. The
// BMC owns the pieeprom.upd/.sig/recovery file lifecycle on the FAT itself.

import (
	"errors"
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/pi-bmc/nanokvm-app/server/service/bootloader"
)

// eepromAppliedMarker is the file the Pi firmware leaves on the FAT after it
// applies a staged update: rpi-eeprom-update renames recovery.bin to
// recovery.000 on success. PREBOOT keyed its cleanup on this file; the BMC
// now does.
const eepromAppliedMarker = "recovery.000"

// BootloaderProvenance is the running bootloader's identity, mirrored by
// U-Boot from the firmware device tree into UEFI variables.
type BootloaderProvenance struct {
	// GitVersion is the rpi-eeprom git hash (/chosen/bootloader/version),
	// distinct from the build-date "Version" the EEPROM image carries.
	GitVersion string
	// Config is the live EEPROM bootconf text (the running configuration).
	Config string
	// UpdatedUnix is when the EEPROM was last flashed; 0 when unknown.
	UpdatedUnix int64
}

// Available reports whether U-Boot reported any provenance.
func (p BootloaderProvenance) Available() bool {
	return p.GitVersion != "" || p.Config != "" || p.UpdatedUnix != 0
}

// GetBootloaderProvenance reads the bootloader variables U-Boot publishes over
// I2C. It returns a zero value (not an error) when the variable store is
// unavailable, so callers treat missing provenance as "not reported yet"
// rather than as a failure.
func (c *Controller) GetBootloaderProvenance() BootloaderProvenance {
	info, err := bootloader.GetStore().Load()
	if err != nil {
		if !errors.Is(err, bootloader.ErrNotConfigured) {
			log.Warnf("firmware: reading bootloader variables: %v", err)
		}
		return BootloaderProvenance{}
	}
	var updated int64
	if !info.UpdatedAt.IsZero() {
		updated = info.UpdatedAt.Unix()
	}
	return BootloaderProvenance{
		GitVersion:  info.Version,
		Config:      info.Config,
		UpdatedUnix: updated,
	}
}

// reconcilePieepromFiles removes staged EEPROM update files once the firmware
// has applied them. rpi-eeprom-update renames recovery.bin to recovery.000
// after a successful flash, so that marker's presence means the pieeprom.upd
// and pieeprom.sig the BMC staged have been consumed.
//
// Best-effort and idempotent: no marker is the common (nothing applied) case,
// and removing an already-absent file is not an error.
func (c *Controller) reconcilePieepromFiles() {
	if ok, _ := c.hasFileOnImage(eepromAppliedMarker); !ok {
		return
	}
	for _, name := range []string{eepromAppliedMarker, eepromPendingFile, eepromPendingSigFile} {
		if err := c.RemoveFileFromImage(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warnf("firmware: removing applied %s: %v", name, err)
		}
	}
	log.Info("firmware: cleaned up applied EEPROM update files")
}
