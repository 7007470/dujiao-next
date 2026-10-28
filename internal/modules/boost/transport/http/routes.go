package boosthttp

import "github.com/gin-gonic/gin"

// RegisterPublicRoutes 注册用户端公开路由（挂在 /api/v1/public 下，不需要登录）。
func RegisterPublicRoutes(public gin.IRoutes, handler *StorefrontHandler) {
	if public == nil || handler == nil {
		panic("boost public routes: required dependency is nil")
	}
	public.GET("/boost/games", handler.ListGames)
	public.GET("/boost/games/:code", handler.GetGame)
	public.POST("/boost/quote", handler.PublicQuote)
}

// RegisterUserRoutes 注册用户端登录后路由（挂在 /api/v1 下，已经过用户 JWT）。
func RegisterUserRoutes(user gin.IRoutes, handler *StorefrontHandler) {
	if user == nil || handler == nil {
		panic("boost user routes: required dependency is nil")
	}
	user.POST("/boost/quote", handler.UserQuote)
}

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

// RegisterPointsPublicRoutes 用户端公开：积分包列表。
func RegisterPointsPublicRoutes(public gin.IRoutes, handler *PointsHandler) {
	if public == nil || handler == nil {
		panic("boost points public routes: required dependency is nil")
	}
	public.GET("/boost/point-packs", handler.PublicPacks)
}

// RegisterPointsUserRoutes 用户端登录后：我的积分、积分明细。
func RegisterPointsUserRoutes(user gin.IRoutes, handler *PointsHandler) {
	if user == nil || handler == nil {
		panic("boost points user routes: required dependency is nil")
	}
	user.GET("/boost/points", handler.MySummary)
	user.GET("/boost/points/ledger", handler.MyLedger)
}

// RegisterPointsAdminRoutes 后台积分管理（字面量路径，见 RegisterAdminRoutes 注释）。
func RegisterPointsAdminRoutes(authorized gin.IRoutes, handler *PointsHandler) {
	if authorized == nil || handler == nil {
		panic("boost points admin routes: required dependency is nil")
	}
	authorized.GET("/boost/point-packs", handler.ListPacks)
	authorized.POST("/boost/point-packs", handler.CreatePack)
	authorized.PUT("/boost/point-packs/:id", handler.UpdatePack)

	authorized.GET("/boost/point-accounts", handler.ListAccounts)
	authorized.GET("/boost/point-ledger", handler.ListLedger)
	authorized.GET("/boost/users/:id/points", handler.GetUserPoints)
	authorized.POST("/boost/points/grant", handler.Grant)
	authorized.POST("/boost/points/deduct", handler.Deduct)
	authorized.POST("/boost/points/revoke-order", handler.RevokeOrder)
}
