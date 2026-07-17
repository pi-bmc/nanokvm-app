package redfish

// trusted_components.go exposes the RPi 5 bootloader (rpi-eeprom) as a Redfish
// TrustedComponent — the platform root of trust — with its running firmware as
// a nested SoftwareInventory. The version and flash time come from the UEFI
// variables U-Boot publishes over I2C (server/service/bootloader), read here
// through firmware.Controller.GetBootloaderProvenance.
//
// Shape (all reachable from ComputerSystem.Links.TrustedComponents):
//
//	/redfish/v1/Systems/1/TrustedComponents                     collection
//	/redfish/v1/Systems/1/TrustedComponents/Bootloader          the RoT
//	/redfish/v1/Systems/1/TrustedComponents/Bootloader/SoftwareImages/Active
//	                                                            its firmware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stmcginnis/gofish/schemas"

	"github.com/pi-bmc/nanokvm-app/server/service/firmware"
)

// bootloaderProvenance reads the running bootloader's version/config/flash-time
// from the firmware controller. Returns a zero value when U-Boot has not
// published the variables yet; the resources still exist (the RoT is physical),
// they just omit the version until it is reported.
func bootloaderProvenance() firmware.BootloaderProvenance {
	return firmware.GetController().GetBootloaderProvenance()
}

func (s *Service) GetTrustedComponentCollection(c *gin.Context) {
	c.JSON(http.StatusOK, newCollection(
		"TrustedComponentCollection", "Trusted Component Collection",
		trustedComponentsPath,
		Link(bootloaderComponentPath),
	))
}

func (s *Service) GetTrustedComponentBootloader(c *gin.Context) {
	prov := bootloaderProvenance()

	activeImage := Link(bootloaderSoftwarePath)
	integratedInto := Link(systemPath)

	c.JSON(http.StatusOK, TrustedComponent{
		Resource: Resource{
			ODataType:    "#TrustedComponent.v1_4_0.TrustedComponent",
			ODataID:      bootloaderComponentPath,
			ODataContext: context("TrustedComponent.TrustedComponent"),
			ID:           "Bootloader",
			Name:         "Bootloader EEPROM",
			Description:  "Raspberry Pi 5 bootloader (rpi-eeprom) — the platform root of trust.",
		},
		// Integrated: the loader lives in the SoC's boot SPI flash, not a
		// discrete pluggable part.
		TrustedComponentType: schemas.IntegratedTrustedComponentType,
		Manufacturer:         "Raspberry Pi",
		Model:                "RPi5 Bootloader EEPROM",
		FirmwareVersion:      prov.GitVersion,
		Status:               &Status{State: schemas.EnabledState, Health: schemas.OKHealth},
		Links: &TrustedComponentLinks{
			ActiveSoftwareImage: &activeImage,
			SoftwareImages:      Links{activeImage},
			IntegratedInto:      &integratedInto,
		},
	})
}

func (s *Service) GetBootloaderSoftwareInventory(c *gin.Context) {
	prov := bootloaderProvenance()

	inv := SoftwareInventory{
		Resource: Resource{
			ODataType:    "#SoftwareInventory.v1_8_0.SoftwareInventory",
			ODataID:      bootloaderSoftwarePath,
			ODataContext: context("SoftwareInventory.SoftwareInventory"),
			ID:           "Active",
			Name:         "Bootloader Firmware",
			Description:  "Running rpi-eeprom bootloader image.",
		},
		SoftwareID: "rpi-eeprom",
		Version:    prov.GitVersion,
		// The version is a git hash, not a dotted/semantic version.
		VersionScheme: schemas.OEMVersionScheme,
		// The BMC stages pieeprom.upd for the host's rpi-eeprom-update to
		// flash on next boot (see firmware.SetEEPROMConfig).
		Updateable: true,
		Status:     &Status{State: schemas.EnabledState, Health: schemas.OKHealth},
	}
	if prov.UpdatedUnix != 0 {
		inv.ReleaseDate = time.Unix(prov.UpdatedUnix, 0).UTC().Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, inv)
}
