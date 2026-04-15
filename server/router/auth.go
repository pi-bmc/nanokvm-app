package router

import (
	"github.com/gin-gonic/gin"

	"github.com/tinkerbell-community/NanoKVM/server/middleware"
	"github.com/tinkerbell-community/NanoKVM/server/service/auth"
)

func authRouter(r *gin.Engine) {
	service := auth.NewService()

	r.POST("/api/auth/login", service.Login) // login

	api := r.Group("/api").Use(middleware.CheckToken())

	api.GET("/auth/password", service.IsPasswordUpdated) // is password updated
	api.GET("/auth/account", service.GetAccount)         // get account
	api.POST("/auth/password", service.ChangePassword)   // change password
	api.POST("/auth/logout", service.Logout)             // logout
}
