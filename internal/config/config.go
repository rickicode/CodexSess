package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type Config struct {
	BindAddr                 string            `yaml:"-"`
	DataDir                  string            `yaml:"data_dir"`
	MasterKeyPath            string            `yaml:"master_key_path"`
	AuthStoreDir             string            `yaml:"auth_store_dir"`
	CodexHome                string            `yaml:"codex_home"`
	CodexBin                 string            `yaml:"codex_bin"`
	ProxyAPIKey              string            `yaml:"codexsess_api_key"`
	ModelMappings            map[string]string `yaml:"model_mappings"`
	UsageAlertThreshold      int               `yaml:"usage_alert_threshold"`
	UsageAutoSwitchThreshold int               `yaml:"usage_auto_switch_threshold"`
	AdminUsername            string            `yaml:"admin_username"`
	AdminPasswordHash        string            `yaml:"admin_password_hash"`
	LogLevel                 string            `yaml:"log_level"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	base := defaultDataDir(home)
	return Config{
		BindAddr:                 resolveBindAddr(),
		DataDir:                  base,
		MasterKeyPath:            filepath.Join(base, "master.key"),
		AuthStoreDir:             filepath.Join(base, "auth-accounts"),
		CodexHome:                filepath.Join(home, ".codex"),
		CodexBin:                 resolveCodexBin(""),
		ModelMappings:            map[string]string{},
		UsageAlertThreshold:      5,
		UsageAutoSwitchThreshold: 2,
		AdminUsername:            "admin",
		AdminPasswordHash:        "",
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
		if err := ensureAdminPassword(&cfg); err != nil {
			return cfg, err
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
	def := Default()
	if strings.TrimSpace(cfg.AuthStoreDir) == "" {
		cfg.AuthStoreDir = def.AuthStoreDir
	}
	if strings.TrimSpace(cfg.CodexHome) == "" {
		cfg.CodexHome = def.CodexHome
	}
	cfg.CodexBin = resolveCodexBin(cfg.CodexBin)
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	if cfg.UsageAlertThreshold < 0 || cfg.UsageAlertThreshold > 100 {
		cfg.UsageAlertThreshold = def.UsageAlertThreshold
	}
	if cfg.UsageAutoSwitchThreshold < 0 || cfg.UsageAutoSwitchThreshold > 100 {
		cfg.UsageAutoSwitchThreshold = def.UsageAutoSwitchThreshold
	}
	if cfg.ProxyAPIKey == "" {
		k, err := randomKey("sk-")
		if err != nil {
			return cfg, err
		}
		cfg.ProxyAPIKey = k
		if err := Save(cfg); err != nil {
			return cfg, err
		}
	}
	if strings.TrimSpace(cfg.AdminUsername) == "" {
		cfg.AdminUsername = def.AdminUsername
	}
	hadAdminHash := strings.TrimSpace(cfg.AdminPasswordHash) != ""
	if err := ensureAdminPassword(&cfg); err != nil {
		return cfg, err
	}
	if !hadAdminHash {
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

func randomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func ensureAdminPassword(cfg *Config) error {
	if strings.TrimSpace(cfg.AdminPasswordHash) != "" {
		return nil
	}
	pass, err := randomPassword()
	if err != nil {
		return err
	}
	encoded := HashPassword(pass)
	if strings.TrimSpace(encoded) == "" {
		return errors.New("failed to hash generated admin password")
	}
	cfg.AdminPasswordHash = encoded
	fmt.Printf("Generated admin password (one-time): %s\n", pass)
	return nil
}

func resolveBindAddr() string {
	port := 3061
	if raw := strings.TrimSpace(os.Getenv("PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("CODEXSESS_BIND_ADDR")); raw != "" {
		if strings.HasPrefix(raw, ":") {
			return "127.0.0.1" + raw
		}
		if _, _, err := net.SplitHostPort(raw); err == nil {
			return raw
		}
		if !strings.Contains(raw, ":") {
			return fmt.Sprintf("%s:%d", raw, port)
		}
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
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

func defaultDataDir(home string) string {
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "codexsess")
		}
	}
	return filepath.Join(home, ".codexsess")
}

func HashPassword(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return ""
	}
	return string(hash)
}

func VerifyPassword(password, encoded string) bool {
	clean := strings.TrimSpace(encoded)
	if clean == "" {
		return false
	}
	if isLegacySHA256(clean) {
		sum := sha256.Sum256([]byte(password))
		return strings.EqualFold(hex.EncodeToString(sum[:]), clean)
	}
	if !strings.HasPrefix(clean, "$2") {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(clean), []byte(password)) == nil
}

func PasswordHashNeedsUpgrade(encoded string) bool {
	return isLegacySHA256(strings.TrimSpace(encoded))
}

func isLegacySHA256(encoded string) bool {
	if len(encoded) != 64 {
		return false
	}
	for _, r := range encoded {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}
