package router

import (
	"github.com/gin-gonic/gin"

	"github.com/tinkerbell-community/NanoKVM/server/middleware"
	"github.com/tinkerbell-community/NanoKVM/server/service/ws"
)

func wsRouter(r *gin.Engine) {
	service := ws.NewService()
	api := r.Group("/api").Use(middleware.CheckToken())

	api.GET("/ws", service.Connect)
}
