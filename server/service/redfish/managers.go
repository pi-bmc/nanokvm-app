package redfish

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

func (s *Service) GetManagerCollection(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"@odata.type":         "#ManagerCollection.ManagerCollection",
		"@odata.id":           "/redfish/v1/Managers",
		"@odata.context":      "/redfish/v1/$metadata#ManagerCollection.ManagerCollection",
		"Name":                "Manager Collection",
		"Members@odata.count": 1,
		"Members": []gin.H{
			{"@odata.id": "/redfish/v1/Managers/1"},
		},
	})
}

func (s *Service) GetManager(c *gin.Context) {
	firmwareVersion := "1.0.0"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		firmwareVersion = info.Main.Version
	}

	c.JSON(http.StatusOK, gin.H{
		"@odata.type":     "#Manager.v1_11_0.Manager",
		"@odata.id":       "/redfish/v1/Managers/1",
		"@odata.context":  "/redfish/v1/$metadata#Manager.Manager",
		"Id":              "1",
		"Name":            "NanoKVM BMC",
		"ManagerType":     "BMC",
		"FirmwareVersion": firmwareVersion,
		"SerialInterfaces": gin.H{
			"@odata.id": "/redfish/v1/Managers/1/SerialInterfaces",
		},
		"VirtualMedia": gin.H{
			"@odata.id": "/redfish/v1/Managers/1/VirtualMedia",
		},
		"NetworkInterfaces": gin.H{
			"@odata.id": "/redfish/v1/Managers/1/NetworkInterfaces",
		},
		// Links.ManagerForServers binds this BMC to the system(s) it
		// manages. Standards-based clients (Dell terraform provider,
		// bmclib) resolve system_id from this link when invoking actions
		// that target a specific ComputerSystem.
		//
		// Links.Oem.Dell.DellAttributes points the Dell terraform provider
		// at our fake iDRAC AttributeRegistry. The provider hard-codes a
		// Dell.Manager() unmarshal whose generation check (sub-17G vs
		// 17G+) gates the boot-source-override code path; we report 14G
		// so the standard PATCH /Systems/1 path is used.
		"Links": gin.H{
			"ManagerForServers": []gin.H{
				{"@odata.id": "/redfish/v1/Systems/1"},
			},
			"Oem": gin.H{
				"Dell": gin.H{
					"DellAttributes": []gin.H{
						{"@odata.id": "/redfish/v1/Managers/1/Oem/Dell/DellAttributes/iDRAC.Embedded.1"},
					},
				},
			},
		},
		// Empty Oem/Actions.Oem keep gofish's dell.Manager() unmarshal
		// from erroring on "unexpected end of JSON input" — the wrapper
		// json.Unmarshal's the raw bytes of each field and aborts when
		// they're absent rather than `{}`.
		"Oem":     gin.H{"Dell": gin.H{}},
		"Actions": gin.H{"Oem": gin.H{}},
	})
}

// GetDellIDRACAttributes serves the iDRAC.Embedded.1 attribute bag the
// Dell terraform provider needs to determine server generation. We
// claim "14G" so the provider takes the standard PATCH /Systems/1
// path (sub-17G) rather than the 17G Settings-URI flow we don't have.
func (s *Service) GetDellIDRACAttributes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"@odata.type":       "#DellAttributes.v1_0_0.DellAttributes",
		"@odata.id":         "/redfish/v1/Managers/1/Oem/Dell/DellAttributes/iDRAC.Embedded.1",
		"@odata.context":    "/redfish/v1/$metadata#DellAttributes.DellAttributes",
		"Id":                "iDRAC.Embedded.1",
		"Name":              "iDRAC Attributes",
		"AttributeRegistry": "ManagerAttributeRegistry.v1_0_0",
		"Attributes": gin.H{
			"Info.1.ServerGen": "14G",
		},
	})
}
