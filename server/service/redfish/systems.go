package redfish

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/BMCPi/NanoKVM/server/service/efivars"
	"github.com/BMCPi/NanoKVM/server/service/firmware"
	"github.com/BMCPi/NanoKVM/server/service/power"
)

var validBootTargets = map[string]bool{
	"None":      true,
	"Pxe":       true,
	"Hdd":       true,
	"Cd":        true,
	"BiosSetup": true,
	"UefiHttp":  true,
}

func (s *Service) GetSystemCollection(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"@odata.type":         "#ComputerSystemCollection.ComputerSystemCollection",
		"@odata.id":           "/redfish/v1/Systems",
		"@odata.context":      "/redfish/v1/$metadata#ComputerSystemCollection.ComputerSystemCollection",
		"Name":                "Computer System Collection",
		"Members@odata.count": 1,
		"Members": []gin.H{
			{"@odata.id": "/redfish/v1/Systems/1"},
		},
	})
}

func (s *Service) GetSystem(c *gin.Context) {
	c.JSON(http.StatusOK, buildSystemResource())
}

func (s *Service) ResetSystem(c *gin.Context) {
	var req struct {
		ResetType string `json:"ResetType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid request body")
		return
	}

	ctrl := power.GetController()
	var err error

	switch req.ResetType {
	case "On":
		err = ctrl.PowerOn()
	case "ForceOff":
		err = ctrl.PowerOff()
	case "GracefulShutdown":
		err = ctrl.PowerOff()
	case "ForceRestart", "PowerCycle":
		err = ctrl.Reset()
	default:
		redfishErrorResponse(c, http.StatusBadRequest, "invalid ResetType: "+req.ResetType)
		return
	}

	if err != nil {
		redfishErrorResponse(c, http.StatusInternalServerError, req.ResetType+" failed: "+err.Error())
		return
	}

	log.Debugf("redfish reset action: %s", req.ResetType)
	c.Status(http.StatusNoContent)
}

func (s *Service) PatchSystem(c *gin.Context) {
	var req struct {
		Boot struct {
			BootSourceOverrideTarget  string `json:"BootSourceOverrideTarget"`
			BootSourceOverrideEnabled string `json:"BootSourceOverrideEnabled"` // "Once" | "Continuous" | "Disabled"
			// Mode (Legacy|UEFI) is accepted but ignored — the RPi5 firmware
			// path is UEFI-only, so there's no toggle to honour. We expose
			// it in the response below so PATCH echoes are consistent.
			BootSourceOverrideMode string `json:"BootSourceOverrideMode"`
		} `json:"Boot"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid request body")
		return
	}

	target := req.Boot.BootSourceOverrideTarget
	enabled := req.Boot.BootSourceOverrideEnabled
	if enabled == "" {
		enabled = "Once" // default to once per Redfish convention
	}

	// Disabled clears the override regardless of target.
	if enabled == "Disabled" || target == "None" {
		clearBootOverride()
		log.Debugf("redfish boot override cleared")
		c.JSON(http.StatusOK, buildSystemResource())
		return
	}

	if !validBootTargets[target] {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid BootSourceOverrideTarget: "+target)
		return
	}

	if err := setBootOverride(target, enabled); err != nil {
		log.Warnf("redfish: boot override write failed: %v", err)
	}

	log.Debugf("redfish boot override: target=%s enabled=%s", target, enabled)
	c.JSON(http.StatusOK, buildSystemResource())
}

// setBootOverride applies a boot source override. When the UEFI variable
// store (I2C EEPROM) is available it drives the EFI boot manager directly:
// BootNext for Once, a BootOrder reorder for Continuous. Otherwise it falls
// back to the legacy U-Boot env files (boot_targets).
func setBootOverride(target, enabled string) error {
	if mgr := efivars.GetManager(); mgr.Available() {
		// BiosSetup has no Boot#### entry to point at; treat as no-op like
		// the env path (which maps it to an empty boot_targets) does.
		if target == "BiosSetup" {
			return nil
		}
		return mgr.SetBootSourceOverride(efivars.BootTarget(target), enabled != "Continuous")
	}

	fwCtrl := firmware.GetController()
	ubootTargets := firmware.RedfishToUBoot[target]
	if enabled == "Continuous" {
		return fwCtrl.SetBootTarget(ubootTargets)
	}
	return fwCtrl.SetBootTargetOnce(ubootTargets)
}

