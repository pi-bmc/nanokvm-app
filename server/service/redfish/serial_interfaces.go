package redfish

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type serialConfig struct {
	BitRate     int    `json:"BitRate"`
	Parity      string `json:"Parity"`
	DataBits    int    `json:"DataBits"`
	StopBits    int    `json:"StopBits"`
	FlowControl string `json:"FlowControl"`
}

var (
	serialCfg = serialConfig{
		BitRate:     115200,
		Parity:      "None",
		DataBits:    8,
		StopBits:    1,
		FlowControl: "None",
	}
	serialMu sync.Mutex
)

func (s *Service) GetSerialInterfaceCollection(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"@odata.type":    "#SerialInterfaceCollection.SerialInterfaceCollection",
		"@odata.id":      "/redfish/v1/Managers/1/SerialInterfaces",
		"@odata.context": "/redfish/v1/$metadata#SerialInterfaceCollection.SerialInterfaceCollection",
		"Name":           "Serial Interface Collection",
		"Members@odata.count": 1,
		"Members": []gin.H{
			{"@odata.id": "/redfish/v1/Managers/1/SerialInterfaces/1"},
		},
	})
}

func (s *Service) GetSerialInterface(c *gin.Context) {
	c.JSON(http.StatusOK, buildSerialInterfaceResource())
}

func (s *Service) PatchSerialInterface(c *gin.Context) {
	var req serialConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid request body")
		return
	}

	serialMu.Lock()
	if req.BitRate != 0 {
		serialCfg.BitRate = req.BitRate
	}
	if req.Parity != "" {
		serialCfg.Parity = req.Parity
	}
	if req.DataBits != 0 {
		serialCfg.DataBits = req.DataBits
	}
	if req.StopBits != 0 {
		serialCfg.StopBits = req.StopBits
	}
	if req.FlowControl != "" {
		serialCfg.FlowControl = req.FlowControl
	}
	serialMu.Unlock()

	log.Debugf("redfish serial interface updated")
	c.JSON(http.StatusOK, buildSerialInterfaceResource())
}

func buildSerialInterfaceResource() gin.H {
	serialMu.Lock()
	cfg := serialCfg
	serialMu.Unlock()

	return gin.H{
		"@odata.type":    "#SerialInterface.v1_1_7.SerialInterface",
		"@odata.id":      "/redfish/v1/Managers/1/SerialInterfaces/1",
		"@odata.context": "/redfish/v1/$metadata#SerialInterface.SerialInterface",
		"Id":             "1",
		"Name":           "Serial Interface 1",
		"BitRate":        cfg.BitRate,
		"Parity":         cfg.Parity,
		"DataBits":       cfg.DataBits,
		"StopBits":       cfg.StopBits,
		"FlowControl":    cfg.FlowControl,
	}
}
