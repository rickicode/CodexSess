package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleWebSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		base := externalBaseURLFromRequest(r, s.bindAddr)
		modelMappings := s.currentModelMappings()
		s.mu.RLock()
		usageAlertThreshold := s.svc.Cfg.UsageAlertThreshold
		usageAutoSwitchThreshold := s.svc.Cfg.UsageAutoSwitchThreshold
		usageSchedulerEnabled := s.svc.Cfg.UsageSchedulerEnabled
		usageSchedulerInterval := config.NormalizeUsageSchedulerIntervalMinutes(s.svc.Cfg.UsageSchedulerInterval)
		usageRefreshTimeoutSec := config.NormalizeUsageRefreshTimeoutSeconds(s.svc.Cfg.UsageRefreshTimeoutSec)
		usageSwitchTimeoutSec := config.NormalizeUsageSwitchTimeoutSeconds(s.svc.Cfg.UsageSwitchTimeoutSec)
		directAPIStrategy := config.NormalizeDirectAPIStrategy(s.svc.Cfg.DirectAPIStrategy)
		zoStrategy := config.NormalizeZoAPIStrategy(s.svc.Cfg.ZoAPIStrategy)
		s.mu.RUnlock()
		updateInfo := s.getUpdateInfo(r.Context(), false)
		claudeCodeStatus := s.claudeCodeIntegrationStatus(base)
		respondJSON(w, 200, map[string]any{
			"api_key":                          s.currentAPIKey(),
			"api_mode":                         s.currentAPIMode(),
			"openai_endpoint":                  strings.TrimRight(base, "/") + "/v1/chat/completions",
			"claude_endpoint":                  strings.TrimRight(base, "/") + "/v1/messages",
			"auth_json_endpoint":               strings.TrimRight(base, "/") + "/v1/auth.json",
			"usage_status_endpoint":            strings.TrimRight(base, "/") + "/v1/usage",
			"openai_models_url":                strings.TrimRight(base, "/") + "/v1/models",
			"openai_chat_url":                  strings.TrimRight(base, "/") + "/v1/chat/completions",
			"openai_responses_url":             strings.TrimRight(base, "/") + "/v1/responses",
			"zo_models_url":                    strings.TrimRight(base, "/") + "/zo/v1/models",
			"zo_chat_url":                      strings.TrimRight(base, "/") + "/zo/v1/chat/completions",
			"available_models":                 codexAvailableModels(),
			"model_mappings":                   modelMappings,
			"usage_alert_threshold":            usageAlertThreshold,
			"usage_auto_switch_threshold":      usageAutoSwitchThreshold,
			"usage_scheduler_enabled":          usageSchedulerEnabled,
			"usage_scheduler_interval_minutes": usageSchedulerInterval,
			"usage_refresh_timeout_seconds":    usageRefreshTimeoutSec,
			"usage_switch_timeout_seconds":     usageSwitchTimeoutSec,
			"direct_api_strategy":              directAPIStrategy,
			"zo_api_strategy":                  zoStrategy,
			"direct_api_inject_prompt":         true,
			"claude_code":                      claudeCodeStatus,
			"app_version":                      updateInfo.CurrentVersion,
			"codex_version":                    firstNonEmpty(s.codexVersion, "unknown"),
			"latest_version":                   updateInfo.LatestVersion,
			"release_url":                      updateInfo.ReleaseURL,
			"latest_changelog":                 updateInfo.LatestChangelog,
			"update_available":                 updateInfo.UpdateAvailable,
			"update_checked_at":                updateInfo.CheckedAt,
			"update_check_error":               updateInfo.CheckError,
		})
		return
	case http.MethodPost:
		var req struct {
			APIMode                  *string `json:"api_mode"`
			UsageAlertThreshold      *int    `json:"usage_alert_threshold"`
			UsageAutoSwitchThreshold *int    `json:"usage_auto_switch_threshold"`
			UsageSchedulerEnabled    *bool   `json:"usage_scheduler_enabled"`
			UsageSchedulerInterval   *int    `json:"usage_scheduler_interval_minutes"`
			UsageRefreshTimeoutSec   *int    `json:"usage_refresh_timeout_seconds"`
			UsageSwitchTimeoutSec    *int    `json:"usage_switch_timeout_seconds"`
			DirectAPIStrategy        *string `json:"direct_api_strategy"`
			ZoAPIStrategy            *string `json:"zo_api_strategy"`
			AdminPassword            *string `json:"admin_password"`
			DirectAPIInjectPrompt    *bool   `json:"direct_api_inject_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		s.mu.Lock()
		cfg := s.svc.Cfg
		before := cfg
		if req.APIMode != nil {
			cfg.APIMode = config.NormalizeAPIMode(strings.TrimSpace(*req.APIMode))
		}
		if req.UsageAlertThreshold != nil {
			v := *req.UsageAlertThreshold
			if v < 0 || v > 100 {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "usage_alert_threshold must be in range 0..100")
				return
			}
			cfg.UsageAlertThreshold = v
		}
		if req.UsageAutoSwitchThreshold != nil {
			v := *req.UsageAutoSwitchThreshold
			if v < 0 || v > 100 {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "usage_auto_switch_threshold must be in range 0..100")
				return
			}
			cfg.UsageAutoSwitchThreshold = v
		}
		if req.UsageSchedulerEnabled != nil {
			cfg.UsageSchedulerEnabled = *req.UsageSchedulerEnabled
		}
		if req.UsageSchedulerInterval != nil {
			v := config.NormalizeUsageSchedulerIntervalMinutes(*req.UsageSchedulerInterval)
			cfg.UsageSchedulerInterval = v
		}
		if req.UsageRefreshTimeoutSec != nil {
			cfg.UsageRefreshTimeoutSec = config.NormalizeUsageRefreshTimeoutSeconds(*req.UsageRefreshTimeoutSec)
		}
		if req.UsageSwitchTimeoutSec != nil {
			cfg.UsageSwitchTimeoutSec = config.NormalizeUsageSwitchTimeoutSeconds(*req.UsageSwitchTimeoutSec)
		}
		if req.DirectAPIStrategy != nil {
			cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(strings.TrimSpace(*req.DirectAPIStrategy))
		}
		if req.ZoAPIStrategy != nil {
			cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(strings.TrimSpace(*req.ZoAPIStrategy))
		}
		var adminPasswordChanged bool
		if req.AdminPassword != nil {
			plain := strings.TrimSpace(*req.AdminPassword)
			if plain == "" {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "admin_password cannot be empty")
				return
			}
			cfg.AdminPasswordHash = config.HashPassword(plain)
			adminPasswordChanged = true
		}
		cfg.DirectAPIInjectPrompt = true
		s.svc.Cfg = cfg
		s.adminPasswordHash = strings.TrimSpace(cfg.AdminPasswordHash)
		s.mu.Unlock()
		if req.APIMode != nil {
			if err := s.saveSetting(r.Context(), store.SettingAPIMode, cfg.APIMode); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.DirectAPIStrategy != nil {
			if err := s.saveSetting(r.Context(), store.SettingDirectAPIStrategy, cfg.DirectAPIStrategy); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.ZoAPIStrategy != nil {
			if err := s.saveSetting(r.Context(), store.SettingZoAPIStrategy, cfg.ZoAPIStrategy); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageAlertThreshold != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageAlertThreshold, strconv.Itoa(cfg.UsageAlertThreshold)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageAutoSwitchThreshold != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageAutoSwitchThreshold, strconv.Itoa(cfg.UsageAutoSwitchThreshold)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageSchedulerEnabled != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageSchedulerEnabled, strconv.FormatBool(cfg.UsageSchedulerEnabled)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageSchedulerInterval != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageSchedulerInterval, strconv.Itoa(cfg.UsageSchedulerInterval)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageRefreshTimeoutSec != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageRefreshTimeoutSec, strconv.Itoa(cfg.UsageRefreshTimeoutSec)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageSwitchTimeoutSec != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageSwitchTimeoutSec, strconv.Itoa(cfg.UsageSwitchTimeoutSec)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if adminPasswordChanged {
			if err := s.saveSetting(r.Context(), store.SettingAdminPasswordHash, cfg.AdminPasswordHash); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}

		changed := map[string]map[string]any{}
		if before.APIMode != cfg.APIMode {
			changed["api_mode"] = map[string]any{"from": before.APIMode, "to": cfg.APIMode}
		}
		if before.UsageAlertThreshold != cfg.UsageAlertThreshold {
			changed["usage_alert_threshold"] = map[string]any{"from": before.UsageAlertThreshold, "to": cfg.UsageAlertThreshold}
		}
		if before.UsageAutoSwitchThreshold != cfg.UsageAutoSwitchThreshold {
			changed["usage_auto_switch_threshold"] = map[string]any{"from": before.UsageAutoSwitchThreshold, "to": cfg.UsageAutoSwitchThreshold}
		}
		if before.UsageSchedulerEnabled != cfg.UsageSchedulerEnabled {
			changed["usage_scheduler_enabled"] = map[string]any{"from": before.UsageSchedulerEnabled, "to": cfg.UsageSchedulerEnabled}
		}
		if before.UsageSchedulerInterval != cfg.UsageSchedulerInterval {
			changed["usage_scheduler_interval_minutes"] = map[string]any{"from": before.UsageSchedulerInterval, "to": cfg.UsageSchedulerInterval}
		}
		if before.UsageRefreshTimeoutSec != cfg.UsageRefreshTimeoutSec {
			changed["usage_refresh_timeout_seconds"] = map[string]any{"from": before.UsageRefreshTimeoutSec, "to": cfg.UsageRefreshTimeoutSec}
		}
		if before.UsageSwitchTimeoutSec != cfg.UsageSwitchTimeoutSec {
			changed["usage_switch_timeout_seconds"] = map[string]any{"from": before.UsageSwitchTimeoutSec, "to": cfg.UsageSwitchTimeoutSec}
		}
		if before.DirectAPIStrategy != cfg.DirectAPIStrategy {
			changed["direct_api_strategy"] = map[string]any{"from": before.DirectAPIStrategy, "to": cfg.DirectAPIStrategy}
		}
		if before.ZoAPIStrategy != cfg.ZoAPIStrategy {
			changed["zo_api_strategy"] = map[string]any{"from": before.ZoAPIStrategy, "to": cfg.ZoAPIStrategy}
		}
		if req.AdminPassword != nil {
			changed["admin_password"] = map[string]any{"from": "updated", "to": "updated"}
		}
		if len(changed) > 0 {
			s.svc.AddSystemLog(r.Context(), "settings_change", "Settings updated", map[string]any{
				"changed": changed,
				"source":  "ui",
			})
		}

		respondJSON(w, 200, map[string]any{
			"api_mode":                         cfg.APIMode,
			"direct_api_strategy":              cfg.DirectAPIStrategy,
			"zo_api_strategy":                  cfg.ZoAPIStrategy,
			"direct_api_inject_prompt":         true,
			"ok":                               true,
			"usage_alert_threshold":            cfg.UsageAlertThreshold,
			"usage_auto_switch_threshold":      cfg.UsageAutoSwitchThreshold,
			"usage_scheduler_enabled":          cfg.UsageSchedulerEnabled,
			"usage_scheduler_interval_minutes": config.NormalizeUsageSchedulerIntervalMinutes(cfg.UsageSchedulerInterval),
			"usage_refresh_timeout_seconds":    config.NormalizeUsageRefreshTimeoutSeconds(cfg.UsageRefreshTimeoutSec),
			"usage_switch_timeout_seconds":     config.NormalizeUsageSwitchTimeoutSeconds(cfg.UsageSwitchTimeoutSec),
		})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) shouldInjectDirectAPIPrompt() bool {
	return true
}

func (s *Server) handleWebClaudeCodeSettings(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURLFromRequest(r, s.bindAddr)
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"claude_code": s.claudeCodeIntegrationStatus(base),
		})
		return
	case http.MethodPost:
		status, err := s.enableClaudeCodeIntegration(base, claudeCodeEnableOptions{})
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"claude_code": status,
		})
		return
	default:
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
}

type claudeCodeIntegrationStatus struct {
	Connected       bool              `json:"connected"`
	BaseURL         string            `json:"base_url"`
	APIKey          string            `json:"api_key"`
	Provider        string            `json:"provider"`
	EnvFilePath     string            `json:"env_file_path"`
	Profiles        []string          `json:"profiles"`
	ModelPreset     map[string]string `json:"model_preset"`
	ActivateCommand string            `json:"activate_command"`
}

var claudeCodeModelPresetDefaults = map[string]string{
	"claude-opus-4-1":            "gpt-5.3-codex",
	"claude-opus-4-0":            "gpt-5.3-codex",
	"claude-3-opus-20240229":     "gpt-5.3-codex",
	"claude-sonnet-4-6":          "gpt-5.2-codex",
	"claude-sonnet-4-5":          "gpt-5.2-codex",
	"claude-sonnet-4-1":          "gpt-5.2-codex",
	"claude-sonnet-4-0":          "gpt-5.2-codex",
	"claude-sonnet-4":            "gpt-5.2-codex",
	"claude-3-7-sonnet-latest":   "gpt-5.2-codex",
	"claude-3-5-sonnet-latest":   "gpt-5.1-codex-max",
	"claude-3-5-sonnet-20241022": "gpt-5.1-codex-max",
	"claude-haiku-4-5-20251001":  "gpt-5.1-codex-max",
	"claude-haiku-4-5":           "gpt-5.1-codex-max",
	"claude-3-5-haiku-latest":    "gpt-5.1-codex-max",
	"claude-3-5-haiku-20241022":  "gpt-5.1-codex-max",
	"claude-haiku-3-5":           "gpt-5.1-codex-max",
}

func (s *Server) claudeCodeIntegrationStatus(base string) claudeCodeIntegrationStatus {
	baseURL := strings.TrimRight(strings.TrimSpace(base), "/")
	provider := "codex"
	if baseURL != "" {
		if apiKey := strings.TrimSpace(s.currentAPIKey()); apiKey != "" {
			_ = ensureClaudeEnvFile(baseURL, apiKey)
		}
	}
	envPath, profilePaths := claudeCodeIntegrationPaths()
	currentMappings := s.currentModelMappings()
	profilesConfigured := make([]string, 0, len(profilePaths))
	for _, p := range profilePaths {
		ok, err := profileHasCodexsessSource(p, envPath)
		if err == nil && ok {
			profilesConfigured = append(profilesConfigured, p)
		}
	}
	connected := false
	if len(profilesConfigured) > 0 {
		if b, err := os.ReadFile(envPath); err == nil {
			content := string(b)
			hasBase := strings.Contains(content, "ANTHROPIC_BASE_URL=")
			hasToken := strings.Contains(content, "ANTHROPIC_AUTH_TOKEN=")
			connected = hasBase && hasToken
		}
	}
	return claudeCodeIntegrationStatus{
		Connected:       connected,
		BaseURL:         baseURL,
		APIKey:          s.currentAPIKey(),
		Provider:        provider,
		EnvFilePath:     envPath,
		Profiles:        profilesConfigured,
		ModelPreset:     mergedClaudeModelPreset(currentMappings),
		ActivateCommand: claudeCodeActivateCommand(envPath),
	}
}

func claudeCodeActivateCommand(envPath string) string {
	escaped := strings.ReplaceAll(strings.TrimSpace(envPath), `"`, `\"`)
	if escaped == "" {
		return ""
	}
	return `. "` + escaped + `"`
}

