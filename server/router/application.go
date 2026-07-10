package router

import (
	"github.com/pi-bmc/nanokvm-app/server/middleware"
	"github.com/pi-bmc/nanokvm-app/server/service/application"

	"github.com/gin-gonic/gin"
)

func applicationRouter(r *gin.Engine) {
	service := application.NewService()
	api := r.Group("/api").Use(middleware.CheckToken())

	api.GET("/application/version", service.GetVersion)            // get application version
	api.POST("/application/update", service.Update)                // update application
	api.POST("/application/update/offline", service.OfflineUpdate) // update application offline
}
