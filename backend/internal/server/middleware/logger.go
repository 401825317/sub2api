package middleware

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 请求日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		startTime := time.Now()

		// 请求路径
		path := c.Request.URL.Path

		// 处理请求
		c.Next()

		// 跳过健康检查等高频探针路径的日志
		if path == "/health" || path == "/setup/status" {
			return
		}

		endTime := time.Now()
		latency := endTime.Sub(startTime)

		method := c.Request.Method
		statusCode := c.Writer.Status()
		clientIP := ip.GetClientIP(c)
		protocol := c.Request.Proto
		accountID, hasAccountID := c.Request.Context().Value(ctxkey.AccountID).(int64)
		platform, _ := c.Request.Context().Value(ctxkey.Platform).(string)
		model, _ := c.Request.Context().Value(ctxkey.Model).(string)

		fields := []zap.Field{
			zap.String("component", "http.access"),
			zap.Int("status_code", statusCode),
			zap.Int64("latency_ms", latency.Milliseconds()),
			zap.String("client_ip", clientIP),
			zap.String("protocol", protocol),
			zap.String("method", method),
			zap.String("path", path),
		}
		if hasAccountID && accountID > 0 {
			fields = append(fields, zap.Int64("account_id", accountID))
		}
		if platform != "" {
			fields = append(fields, zap.String("platform", platform))
		}
		if model != "" {
			fields = append(fields, zap.String("model", model))
		}
		if model == "gpt-5.5" {
			fields = appendGPT55TimingFields(c, fields, latency)
		}

		l := logger.FromContext(c.Request.Context()).With(fields...)
		l.Info("http request completed", zap.Time("completed_at", endTime))

		if len(c.Errors) > 0 {
			l.Warn("http request contains gin errors", zap.String("errors", c.Errors.String()))
		}
	}
}

func appendGPT55TimingFields(c *gin.Context, fields []zap.Field, totalLatency time.Duration) []zap.Field {
	fields = append(fields, zap.Bool("detailed_timing", true))
	totalMs := totalLatency.Milliseconds()
	if totalMs >= 0 {
		fields = append(fields, zap.Int64("total_latency_ms", totalMs))
	}

	authMs, hasAuth := contextLatencyMs(c, service.OpsAuthLatencyMsKey)
	routingMs, hasRouting := contextLatencyMs(c, service.OpsRoutingLatencyMsKey)
	upstreamMs, hasUpstream := contextLatencyMs(c, service.OpsUpstreamLatencyMsKey)
	responseMs, hasResponse := contextLatencyMs(c, service.OpsResponseLatencyMsKey)
	ttftMs, hasTTFT := contextLatencyMs(c, service.OpsTimeToFirstTokenMsKey)

	if hasAuth {
		fields = append(fields, zap.Int64("auth_latency_ms", authMs))
	}
	if hasRouting {
		fields = append(fields, zap.Int64("routing_latency_ms", routingMs))
	}
	if hasUpstream {
		fields = append(fields, zap.Int64("upstream_latency_ms", upstreamMs))
	}
	if hasResponse {
		fields = append(fields, zap.Int64("response_latency_ms", responseMs))
	}
	if hasTTFT {
		fields = append(fields, zap.Int64("time_to_first_token_ms", ttftMs))
		if totalMs >= ttftMs {
			fields = append(fields, zap.Int64("after_first_token_ms", totalMs-ttftMs))
		}
	}
	if totalMs >= 0 {
		knownMs := int64(0)
		if hasAuth {
			knownMs += authMs
		}
		if hasRouting {
			knownMs += routingMs
		}
		if hasUpstream {
			knownMs += upstreamMs
		}
		if hasResponse {
			knownMs += responseMs
		}
		if knownMs > 0 && totalMs >= knownMs {
			fields = append(fields, zap.Int64("unattributed_latency_ms", totalMs-knownMs))
		}
	}

	return fields
}

func contextLatencyMs(c *gin.Context, key string) (int64, bool) {
	if c == nil || key == "" {
		return 0, false
	}
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	var ms int64
	switch t := v.(type) {
	case int:
		ms = int64(t)
	case int32:
		ms = int64(t)
	case int64:
		ms = t
	case float64:
		ms = int64(t)
	default:
		return 0, false
	}
	if ms < 0 {
		return 0, false
	}
	return ms, true
}
