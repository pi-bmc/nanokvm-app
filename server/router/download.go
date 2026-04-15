package router

import (
	"github.com/gin-gonic/gin"
	"github.com/tinkerbell-community/NanoKVM/server/service/download"

	"github.com/tinkerbell-community/NanoKVM/server/middleware"
)

func downloadRouter(r *gin.Engine) {
	service := download.NewService()
	api := r.Group("/api").Use(middleware.CheckToken())

	api.POST("/download/image", service.DownloadImage)       // download image
	api.GET("/download/image/status", service.StatusImage)   // download image
	api.GET("/download/image/enabled", service.ImageEnabled) // download image
	api.POST("/download/file", service.DownloadImageFile)       // download image
}
