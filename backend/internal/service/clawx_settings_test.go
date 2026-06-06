package service

import (
	"context"
	"testing"
)

type clawXSettingsRepoStub struct {
	values map[string]string
}

func (r *clawXSettingsRepoStub) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *clawXSettingsRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *clawXSettingsRepoStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *clawXSettingsRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *clawXSettingsRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *clawXSettingsRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *clawXSettingsRepoStub) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestGetClawXRuntimeSettingsHandlesPublicAPIBaseURLWithV1(t *testing.T) {
	svc := &SettingService{settingRepo: &clawXSettingsRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled: "true",
		SettingKeyEmailVerifyEnabled:  "true",
		SettingKeyAPIBaseURL:          "https://api.example.test/v1/",
	}}}

	got := svc.GetClawXRuntimeSettings(context.Background())

	if got.APIOrigin != "https://api.example.test" {
		t.Fatalf("APIOrigin = %q, want https://api.example.test", got.APIOrigin)
	}
	if got.GatewayBaseURL != "https://api.example.test/v1" {
		t.Fatalf("GatewayBaseURL = %q, want https://api.example.test/v1", got.GatewayBaseURL)
	}
	if got.GatewayBaseURLExplicit {
		t.Fatal("GatewayBaseURLExplicit = true, want false for public API base URL")
	}
	if !got.RegistrationEnabled {
		t.Fatal("RegistrationEnabled should inherit public registration setting")
	}
	if !got.EmailVerifyEnabled {
		t.Fatal("EmailVerifyEnabled should inherit public email verify setting")
	}
}

func TestGetClawXRuntimeSettingsAppliesClawXOverrides(t *testing.T) {
	svc := &SettingService{settingRepo: &clawXSettingsRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled:          "true",
		SettingKeyClawXEnabled:                 "false",
		SettingKeyClawXRegistrationEnabled:     "false",
		SettingKeyClawXLoginEnabled:            "0",
		SettingKeyClawXActivationRequired:      "true",
		SettingKeyClawXGatewayBaseURL:          "https://gateway.example.test",
		SettingKeyClawXProviderKey:             "custom-provider",
		SettingKeyClawXProviderName:            "Custom Provider",
		SettingKeyClawXAPIProtocol:             "openai-chat-completions",
		SettingKeyClawXDefaultModel:            "model-primary",
		SettingKeyClawXFallbackModels:          `["model-a","model-b","model-a",""]`,
		SettingKeyClawXOfflineGraceSeconds:     "120",
		SettingKeyClawXVerifyMemoryCacheSecs:   "15",
		SettingKeyClawXSkillMarketplaceEnabled: "true",
		SettingKeyClawXDefaultGroupID:          "42",
	}}}

	got := svc.GetClawXRuntimeSettings(context.Background())

	if got.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	if got.RegistrationEnabled {
		t.Fatal("RegistrationEnabled = true, want false")
	}
	if got.LoginEnabled {
		t.Fatal("LoginEnabled = true, want false")
	}
	if !got.ActivationRequired {
		t.Fatal("ActivationRequired = false, want true")
	}
	if got.APIOrigin != "https://gateway.example.test" || got.GatewayBaseURL != "https://gateway.example.test/v1" {
		t.Fatalf("base URLs = %q / %q", got.APIOrigin, got.GatewayBaseURL)
	}
	if !got.GatewayBaseURLExplicit {
		t.Fatal("GatewayBaseURLExplicit = false, want true")
	}
	if got.ProviderKey != "custom-provider" || got.ProviderName != "Custom Provider" {
		t.Fatalf("provider = %q / %q", got.ProviderKey, got.ProviderName)
	}
	if got.APIProtocol != "openai-chat-completions" || got.DefaultModel != "model-primary" {
		t.Fatalf("runtime protocol/model = %q / %q", got.APIProtocol, got.DefaultModel)
	}
	if len(got.FallbackModels) != 2 || got.FallbackModels[0] != "model-a" || got.FallbackModels[1] != "model-b" {
		t.Fatalf("FallbackModels = %#v, want model-a/model-b", got.FallbackModels)
	}
	if got.OfflineGraceSeconds != 120 || got.VerifyMemoryCacheSecs != 15 {
		t.Fatalf("offline settings = %d / %d", got.OfflineGraceSeconds, got.VerifyMemoryCacheSecs)
	}
	if !got.SkillMarketplaceEnabled {
		t.Fatal("SkillMarketplaceEnabled = false, want true")
	}
	if got.DefaultGroupID == nil || *got.DefaultGroupID != 42 {
		t.Fatalf("DefaultGroupID = %#v, want 42", got.DefaultGroupID)
	}
}

func TestApplyClawXRequestOriginOverridesNonExplicitBaseURL(t *testing.T) {
	settings := ClawXRuntimeSettings{
		APIOrigin:      "https://junfeiai.com",
		GatewayBaseURL: "https://junfeiai.com/v1",
	}

	got := ApplyClawXRequestOrigin(settings, "https://zz-cn.lingzhiwuxian.com")

	if got.APIOrigin != "https://zz-cn.lingzhiwuxian.com" {
		t.Fatalf("APIOrigin = %q, want https://zz-cn.lingzhiwuxian.com", got.APIOrigin)
	}
	if got.GatewayBaseURL != "https://zz-cn.lingzhiwuxian.com/v1" {
		t.Fatalf("GatewayBaseURL = %q, want https://zz-cn.lingzhiwuxian.com/v1", got.GatewayBaseURL)
	}
}

func TestApplyClawXRequestOriginKeepsExplicitGatewayOverride(t *testing.T) {
	settings := ClawXRuntimeSettings{
		APIOrigin:              "https://gateway.example.test",
		GatewayBaseURL:         "https://gateway.example.test/v1",
		GatewayBaseURLExplicit: true,
	}

	got := ApplyClawXRequestOrigin(settings, "https://zz-cn.lingzhiwuxian.com")

	if got.APIOrigin != "https://gateway.example.test" {
		t.Fatalf("APIOrigin = %q, want https://gateway.example.test", got.APIOrigin)
	}
	if got.GatewayBaseURL != "https://gateway.example.test/v1" {
		t.Fatalf("GatewayBaseURL = %q, want https://gateway.example.test/v1", got.GatewayBaseURL)
	}
}