type claudeCodeEnableOptions struct {
}

func (s *Server) enableClaudeCodeIntegration(base string, opts claudeCodeEnableOptions) (claudeCodeIntegrationStatus, error) {
	_ = opts
	baseURL := strings.TrimRight(strings.TrimSpace(base), "/")
	if baseURL == "" {
		return claudeCodeIntegrationStatus{}, fmt.Errorf("failed to resolve CodexSess base URL")
	}
	apiKey := strings.TrimSpace(s.currentAPIKey())
	if apiKey == "" {
		return claudeCodeIntegrationStatus{}, fmt.Errorf("api key is empty")
	}
	if err := ensureClaudeSettings(baseURL, apiKey, ""); err != nil {
		return claudeCodeIntegrationStatus{}, err
	}
	if err := ensureClaudeOnboardingCompleted(); err != nil {
		return claudeCodeIntegrationStatus{}, err
	}

	envPath, profilePaths := claudeCodeIntegrationPaths()
	if err := ensureClaudeEnvFile(baseURL, apiKey); err != nil {
		return claudeCodeIntegrationStatus{}, err
	}

	for _, profile := range profilePaths {
		if err := ensureCodexsessProfileIntegration(profile, envPath); err != nil {
			return claudeCodeIntegrationStatus{}, err
		}
	}

	s.mu.Lock()
	cfg := s.svc.Cfg
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	for alias, model := range claudeCodeModelPresetDefaults {
		key := strings.TrimSpace(alias)
		if key == "" {
			continue
		}
		existing := strings.TrimSpace(cfg.ModelMappings[key])
		if existing == "" || strings.Contains(existing, ":") || !isValidCodexModel(existing) {
			cfg.ModelMappings[key] = strings.TrimSpace(model)
		}
	}
	s.svc.Cfg = cfg
	s.mu.Unlock()
	if raw, err := json.Marshal(cfg.ModelMappings); err == nil {
		_ = s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
	}

	return s.claudeCodeIntegrationStatus(baseURL), nil
}

