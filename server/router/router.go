package router

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"NanoKVM-Server/gintemplrenderer"
	"NanoKVM-Server/templates"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func Init(r *gin.Engine) {
	web(r)
	server(r)
	log.Debugf("router init done")
}

func web(r *gin.Engine) {
	execPath, err := os.Executable()
	if err != nil {
		panic("invalid executable path")
	}

	execDir := filepath.Dir(execPath)
	webPath := fmt.Sprintf("%s/web", execDir)

	// Serve static assets (JS bundles, CSS, images, etc.) from the Vite build output.
	r.Static("/assets", filepath.Join(webPath, "assets"))
	r.StaticFile("/sipeed.ico", filepath.Join(webPath, "sipeed.ico"))

	// Determine the JS entry point from the Vite build.
	bundlePath := findBundlePath(webPath)

	// Server-rendered templ pages (BMC dashboard, serial console, settings)
	r.GET("/dashboard", func(c *gin.Context) {
		render := gintemplrenderer.New(c.Request.Context(), http.StatusOK, templates.DashboardPage())
		c.Render(http.StatusOK, render)
	})
	r.GET("/console", func(c *gin.Context) {
		render := gintemplrenderer.New(c.Request.Context(), http.StatusOK, templates.ConsolePage())
		c.Render(http.StatusOK, render)
	})
	r.GET("/settings", func(c *gin.Context) {
		render := gintemplrenderer.New(c.Request.Context(), http.StatusOK, templates.SettingsPage())
		c.Render(http.StatusOK, render)
	})

	// React SPA routes (login, password, root)
	r.GET("/", func(c *gin.Context) {
		render := gintemplrenderer.New(c.Request.Context(), http.StatusOK, templates.IndexPage(bundlePath))
		c.Render(http.StatusOK, render)
	})
	r.GET("/auth/login", func(c *gin.Context) {
		render := gintemplrenderer.New(c.Request.Context(), http.StatusOK, templates.LoginPage(bundlePath))
		c.Render(http.StatusOK, render)
	})
	r.GET("/auth/password", func(c *gin.Context) {
		render := gintemplrenderer.New(c.Request.Context(), http.StatusOK, templates.PasswordPage(bundlePath))
		c.Render(http.StatusOK, render)
	})
}

// findBundlePath locates the Vite-built JS entry point in the assets directory.
// Vite generates hashed filenames like /assets/index-<hash>.js.
func findBundlePath(webPath string) string {
	assetsDir := filepath.Join(webPath, "assets")
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		log.Warnf("could not read web assets directory: %v", err)
		return "/assets/index.js"
	}

	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) == ".js" && len(name) > 8 && name[:5] == "index" {
			return fmt.Sprintf("/assets/%s", name)
		}
	}

	return "/assets/index.js"
}

func server(r *gin.Engine) {
	authRouter(r)
	applicationRouter(r)
	vmRouter(r)
	storageRouter(r)
	networkRouter(r)
	picoclawRouter(r)
	wsRouter(r)
	downloadRouter(r)
	extensionsRouter(r)
	redfishRouter(r)
}
