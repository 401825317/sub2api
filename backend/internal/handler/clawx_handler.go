package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type ClawXHandler struct {
	authService    *service.AuthService
	userService    *service.UserService
	settingService *service.SettingService
	apiKeyService  *service.APIKeyService
	redeemService  *service.RedeemService
}

func NewClawXHandler(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	apiKeyService *service.APIKeyService,
	redeemService *service.RedeemService,
) *ClawXHandler {
	return &ClawXHandler{
		authService:    authService,
		userService:    userService,
		settingService: settingService,
		apiKeyService:  apiKeyService,
		redeemService:  redeemService,
	}
}

type ClawXDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Arch       string `json:"arch,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
}

type clawXRuntimePayload struct {
	ProviderKey    string   `json:"providerKey"`
	ProviderName   string   `json:"providerName,omitempty"`
	BaseURL        string   `json:"baseUrl"`
	APIProtocol    string   `json:"apiProtocol"`
	DefaultModel   string   `json:"defaultModel"`
	FallbackModels []string `json:"fallbackModels"`
}

type clawXOfflinePayload struct {
	GraceSeconds          int `json:"graceSeconds"`
	VerifyMemoryCacheSecs int `json:"verifyMemoryCacheSeconds"`
}

type clawXDevicePayload struct {
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	Platform   string     `json:"platform,omitempty"`
	Arch       string     `json:"arch,omitempty"`
	AppVersion string     `json:"appVersion,omitempty"`
	Status     string     `json:"status"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

type clawXAuthPayload struct {
	AccessToken  string              `json:"accessToken"`
	RefreshToken string              `json:"refreshToken,omitempty"`
	ExpiresIn    int                 `json:"expiresIn,omitempty"`
	TokenType    string              `json:"tokenType"`
	User         *dto.User           `json:"user"`
	Device       clawXDevicePayload  `json:"device"`
	Runtime      clawXRuntimePayload `json:"runtime"`
	Offline      clawXOfflinePayload `json:"offline"`
}

type clawXRegisterRequest struct {
	Account          string      `json:"account"`
	Email            string      `json:"email"`
	Password         string      `json:"password"`
	ActivationCode   string      `json:"activationCode"`
	ActivationTicket string      `json:"activationTicket"`
	VerifyCode       string      `json:"verifyCode"`
	Device           ClawXDevice `json:"device"`
}

type clawXSendVerifyCodeRequest struct {
	Account string `json:"account"`
	Email   string `json:"email"`
}

type clawXLoginRequest struct {
	Account  string      `json:"account"`
	Email    string      `json:"email"`
	Password string      `json:"password"`
	Device   ClawXDevice `json:"device"`
}

type clawXActivationCheckRequest struct {
	Code   string      `json:"code"`
	Device ClawXDevice `json:"device"`
}

type clawXVerifyRequest struct {
	Device  ClawXDevice         `json:"device"`
	Runtime clawXRuntimePayload `json:"runtime"`
}

type clawXRelayTokenRequest struct {
	Device ClawXDevice `json:"device"`
	Scope  []string    `json:"scope"`
}

type clawXUnregisterDeviceRequest struct {
	DeviceID string `json:"deviceId"`
}

func (h *ClawXHandler) runtimeSettings(c *gin.Context) service.ClawXRuntimeSettings {
	if h == nil || h.settingService == nil {
		return service.ClawXRuntimeSettings{}
	}
	return h.settingService.GetClawXRuntimeSettings(c.Request.Context())
}

func clawXRuntime(settings service.ClawXRuntimeSettings) clawXRuntimePayload {
	return clawXRuntimePayload{
		ProviderKey:    settings.ProviderKey,
		ProviderName:   settings.ProviderName,
		BaseURL:        settings.GatewayBaseURL,
		APIProtocol:    settings.APIProtocol,
		DefaultModel:   settings.DefaultModel,
		FallbackModels: settings.FallbackModels,
	}
}

func clawXOffline(settings service.ClawXRuntimeSettings) clawXOfflinePayload {
	return clawXOfflinePayload{
		GraceSeconds:          settings.OfflineGraceSeconds,
		VerifyMemoryCacheSecs: settings.VerifyMemoryCacheSecs,
	}
}

func clawXDevice(device ClawXDevice) clawXDevicePayload {
	id := strings.TrimSpace(device.ID)
	if id == "" {
		id = "unknown"
	}
	now := time.Now().UTC()
	return clawXDevicePayload{
		ID:         id,
		Name:       strings.TrimSpace(device.Name),
		Platform:   strings.TrimSpace(device.Platform),
		Arch:       strings.TrimSpace(device.Arch),
		AppVersion: strings.TrimSpace(device.AppVersion),
		Status:     "active",
		LastSeenAt: &now,
	}
}

