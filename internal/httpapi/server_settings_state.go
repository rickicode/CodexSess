package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) currentAPIKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apiKey
}

func (s *Server) isValidAPIKey(r *http.Request) bool {
	key := s.currentAPIKey()
	if BearerToken(r.Header.Get("Authorization")) == key {
		return true
	}
	return strings.TrimSpace(r.Header.Get("x-api-key")) == key
}

func (s *Server) setAPIKey(v string) {
	s.mu.Lock()
	s.apiKey = v
	s.mu.Unlock()
}

func (s *Server) setAdminPasswordHash(v string) {
	s.mu.Lock()
	s.adminPasswordHash = strings.TrimSpace(v)
	s.mu.Unlock()
}

func (s *Server) currentAdminPasswordHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.adminPasswordHash)
}

func (s *Server) bootstrapSettingsFromStore(ctx context.Context) error {
	if s.svc == nil || s.svc.Store == nil {
		return nil
	}
	cfg := s.svc.Cfg
	var errs []string

	apiKey, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingAPIKey)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		apiKey = strings.TrimSpace(apiKey)
		if apiKey != "" {
			cfg.ProxyAPIKey = apiKey
			s.setAPIKey(apiKey)
		}
	} else {
		if strings.TrimSpace(cfg.ProxyAPIKey) == "" {
			if k, genErr := randomProxyKey(); genErr == nil {
				cfg.ProxyAPIKey = k
			} else {
				errs = append(errs, genErr.Error())
			}
		}
		if strings.TrimSpace(cfg.ProxyAPIKey) != "" {
			if err := s.svc.Store.SetSetting(ctx, store.SettingAPIKey, cfg.ProxyAPIKey); err != nil {
				errs = append(errs, err.Error())
			}
			s.setAPIKey(cfg.ProxyAPIKey)
		}
	}

	apiMode, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingAPIMode)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.APIMode = config.NormalizeAPIMode(apiMode)
	} else {
		cfg.APIMode = config.NormalizeAPIMode(cfg.APIMode)
		if err := s.svc.Store.SetSetting(ctx, store.SettingAPIMode, cfg.APIMode); err != nil {
			errs = append(errs, err.Error())
		}
	}

	directStrategy, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingDirectAPIStrategy)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(directStrategy)
	} else {
		cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(cfg.DirectAPIStrategy)
		if err := s.svc.Store.SetSetting(ctx, store.SettingDirectAPIStrategy, cfg.DirectAPIStrategy); err != nil {
			errs = append(errs, err.Error())
		}
	}

	zoStrategy, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingZoAPIStrategy)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(zoStrategy)
	} else {
		cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(cfg.ZoAPIStrategy)
		if err := s.svc.Store.SetSetting(ctx, store.SettingZoAPIStrategy, cfg.ZoAPIStrategy); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageAlertThreshold, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageAlertThreshold)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageAlertThreshold)); parseErr == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAlertThreshold = parsed
		}
	} else {
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageAlertThreshold, strconv.Itoa(cfg.UsageAlertThreshold)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageAutoSwitchThreshold, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageAutoSwitchThreshold)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageAutoSwitchThreshold)); parseErr == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAutoSwitchThreshold = parsed
		}
	} else {
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageAutoSwitchThreshold, strconv.Itoa(cfg.UsageAutoSwitchThreshold)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageSchedulerInterval, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageSchedulerInterval)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageSchedulerInterval)); parseErr == nil {
			cfg.UsageSchedulerInterval = config.NormalizeUsageSchedulerIntervalMinutes(parsed)
		}
	} else {
		cfg.UsageSchedulerInterval = config.NormalizeUsageSchedulerIntervalMinutes(cfg.UsageSchedulerInterval)
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageSchedulerInterval, strconv.Itoa(cfg.UsageSchedulerInterval)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageSchedulerEnabled, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageSchedulerEnabled)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.UsageSchedulerEnabled = strings.EqualFold(strings.TrimSpace(usageSchedulerEnabled), "true")
	} else {
		if cfg.UsageSchedulerEnabled {
			if err := s.svc.Store.SetSetting(ctx, store.SettingUsageSchedulerEnabled, "true"); err != nil {
				errs = append(errs, err.Error())
			}
		} else {
			if err := s.svc.Store.SetSetting(ctx, store.SettingUsageSchedulerEnabled, "false"); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}

	usageRefreshTimeoutSec, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageRefreshTimeoutSec)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageRefreshTimeoutSec)); parseErr == nil {
			cfg.UsageRefreshTimeoutSec = config.NormalizeUsageRefreshTimeoutSeconds(parsed)
		}
	} else {
		cfg.UsageRefreshTimeoutSec = config.NormalizeUsageRefreshTimeoutSeconds(cfg.UsageRefreshTimeoutSec)
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageRefreshTimeoutSec, strconv.Itoa(cfg.UsageRefreshTimeoutSec)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageSwitchTimeoutSec, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageSwitchTimeoutSec)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageSwitchTimeoutSec)); parseErr == nil {
			cfg.UsageSwitchTimeoutSec = config.NormalizeUsageSwitchTimeoutSeconds(parsed)
		}
	} else {
		cfg.UsageSwitchTimeoutSec = config.NormalizeUsageSwitchTimeoutSeconds(cfg.UsageSwitchTimeoutSec)
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageSwitchTimeoutSec, strconv.Itoa(cfg.UsageSwitchTimeoutSec)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	codexHome, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingCodexHome)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		codexHome = strings.TrimSpace(codexHome)
		if codexHome != "" {
			cfg.CodexHome = codexHome
		}
	} else {
		if strings.TrimSpace(cfg.CodexHome) != "" {
			if err := s.svc.Store.SetSetting(ctx, store.SettingCodexHome, strings.TrimSpace(cfg.CodexHome)); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}

	mappingsRaw, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingModelMappings)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok && strings.TrimSpace(mappingsRaw) != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(mappingsRaw), &parsed); err != nil {
			errs = append(errs, err.Error())
		} else {
			cfg.ModelMappings = parsed
		}
	} else {
		if cfg.ModelMappings == nil {
			cfg.ModelMappings = map[string]string{}
		}
		if raw, err := json.Marshal(cfg.ModelMappings); err == nil {
			if err := s.svc.Store.SetSetting(ctx, store.SettingModelMappings, string(raw)); err != nil {
				errs = append(errs, err.Error())
			}
		} else {
			errs = append(errs, err.Error())
		}
	}

	passwordHash, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingAdminPasswordHash)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		passwordHash = strings.TrimSpace(passwordHash)
		if passwordHash != "" {
			cfg.AdminPasswordHash = passwordHash
			s.setAdminPasswordHash(passwordHash)
		}
	} else if strings.TrimSpace(cfg.AdminPasswordHash) != "" {
		if err := s.svc.Store.SetSetting(ctx, store.SettingAdminPasswordHash, strings.TrimSpace(cfg.AdminPasswordHash)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	s.svc.Cfg = cfg
	if len(errs) > 0 {
		return fmt.Errorf("settings bootstrap: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *Server) saveSetting(ctx context.Context, key string, value string) error {
	if s != nil && s.svc != nil && s.svc.Store != nil {
		return s.svc.Store.SetSetting(ctx, key, value)
	}
	cfg, err := config.LoadOrInit()
	if err != nil {
		return err
	}
	switch key {
	case store.SettingAPIKey:
		cfg.ProxyAPIKey = strings.TrimSpace(value)
	case store.SettingAPIMode:
		cfg.APIMode = config.NormalizeAPIMode(value)
	case store.SettingDirectAPIStrategy:
		cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(value)
	case store.SettingZoAPIStrategy:
		cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(value)
	case store.SettingCodexHome:
		cfg.CodexHome = strings.TrimSpace(value)
	case store.SettingUsageAlertThreshold:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAlertThreshold = parsed
		}
	case store.SettingUsageAutoSwitchThreshold:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAutoSwitchThreshold = parsed
		}
	case store.SettingUsageSchedulerEnabled:
		cfg.UsageSchedulerEnabled = strings.EqualFold(strings.TrimSpace(value), "true")
	case store.SettingUsageSchedulerInterval:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.UsageSchedulerInterval = config.NormalizeUsageSchedulerIntervalMinutes(parsed)
		}
	case store.SettingUsageRefreshTimeoutSec:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.UsageRefreshTimeoutSec = config.NormalizeUsageRefreshTimeoutSeconds(parsed)
		}
	case store.SettingUsageSwitchTimeoutSec:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.UsageSwitchTimeoutSec = config.NormalizeUsageSwitchTimeoutSeconds(parsed)
		}
	case store.SettingModelMappings:
		mappings := map[string]string{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &mappings); err != nil {
			return err
		}
		cfg.ModelMappings = mappings
	case store.SettingAdminPasswordHash:
		cfg.AdminPasswordHash = strings.TrimSpace(value)
	default:
		return nil
	}
	return config.Save(cfg)
}

