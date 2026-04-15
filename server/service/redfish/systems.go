package redfish

import (
	"net/http"
	"sync"
	"time"

	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var (
	bootOverrideTarget = "None"
	bootMu             sync.Mutex
)

var validBootTargets = map[string]bool{
	"None":      true,
	"Pxe":       true,
	"Hdd":       true,
	"Cd":        true,
	"BiosSetup": true,
}

func (s *Service) GetSystemCollection(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"@odata.type":    "#ComputerSystemCollection.ComputerSystemCollection",
		"@odata.id":      "/redfish/v1/Systems",
		"@odata.context": "/redfish/v1/$metadata#ComputerSystemCollection.ComputerSystemCollection",
		"Name":           "Computer System Collection",
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

	conf := config.GetInstance().Hardware

	switch req.ResetType {
	case "On":
		if err := writeGpio(conf.GPIOPower, 800*time.Millisecond); err != nil {
			redfishErrorResponse(c, http.StatusInternalServerError, "power on failed")
			return
		}

	case "ForceOff":
		if err := writeGpio(conf.GPIOPower, 5000*time.Millisecond); err != nil {
			redfishErrorResponse(c, http.StatusInternalServerError, "force off failed")
			return
		}

	case "GracefulShutdown":
		if err := writeGpio(conf.GPIOPower, 800*time.Millisecond); err != nil {
			redfishErrorResponse(c, http.StatusInternalServerError, "graceful shutdown failed")
			return
		}

	case "ForceRestart":
		if err := writeGpio(conf.GPIOReset, 800*time.Millisecond); err != nil {
			redfishErrorResponse(c, http.StatusInternalServerError, "force restart failed")
			return
		}

	case "PowerCycle":
		if err := writeGpio(conf.GPIOPower, 5000*time.Millisecond); err != nil {
			redfishErrorResponse(c, http.StatusInternalServerError, "power cycle off failed")
			return
		}
		time.Sleep(2 * time.Second)
		if err := writeGpio(conf.GPIOPower, 800*time.Millisecond); err != nil {
			redfishErrorResponse(c, http.StatusInternalServerError, "power cycle on failed")
			return
		}

	default:
		redfishErrorResponse(c, http.StatusBadRequest, "invalid ResetType: "+req.ResetType)
		return
	}

	log.Debugf("redfish reset action: %s", req.ResetType)
	c.Status(http.StatusNoContent)
}

func (s *Service) PatchSystem(c *gin.Context) {
	var req struct {
		Boot struct {
			BootSourceOverrideTarget string `json:"BootSourceOverrideTarget"`
		} `json:"Boot"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid request body")
		return
	}

	target := req.Boot.BootSourceOverrideTarget
	if !validBootTargets[target] {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid BootSourceOverrideTarget: "+target)
		return
	}

	bootMu.Lock()
	bootOverrideTarget = target
	bootMu.Unlock()

	log.Debugf("redfish boot override target set to: %s", target)
	c.JSON(http.StatusOK, buildSystemResource())
}

func buildSystemResource() gin.H {
	powerState := "Off"

	conf := config.GetInstance().Hardware
	on, err := readGpio(conf.GPIOPowerLED)
	if err == nil && on {
		powerState = "On"
	}

	bootMu.Lock()
	currentTarget := bootOverrideTarget
	bootMu.Unlock()

	return gin.H{
		"@odata.type":    "#ComputerSystem.v1_13_0.ComputerSystem",
		"@odata.id":      "/redfish/v1/Systems/1",
		"@odata.context": "/redfish/v1/$metadata#ComputerSystem.ComputerSystem",
		"Id":             "1",
		"Name":           "Computer System",
		"SystemType":     "Physical",
		"PowerState":     powerState,
		"Boot": gin.H{
			"BootSourceOverrideTarget":  currentTarget,
			"BootSourceOverrideEnabled": "Once",
		},
		"Actions": gin.H{
			"#ComputerSystem.Reset": gin.H{
				"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
				"ResetType@Redfish.AllowableValues": []string{
					"On", "ForceOff", "GracefulShutdown", "ForceRestart", "PowerCycle",
				},
			},
		},
	}
}