func (h *ClawXHandler) Bootstrap(c *gin.Context) {
	settings := h.runtimeSettings(c)
	if !settings.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    http.StatusServiceUnavailable,
			"message": "server_disabled",
			"reason":  "server_disabled",
		})
		return
	}
	response.Success(c, gin.H{
		"service": gin.H{
			"name":        settings.ProviderKey,
			"displayName": settings.ProviderName,
			"apiOrigin":   settings.APIOrigin,
		},
		"auth": gin.H{
			"registrationEnabled": settings.RegistrationEnabled,
			"loginEnabled":        settings.LoginEnabled,
			"activationRequired":  settings.ActivationRequired,
		},
		"runtime": clawXRuntime(settings),
		"offline": clawXOffline(settings),
		"skills": gin.H{
			"bundledOpenClawEnabled":    true,
			"remoteMarketplaceEnabled":  settings.SkillMarketplaceEnabled,
			"remoteMarketplaceBaseUrl":  nil,
			"requiresRemoteMarketplace": false,
		},
	})
}

func (h *ClawXHandler) ActivationCheck(c *gin.Context) {
	var req clawXActivationCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		response.BadRequest(c, "activation code is required")
		return
	}
	if h.redeemService == nil {
		response.InternalError(c, "activation service unavailable")
		return
	}
	redeemCode, err := h.redeemService.GetByCode(c.Request.Context(), code)
	if err != nil || redeemCode == nil {
		response.Success(c, gin.H{
			"valid":     false,
			"errorCode": "activation_invalid",
		})
		return
	}
	if !service.CanUseClawXActivationRedeemCode(redeemCode) {
		errorCode := "activation_invalid"
		if !redeemCode.CanUse() {
			errorCode = "activation_consumed"
			if redeemCode.IsExpired() {
				errorCode = "activation_expired"
			}
		}
		response.Success(c, gin.H{
			"valid":     false,
			"errorCode": errorCode,
		})
		return
	}
	response.Success(c, gin.H{
		"valid":                true,
		"requiresRegistration": true,
		"activationTicket":     code,
		"expiresIn":            600,
		"device":               clawXDevice(req.Device),
		"entitlementPreview": gin.H{
			"type":         redeemCode.Type,
			"value":        redeemCode.Value,
			"groupId":      redeemCode.GroupID,
			"validityDays": redeemCode.ValidityDays,
			"expiresAt":    redeemCode.ExpiresAt,
		},
	})
}

func (h *ClawXHandler) Register(c *gin.Context) {
	settings := h.runtimeSettings(c)
	if !settings.RegistrationEnabled {
		response.Forbidden(c, "registration is disabled")
		return
	}
	var req clawXRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	email := normalizeClawXAccount(req.Email, req.Account)
	if email == "" || req.Password == "" {
		response.BadRequest(c, "email and password are required")
		return
	}
	invitationCode := firstNonEmptyClawX(req.ActivationTicket, req.ActivationCode)
	if settings.ActivationRequired && strings.TrimSpace(invitationCode) == "" {
		response.BadRequest(c, "activation is required")
		return
	}

	user, err := h.authService.RegisterForClawX(
		c.Request.Context(),
		email,
		req.Password,
		strings.TrimSpace(req.VerifyCode),
		strings.TrimSpace(invitationCode),
		settings.ActivationRequired,
		settings.DefaultGroupID,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.writeAuthPayload(c, user, req.Device)
}

func (h *ClawXHandler) SendVerifyCode(c *gin.Context) {
	settings := h.runtimeSettings(c)
	if !settings.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    http.StatusServiceUnavailable,
			"message": "server_disabled",
			"reason":  "server_disabled",
		})
		return
	}
	if !settings.RegistrationEnabled {
		response.Forbidden(c, "registration is disabled")
		return
	}
	var req clawXSendVerifyCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	email := normalizeClawXAccount(req.Email, req.Account)
	if email == "" {
		response.BadRequest(c, "email is required")
		return
	}
	result, err := h.authService.SendVerifyCodeAsync(c.Request.Context(), email, c.GetHeader("Accept-Language"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"message":   "Verification code sent successfully",
		"countdown": result.Countdown,
	})
}