// clearBootOverride removes any pending boot source override.
func clearBootOverride() {
	if mgr := efivars.GetManager(); mgr.Available() {
		if err := mgr.ClearBootSourceOverride(); err != nil {
			log.Warnf("redfish: clear BootNext failed: %v", err)
		}
		return
	}

	fwCtrl := firmware.GetController()
	if err := fwCtrl.SetBootTarget(""); err != nil {
		log.Warnf("redfish: clear persistent boot failed: %v", err)
	}
	if err := fwCtrl.SetBootTargetOnce(""); err != nil {
		log.Warnf("redfish: clear once boot failed: %v", err)
	}
}

// readBootOverride reports the current boot source override and, when the
// UEFI variable store is available, the BootOrder as Boot#### references.
// Store path: a pending BootNext maps to "Once"; a continuous override is a
// plain BootOrder reorder and reads back as "Disabled". Env fallback keeps
// the legacy boot_targets semantics.
func readBootOverride() (target, enabled string, bootOrder []string) {
	target, enabled = "None", "Disabled"

	if mgr := efivars.GetManager(); mgr.Available() {
		if t, e, err := mgr.BootSourceOverride(); err == nil {
			if t != efivars.TargetUnknown {
				target = string(t)
			}
			enabled = e
		} else {
			log.Warnf("redfish: read boot override failed: %v", err)
		}
		if order, err := mgr.BootOrder(); err == nil {
			bootOrder = make([]string, len(order))
			for i, id := range order {
				bootOrder[i] = fmt.Sprintf("Boot%04X", id)
			}
		}
		return target, enabled, bootOrder
	}

	// Legacy env fallback — persistent takes precedence over once.
	fwCtrl := firmware.GetController()
	if ubootTargets, err := fwCtrl.GetBootTarget(); err == nil && ubootTargets != "" {
		if rt, ok := firmware.UBootToRedfish[ubootTargets]; ok {
			return rt, "Continuous", nil
		}
	}
	if ubootTargets, err := fwCtrl.GetOnceBootTarget(); err == nil && ubootTargets != "" {
		if rt, ok := firmware.UBootToRedfish[ubootTargets]; ok {
			return rt, "Once", nil
		}
	}
	return target, enabled, nil
}

func buildSystemResource() gin.H {
	powerState := "Off"

	ctrl := power.GetController()
	on, err := ctrl.State()
	if err == nil && on {
		powerState = "On"
	}

	currentTarget, overrideEnabled, bootOrder := readBootOverride()

	bootInfo := gin.H{
		"BootSourceOverrideTarget":  currentTarget,
		"BootSourceOverrideEnabled": overrideEnabled,
		"BootSourceOverrideTarget@Redfish.AllowableValues": []string{
			"None", "Pxe", "Hdd", "Cd", "BiosSetup", "UefiHttp",
		},
		"BootSourceOverrideEnabled@Redfish.AllowableValues": []string{
			"Disabled", "Once", "Continuous",
		},
		"BootSourceOverrideMode": "UEFI",
		"BootSourceOverrideMode@Redfish.AllowableValues": []string{
			"UEFI",
		},
	}
	if bootOrder != nil {
		bootInfo["BootOrder"] = bootOrder
	}

	// Read inventory from firmware env.
	systemInfo := gin.H{
		"@odata.type":    "#ComputerSystem.v1_13_0.ComputerSystem",
		"@odata.id":      "/redfish/v1/Systems/1",
		"@odata.context": "/redfish/v1/$metadata#ComputerSystem.ComputerSystem",
		"Id":             "1",
		"Name":           "Computer System",
		"SystemType":     "Physical",
		"PowerState":     powerState,
		"Boot":           bootInfo,
		"Actions": gin.H{
			"#ComputerSystem.Reset": gin.H{
				"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
				"ResetType@Redfish.AllowableValues": []string{
					"On", "ForceOff", "GracefulShutdown", "ForceRestart", "PowerCycle",
				},
			},
		},
		// Bios link points the Redfish client at the EEPROM configuration
		// surface (see bios.go). Standard navigation property — clients
		// follow @odata.id to GET the current bootloader settings.
		"Bios": gin.H{
			"@odata.id": "/redfish/v1/Systems/1/Bios",
		},
	}

	if inv, err := firmware.GetController().GetInventory(); err == nil {
		if v, ok := inv["board_name"]; ok {
			systemInfo["Model"] = v
		}
		if v, ok := inv["serial#"]; ok {
			systemInfo["SerialNumber"] = v
		}
		if v, ok := inv["ethaddr"]; ok {
			systemInfo["MACAddress"] = v
		}
		if v, ok := inv["vendor"]; ok {
			systemInfo["Manufacturer"] = v
		}
		if v, ok := inv["cpu"]; ok {
			systemInfo["ProcessorSummary"] = gin.H{"Model": v}
		}
		if v, ok := inv["ver"]; ok {
			systemInfo["FirmwareVersion"] = v
		}
	}

	return systemInfo
}
