package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()
	if cfg.BindAddr == "" {
		t.Fatal("BindAddr empty")
	}
	if cfg.DataDir == "" {
		t.Fatal("DataDir empty")
	}
	if cfg.MasterKeyPath == "" {
		t.Fatal("MasterKeyPath empty")
	}
	if cfg.SystemLogMaxRows <= 0 {
		t.Fatalf("SystemLogMaxRows unexpected: %d", cfg.SystemLogMaxRows)
	}
	if cfg.UsageSchedulerInterval != 60 {
		t.Fatalf("UsageSchedulerInterval default mismatch: got %d want 60", cfg.UsageSchedulerInterval)
	}
}

func TestResolveBindAddr_DefaultIsLocalhost(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("CODEXSESS_PUBLIC", "")
	if got := resolveBindAddr(); got != "127.0.0.1:3061" {
		t.Fatalf("unexpected default bind addr: %q", got)
	}
}

func TestResolveBindAddr_UsesPortEnv(t *testing.T) {
	t.Setenv("PORT", "4021")
	t.Setenv("CODEXSESS_PUBLIC", "")
	if got := resolveBindAddr(); got != "127.0.0.1:4021" {
		t.Fatalf("unexpected bind addr with PORT: %q", got)
	}
}

func TestResolveBindAddr_PublicEnabled(t *testing.T) {
	t.Setenv("PORT", "3061")
	t.Setenv("CODEXSESS_PUBLIC", "true")
	if got := resolveBindAddr(); got != "0.0.0.0:3061" {
		t.Fatalf("unexpected public bind addr: %q", got)
	}
}

func TestResolveBindAddr_PublicEnvInvalidFallsBackToLocalhost(t *testing.T) {
	t.Setenv("PORT", "3061")
	t.Setenv("CODEXSESS_PUBLIC", "invalid")
	if got := resolveBindAddr(); got != "127.0.0.1:3061" {
		t.Fatalf("unexpected fallback bind addr: %q", got)
	}
}

func TestResolveCodexBin_Default(t *testing.T) {
	t.Setenv("CODEXSESS_CODEX_BIN", "")
	if got := resolveCodexBin(""); got != "codex" {
		t.Fatalf("unexpected default codex bin: %q", got)
	}
}

func TestResolveCodexBin_UsesConfigValue(t *testing.T) {
	t.Setenv("CODEXSESS_CODEX_BIN", "")
	if got := resolveCodexBin("/usr/local/bin/codex"); got != "/usr/local/bin/codex" {
		t.Fatalf("unexpected codex bin from config: %q", got)
	}
}

func TestResolveCodexBin_EnvOverride(t *testing.T) {
	t.Setenv("CODEXSESS_CODEX_BIN", "/opt/codex/bin/codex")
	if got := resolveCodexBin("codex"); got != "/opt/codex/bin/codex" {
		t.Fatalf("unexpected codex bin from env override: %q", got)
	}
}

func TestNormalizeUsageSchedulerIntervalMinutes(t *testing.T) {
	if got := NormalizeUsageSchedulerIntervalMinutes(5); got != 10 {
		t.Fatalf("expected min 10, got %d", got)
	}
	if got := NormalizeUsageSchedulerIntervalMinutes(30); got != 30 {
		t.Fatalf("expected 30, got %d", got)
	}
	if got := NormalizeUsageSchedulerIntervalMinutes(180); got != 180 {
		t.Fatalf("expected 180, got %d", got)
	}
	if got := NormalizeUsageSchedulerIntervalMinutes(420); got != 300 {
		t.Fatalf("expected max 300, got %d", got)
	}
}

func TestLoadOrInit_RepairsEmptyPathFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PORT", "")
	t.Setenv("CODEXSESS_PUBLIC", "")
	t.Setenv("CODEXSESS_CODEX_BIN", "")

	cfgDir := filepath.Join(home, ".codexsess")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir cfg dir: %v", err)
	}
	raw := "" +
		"data_dir: \"\"\n" +
		"master_key_path: \"\"\n" +
		"auth_store_dir: \"\"\n" +
		"codex_home: /home/test/.codex\n" +
		"codex_bin: codex\n" +
		"codexsess_api_key: sk-test\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadOrInit()
	if err != nil {
		t.Fatalf("LoadOrInit error: %v", err)
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		t.Fatal("expected DataDir to be repaired")
	}
	if strings.TrimSpace(cfg.MasterKeyPath) == "" {
		t.Fatal("expected MasterKeyPath to be repaired")
	}
	if strings.TrimSpace(cfg.AuthStoreDir) == "" {
		t.Fatal("expected AuthStoreDir to be repaired")
	}
}

func TestLoadOrInit_BackfillsMissingBooleanDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PORT", "")
	t.Setenv("CODEXSESS_PUBLIC", "")
	t.Setenv("CODEXSESS_CODEX_BIN", "")

	cfgDir := filepath.Join(home, ".codexsess")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir cfg dir: %v", err)
	}
	raw := "" +
		"data_dir: " + filepath.Join(home, ".codexsess") + "\n" +
		"master_key_path: " + filepath.Join(home, ".codexsess", "master.key") + "\n" +
		"auth_store_dir: " + filepath.Join(home, ".codexsess", "auth-accounts") + "\n" +
		"codex_home: /home/test/.codex\n" +
		"codex_bin: codex\n" +
		"codexsess_api_key: sk-test\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadOrInit()
	if err != nil {
		t.Fatalf("LoadOrInit error: %v", err)
	}
	if !cfg.UsageSchedulerEnabled {
		t.Fatal("expected UsageSchedulerEnabled to backfill to true default when key missing")
	}
}
