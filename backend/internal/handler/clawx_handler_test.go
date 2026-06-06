package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestClawXRequestOriginUsesForwardedPublicOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "http://internal/api/clawx/bootstrap", nil)
	req.Header.Set("X-Forwarded-Host", "zz-cn.lingzhiwuxian.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	got := clawXRequestOrigin(c)

	if got != "https://zz-cn.lingzhiwuxian.com" {
		t.Fatalf("clawXRequestOrigin = %q, want https://zz-cn.lingzhiwuxian.com", got)
	}
}

func TestClawXRequestOriginKeepsLocalHTTPFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/clawx/bootstrap", nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	got := clawXRequestOrigin(c)

	if got != "http://127.0.0.1:8080" {
		t.Fatalf("clawXRequestOrigin = %q, want http://127.0.0.1:8080", got)
	}
}
