package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

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

func TestRelayAPIKeyNeedsDefaultGroupBindingOnlyForUngroupedKeys(t *testing.T) {
	defaultGroupID := int64(2)
	existingGroupID := int64(7)
	settings := service.ClawXRuntimeSettings{DefaultGroupID: &defaultGroupID}

	if !relayAPIKeyNeedsDefaultGroupBinding(&service.APIKey{}, settings) {
		t.Fatal("ungrouped relay key should need default group binding")
	}
	if relayAPIKeyNeedsDefaultGroupBinding(&service.APIKey{GroupID: &existingGroupID}, settings) {
		t.Fatal("already grouped relay key should not be rebound")
	}
	if relayAPIKeyNeedsDefaultGroupBinding(&service.APIKey{}, service.ClawXRuntimeSettings{}) {
		t.Fatal("relay key should not be rebound without a configured default group")
	}
}
