package router

import (
	"github.com/gin-gonic/gin"

	"github.com/tinkerbell-community/NanoKVM/server/middleware"
	"github.com/tinkerbell-community/NanoKVM/server/service/storage"
)

func storageRouter(r *gin.Engine) {
	service := storage.NewService()
	api := r.Group("/api").Use(middleware.CheckToken())

	api.GET("/storage/image", service.GetImages)               // get image list
	api.GET("/storage/image/mounted", service.GetMountedImage) // get mounted image
	api.POST("/storage/image/mount", service.MountImage)       // mount image
	api.GET("/storage/cdrom", service.GetCdRom)                // get CD-ROM flag
	api.POST("/storage/image/delete", service.DeleteImage)     // delete image
}