func ensureClaudeEnvFile(baseURL string, apiKey string) error {
	envPath, _ := claudeCodeIntegrationPaths()
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		return err
	}
	envContent := fmt.Sprintf(
		"# Managed by CodexSess\nunset ANTHROPIC_API_KEY\nexport ANTHROPIC_BASE_URL=%q\nexport ANTHROPIC_AUTH_TOKEN=%q\n",
		strings.TrimSpace(baseURL),
		strings.TrimSpace(apiKey),
	)
	return os.WriteFile(envPath, []byte(envContent), 0o600)
}

func ensureClaudeSettings(baseURL string, apiKey string, forcedModel string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	doc := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			log.Printf("claude settings parse skipped: path=%s err=%v", settingsPath, err)
			return nil
		}
	}

	permissions, ok := doc["permissions"].(map[string]any)
	if !ok {
		if existing, exists := doc["permissions"]; exists && existing != nil {
			log.Printf("claude settings permissions skipped: path=%s unexpected_type=%T", settingsPath, existing)
			return nil
		}
		permissions = map[string]any{}
	}
	permissions["defaultMode"] = "bypassPermissions"
	doc["permissions"] = permissions

	envMap, ok := doc["env"].(map[string]any)
	if !ok {
		if existing, exists := doc["env"]; exists && existing != nil {
			log.Printf("claude settings env skipped: path=%s unexpected_type=%T", settingsPath, existing)
			return nil
		}
		envMap = map[string]any{}
	}
	envMap["ANTHROPIC_BASE_URL"] = strings.TrimSpace(baseURL)
	envMap["ANTHROPIC_AUTH_TOKEN"] = strings.TrimSpace(apiKey)
	delete(envMap, "ANTHROPIC_API_KEY")
	doc["env"] = envMap

	model := strings.TrimSpace(forcedModel)
	if model != "" {
		doc["model"] = model
	} else {
		delete(doc, "model")
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(settingsPath, out, 0o600)
}

