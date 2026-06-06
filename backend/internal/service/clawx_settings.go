package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

const (
	SettingKeyClawXEnabled                 = "clawx_enabled"
	SettingKeyClawXRegistrationEnabled     = "clawx_registration_enabled"
	SettingKeyClawXLoginEnabled            = "clawx_login_enabled"
	SettingKeyClawXActivationRequired      = "clawx_activation_required"
	SettingKeyClawXGatewayBaseURL          = "clawx_gateway_base_url"
	SettingKeyClawXProviderKey             = "clawx_provider_key"
	SettingKeyClawXProviderName            = "clawx_provider_name"
	SettingKeyClawXAPIProtocol             = "clawx_api_protocol"
	SettingKeyClawXDefaultModel            = "clawx_default_model"
	SettingKeyClawXFallbackModels          = "clawx_fallback_models"
	SettingKeyClawXOfflineGraceSeconds     = "clawx_offline_grace_seconds"
	SettingKeyClawXVerifyMemoryCacheSecs   = "clawx_verify_memory_cache_seconds"
	SettingKeyClawXSkillMarketplaceEnabled = "clawx_skill_marketplace_enabled"
	SettingKeyClawXDefaultGroupID          = "clawx_default_group_id"
)

const (
	defaultClawXAPIOrigin             = "https://junfeiai.com"
	defaultClawXGatewayBaseURL        = defaultClawXAPIOrigin + "/v1"
	defaultClawXProviderKey           = "junfeiai"
	defaultClawXProviderName          = "JunFeiAI"
	defaultClawXAPIProtocol           = "anthropic-messages"
	defaultClawXDefaultModel          = "gpt-5.5"
	defaultClawXOfflineGraceSeconds   = 7 * 24 * 60 * 60
	defaultClawXVerifyMemoryCacheSecs = 300
)

type ClawXRuntimeSettings struct {
	Enabled                 bool
	RegistrationEnabled     bool
	LoginEnabled            bool
	ActivationRequired      bool
	APIOrigin               string
	GatewayBaseURL          string
	GatewayBaseURLExplicit  bool
	ProviderKey             string
	ProviderName            string
	APIProtocol             string
	DefaultModel            string
	FallbackModels          []string
	OfflineGraceSeconds     int
	VerifyMemoryCacheSecs   int
	SkillMarketplaceEnabled bool
	DefaultGroupID          *int64
}

func (s *SettingService) GetClawXRuntimeSettings(ctx context.Context) ClawXRuntimeSettings {
	out := ClawXRuntimeSettings{
		Enabled:               true,
		RegistrationEnabled:   true,
		LoginEnabled:          true,
		ActivationRequired:    false,
		APIOrigin:             defaultClawXAPIOrigin,
		GatewayBaseURL:        defaultClawXGatewayBaseURL,
		ProviderKey:           defaultClawXProviderKey,
		ProviderName:          defaultClawXProviderName,
		APIProtocol:           defaultClawXAPIProtocol,
		DefaultModel:          defaultClawXDefaultModel,
		OfflineGraceSeconds:   defaultClawXOfflineGraceSeconds,
		VerifyMemoryCacheSecs: defaultClawXVerifyMemoryCacheSecs,
	}
	if s == nil || s.settingRepo == nil {
		return out
	}

	public, _ := s.GetPublicSettings(ctx)
	if public != nil {
		out.RegistrationEnabled = public.RegistrationEnabled
		if raw := strings.TrimSpace(public.APIBaseURL); raw != "" {
			applyClawXBaseURL(&out, raw)
		}
	}

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyClawXEnabled,
		SettingKeyClawXRegistrationEnabled,
		SettingKeyClawXLoginEnabled,
		SettingKeyClawXActivationRequired,
		SettingKeyClawXGatewayBaseURL,
		SettingKeyClawXProviderKey,
		SettingKeyClawXProviderName,
		SettingKeyClawXAPIProtocol,
		SettingKeyClawXDefaultModel,
		SettingKeyClawXFallbackModels,
		SettingKeyClawXOfflineGraceSeconds,
		SettingKeyClawXVerifyMemoryCacheSecs,
		SettingKeyClawXSkillMarketplaceEnabled,
		SettingKeyClawXDefaultGroupID,
	})
	if err != nil {
		return out
	}

	if raw, ok := values[SettingKeyClawXEnabled]; ok {
		out.Enabled = !isFalseSettingValue(raw)
	}
	if raw, ok := values[SettingKeyClawXRegistrationEnabled]; ok && strings.TrimSpace(raw) != "" {
		out.RegistrationEnabled = raw == "true"
	}
	if raw, ok := values[SettingKeyClawXLoginEnabled]; ok && strings.TrimSpace(raw) != "" {
		out.LoginEnabled = !isFalseSettingValue(raw)
	}
	if raw, ok := values[SettingKeyClawXActivationRequired]; ok && strings.TrimSpace(raw) != "" {
		out.ActivationRequired = raw == "true"
	}
	if raw := strings.TrimSpace(values[SettingKeyClawXGatewayBaseURL]); raw != "" {
		applyClawXBaseURL(&out, raw)
		out.GatewayBaseURLExplicit = true
	}
	if raw := strings.TrimSpace(values[SettingKeyClawXProviderKey]); raw != "" {
		out.ProviderKey = raw
	}
	if raw := strings.TrimSpace(values[SettingKeyClawXProviderName]); raw != "" {
		out.ProviderName = raw
	}
	if raw := strings.TrimSpace(values[SettingKeyClawXAPIProtocol]); raw != "" {
		out.APIProtocol = raw
	}
	if raw := strings.TrimSpace(values[SettingKeyClawXDefaultModel]); raw != "" {
		out.DefaultModel = raw
	}
	if models := parseClawXFallbackModels(values[SettingKeyClawXFallbackModels]); len(models) > 0 {
		out.FallbackModels = models
	}
	if seconds, ok := parsePositiveInt(values[SettingKeyClawXOfflineGraceSeconds]); ok {
		out.OfflineGraceSeconds = seconds
	}
	if seconds, ok := parsePositiveInt(values[SettingKeyClawXVerifyMemoryCacheSecs]); ok {
		out.VerifyMemoryCacheSecs = seconds
	}
	if raw, ok := values[SettingKeyClawXSkillMarketplaceEnabled]; ok && strings.TrimSpace(raw) != "" {
		out.SkillMarketplaceEnabled = raw == "true"
	}
	if groupID, ok := parsePositiveInt64(values[SettingKeyClawXDefaultGroupID]); ok {
		out.DefaultGroupID = &groupID
	}

	return out
}

func ApplyClawXRequestOrigin(settings ClawXRuntimeSettings, raw string) ClawXRuntimeSettings {
	if settings.GatewayBaseURLExplicit {
		return settings
	}
	applyClawXBaseURL(&settings, raw)
	return settings
}

func applyClawXBaseURL(out *ClawXRuntimeSettings, raw string) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return
	}
	if strings.HasSuffix(raw, "/v1") {
		out.GatewayBaseURL = raw
		out.APIOrigin = strings.TrimSuffix(raw, "/v1")
		return
	}
	out.APIOrigin = raw
	out.GatewayBaseURL = raw + "/v1"
}

func parseClawXFallbackModels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values []string
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil
		}
	} else {
		values = strings.Split(raw, ",")
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func parsePositiveInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func parsePositiveInt64(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}
