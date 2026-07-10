package redfish

import (
	"net/http"
	"strings"

	"github.com/pi-bmc/nanokvm-app/server/config"
	"github.com/pi-bmc/nanokvm-app/server/service/serial"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type serialPatchRequest struct {
	BitRate     int    `json:"BitRate"`
	Parity      string `json:"Parity"`
	DataBits    int    `json:"DataBits"`
	StopBits    int    `json:"StopBits"`
	FlowControl string `json:"FlowControl"`
}

func (s *Service) GetSerialInterfaceCollection(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"@odata.type":         "#SerialInterfaceCollection.SerialInterfaceCollection",
		"@odata.id":           "/redfish/v1/Managers/1/SerialInterfaces",
		"@odata.context":      "/redfish/v1/$metadata#SerialInterfaceCollection.SerialInterfaceCollection",
		"Name":                "Serial Interface Collection",
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
	var req serialPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		redfishErrorResponse(c, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg := config.GetInstance()

	if req.BitRate != 0 {
		cfg.Serial.BaudRate = req.BitRate
	}
	if req.Parity != "" {
		cfg.Serial.Parity = strings.ToLower(req.Parity)
	}
	if req.DataBits != 0 {
		cfg.Serial.DataBits = req.DataBits
	}
	if req.StopBits != 0 {
		cfg.Serial.StopBits = req.StopBits
	}
	if req.FlowControl != "" {
		cfg.Serial.FlowControl = strings.ToLower(req.FlowControl)
	}

	// NOTE: active serial broker sessions will not pick up new settings
	// until the next Connect(). A broker restart may be needed.
	log.Debugf("redfish serial interface updated via central config")
	c.JSON(http.StatusOK, buildSerialInterfaceResource())
}

// titleCase capitalises the first letter of a lower-case config value
// so the Redfish response uses the conventional "None" / "Even" / "Odd" form.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func buildSerialInterfaceResource() gin.H {
	cfg := config.GetInstance().Serial
	broker := serial.GetBroker()

	return gin.H{
		"@odata.type":      "#SerialInterface.v1_1_7.SerialInterface",
		"@odata.id":        "/redfish/v1/Managers/1/SerialInterfaces/1",
		"@odata.context":   "/redfish/v1/$metadata#SerialInterface.SerialInterface",
		"Id":               "1",
		"Name":             "Serial Interface 1",
		"InterfaceEnabled": broker.Active(),
		"BitRate":          cfg.BaudRate,
		"Parity":           titleCase(cfg.Parity),
		"DataBits":         cfg.DataBits,
		"StopBits":         cfg.StopBits,
		"FlowControl":      titleCase(cfg.FlowControl),
	}
}
