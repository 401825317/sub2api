package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RegisterClawXRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	jwtAuth servermiddleware.JWTAuthMiddleware,
	redisClient *redis.Client,
) {
	if h == nil || h.ClawX == nil {
		return
	}
	rateLimiter := middleware.NewRateLimiter(redisClient)
	registerClawXRouteGroup(r.Group("/api/clawx"), h.ClawX, jwtAuth, rateLimiter)
	registerClawXRouteGroup(r.Group("/api/clawbox"), h.ClawX, jwtAuth, rateLimiter)
}

func registerClawXRouteGroup(group *gin.RouterGroup, h *handler.ClawXHandler, jwtAuth servermiddleware.JWTAuthMiddleware, rateLimiter *middleware.RateLimiter) {
	group.GET("/bootstrap", h.Bootstrap)
	group.POST("/activation/check", h.ActivationCheck)
	group.POST("/verification/send-code", rateLimiter.LimitWithOptions("clawx-send-verify-code", 5, time.Minute, middleware.RateLimitOptions{
		FailureMode: middleware.RateLimitFailClose,
	}), h.SendVerifyCode)
	group.POST("/register", rateLimiter.LimitWithOptions("clawx-register", 5, time.Minute, middleware.RateLimitOptions{
		FailureMode: middleware.RateLimitFailClose,
	}), h.Register)
	group.POST("/login", rateLimiter.LimitWithOptions("clawx-login", 20, time.Minute, middleware.RateLimitOptions{
		FailureMode: middleware.RateLimitFailClose,
	}), h.Login)

	authenticated := group.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	{
		authenticated.POST("/auth/verify", h.Verify)
		authenticated.POST("/auth/unregister-device", h.UnregisterDevice)
		authenticated.POST("/relay-token", h.RelayToken)
		authenticated.GET("/user/self", h.UserSelf)
	}
}
