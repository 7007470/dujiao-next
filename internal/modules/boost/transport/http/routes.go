package boosthttp

import "github.com/gin-gonic/gin"

// RegisterAdminRoutes 注册后台代练目录管理路由（挂在 /api/v1/admin 下，已经过 JWT + RBAC）。
// 路径必须是字面量：internal/app/httpserver/rbac_coverage_test.go 会静态扫描并要求被角色种子覆盖。
func RegisterAdminRoutes(authorized gin.IRoutes, handler *AdminHandler) {
	if authorized == nil || handler == nil {
		panic("boost admin routes: required dependency is nil")
	}
	authorized.GET("/boost/games", handler.ListGames)
	authorized.POST("/boost/games", handler.CreateGame)
	authorized.GET("/boost/games/:id", handler.GetGame)
	authorized.PUT("/boost/games/:id", handler.UpdateGame)
	authorized.POST("/boost/games/:id/status", handler.SetGameStatus)

	authorized.GET("/boost/games/:id/servers", handler.ListServers)
	authorized.POST("/boost/games/:id/servers", handler.CreateServer)
	authorized.PUT("/boost/games/:id/servers/:server_id", handler.UpdateServer)
	authorized.POST("/boost/games/:id/servers/:server_id/status", handler.SetServerStatus)

	authorized.GET("/boost/games/:id/features", handler.ListFeatures)
	authorized.POST("/boost/features", handler.CreateFeature)
	authorized.POST("/boost/features/validate", handler.ValidateFeature)
	authorized.POST("/boost/features/preview", handler.PreviewFeature)
	authorized.GET("/boost/features/:id", handler.GetFeature)
	authorized.PUT("/boost/features/:id", handler.UpdateFeature)
	authorized.POST("/boost/features/:id/status", handler.SetFeatureStatus)
	authorized.POST("/boost/features/:id/move", handler.MoveFeature)
	authorized.GET("/boost/features/:id/versions", handler.ListFeatureVersions)
	authorized.GET("/boost/features/:id/versions/:version", handler.GetFeatureVersion)
}