func (s *Server) currentAPIMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return config.NormalizeAPIMode(s.svc.Cfg.APIMode)
}

func (s *Server) currentDirectAPIStrategy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return config.NormalizeDirectAPIStrategy(s.svc.Cfg.DirectAPIStrategy)
}

func (s *Server) currentModelMappings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for k, v := range config.Default().ModelMappings {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	for k, v := range s.svc.Cfg.ModelMappings {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func (s *Server) resolveMappedModel(requested string) string {
	model := strings.TrimSpace(requested)
	if model == "" {
		return model
	}
	modelLower := strings.ToLower(model)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if target, ok := s.svc.Cfg.ModelMappings[model]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	if target, ok := s.svc.Cfg.ModelMappings[modelLower]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	// Default Claude alias fallback so proxy remains usable before explicit UI seeding.
	if target, ok := claudeCodeModelPresetDefaults[modelLower]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	return model
}

func (s *Server) upsertModelMapping(alias, model string) error {
	s.mu.Lock()
	cfg := s.svc.Cfg
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	cfg.ModelMappings[strings.TrimSpace(alias)] = strings.TrimSpace(model)
	raw, err := json.Marshal(cfg.ModelMappings)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	return s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
}

func (s *Server) deleteModelMapping(alias string) error {
	s.mu.Lock()
	cfg := s.svc.Cfg
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	delete(cfg.ModelMappings, strings.TrimSpace(alias))
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	raw, err := json.Marshal(cfg.ModelMappings)
	if err != nil {
		return err
	}
	return s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
}