func ensureClaudeOnboardingCompleted() error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	doc := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			log.Printf("claude onboarding parse skipped: path=%s err=%v", path, err)
			return nil
		}
	}
	doc["hasCompletedOnboarding"] = true
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o600)
}

func (s *Server) enforceClaudeCodexOnlyConfig() {
	s.mu.Lock()
	cfg := s.svc.Cfg
	changed := false
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	for alias, fallback := range claudeCodeModelPresetDefaults {
		key := strings.TrimSpace(alias)
		if key == "" {
			continue
		}
		current := strings.TrimSpace(cfg.ModelMappings[key])
		if current == "" {
			continue
		}
		if isValidCodexModel(current) {
			continue
		}
		cfg.ModelMappings[key] = strings.TrimSpace(fallback)
		changed = true
	}
	if changed {
		s.svc.Cfg = cfg
		if raw, err := json.Marshal(cfg.ModelMappings); err == nil {
			_ = s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
		}
	}
	s.mu.Unlock()
}

func claudeCodeIntegrationPaths() (string, []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	envPath := filepath.Join(home, ".codexsess", "claude-code.env")
	profiles := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".zprofile"),
	}
	return envPath, profiles
}

func profileHasCodexsessSource(profilePath string, envPath string) (bool, error) {
	b, err := os.ReadFile(profilePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sourceLine := codexsessProfileSourceLine(envPath)
	content := string(b)
	if strings.Contains(content, codexsessProfileBlockStart) && strings.Contains(content, codexsessProfileBlockEnd) {
		return true, nil
	}
	return strings.Contains(content, sourceLine), nil
}

const (
	codexsessProfileBlockStart = "# >>> CodexSess Claude Code >>>"
	codexsessProfileBlockEnd   = "# <<< CodexSess Claude Code <<<"
)

func ensureCodexsessProfileIntegration(profilePath string, envPath string) error {
	b, err := os.ReadFile(profilePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(b)
	content = removeCodexsessProfileBlock(content)
	content = removeLegacyCodexsessHeader(content)

	var builder strings.Builder
	builder.WriteString(strings.TrimRight(content, "\n"))
	if strings.TrimSpace(content) != "" {
		builder.WriteString("\n")
	}
	builder.WriteString(codexsessProfileBlockStart)
	builder.WriteString("\n")
	builder.WriteString(codexsessProfileSourceLine(envPath))
	builder.WriteString("\n")
	builder.WriteString(codexsessProfileBlockEnd)
	builder.WriteString("\n")
	return os.WriteFile(profilePath, []byte(builder.String()), 0o600)
}

func codexsessProfileSourceLine(envPath string) string {
	escaped := strings.ReplaceAll(envPath, `"`, `\"`)
	return `[ -f "` + escaped + `" ] && . "` + escaped + `"`
}

func removeCodexsessProfileBlock(content string) string {
	text := strings.TrimRight(content, "\n")
	for {
		start := strings.Index(text, codexsessProfileBlockStart)
		if start < 0 {
			break
		}
		endRel := strings.Index(text[start:], codexsessProfileBlockEnd)
		if endRel < 0 {
			text = strings.TrimSpace(text[:start])
			break
		}
		end := start + endRel + len(codexsessProfileBlockEnd)
		text = strings.TrimSpace(text[:start] + "\n" + text[end:])
	}
	return text
}

func removeLegacyCodexsessHeader(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 0 {
		return strings.TrimSpace(content)
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "# Added by CodexSess for Claude Code" {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func copyModelPresetMap() map[string]string {
	out := make(map[string]string, len(claudeCodeModelPresetDefaults))
	for alias, model := range claudeCodeModelPresetDefaults {
		out[alias] = model
	}
	return out
}

func mergedClaudeModelPreset(current map[string]string) map[string]string {
	merged := copyModelPresetMap()
	for alias, fallback := range merged {
		override := strings.TrimSpace(current[alias])
		if override != "" {
			merged[alias] = override
			continue
		}
		merged[alias] = strings.TrimSpace(fallback)
	}
	return merged
}

func (s *Server) handleWebLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				if v > 1000 {
					v = 1000
				}
				limit = v
			}
		}
		if s.traffic == nil {
			respondJSON(w, 200, map[string]any{"ok": true, "lines": []string{}})
			return
		}
		lines, err := s.traffic.ReadTail(limit)
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "lines": lines})
		return
	case http.MethodDelete:
		if s.traffic == nil {
			respondJSON(w, 200, map[string]any{"ok": true})
			return
		}
		if err := s.traffic.Clear(); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebCodingSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := s.svc.ListCodingSessions(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":       true,
			"sessions": mapCodingSessions(sessions),
		})
		return
	case http.MethodPost:
		var req struct {
			Title          string `json:"title"`
			Model          string `json:"model"`
			ReasoningLevel string `json:"reasoning_level"`
			WorkDir        string `json:"work_dir"`
			SandboxMode    string `json:"sandbox_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		session, err := s.svc.CreateCodingSession(r.Context(), req.Title, req.Model, req.ReasoningLevel, req.WorkDir, req.SandboxMode)
		if err != nil {
			respondErr(w, 400, "bad_request", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":      true,
			"session": mapCodingSession(session),
		})
		return
	case http.MethodPut:
		var req struct {
			SessionID      string `json:"session_id"`
			Model          string `json:"model"`
			ReasoningLevel string `json:"reasoning_level"`
			WorkDir        string `json:"work_dir"`
			SandboxMode    string `json:"sandbox_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		session, err := s.svc.UpdateCodingSessionPreferences(r.Context(), req.SessionID, req.Model, req.ReasoningLevel, req.WorkDir, req.SandboxMode)
		if err != nil {
			respondErr(w, 400, "bad_request", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":      true,
			"session": mapCodingSession(session),
		})
		return
	case http.MethodDelete:
		sessionID := strings.TrimSpace(r.URL.Query().Get("id"))
		if sessionID == "" {
			respondErr(w, 400, "bad_request", "session id is required")
			return
		}
		if err := s.svc.DeleteCodingSession(r.Context(), sessionID); err != nil {
			respondErr(w, 400, "bad_request", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebCodingTemplateHome(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status, err := s.svc.CodingTemplateHomeStatus(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":     true,
			"status": status,
		})
		return
	case http.MethodPost:
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		action := strings.TrimSpace(strings.ToLower(req.Action))
		switch action {
		case "", "initialize":
			if _, err := s.svc.EnsureCodingTemplateHome(); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		case "resync":
			if _, err := s.svc.RefreshCodingTemplateHome(); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		default:
			respondErr(w, 400, "bad_request", "action must be initialize or resync")
			return
		}
		status, err := s.svc.CodingTemplateHomeStatus(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":     true,
			"status": status,
		})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebClientEventLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Type    string         `json:"type"`
		Source  string         `json:"source"`
		Level   string         `json:"level"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	eventType := firstNonEmpty(strings.TrimSpace(req.Type), "event")
	eventSource := firstNonEmpty(strings.TrimSpace(req.Source), "web-console")
	eventLevel := firstNonEmpty(strings.TrimSpace(req.Level), "info")
	eventMessage := firstNonEmpty(strings.TrimSpace(req.Message), "-")
	meta := "-"
	if len(req.Data) > 0 {
		if b, err := json.Marshal(req.Data); err == nil {
			meta = string(b)
		}
	}
	log.Printf("[EVENT] source=%s type=%s level=%s message=%s meta=%s", eventSource, eventType, eventLevel, eventMessage, meta)
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebModelMappings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, 200, map[string]any{
			"ok":               true,
			"available_models": codexAvailableModels(),
			"mappings":         s.currentModelMappings(),
		})
		return
	case http.MethodPost:
		var req struct {
			Alias string `json:"alias"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		alias := strings.TrimSpace(req.Alias)
		model := strings.TrimSpace(req.Model)
		if alias == "" || model == "" {
			respondErr(w, 400, "bad_request", "alias and model are required")
			return
		}
		if !isValidCodexModel(model) {
			respondErr(w, 400, "bad_request", "invalid target model")
			return
		}
		if err := s.upsertModelMapping(alias, model); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	case http.MethodDelete:
		alias := strings.TrimSpace(r.URL.Query().Get("alias"))
		if alias == "" {
			respondErr(w, 400, "bad_request", "alias is required")
			return
		}
		if err := s.deleteModelMapping(alias); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		APIKey     string `json:"api_key"`
		Regenerate bool   `json:"regenerate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	newKey := strings.TrimSpace(req.APIKey)
	if req.Regenerate {
		k, err := randomProxyKey()
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		newKey = k
	}
	if newKey == "" {
		respondErr(w, 400, "bad_request", "api_key required")
		return
	}
	if err := s.saveSetting(r.Context(), store.SettingAPIKey, newKey); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	s.mu.Lock()
	cfg := s.svc.Cfg
	cfg.ProxyAPIKey = newKey
	s.svc.Cfg = cfg
	s.mu.Unlock()
	s.setAPIKey(newKey)
	baseURL := externalBaseURLFromRequest(r, s.bindAddr)
	if strings.TrimSpace(baseURL) != "" {
		if err := ensureClaudeSettings(baseURL, newKey, ""); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		if err := ensureClaudeOnboardingCompleted(); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		if err := ensureClaudeEnvFile(baseURL, newKey); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
	}
	respondJSON(w, 200, map[string]any{"ok": true, "api_key": newKey})
}

func (s *Server) handleWebBrowserStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	base := oauthBaseURLFromRequest(r)
	login, err := s.svc.StartBrowserLoginWeb(r.Context(), base, "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "login_id": login.LoginID, "auth_url": login.AuthURL})
}

func (s *Server) handleWebBrowserCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.svc.CancelBrowserLoginWeb(strings.TrimSpace(req.LoginID))
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebBrowserCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	loginID := strings.TrimSpace(r.URL.Query().Get("login_id"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))

	var err error
	if loginID != "" {
		_, err = s.svc.CompleteBrowserLoginCode(r.Context(), loginID, code, state)
	} else {
		_, err = s.svc.CompleteBrowserLoginCodeByState(r.Context(), code, state)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("authentication failed: " + err.Error()))
		return
	}
	s.svc.CancelBrowserLoginWeb(loginID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><body style='font-family: sans-serif;background:#0f172a;color:#f8fafc;padding:20px'>Login success. You can close this tab and return to codexsess.</body></html>"))
}

func (s *Server) handleWebBrowserComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID     string `json:"login_id"`
		CallbackURL string `json:"callback_url"`
		Alias       string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	loginID := strings.TrimSpace(req.LoginID)
	callbackURL := strings.TrimSpace(req.CallbackURL)
	if loginID == "" {
		respondErr(w, 400, "bad_request", "login_id required")
		return
	}
	if callbackURL == "" {
		respondErr(w, 400, "bad_request", "callback_url required")
		return
	}
	acc, err := s.svc.CompleteFromManualCallback(r.Context(), loginID, callbackURL, strings.TrimSpace(req.Alias))
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	s.svc.CancelBrowserLoginWeb(loginID)
	respondJSON(w, 200, map[string]any{
		"ok": true,
		"account": map[string]any{
			"id":    acc.ID,
			"email": acc.Email,
		},
	})
}

func (s *Server) handleWebDeviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	login, err := s.svc.StartDeviceLogin(r.Context(), "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok": true,
		"login": map[string]any{
			"login_id":                  login.LoginID,
			"user_code":                 login.UserCode,
			"verification_uri":          login.VerificationURI,
			"verification_uri_complete": login.VerificationURIComplete,
			"interval_seconds":          login.IntervalSeconds,
			"expires_at":                login.ExpiresAt,
		},
	})
}

func (s *Server) handleWebDevicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.PollDeviceLogin(r.Context(), strings.TrimSpace(req.LoginID))
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func randomProxyKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(buf), nil
}

func oauthBaseURLFromRequest(r *http.Request) string {
	scheme := "http"
	if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); raw != "" {
		parts := strings.Split(raw, ",")
		if p := strings.ToLower(strings.TrimSpace(parts[0])); p == "http" || p == "https" {
			scheme = p
		}
	}
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return scheme + "://localhost:3061"
	}
	parts := strings.Split(host, ",")
	return scheme + "://" + strings.TrimSpace(parts[0])
}

func externalBaseURLFromRequest(r *http.Request, bindAddr string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host != "" {
		return scheme + "://" + host
	}
	return scheme + "://" + bindAddr
}

func expandSuggestionPath(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		clean = "~/"
	}
	if strings.HasPrefix(clean, "~/") || clean == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			suffix := strings.TrimPrefix(clean, "~")
			return filepath.Clean(filepath.Join(home, suffix))
		}
	}
	if filepath.IsAbs(clean) {
		return filepath.Clean(clean)
	}
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Clean(clean)
	}
	return filepath.Clean(filepath.Join(wd, clean))
}
