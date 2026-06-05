package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

func RegisterClawXRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	jwtAuth servermiddleware.JWTAuthMiddleware,
) {
	if h == nil || h.ClawX == nil {
		return
	}
	registerClawXRouteGroup(r.Group("/api/clawx"), h.ClawX, jwtAuth)
	registerClawXRouteGroup(r.Group("/api/clawbox"), h.ClawX, jwtAuth)
}

func registerClawXRouteGroup(group *gin.RouterGroup, h *handler.ClawXHandler, jwtAuth servermiddleware.JWTAuthMiddleware) {
	group.GET("/bootstrap", h.Bootstrap)
	group.POST("/activation/check", h.ActivationCheck)
	group.POST("/register", h.Register)
	group.POST("/login", h.Login)

	authenticated := group.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	{
		authenticated.POST("/auth/verify", h.Verify)
		authenticated.POST("/auth/unregister-device", h.UnregisterDevice)
		authenticated.POST("/relay-token", h.RelayToken)
		authenticated.GET("/user/self", h.UserSelf)
	}
}
