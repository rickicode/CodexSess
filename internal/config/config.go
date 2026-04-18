package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	BindAddr                 string            `yaml:"-"`
	DataDir                  string            `yaml:"data_dir"`
	MasterKeyPath            string            `yaml:"master_key_path"`
	AuthStoreDir             string            `yaml:"auth_store_dir"`
	CodexHome                string            `yaml:"codex_home"`
	CodexBin                 string            `yaml:"codex_bin"`
	ProxyAPIKey              string            `yaml:"-"`
	ModelMappings            map[string]string `yaml:"-"`
	UsageAlertThreshold      int               `yaml:"usage_alert_threshold"`
	UsageAutoSwitchThreshold int               `yaml:"usage_auto_switch_threshold"`
	UsageSchedulerEnabled    bool              `yaml:"usage_scheduler_enabled"`
	UsageSchedulerInterval   int               `yaml:"usage_scheduler_interval_minutes"`
	UsageRefreshTimeoutSec   int               `yaml:"usage_scheduler_refresh_timeout_seconds"`
	UsageSwitchTimeoutSec    int               `yaml:"usage_scheduler_switch_timeout_seconds"`
	DirectAPIStrategy        string            `yaml:"direct_api_strategy"`
	CLISwitchNotifyCmd       string            `yaml:"cli_switch_notify_cmd"`
	SystemLogMaxRows         int               `yaml:"system_log_max_rows"`
	AdminUsername            string            `yaml:"admin_username"`
	AdminPassword            string            `yaml:"admin_password,omitempty"`
	AdminPasswordHash        string            `yaml:"admin_password_hash"`
	LogLevel                 string            `yaml:"log_level"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	base := defaultDataDir(home)
	return Config{
		BindAddr:      resolveBindAddr(),
		DataDir:       base,
		MasterKeyPath: filepath.Join(base, "master.key"),
		AuthStoreDir:  filepath.Join(base, "auth-accounts"),
		CodexHome:     filepath.Join(home, ".codex"),
		CodexBin:      resolveCodexBin(""),
		ModelMappings: map[string]string{
			"gpt-4o":                     "gpt-5.1-codex-max",
			"gpt-4o-mini":                "gpt-5.1-codex-max",
			"gpt-4.1":                    "gpt-5.1-codex-max",
			"gpt-4.1-mini":               "gpt-5.1-codex-max",
			"gpt-4.1-nano":               "gpt-5.1-codex-max",
			"gpt-4o-realtime-preview":    "gpt-5.1-codex-max",
			"gpt-4-turbo":                "gpt-5.1-codex-max",
			"gpt-4":                      "gpt-5.1-codex-max",
			"gpt-3.5-turbo":              "gpt-5.1-codex-max",
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
		},
		UsageAlertThreshold:      5,
		UsageAutoSwitchThreshold: 15,
		UsageSchedulerEnabled:    true,
		UsageSchedulerInterval:   60,
		UsageRefreshTimeoutSec:   120,
		UsageSwitchTimeoutSec:    45,
		DirectAPIStrategy:        "round_robin",
		CLISwitchNotifyCmd:       "",
		SystemLogMaxRows:         1000,
		AdminUsername:            "admin",
		AdminPasswordHash:        HashPassword("hijilabs"),
		LogLevel:                 "info",
	}
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codexsess", "config.yaml")
}

func LoadOrInit() (Config, error) {
	cfg := Default()
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return cfg, err
	}
	p := configPath()
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		if cfg.ProxyAPIKey == "" {
			key, err := randomKey("sk-")
			if err != nil {
				return cfg, err
			}
			cfg.ProxyAPIKey = key
		}
		if err := Save(cfg); err != nil {
			return cfg, err
		}
		cfg.BindAddr = resolveBindAddr()
		return cfg, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	var raw map[string]any
	_ = yaml.Unmarshal(b, &raw)
	def := Default()
	if v := strings.TrimSpace(asString(raw["codexsess_api_key"])); v != "" {
		cfg.ProxyAPIKey = v
	}
	if m := readStringMap(raw["model_mappings"]); len(m) > 0 {
		cfg.ModelMappings = m
	}
	if v := strings.TrimSpace(asString(raw["admin_password"])); v != "" && strings.TrimSpace(cfg.AdminPassword) == "" {
		cfg.AdminPassword = v
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		cfg.DataDir = def.DataDir
	}
	if strings.TrimSpace(cfg.MasterKeyPath) == "" {
		cfg.MasterKeyPath = filepath.Join(cfg.DataDir, "master.key")
	}
	if strings.TrimSpace(cfg.AuthStoreDir) == "" {
		cfg.AuthStoreDir = filepath.Join(cfg.DataDir, "auth-accounts")
	}
	if strings.TrimSpace(cfg.CodexHome) == "" {
		cfg.CodexHome = def.CodexHome
	}
	cfg.CodexBin = resolveCodexBin(cfg.CodexBin)
	cfg.DirectAPIStrategy = NormalizeDirectAPIStrategy(cfg.DirectAPIStrategy)
	cfg.CLISwitchNotifyCmd = resolveCLISwitchNotifyCmd(cfg.CLISwitchNotifyCmd)
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	shouldSave := false
	for alias, target := range def.ModelMappings {
		key := strings.TrimSpace(alias)
		if key == "" {
			continue
		}
		if strings.TrimSpace(cfg.ModelMappings[key]) != "" {
			continue
		}
		cfg.ModelMappings[key] = strings.TrimSpace(target)
	}
	if cfg.UsageAlertThreshold < 0 || cfg.UsageAlertThreshold > 100 {
		cfg.UsageAlertThreshold = def.UsageAlertThreshold
	}
	if cfg.UsageAutoSwitchThreshold < 0 || cfg.UsageAutoSwitchThreshold > 100 {
		cfg.UsageAutoSwitchThreshold = def.UsageAutoSwitchThreshold
	}
	cfg.UsageSchedulerInterval = NormalizeUsageSchedulerIntervalMinutes(cfg.UsageSchedulerInterval)
	cfg.UsageRefreshTimeoutSec = normalizeUsageRefreshTimeoutSecondsWithEnv(cfg.UsageRefreshTimeoutSec)
	cfg.UsageSwitchTimeoutSec = normalizeUsageSwitchTimeoutSecondsWithEnv(cfg.UsageSwitchTimeoutSec)
	if _, ok := raw["system_log_max_rows"]; !ok {
		cfg.SystemLogMaxRows = def.SystemLogMaxRows
	}
	if cfg.SystemLogMaxRows < 0 {
		cfg.SystemLogMaxRows = def.SystemLogMaxRows
	}
	if cfg.SystemLogMaxRows > def.SystemLogMaxRows {
		cfg.SystemLogMaxRows = def.SystemLogMaxRows
	}
	if _, ok := raw["usage_scheduler_enabled"]; !ok {
		cfg.UsageSchedulerEnabled = def.UsageSchedulerEnabled
	}
	if _, ok := raw["usage_scheduler_interval_minutes"]; !ok {
		cfg.UsageSchedulerInterval = def.UsageSchedulerInterval
	}
	if _, ok := raw["usage_scheduler_refresh_timeout_seconds"]; !ok {
		cfg.UsageRefreshTimeoutSec = def.UsageRefreshTimeoutSec
	}
	if _, ok := raw["usage_scheduler_switch_timeout_seconds"]; !ok {
		cfg.UsageSwitchTimeoutSec = def.UsageSwitchTimeoutSec
	}
	if cfg.ProxyAPIKey == "" {
		k, err := randomKey("sk-")
		if err != nil {
			return cfg, err
		}
		cfg.ProxyAPIKey = k
	}
	if strings.TrimSpace(cfg.AdminUsername) == "" {
		cfg.AdminUsername = def.AdminUsername
	}
	if strings.TrimSpace(cfg.AdminPasswordHash) == "" {
		cfg.AdminPasswordHash = def.AdminPasswordHash
	}
	if plain := strings.TrimSpace(cfg.AdminPassword); plain != "" {
		cfg.AdminPasswordHash = HashPassword(plain)
		cfg.AdminPassword = ""
		shouldSave = true
	}
	if shouldSave {
		if err := Save(cfg); err != nil {
			return cfg, err
		}
	}
	cfg.BindAddr = resolveBindAddr()
	return cfg, nil
}

func Save(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o600)
}

func randomKey(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random key: %w", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

func resolveBindAddr() string {
	port := 3061
	if raw := strings.TrimSpace(os.Getenv("PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
	}
	if publicBindEnabled() {
		return fmt.Sprintf("0.0.0.0:%d", port)
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func publicBindEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_PUBLIC")))
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func resolveCodexBin(current string) string {
	if raw := strings.TrimSpace(os.Getenv("CODEXSESS_CODEX_BIN")); raw != "" {
		return raw
	}
	if raw := strings.TrimSpace(current); raw != "" {
		return raw
	}
	return "codex"
}

func NormalizeDirectAPIStrategy(v string) string {
	mode := strings.TrimSpace(strings.ToLower(v))
	switch mode {
	case "load_balance":
		return "load_balance"
	default:
		return "round_robin"
	}
}

func NormalizeUsageSchedulerIntervalMinutes(v int) int {
	if v < 10 {
		return 10
	}
	if v > 300 {
		return 300
	}
	return v
}

func NormalizeUsageRefreshTimeoutSeconds(v int) int {
	sec := v
	if sec < 30 {
		sec = 30
	}
	if sec > 600 {
		sec = 600
	}
	return sec
}

func NormalizeUsageSwitchTimeoutSeconds(v int) int {
	sec := v
	if sec < 10 {
		sec = 10
	}
	if sec > 300 {
		sec = 300
	}
	return sec
}

func normalizeUsageRefreshTimeoutSecondsWithEnv(current int) int {
	sec := current
	if raw := strings.TrimSpace(os.Getenv("CODEXSESS_USAGE_SCHEDULER_REFRESH_TIMEOUT_SECONDS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			sec = parsed
		}
	}
	return NormalizeUsageRefreshTimeoutSeconds(sec)
}

func normalizeUsageSwitchTimeoutSecondsWithEnv(current int) int {
	sec := current
	if raw := strings.TrimSpace(os.Getenv("CODEXSESS_USAGE_SCHEDULER_SWITCH_TIMEOUT_SECONDS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			sec = parsed
		}
	}
	return NormalizeUsageSwitchTimeoutSeconds(sec)
}

func resolveCLISwitchNotifyCmd(v string) string {
	if raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLI_SWITCH_NOTIFY_CMD")); raw != "" {
		return raw
	}
	return strings.TrimSpace(v)
}

func defaultDataDir(home string) string {
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "codexsess")
		}
	}
	return filepath.Join(home, ".codexsess")
}

func HashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func VerifyPassword(password, encoded string) bool {
	return strings.EqualFold(HashPassword(password), strings.TrimSpace(encoded))
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func readStringMap(v any) map[string]string {
	out := map[string]string{}
	if v == nil {
		return out
	}
	switch raw := v.(type) {
	case map[string]string:
		for k, val := range raw {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			out[key] = strings.TrimSpace(val)
		}
		return out
	case map[string]any:
		for k, val := range raw {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			out[key] = strings.TrimSpace(asString(val))
		}
		return out
	case map[any]any:
		for k, val := range raw {
			key := strings.TrimSpace(asString(k))
			if key == "" {
				continue
			}
			out[key] = strings.TrimSpace(asString(val))
		}
		return out
	default:
		return out
	}
}