func (h *ClawXHandler) Login(c *gin.Context) {
	settings := h.runtimeSettings(c)
	if !settings.LoginEnabled {
		response.Forbidden(c, "login is disabled")
		return
	}
	var req clawXLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	email := normalizeClawXAccount(req.Email, req.Account)
	if email == "" || req.Password == "" {
		response.BadRequest(c, "email and password are required")
		return
	}

	_, user, err := h.authService.Login(c.Request.Context(), email, req.Password)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	h.writeAuthPayload(c, user, req.Device)
}

func (h *ClawXHandler) Verify(c *gin.Context) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req clawXVerifyRequest
	_ = c.ShouldBindJSON(&req)
	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	settings := h.runtimeSettings(c)
	response.Success(c, gin.H{
		"valid":      true,
		"serverTime": time.Now().UTC().Format(time.RFC3339),
		"user":       dto.UserFromService(user),
		"device":     clawXDevice(req.Device),
		"entitlements": gin.H{
			"providerEnabled":     settings.Enabled,
			"modelGatewayEnabled": settings.Enabled,
			"skillsEnabled":       true,
			"groupIds":            user.AllowedGroups,
		},
		"runtime": clawXRuntime(settings),
		"offline": clawXOffline(settings),
	})
}

func (h *ClawXHandler) RelayToken(c *gin.Context) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req clawXRelayTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := h.runtimeSettings(c)
	if !settings.Enabled {
		response.Forbidden(c, "server disabled")
		return
	}
	name := "ClawX"
	deviceID := strings.TrimSpace(req.Device.ID)
	if deviceID != "" {
		name = "ClawX " + deviceID
	}

	existing, err := h.findRelayAPIKey(c, subject.UserID, name)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if existing != nil {
		response.Success(c, h.relayTokenPayload(settings, existing.Key))
		return
	}

	if settings.DefaultGroupID != nil && *settings.DefaultGroupID > 0 {
		if err := h.userService.AddGroupToAllowedGroups(c.Request.Context(), subject.UserID, *settings.DefaultGroupID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	key, err := h.apiKeyService.Create(c.Request.Context(), subject.UserID, service.CreateAPIKeyRequest{
		Name:    name,
		GroupID: settings.DefaultGroupID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.relayTokenPayload(settings, key.Key))
}

func (h *ClawXHandler) UserSelf(c *gin.Context) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"user":    dto.UserFromService(user),
		"groups":  user.AllowedGroups,
		"devices": []any{},
	})
}

func (h *ClawXHandler) UnregisterDevice(c *gin.Context) {
	var req clawXUnregisterDeviceRequest
	_ = c.ShouldBindJSON(&req)
	response.Success(c, gin.H{
		"removed":  strings.TrimSpace(req.DeviceID) != "",
		"deviceId": strings.TrimSpace(req.DeviceID),
	})
}

func (h *ClawXHandler) writeAuthPayload(c *gin.Context, user *service.User, device ClawXDevice) {
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	tokenPair, err := h.authService.GenerateTokenPair(c.Request.Context(), user, "")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	settings := h.runtimeSettings(c)
	response.Success(c, clawXAuthPayload{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
		TokenType:    "Bearer",
		User:         dto.UserFromService(user),
		Device:       clawXDevice(device),
		Runtime:      clawXRuntime(settings),
		Offline:      clawXOffline(settings),
	})
}

func (h *ClawXHandler) relayTokenPayload(settings service.ClawXRuntimeSettings, token string) gin.H {
	return gin.H{
		"token":     token,
		"tokenType": "sub2api-api-key",
		"expiresIn": nil,
		"runtime":   clawXRuntime(settings),
		"warning": func() any {
			if settings.DefaultGroupID == nil {
				return "clawx_default_group_id is not configured; gateway may reject this key until it is bound to a group"
			}
			return nil
		}(),
	}
}

func (h *ClawXHandler) findRelayAPIKey(c *gin.Context, userID int64, name string) (*service.APIKey, error) {
	keys, _, err := h.apiKeyService.List(c.Request.Context(), userID, pagination.PaginationParams{
		Page:      1,
		PageSize:  1000,
		SortBy:    "created_at",
		SortOrder: "desc",
	}, service.APIKeyListFilters{
		Search: name,
		Status: service.StatusActive,
	})
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].Name == name && keys[i].Status == service.StatusActive && !keys[i].IsExpired() {
			return &keys[i], nil
		}
	}
	return nil, nil
}

func normalizeClawXAccount(email, account string) string {
	email = strings.TrimSpace(email)
	if email != "" {
		return email
	}
	return strings.TrimSpace(account)
}

func firstNonEmptyClawX(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
