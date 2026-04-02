package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func TestEnsureCodingCLIAccountForCoding_KeepCurrentCLIWhenAlreadySet(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	current := seedCodingTestAccount(t, st, cry, cfg, "acc_current", "current@example.com", true)
	_ = seedCodingTestAccount(t, st, cry, cfg, "acc_other", "other@example.com", false)

	if _, err := svc.UseAccountCLI(t.Context(), current.ID); err != nil {
		t.Fatalf("set cli active current: %v", err)
	}
	if err := svc.ensureCodingCLIAccountForCoding(t.Context()); err != nil {
		t.Fatalf("ensureCodingCLIAccountForCoding: %v", err)
	}

	activeID, err := svc.ActiveCLIAccountID(t.Context())
	if err != nil {
		t.Fatalf("ActiveCLIAccountID: %v", err)
	}
	if activeID != current.ID {
		t.Fatalf("expected active cli %s, got %s", current.ID, activeID)
	}
}

func TestEnsureCodingCLIAccountForCoding_SelectsFirstWhenCLIEmpty(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	first := seedCodingTestAccount(t, st, cry, cfg, "acc_first", "first@example.com", false)
	_ = seedCodingTestAccount(t, st, cry, cfg, "acc_second", "second@example.com", false)

	if err := svc.ensureCodingCLIAccountForCoding(t.Context()); err != nil {
		t.Fatalf("ensureCodingCLIAccountForCoding: %v", err)
	}

	activeID, err := svc.ActiveCLIAccountID(t.Context())
	if err != nil {
		t.Fatalf("ActiveCLIAccountID: %v", err)
	}
	if activeID != first.ID {
		t.Fatalf("expected active cli %s, got %s", first.ID, activeID)
	}
}

func TestEnsureCodingRuntimeHome_ChatUsesActiveCLIAccount(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	active := seedCodingTestAccount(t, st, cry, cfg, "acc_chat_runtime", "chat-runtime@example.com", false)
	active.UsageHourlyPct = 90
	active.UsageWeeklyPct = 90
	active.UsageFetchedAt = time.Now().UTC()
	active.UsageLastError = ""
	if err := st.UpsertAccount(t.Context(), active); err != nil {
		t.Fatalf("UpsertAccount active health: %v", err)
	}
	if _, err := svc.UseAccountCLI(t.Context(), active.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}

	runtimeHome, runtimeAccount, err := svc.ensureCodingRuntimeHome(t.Context(), "sess_runtime_chat", codingRuntimeRoleChat)
	if err != nil {
		t.Fatalf("ensureCodingRuntimeHome chat: %v", err)
	}
	if runtimeAccount.ID != active.ID {
		t.Fatalf("expected chat runtime to keep active cli account %q, got %q", active.ID, runtimeAccount.ID)
	}
	if got := svc.readCodingRuntimeAccountMarker("sess_runtime_chat", codingRuntimeRoleChat); got != runtimeAccount.ID {
		t.Fatalf("expected chat marker %q, got %q", runtimeAccount.ID, got)
	}
	if _, err := os.Stat(filepath.Join(runtimeHome, "auth.json")); err != nil {
		t.Fatalf("expected chat auth.json: %v", err)
	}
}

func TestSelectCodingRuntimeAccountExcluding_PrefersHigherWeeklyHeadroom(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	lowWeekly := seedCodingTestAccount(t, st, cry, cfg, "acc_low_weekly", "low-weekly@example.com", false)
	lowWeekly.UsageHourlyPct = 95
	lowWeekly.UsageWeeklyPct = 40
	lowWeekly.UsageFetchedAt = time.Now().UTC()
	lowWeekly.UsageLastError = ""
	if err := st.UpsertAccount(t.Context(), lowWeekly); err != nil {
		t.Fatalf("UpsertAccount lowWeekly: %v", err)
	}

	highWeekly := seedCodingTestAccount(t, st, cry, cfg, "acc_high_weekly", "high-weekly@example.com", false)
	highWeekly.UsageHourlyPct = 15
	highWeekly.UsageWeeklyPct = 85
	highWeekly.UsageFetchedAt = time.Now().UTC()
	highWeekly.UsageLastError = ""
	if err := st.UpsertAccount(t.Context(), highWeekly); err != nil {
		t.Fatalf("UpsertAccount highWeekly: %v", err)
	}

	selected, err := svc.selectCodingRuntimeAccountExcluding(t.Context(), "sess_usage_priority", codingRuntimeRoleExecutor, nil)
	if err != nil {
		t.Fatalf("selectCodingRuntimeAccountExcluding: %v", err)
	}
	if selected.ID != highWeekly.ID {
		t.Fatalf("expected weekly-priority selection %q, got %q", highWeekly.ID, selected.ID)
	}
}

func TestMarkCodingRuntimeAccountUsageLimited_ExcludesAccountFromFutureRuntimeSelection(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	limited := seedCodingTestAccount(t, st, cry, cfg, "acc_limited", "limited@example.com", false)
	limited.UsageHourlyPct = 90
	limited.UsageWeeklyPct = 90
	limited.UsageFetchedAt = time.Now().UTC()
	limited.UsageLastError = ""
	if err := st.UpsertAccount(t.Context(), limited); err != nil {
		t.Fatalf("UpsertAccount limited: %v", err)
	}

	healthy := seedCodingTestAccount(t, st, cry, cfg, "acc_healthy", "healthy@example.com", false)
	healthy.UsageHourlyPct = 70
	healthy.UsageWeeklyPct = 70
	healthy.UsageFetchedAt = time.Now().UTC()
	healthy.UsageLastError = ""
	if err := st.UpsertAccount(t.Context(), healthy); err != nil {
		t.Fatalf("UpsertAccount healthy: %v", err)
	}

	svc.markCodingRuntimeAccountUsageLimited(t.Context(), limited.ID, fmt.Errorf("You've hit your usage limit"))

	usage, err := st.GetUsage(t.Context(), limited.ID)
	if err != nil {
		t.Fatalf("GetUsage limited: %v", err)
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(usage.LastError)), "usage limit") {
		t.Fatalf("expected usage limit marker, got %q", usage.LastError)
	}

	selected, err := svc.selectCodingRuntimeAccountExcluding(t.Context(), "sess_usage_limit_skip", codingRuntimeRoleExecutor, nil)
	if err != nil {
		t.Fatalf("selectCodingRuntimeAccountExcluding: %v", err)
	}
	if selected.ID != healthy.ID {
		t.Fatalf("expected limited account to be excluded, got %q", selected.ID)
	}
}

func TestCodingRuntimeRoots_AvoidProjectScopedDataDir(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("HOME", homeRoot)

	svc, _, _, _ := newCodingTestService(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	svc.Cfg.DataDir = filepath.Join(cwd, "internal", "httpapi")

	want := filepath.Join(homeRoot, ".codexsess", "runtimes")
	if got := filepath.Clean(svc.codingRuntimesRoot()); got != filepath.Clean(want) {
		t.Fatalf("expected runtime root to fall back outside repo, got %q want %q", got, want)
	}
}

func TestEnsureCodingRuntimeHome_BootstrapFailureCleansRuntimeRoot(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)

	if _, _, err := svc.ensureCodingRuntimeHome(t.Context(), "sess_runtime_bootstrap_fail", codingRuntimeRoleChat); err == nil {
		t.Fatalf("expected ensureCodingRuntimeHome to fail when no healthy account exists")
	}
	if _, err := os.Stat(svc.codingSessionRuntimeRoot("sess_runtime_bootstrap_fail")); !os.IsNotExist(err) {
		t.Fatalf("expected runtime root to be removed after bootstrap failure, got err=%v", err)
	}
}

func TestEnsureCodingRuntimeHome_DoesNotCreateProjectAgentsWhenMissing(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, st, cry, cfg, "acc_runtime_agents_create", "runtime-agents-create@example.com", false)

	projectDir := t.TempDir()
	sessionID := "sess_runtime_agents_create"
	now := time.Now().UTC()
	if _, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             sessionID,
		Title:          "Runtime Agents Create",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        projectDir,
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	}); err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	if _, _, err := svc.ensureCodingRuntimeHome(t.Context(), sessionID, codingRuntimeRoleChat); err != nil {
		t.Fatalf("ensureCodingRuntimeHome: %v", err)
	}

	if _, err := os.Stat(filepath.Join(projectDir, runtimeProjectAgentsFileName)); !os.IsNotExist(err) {
		t.Fatalf("expected runtime bootstrap to leave AGENTS.md untouched, got err=%v", err)
	}
}

func TestEnsureCodingRuntimeHome_ProjectAgentsNoOpForExistingFile(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, st, cry, cfg, "acc_runtime_agents_skip", "runtime-agents-skip@example.com", false)

	projectDir := t.TempDir()
	agentsPath := filepath.Join(projectDir, runtimeProjectAgentsFileName)
	original := "custom AGENTS content that should stay\n"
	if err := os.WriteFile(agentsPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile AGENTS.md: %v", err)
	}

	sessionID := "sess_runtime_agents_skip"
	now := time.Now().UTC()
	if _, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             sessionID,
		Title:          "Runtime Agents Skip",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        projectDir,
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	}); err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	if _, _, err := svc.ensureCodingRuntimeHome(t.Context(), sessionID, codingRuntimeRoleChat); err != nil {
		t.Fatalf("ensureCodingRuntimeHome: %v", err)
	}

	raw, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("ReadFile AGENTS.md: %v", err)
	}
	if got := string(raw); got != original {
		t.Fatalf("expected existing AGENTS.md to stay unchanged, got %q", got)
	}
}

func TestEnsureCodingRuntimeHome_SanitizesLegacyRuntimeSkillsAndMemoryConfig(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, st, cry, cfg, "acc_runtime_sanitize", "runtime-sanitize@example.com", false)

	sessionID := "sess_runtime_sanitize"
	now := time.Now().UTC()
	if _, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             sessionID,
		Title:          "Runtime Sanitize",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	}); err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	runtimeHome := svc.codingRuntimeHome(sessionID, codingRuntimeRoleChat)
	if err := os.MkdirAll(filepath.Join(runtimeHome, "skills", "using-superpowers"), 0o700); err != nil {
		t.Fatalf("mkdir legacy using-superpowers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeHome, "skills", "using-superpowers", "SKILL.md"), []byte("# legacy\n"), 0o600); err != nil {
		t.Fatalf("write legacy using-superpowers: %v", err)
	}
	legacyConfig := "approval_policy = \"never\"\n\n[mcp_servers.memory]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-memory\"]\n"
	if err := os.WriteFile(filepath.Join(runtimeHome, "config.toml"), []byte(legacyConfig), 0o600); err != nil {
		t.Fatalf("write legacy runtime config: %v", err)
	}

	if _, _, err := svc.ensureCodingRuntimeHome(t.Context(), sessionID, codingRuntimeRoleChat); err != nil {
		t.Fatalf("ensureCodingRuntimeHome: %v", err)
	}

	if _, err := os.Stat(filepath.Join(runtimeHome, "skills", "using-superpowers", "SKILL.md")); err != nil {
		t.Fatalf("expected runtime home to re-seed chat using-superpowers, got err=%v", err)
	}
	if err := os.MkdirAll(filepath.Join(runtimeHome, "skills", "legacy-extra"), 0o700); err != nil {
		t.Fatalf("mkdir legacy extra skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeHome, "skills", "legacy-extra", "SKILL.md"), []byte("# legacy extra\n"), 0o600); err != nil {
		t.Fatalf("write legacy extra skill: %v", err)
	}
	if _, _, err := svc.ensureCodingRuntimeHome(t.Context(), sessionID, codingRuntimeRoleChat); err != nil {
		t.Fatalf("ensureCodingRuntimeHome second pass: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeHome, "skills", "legacy-extra", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("expected runtime home to prune unknown legacy skill, got err=%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(runtimeHome, "config.toml"))
	if err != nil {
		t.Fatalf("read sanitized runtime config: %v", err)
	}
	if strings.Contains(string(raw), "[mcp_servers.memory]") {
		t.Fatalf("expected runtime config to remove mcp_servers.memory, got %q", string(raw))
	}
}

func TestCleanupStaleCodingRuntimeHomes_RemovesStalePendingDirs(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)
	root := svc.codingRuntimesRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll runtimes root: %v", err)
	}

	stalePath := filepath.Join(root, codingRuntimePendingPrefix+"stale_case")
	if err := os.MkdirAll(stalePath, 0o700); err != nil {
		t.Fatalf("MkdirAll stale pending runtime: %v", err)
	}
	if err := os.Chtimes(stalePath, time.Now().Add(-2*codingRuntimePendingTTL), time.Now().Add(-2*codingRuntimePendingTTL)); err != nil {
		t.Fatalf("Chtimes stale pending runtime: %v", err)
	}
	freshPath := filepath.Join(root, codingRuntimePendingPrefix+"fresh_case")
	if err := os.MkdirAll(freshPath, 0o700); err != nil {
		t.Fatalf("MkdirAll fresh pending runtime: %v", err)
	}

	svc.cleanupStaleCodingRuntimeHomes(t.Context())

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale pending runtime to be removed, got err=%v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("expected fresh pending runtime to remain, got err=%v", err)
	}
}

func TestDeleteCodingSession_RemovesRuntimeHomeImmediately(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_delete_runtime_home")

	runtimeRoot := svc.codingSessionRuntimeRoot("sess_delete_runtime_home")
	runtimeHome := filepath.Join(runtimeRoot, codingRuntimeRoleChat, "codex-home")
	if err := os.MkdirAll(runtimeHome, 0o700); err != nil {
		t.Fatalf("MkdirAll runtime home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeHome, "state_5.sqlite"), []byte("stub"), 0o600); err != nil {
		t.Fatalf("WriteFile runtime state: %v", err)
	}

	if err := svc.DeleteCodingSession(t.Context(), "sess_delete_runtime_home"); err != nil {
		t.Fatalf("DeleteCodingSession: %v", err)
	}
	if _, err := os.Stat(runtimeRoot); !os.IsNotExist(err) {
		t.Fatalf("expected runtime root to be removed on session delete, got err=%v", err)
	}
}

func TestEnsureCodingTemplateHome_SeedsBaselineConfig(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)

	root, err := svc.ensureCodingTemplateHome()
	if err != nil {
		t.Fatalf("ensureCodingTemplateHome: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile config.toml: %v", err)
	}
	configText := string(raw)
	for _, want := range []string{
		`approval_policy = "never"`,
		`sandbox_mode = "danger-full-access"`,
		`[mcp_servers.playwright]`,
		`args = ["@playwright/mcp@latest"]`,
		`[mcp_servers.filesystem]`,
		`@modelcontextprotocol/server-filesystem`,
		`[mcp_servers.git]`,
		`mcp-server-git`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("expected template config to contain %q, got %q", want, configText)
		}
	}
	homePath, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if !strings.Contains(configText, homePath) {
		t.Fatalf("expected template config to scope filesystem/git baseline to home path %q, got %q", homePath, configText)
	}
	agentRaw, err := os.ReadFile(filepath.Join(root, "agents", "agent-organizer.toml"))
	if err != nil {
		t.Fatalf("ReadFile agent-organizer agent: %v", err)
	}
	if !strings.Contains(string(agentRaw), `model = "gpt-5.3-codex"`) {
		t.Fatalf("expected bundled heavy agent model to be gpt-5.3-codex, got %q", string(agentRaw))
	}
	miniRaw, err := os.ReadFile(filepath.Join(root, "agents", "agent-installer.toml"))
	if err != nil {
		t.Fatalf("ReadFile agent-installer agent: %v", err)
	}
	if !strings.Contains(string(miniRaw), `model = "gpt-5.1-codex-mini"`) {
		t.Fatalf("expected bundled very-light agent model to be gpt-5.1-codex-mini, got %q", string(miniRaw))
	}
	if _, err := os.Stat(filepath.Join(root, "superpowers")); err != nil {
		t.Fatalf("expected template superpowers repo install: %v", err)
	}
	for _, skill := range []string{
		"using-superpowers",
		"brainstorming",
		"writing-plans",
		"executing-plans",
		"subagent-driven-development",
		"systematic-debugging",
		"verification-before-completion",
		"using-git-worktrees",
	} {
		if _, err := os.Stat(filepath.Join(root, "skills", skill, "SKILL.md")); err != nil {
			t.Fatalf("expected required superpowers skill %q in template skills: %v", skill, err)
		}
	}
}

func TestRefreshCodingTemplateHome_KeepsExistingSuperpowersRepoInstall(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)

	root, err := svc.ensureCodingTemplateHome()
	if err != nil {
		t.Fatalf("ensureCodingTemplateHome: %v", err)
	}
	markerPath := filepath.Join(root, "superpowers", ".install-once-marker")
	if err := os.WriteFile(markerPath, []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// If refresh attempted reinstall/pull, this invalid source path would fail.
	t.Setenv(codingSuperpowersRepoPathEnv, filepath.Join(t.TempDir(), "missing-superpowers-source"))

	refreshedRoot, err := svc.refreshCodingTemplateHome()
	if err != nil {
		t.Fatalf("refreshCodingTemplateHome: %v", err)
	}
	if filepath.Clean(refreshedRoot) != filepath.Clean(root) {
		t.Fatalf("expected refresh to keep same template root, got %q want %q", refreshedRoot, root)
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker after refresh: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "keep" {
		t.Fatalf("expected existing superpowers marker to survive refresh, got %q", string(raw))
	}
}

func TestEnsureCodingTemplateHome_SyncsUserCodexBaseline(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("HOME", homeRoot)
	sourceRoot := filepath.Join(homeRoot, ".codex")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "skills", "custom-skill"), 0o700); err != nil {
		t.Fatalf("seed skills dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceRoot, "mcp", "custom-mcp"), 0o700); err != nil {
		t.Fatalf("seed mcp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "skills", "custom-skill", "SKILL.md"), []byte("# custom skill\n"), 0o600); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "config.toml"), []byte("[mcp_servers.custom]\ncommand = \"npx\"\nargs = [\"custom-mcp\"]\nuser_theme = \"midnight\"\n"), 0o600); err != nil {
		t.Fatalf("write source config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "settings.json"), []byte(`{"theme":"midnight","telemetry":false}`), 0o600); err != nil {
		t.Fatalf("write source settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "auth.json"), []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatalf("write source auth: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sourceRoot, "agents"), 0o700); err != nil {
		t.Fatalf("seed agents dir: %v", err)
	}
	customBundled := "name = \"agent-organizer\"\nmodel = \"custom-local-model\"\n"
	if err := os.WriteFile(filepath.Join(sourceRoot, "agents", "agent-organizer.toml"), []byte(customBundled), 0o600); err != nil {
		t.Fatalf("write existing bundled agent override: %v", err)
	}

	svc, _, _, _ := newCodingTestService(t)
	root, err := svc.ensureCodingTemplateHome()
	if err != nil {
		t.Fatalf("ensureCodingTemplateHome: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatalf("read synced config: %v", err)
	}
	configText := string(raw)
	for _, want := range []string{
		`[mcp_servers.custom]`,
		`command = "npx"`,
		`user_theme = "midnight"`,
		`approval_policy = "never"`,
		`sandbox_mode = "danger-full-access"`,
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("expected synced template config to contain %q, got %q", want, configText)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "skills", "custom-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected skill file to sync into template home: %v", err)
	}
	for _, skill := range []string{
		"using-superpowers",
		"brainstorming",
		"writing-plans",
		"executing-plans",
		"subagent-driven-development",
		"systematic-debugging",
		"verification-before-completion",
		"using-git-worktrees",
	} {
		if _, err := os.Stat(filepath.Join(root, "skills", skill, "SKILL.md")); err != nil {
			t.Fatalf("expected required superpowers skill %q in template home: %v", skill, err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "agents", "custom-agent.toml"), []byte("name = \"custom-agent\"\nmodel = \"gpt-5.2-codex\"\n"), 0o600); err != nil {
		t.Fatalf("write agent file: %v", err)
	}
	root, err = svc.refreshCodingTemplateHome()
	if err != nil {
		t.Fatalf("refreshCodingTemplateHome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "custom-agent.toml")); err != nil {
		t.Fatalf("expected agent file to sync into template home: %v", err)
	}
	sourceBundledRaw, err := os.ReadFile(filepath.Join(sourceRoot, "agents", "agent-organizer.toml"))
	if err != nil {
		t.Fatalf("read user bundled agent override: %v", err)
	}
	if string(sourceBundledRaw) != customBundled {
		t.Fatalf("expected existing user bundled agent to stay untouched, got %q", string(sourceBundledRaw))
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "agents", "agent-installer.toml")); err != nil {
		t.Fatalf("expected missing bundled agent to be installed into user codex home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "skills", "brainstorming", "SKILL.md")); err != nil {
		t.Fatalf("expected bundled brainstorming skill to be installed into user codex home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "skills", "brainstorming", "scripts", "frame-template.html")); err != nil {
		t.Fatalf("expected bundled brainstorming frame template in user codex home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, "skills", "brainstorming", "scripts", "helper.js")); err != nil {
		t.Fatalf("expected bundled brainstorming helper script in user codex home: %v", err)
	}
	templateBundledRaw, err := os.ReadFile(filepath.Join(root, "agents", "agent-organizer.toml"))
	if err != nil {
		t.Fatalf("read template bundled agent: %v", err)
	}
	if string(templateBundledRaw) != customBundled {
		t.Fatalf("expected template home to preserve synced user agent override, got %q", string(templateBundledRaw))
	}
	if _, err := os.Stat(filepath.Join(root, "settings.json")); err != nil {
		t.Fatalf("expected non-auth settings.json to sync into template home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected auth.json not to sync into template home, got err=%v", err)
	}
}

func TestCleanupStaleCodingRuntimeHomes_RemovesPendingAndOrphans(t *testing.T) {
	svc, st, _, _ := newCodingTestService(t)

	liveSession := store.CodingSession{
		ID:             "sess_live_runtime",
		Title:          "Live Runtime",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "danger-full-access",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	}
	if _, err := st.CreateCodingSession(t.Context(), liveSession); err != nil {
		t.Fatalf("create live session: %v", err)
	}

	makeRuntimeDir := func(name string, age time.Duration) string {
		path := filepath.Join(svc.codingRuntimesRoot(), name)
		if err := os.MkdirAll(filepath.Join(path, "chat", "codex-home"), 0o700); err != nil {
			t.Fatalf("mkdir runtime %s: %v", name, err)
		}
		past := time.Now().Add(-age)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatalf("chtimes runtime %s: %v", name, err)
		}
		return path
	}

	livePath := makeRuntimeDir(liveSession.ID, 2*time.Hour)
	orphanPath := makeRuntimeDir("sess_orphan_runtime", 2*time.Hour)
	pendingPath := makeRuntimeDir("pending_123456", 2*time.Hour)
	freshPendingPath := makeRuntimeDir("pending_fresh", 2*time.Minute)

	svc.cleanupStaleCodingRuntimeHomes(t.Context())

	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("expected live runtime to remain, got %v", err)
	}
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected orphan runtime to be removed, got err=%v", err)
	}
	if _, err := os.Stat(pendingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale pending runtime to be removed, got err=%v", err)
	}
	if _, err := os.Stat(freshPendingPath); err != nil {
		t.Fatalf("expected fresh pending runtime to remain, got %v", err)
	}
}

func TestDeleteCodingSession_RemovesRuntimeRoot(t *testing.T) {
	svc, st, _, _ := newCodingTestService(t)

	session := store.CodingSession{
		ID:             "sess_delete_runtime",
		Title:          "Delete Runtime",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "danger-full-access",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	}
	if _, err := st.CreateCodingSession(t.Context(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtimeRoot := svc.codingSessionRuntimeRoot(session.ID)
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "chat", "codex-home"), 0o700); err != nil {
		t.Fatalf("mkdir runtime root: %v", err)
	}

	if err := svc.DeleteCodingSession(t.Context(), session.ID); err != nil {
		t.Fatalf("DeleteCodingSession: %v", err)
	}
	if _, err := os.Stat(runtimeRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected runtime root to be removed, got err=%v", err)
	}
}

func TestCodingExecAccountDeactivated(t *testing.T) {
	cases := []string{
		"codex runtime failed: unexpected status 401 Unauthorized: Your OpenAI account has been deactivated, auth error code: account_deactivated",
		"codex runtime failed: account_suspended",
	}
	for _, raw := range cases {
		if !codingRuntimeAccountDeactivated(errors.New(raw)) {
			t.Fatalf("expected deactivated detection for %q", raw)
		}
	}
}

func TestCodingExecUsageExhausted(t *testing.T) {
	cases := []string{
		"codex runtime failed: unexpected status 429 Too Many Requests: rate limit exceeded",
		"codex runtime failed: insufficient_quota",
		"codex runtime failed: billing hard limit reached",
		"codex runtime failed: You've hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at Apr 2nd, 2026 10:15 PM.",
	}
	for _, raw := range cases {
		if !codingRuntimeUsageExhausted(errors.New(raw)) {
			t.Fatalf("expected usage exhaustion detection for %q", raw)
		}
	}
}

func TestResolveCommandContent_PreservesRawSlashReviewText(t *testing.T) {
	prompt, visible := resolveCommandContent("chat", "/review focus auth middleware")
	if prompt != "/review focus auth middleware" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if visible != "/review focus auth middleware" {
		t.Fatalf("unexpected user-visible content: %q", visible)
	}
}

func TestResolveCommandContent_PreservesBareSlashReviewText(t *testing.T) {
	prompt, visible := resolveCommandContent("chat", "/review")
	if prompt != "/review" {
		t.Fatalf("expected bare /review to stay intact, got: %q", prompt)
	}
	if visible != "/review" {
		t.Fatalf("unexpected user-visible content: %q", visible)
	}
}

func TestResolveCommandContent_PlainTextStillPassesThrough(t *testing.T) {
	prompt, visible := resolveCommandContent("chat", "Check auth flow")
	if prompt != "Check auth flow" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if visible != "Check auth flow" {
		t.Fatalf("unexpected user-visible content: %q", visible)
	}
}

func newCodingTestService(t *testing.T) (*Service, *store.Store, *icrypto.Crypto, config.Config) {
	t.Helper()
	configureSuperpowersFixtureEnv(t)
	root := t.TempDir()
	dbPath := filepath.Join(root, "data.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")

	svc := New(cfg, st, cry)
	t.Cleanup(func() {
		_ = st.Close()
	})
	return svc, st, cry, cfg
}

func configureSuperpowersFixtureEnv(t *testing.T) {
	t.Helper()
	fixtureRoot := t.TempDir()
	skillsRoot := filepath.Join(fixtureRoot, "skills")
	required := []string{
		"using-superpowers",
		"brainstorming",
		"writing-plans",
		"executing-plans",
		"subagent-driven-development",
		"systematic-debugging",
		"verification-before-completion",
		"using-git-worktrees",
	}
	for _, name := range required {
		skillRoot := filepath.Join(skillsRoot, name)
		if err := os.MkdirAll(skillRoot, 0o700); err != nil {
			t.Fatalf("mkdir superpowers fixture skill %s: %v", name, err)
		}
		body := fmt.Sprintf("---\nname: %s\n---\n\n# %s\n", name, name)
		if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("write superpowers fixture skill %s: %v", name, err)
		}
	}
	t.Setenv(codingSuperpowersRepoPathEnv, fixtureRoot)
	t.Setenv(codingSuperpowersRepoURLEnv, "https://example.invalid/superpowers.git")
}

func seedCodingTestAccount(t *testing.T, st *store.Store, cry *icrypto.Crypto, cfg config.Config, id, email string, activeAPI bool) store.Account {
	t.Helper()
	tokenID, err := cry.Encrypt([]byte("id-token-" + id))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	tokenAccess, err := cry.Encrypt([]byte("access-token-" + id))
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	tokenRefresh, err := cry.Encrypt([]byte("refresh-token-" + id))
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	now := time.Now().UTC()
	account := store.Account{
		ID:           id,
		Email:        email,
		Alias:        email,
		TokenID:      tokenID,
		TokenAccess:  tokenAccess,
		TokenRefresh: tokenRefresh,
		CodexHome:    cfg.CodexHome,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
		Active:       activeAPI,
	}
	if err := st.UpsertAccount(t.Context(), account); err != nil {
		t.Fatalf("upsert account %s: %v", id, err)
	}
	if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
		t.Fatalf("write auth.json for %s: %v", id, err)
	}
	return account
}

func activateCodingTestCLIAccount(t *testing.T, svc *Service, st *store.Store, account store.Account) {
	t.Helper()
	if err := st.SetActiveCLIAccount(t.Context(), account.ID); err != nil {
		t.Fatalf("SetActiveCLIAccount %s: %v", account.ID, err)
	}
	svc.setCLIActiveCache(account.ID)
	if err := svc.syncAccountAuthToCodexHome(account); err != nil {
		t.Fatalf("syncAccountAuthToCodexHome %s: %v", account.ID, err)
	}
}

func TestNormalizeCodingCommandMode_ReviewAliasFallsBackToChat(t *testing.T) {
	if got := normalizeCodingCommandMode("review"); got != "chat" {
		t.Fatalf("expected review alias to fall back to chat, got %q", got)
	}
	if got := normalizeCodingCommandMode(" REVIEW "); got != "chat" {
		t.Fatalf("expected review alias to fall back to chat case-insensitively, got %q", got)
	}
	if got := normalizeCodingCommandMode("chat"); got != "chat" {
		t.Fatalf("expected chat, got %q", got)
	}
}

func TestBuildCodingPrompt_RawSlashReviewBypassesWrapping(t *testing.T) {
	raw := "/review check race condition"
	prompt := buildCodingPrompt("chat", raw)
	if got, want := prompt, raw; got != want {
		t.Fatalf("expected raw slash prompt passthrough: got %q want %q", got, want)
	}
	if strings.Contains(strings.ToLower(prompt), "context hygiene rules") {
		t.Fatalf("raw slash prompt should not include context hygiene wrapper: %q", prompt)
	}
}

func TestResolveStreamAssistantParts_PreservesAssistantMessages(t *testing.T) {
	reply := provider.ChatResult{}
	got := resolveStreamAssistantParts(reply, "HelloHello", []string{"Hello"})
	if len(got) != 1 || got[0] != "Hello" {
		t.Fatalf("expected streamed assistant message to be preserved, got %#v", got)
	}
}

func TestResolveStreamAssistantParts_FallsBackToMergedDeltaText(t *testing.T) {
	reply := provider.ChatResult{}
	got := resolveStreamAssistantParts(reply, "Hello from delta", nil)
	if len(got) != 1 || got[0] != "Hello from delta" {
		t.Fatalf("expected merged delta fallback, got %#v", got)
	}
}

func TestCreateCodingSession_UsesUUIDSessionIDWithSeparateThreadID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, _, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, svc.Store, cry, cfg, "acc_create_session", "create-session@example.com", false)
	svc.Codex.Binary = writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_session_create"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_session_create"}}}'
      exit 0
    fi
  done
fi
exit 1
`)

	session, err := svc.CreateCodingSession(t.Context(), "New Session", "gpt-5.2-codex", "medium", "~/", "full-access")
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}
	if session.ID == "" {
		t.Fatalf("expected public session id to be populated")
	}
	if session.ID == "thread_session_create" {
		t.Fatalf("expected public session id to differ from codex thread id, got %q", session.ID)
	}
	if session.CodexThreadID != "thread_session_create" {
		t.Fatalf("expected codex thread id to be persisted, got %q", session.CodexThreadID)
	}
}

func TestCreateCodingSession_PersistsChatThreadForWorkspaceWriteSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, _, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, svc.Store, cry, cfg, "acc_create_session_legacy_mode", "create-session-legacy-mode@example.com", false)
	svc.Codex.Binary = writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_session_create_legacy_mode"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_session_create_legacy_mode"}}}'
      exit 0
    fi
  done
fi
exit 1
`)

	session, err := svc.CreateCodingSession(t.Context(), "Legacy Mode Session", "gpt-5.2-codex", "medium", "~/", "workspace-write")
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}
	if session.CodexThreadID != "thread_session_create_legacy_mode" {
		t.Fatalf("expected codex thread id to be persisted, got %q", session.CodexThreadID)
	}
	stored, err := svc.Store.GetCodingSession(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if stored.CodexThreadID != "thread_session_create_legacy_mode" {
		t.Fatalf("expected stored canonical thread id to stay aligned with working thread, got %q", stored.CodexThreadID)
	}
}

func TestRestartCodingRuntime_PreservesCanonicalThreadForChatSession(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)

	now := time.Now().UTC()
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             "sess_restart_chat_only_cleanup",
		Title:          "Restart Chat Cleanup",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        cfg.CodexHome,
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_chat_only",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	deferred, err := svc.RestartCodingRuntime(t.Context(), "sess_restart_chat_only_cleanup", false)
	if err != nil {
		t.Fatalf("RestartCodingRuntime: %v", err)
	}
	if deferred {
		t.Fatalf("expected non-running restart to complete immediately")
	}

	updated, err := st.GetCodingSession(t.Context(), "sess_restart_chat_only_cleanup")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if updated.RestartPending {
		t.Fatalf("expected restart_pending cleared after restart")
	}
	if updated.CodexThreadID != "thread_chat_only" {
		t.Fatalf("expected restart cleanup to preserve canonical thread id, got %q", updated.CodexThreadID)
	}
}

func TestPrepareCodingTurnSetup_UsesStoredChatThreadForChatTurn(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	if err := os.MkdirAll(cfg.CodexHome, 0o755); err != nil {
		t.Fatalf("MkdirAll codex home: %v", err)
	}

	now := time.Now().UTC()
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             "sess_legacy_chat_turn",
		Title:          "Legacy Chat Turn",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        cfg.CodexHome,
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_executor_legacy",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	setup, err := svc.prepareCodingTurnSetup(
		t.Context(),
		"sess_legacy_chat_turn",
		"hello from chat-only mode",
		"",
		"",
		"",
		"",
		"chat",
	)
	if err != nil {
		t.Fatalf("prepareCodingTurnSetup: %v", err)
	}
	if setup.session.CodexThreadID != "thread_executor_legacy" {
		t.Fatalf("expected prepared session to reuse executor thread as chat thread, got %q", setup.session.CodexThreadID)
	}
}

func TestSendCodingMessageStream_NormalChatUsesAppServerThreadResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, st, cry, cfg, "acc_normal_chat", "normal-chat@example.com", false)
	svc.Codex.Binary = writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_normal_chat"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      if ! printf '%s' "$line" | grep -q '"text":"plain appserver prompt"'; then
        echo '{"method":"error","params":{"threadId":"thread_normal_chat","turnId":"turn_normal_chat","error":{"message":"unexpected prompt payload"}}}'
        exit 0
      fi
      echo '{"id":"3","result":{"turn":{"id":"turn_normal_chat","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_normal_chat","turn":{"id":"turn_normal_chat"}}}'
      echo '{"method":"item/agentMessage/delta","params":{"threadId":"thread_normal_chat","turnId":"turn_normal_chat","itemId":"item_agent","delta":"hello from app server"}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_normal_chat","turnId":"turn_normal_chat","item":{"type":"agentMessage","id":"item_agent","text":"hello from app server"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_normal_chat","turnId":"turn_normal_chat","tokenUsage":{"total":{"inputTokens":9,"outputTokens":4,"cachedInputTokens":0},"last":{"inputTokens":9,"outputTokens":4,"cachedInputTokens":0},"modelContextWindow":200000}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_normal_chat","turn":{"id":"turn_normal_chat","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             "session_normal_chat",
		Title:          "Normal Chat",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "full-access",
		CodexThreadID:  "thread_normal_chat",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}
	if _, err := st.AppendCodingMessage(t.Context(), store.CodingMessage{
		ID:        "msg_seed_history",
		SessionID: "session_normal_chat",
		Role:      "user",
		Content:   "prior context",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendCodingMessage seed history: %v", err)
	}
	var streamed []provider.ChatEvent
	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"session_normal_chat",
		"plain appserver prompt",
		"gpt-5.2-codex",
		"medium",
		"~/",
		"full-access",
		"chat",
		func(evt provider.ChatEvent) error {
			streamed = append(streamed, evt)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "hello from app server" {
		t.Fatalf("expected app-server assistant content, got %q", got)
	}
	if result.Session.ID != "session_normal_chat" {
		t.Fatalf("expected public session id to stay the database uuid / session id, got %q", result.Session.ID)
	}
	if result.Session.CodexThreadID != "thread_normal_chat" {
		t.Fatalf("expected codex thread id to remain separate, got %q", result.Session.CodexThreadID)
	}
	if len(streamed) == 0 {
		t.Fatalf("expected streamed events")
	}
}

func TestSendCodingMessage_NormalChatUsesAppServerThreadResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, st, cry, cfg, "acc_normal_chat_sync", "normal-chat-sync@example.com", false)
	svc.Codex.Binary = writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_normal_chat_sync"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      if ! printf '%s' "$line" | grep -q '"text":"plain sync appserver prompt"'; then
        echo '{"method":"error","params":{"threadId":"thread_normal_chat_sync","turnId":"turn_normal_chat_sync","error":{"message":"unexpected prompt payload"}}}'
        exit 0
      fi
      echo '{"id":"3","result":{"turn":{"id":"turn_normal_chat_sync","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_normal_chat_sync","turn":{"id":"turn_normal_chat_sync"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_normal_chat_sync","turnId":"turn_normal_chat_sync","item":{"type":"agentMessage","id":"item_agent","text":"hello from sync app server"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_normal_chat_sync","turnId":"turn_normal_chat_sync","tokenUsage":{"total":{"inputTokens":8,"outputTokens":5,"cachedInputTokens":0},"last":{"inputTokens":8,"outputTokens":5,"cachedInputTokens":0},"modelContextWindow":200000}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_normal_chat_sync","turn":{"id":"turn_normal_chat_sync","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             "session_normal_chat_sync",
		Title:          "Normal Chat Sync",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "full-access",
		CodexThreadID:  "thread_normal_chat_sync",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}
	if _, err := st.AppendCodingMessage(t.Context(), store.CodingMessage{
		ID:        "msg_seed_history_sync",
		SessionID: "session_normal_chat_sync",
		Role:      "user",
		Content:   "prior sync context",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendCodingMessage seed history: %v", err)
	}

	result, err := svc.SendCodingMessage(
		t.Context(),
		"session_normal_chat_sync",
		"plain sync appserver prompt",
		"gpt-5.2-codex",
		"medium",
		"~/",
		"full-access",
		"chat",
	)
	if err != nil {
		t.Fatalf("SendCodingMessage: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "hello from sync app server" {
		t.Fatalf("expected app-server assistant content, got %q", got)
	}
	if result.Session.ID != "session_normal_chat_sync" {
		t.Fatalf("expected public session id to stay the database session id, got %q", result.Session.ID)
	}
	if result.Session.CodexThreadID != "thread_normal_chat_sync" {
		t.Fatalf("expected codex thread id to remain separate, got %q", result.Session.CodexThreadID)
	}
}

func TestAppendCodingRunFailureMessage_PersistsWhenContextCanceled(t *testing.T) {
	svc, st, _, _ := newCodingTestService(t)
	startedAt := time.Now().UTC().Add(-1 * time.Minute)
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             "sess_run_failure_ctx_canceled",
		Title:          "Run Failure Context Canceled",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "full-access",
		CreatedAt:      startedAt,
		UpdatedAt:      startedAt,
		LastMessageAt:  startedAt,
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := svc.appendCodingRunFailureMessage(canceledCtx, "sess_run_failure_ctx_canceled", errors.New("upstream runtime failed")); err != nil {
		t.Fatalf("appendCodingRunFailureMessage: %v", err)
	}

	history, err := st.ListCodingMessages(t.Context(), "sess_run_failure_ctx_canceled")
	if err != nil {
		t.Fatalf("ListCodingMessages: %v", err)
	}
	if len(history) == 0 {
		t.Fatalf("expected failure message to be persisted")
	}
	last := history[len(history)-1]
	if strings.ToLower(strings.TrimSpace(last.Role)) != "stderr" {
		t.Fatalf("expected stderr role, got %q", last.Role)
	}
	if got := strings.TrimSpace(last.Content); !strings.HasPrefix(got, "Run failed: upstream runtime failed") {
		t.Fatalf("expected persisted run failure content, got %q", got)
	}
}

func TestSendCodingMessageStream_NormalChatDoesNotPersistInternalPromptArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	seedCodingTestAccount(t, st, cry, cfg, "acc_normal_chat_hidden", "normal-hidden@example.com", false)
	svc.Codex.Binary = writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    rpc_id="$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"'"${rpc_id:-1}"'","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      echo '{"id":"'"${rpc_id:-2}"'","result":{"thread":{"id":"thread_hidden_normal_chat"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"'"${rpc_id:-2}"'","result":{"turn":{"id":"turn_hidden_normal_chat","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_hidden_normal_chat","turn":{"id":"turn_hidden_normal_chat"}}}'
      echo '{"method":"rawResponseItem/completed","params":{"threadId":"thread_hidden_normal_chat","turnId":"turn_hidden_normal_chat","item":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>hidden</permissions instructions>"}]}}}'
      echo '{"method":"rawResponseItem/completed","params":{"threadId":"thread_hidden_normal_chat","turnId":"turn_hidden_normal_chat","item":{"type":"message","role":"user","content":[{"type":"input_text","text":"echo user prompt"}]}}}'
      echo '{"method":"item/started","params":{"threadId":"thread_hidden_normal_chat","turnId":"turn_hidden_normal_chat","item":{"type":"userMessage","id":"item_user","content":[{"type":"text","text":"echo user prompt"}]}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_hidden_normal_chat","turnId":"turn_hidden_normal_chat","item":{"type":"agentMessage","id":"item_agent","text":"TEST final answer"}}}'
      echo '{"method":"rawResponseItem/completed","params":{"threadId":"thread_hidden_normal_chat","turnId":"turn_hidden_normal_chat","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"TEST final answer"}]}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_hidden_normal_chat","turn":{"id":"turn_hidden_normal_chat","status":"completed"}}}'
      sleep 0.1
      exit 0
    fi
  done
fi
exit 1
`)
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             "session_hidden_normal_chat",
		Title:          "Normal Chat Hidden",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "full-access",
		CodexThreadID:  "thread_hidden_normal_chat",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}

	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"session_hidden_normal_chat",
		"echo user prompt",
		"gpt-5.2-codex",
		"medium",
		"~/",
		"full-access",
		"chat",
		nil,
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "TEST final answer" {
		t.Fatalf("expected final assistant reply only, got %q", got)
	}
	history, err := st.ListCodingMessages(t.Context(), "session_hidden_normal_chat")
	if err != nil {
		t.Fatalf("ListCodingMessages: %v", err)
	}
	assistantContents := make([]string, 0, 2)
	for _, msg := range history {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		assistantContents = append(assistantContents, strings.TrimSpace(msg.Content))
	}
	if len(assistantContents) != 1 || assistantContents[0] != "TEST final answer" {
		t.Fatalf("expected exactly one public assistant row with final answer, got %#v", assistantContents)
	}
	for _, msg := range history {
		if strings.Contains(msg.Content, "<permissions instructions>") {
			t.Fatalf("expected internal permissions prompt to stay out of persisted history, found %q", msg.Content)
		}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && strings.Contains(strings.ToLower(msg.Content), "echo user prompt") {
			t.Fatalf("expected user prompt not to be replayed as assistant content, found %q", msg.Content)
		}
	}
}

func TestResolveStreamAssistantParts_PreservesProviderReplyMessages(t *testing.T) {
	reply := provider.ChatResult{
		Messages: []string{"First", "Second"},
		Text:     "First\n\nSecond",
	}
	got := resolveStreamAssistantParts(reply, "stale streamed fallback", []string{"streamed"})
	if len(got) != 2 || got[0] != "First" || got[1] != "Second" {
		t.Fatalf("expected all provider reply messages, got %#v", got)
	}
}

func TestNormalizedAssistantParts_CollapsesCumulativeMessages(t *testing.T) {
	got := normalizedAssistantParts([]string{
		"Saya audit lagi fokus compact.",
		"Saya audit lagi fokus compact.\n\nSaya temukan satu akar bug.",
		"Saya audit lagi fokus compact.\n\nSaya temukan satu akar bug.\n\nFix sudah masuk.",
	}, "")
	if len(got) != 1 {
		t.Fatalf("expected cumulative assistant messages to collapse to one part, got %#v", got)
	}
	if want := "Fix sudah masuk."; !strings.Contains(got[0], want) {
		t.Fatalf("expected final cumulative text to be preserved, got %#v", got)
	}
}

func TestResolveStreamAssistantParts_PreservesNormalizedAssistantMessages(t *testing.T) {
	reply := provider.ChatResult{}
	got := resolveStreamAssistantParts(reply, "stale merged fallback", []string{
		"Saya audit dulu.",
		"Saya audit dulu.\n\nFix sudah masuk.",
		"Final jawaban bersih.",
	})
	if len(got) != 2 || got[0] != "Saya audit dulu.\n\nFix sudah masuk." || got[1] != "Final jawaban bersih." {
		t.Fatalf("expected normalized streamed assistant messages to be preserved, got %#v", got)
	}
}

func TestCodingRunStatus_CollapsesLegacyActorToChat(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)

	release, err := svc.beginCodingRun("sess_executor_alias")
	if err != nil {
		t.Fatalf("beginCodingRun: %v", err)
	}
	defer release()

	svc.setCodingRunActor("sess_executor_alias", "executor")
	svc.setCodingRunCancel("sess_executor_alias", func() {})

	inFlight, _, role := svc.CodingRunStatus("sess_executor_alias")
	if !inFlight {
		t.Fatalf("expected in-flight run")
	}
	if role != "chat" {
		t.Fatalf("expected legacy actor to collapse to chat, got %q", role)
	}

	stopped := svc.StopCodingRun("sess_executor_alias", false)
	if !stopped {
		t.Fatalf("expected executor stop to succeed")
	}
}

func TestSendCodingMessage_LegacyChatHydrationStateNormalizesToChatOnly(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_chat_hydration_pending")

	result, err := svc.SendCodingMessage(
		t.Context(),
		"sess_chat_hydration_pending",
		"/plan",
		"",
		"",
		"",
		"",
		"chat",
	)
	if err != nil {
		t.Fatalf("SendCodingMessage: %v", err)
	}
	want := "Perintah /plan tidak dikenali di mode chat."
	if got := strings.TrimSpace(result.Assistant.Content); got != want {
		t.Fatalf("unexpected assistant response: %q", got)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_chat_hydration_pending")
	if err != nil {
		t.Fatalf("GetCodingSession updated: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to stay unchanged, got %q", updated.Title)
	}
}

func TestSendCodingMessageStream_AutoSwitchesCLIOnDeactivated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	first := seedCodingTestAccount(t, st, cry, cfg, "acc_first", "first@example.com", false)
	second := seedCodingTestAccount(t, st, cry, cfg, "acc_second", "second@example.com", false)
	if _, err := svc.UseAccountCLI(t.Context(), first.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}
	createCodingTestSession(t, st, cfg, "sess_auto_switch")
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"'"${CODEX_HOME}"'","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_switched"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      if grep -q 'id-token-acc_first' "${CODEX_HOME}/auth.json"; then
        echo '{"id":"3","result":{"turn":{"id":"turn_switched_retry","status":"inProgress"}}}'
        echo '{"method":"error","params":{"threadId":"thread_switched","error":{"message":"unexpected status 401 Unauthorized: Your OpenAI account has been deactivated, auth error code: account_deactivated"}}}'
        continue
      fi
      echo '{"id":"3","result":{"turn":{"id":"turn_switched","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_switched","turn":{"id":"turn_switched"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_switched","turnId":"turn_switched","item":{"id":"item_0","type":"agentMessage","text":"switched ok"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_switched","turnId":"turn_switched","tokenUsage":{"last":{"inputTokens":2,"outputTokens":3,"cachedInputTokens":0}}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_switched","turn":{"id":"turn_switched","status":"completed"}}}'
      continue
    fi
  done
fi
`))

	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_auto_switch",
		"hello",
		"",
		"",
		"",
		"",
		"chat",
		nil,
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream auto-switch: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "switched ok" {
		t.Fatalf("expected switched assistant content, got %q", got)
	}
	if got := svc.readCodingRuntimeAccountMarker("sess_auto_switch", codingRuntimeRoleChat); got != second.ID {
		t.Fatalf("expected chat runtime account marker %s after switch, got %s", second.ID, got)
	}
}

func TestSendCodingMessageStream_AutoSwitchesCLIOnUsageExhausted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	first := seedCodingTestAccount(t, st, cry, cfg, "acc_first_usage", "first-usage@example.com", false)
	second := seedCodingTestAccount(t, st, cry, cfg, "acc_second_usage", "second-usage@example.com", false)
	if _, err := svc.UseAccountCLI(t.Context(), first.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}
	createCodingTestSession(t, st, cfg, "sess_auto_switch_usage")
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"'"${CODEX_HOME}"'","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_switched_usage"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      if grep -q 'id-token-acc_first_usage' "${CODEX_HOME}/auth.json"; then
        echo '{"id":"3","result":{"turn":{"id":"turn_switched_usage_retry","status":"inProgress"}}}'
        echo '{"method":"error","params":{"threadId":"thread_switched_usage","error":{"message":"unexpected status 429 Too Many Requests: insufficient_quota"}}}'
        continue
      fi
      echo '{"id":"3","result":{"turn":{"id":"turn_switched_usage","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_switched_usage","turn":{"id":"turn_switched_usage"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_switched_usage","turnId":"turn_switched_usage","item":{"id":"item_0","type":"agentMessage","text":"switched after quota"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_switched_usage","turnId":"turn_switched_usage","tokenUsage":{"last":{"inputTokens":2,"outputTokens":3,"cachedInputTokens":0}}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_switched_usage","turn":{"id":"turn_switched_usage","status":"completed"}}}'
      continue
    fi
  done
fi
`))

	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_auto_switch_usage",
		"hello",
		"",
		"",
		"",
		"",
		"chat",
		nil,
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream auto-switch usage: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "switched after quota" {
		t.Fatalf("expected switched assistant content, got %q", got)
	}
	if got := svc.readCodingRuntimeAccountMarker("sess_auto_switch_usage", codingRuntimeRoleChat); got != second.ID {
		t.Fatalf("expected chat runtime account marker %s after switch, got %s", second.ID, got)
	}
}

func TestSendCodingMessageStream_AutoSwitchesCLIOnHitYourUsageLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	first := seedCodingTestAccount(t, st, cry, cfg, "acc_first_hit_limit", "first-hit-limit@example.com", false)
	second := seedCodingTestAccount(t, st, cry, cfg, "acc_second_hit_limit", "second-hit-limit@example.com", false)
	if _, err := svc.UseAccountCLI(t.Context(), first.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}
	createCodingTestSession(t, st, cfg, "sess_auto_switch_hit_limit")
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"'"${CODEX_HOME}"'","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_switched_hit_limit"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      if grep -q 'id-token-acc_first_hit_limit' "${CODEX_HOME}/auth.json"; then
        echo '{"id":"3","result":{"turn":{"id":"turn_switched_hit_limit_retry","status":"inProgress"}}}'
        echo '{"method":"error","params":{"threadId":"thread_switched_hit_limit","error":{"message":"You'\''ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at Apr 2nd, 2026 10:15 PM."}}}'
        continue
      fi
      echo '{"id":"3","result":{"turn":{"id":"turn_switched_hit_limit","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_switched_hit_limit","turn":{"id":"turn_switched_hit_limit"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_switched_hit_limit","turnId":"turn_switched_hit_limit","item":{"id":"item_0","type":"agentMessage","text":"switched after hit usage limit"}}}'
      echo '{"method":"thread/tokenUsage/updated","params":{"threadId":"thread_switched_hit_limit","turnId":"turn_switched_hit_limit","tokenUsage":{"last":{"inputTokens":2,"outputTokens":3,"cachedInputTokens":0}}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_switched_hit_limit","turn":{"id":"turn_switched_hit_limit","status":"completed"}}}'
      continue
    fi
  done
fi
`))

	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_auto_switch_hit_limit",
		"hello",
		"",
		"",
		"",
		"",
		"chat",
		nil,
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream auto-switch hit usage limit: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "switched after hit usage limit" {
		t.Fatalf("expected switched assistant content, got %q", got)
	}
	if got := svc.readCodingRuntimeAccountMarker("sess_auto_switch_hit_limit", codingRuntimeRoleChat); got != second.ID {
		t.Fatalf("expected chat runtime account marker %s after switch, got %s", second.ID, got)
	}
	authBytes, err := os.ReadFile(filepath.Join(svc.codingRuntimeHome("sess_auto_switch_hit_limit", codingRuntimeRoleChat), "auth.json"))
	if err != nil {
		t.Fatalf("read runtime auth.json: %v", err)
	}
	if !strings.Contains(string(authBytes), "id-token-acc_second_hit_limit") {
		t.Fatalf("expected runtime auth.json to be rewritten for fallback account, got %q", string(authBytes))
	}
}

func TestUpdateThreadStateForCommand_ChatOnlyThreadRolloverKeepsChatAligned(t *testing.T) {
	session := store.CodingSession{
		CodexThreadID: "thread_executor_old",
	}

	updateThreadStateForCommand(&session, "chat", "thread_executor_new")

	if session.CodexThreadID != "thread_executor_new" {
		t.Fatalf("expected codex thread to track executor rollover, got %q", session.CodexThreadID)
	}
}

func TestSendCodingMessageStream_DeactivatedErrorIsSanitized(t *testing.T) {
	cases := []string{
		"unexpected status 401 Unauthorized: Your OpenAI account has been deactivated, auth error code: account_deactivated",
		"account_suspended",
	}
	for _, raw := range cases {
		if !codingRuntimeAccountDeactivated(errors.New(raw)) {
			t.Fatalf("expected deactivated detection for %q", raw)
		}
		sanitized := sanitizeCodingRuntimeUserFacingError(errors.New(raw))
		if sanitized == nil || !strings.Contains(strings.ToLower(sanitized.Error()), "deactivated") {
			t.Fatalf("expected sanitized deactivated runtime error for %q, got %v", raw, sanitized)
		}
	}
}

func TestSendCodingMessageStream_LiveAccessShellErrorIsSanitized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex test runner is unix-only")
	}
	svc, st, cry, cfg := newCodingTestService(t)
	account := seedCodingTestAccount(t, st, cry, cfg, "acc_live_access", "live-access@example.com", false)
	if _, err := svc.UseAccountCLI(t.Context(), account.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}
	createCodingTestSession(t, st, cfg, "sess_live_access_error")
	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_live_access_error"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_live_access_error"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_live_access_error","status":"inProgress"}}}'
      echo '{"method":"error","params":{"threadId":"thread_live_access_error","turnId":"turn_live_access_error","error":{"message":"npm install failed: live access is still not available in this shell; retry after shell live access is enabled for download work"}}}'
      exit 0
    fi
  done
fi
exit 1
`))

	_, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_live_access_error",
		"install dependencies",
		"",
		"",
		"",
		"",
		"chat",
		nil,
	)
	if err == nil {
		t.Fatalf("expected sanitized live-access error")
	}
	if got := strings.TrimSpace(err.Error()); !strings.Contains(strings.ToLower(got), "live access") {
		t.Fatalf("expected concise live-access error mentioning live access, got %q", got)
	}
}

func TestSendCodingMessage_RejectsPlanSlashInChatMode(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_chat_plan_command")

	result, err := svc.SendCodingMessage(
		t.Context(),
		"sess_chat_plan_command",
		"/plan",
		"",
		"",
		"",
		"",
		"chat",
	)
	if err != nil {
		t.Fatalf("SendCodingMessage: %v", err)
	}
	if got := strings.TrimSpace(result.Assistant.Content); got != "Perintah /plan tidak dikenali di mode chat." {
		t.Fatalf("unexpected assistant response: %q", got)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_chat_plan_command")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to remain unchanged, got %q", updated.Title)
	}
}

func TestSendCodingMessage_RejectsPlanAliasInChatMode(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_chat_plan_alias")

	result, err := svc.SendCodingMessage(
		t.Context(),
		"sess_chat_plan_alias",
		"/plan tighten scope",
		"",
		"",
		"",
		"",
		"chat",
	)
	if err != nil {
		t.Fatalf("SendCodingMessage: %v", err)
	}
	want := "Perintah /plan tidak dikenali di mode chat."
	if got := strings.TrimSpace(result.Assistant.Content); got != want {
		t.Fatalf("unexpected assistant response: %q", got)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_chat_plan_alias")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to remain unchanged, got %q", updated.Title)
	}
}

func TestSendCodingMessage_PlanSlashUsesChatOnlyUnknownCommand(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_legacy_plan_cmd")

	result, err := svc.SendCodingMessage(
		t.Context(),
		"sess_legacy_plan_cmd",
		"/plan tighten the orchestration loop",
		"",
		"",
		"",
		"",
		"chat",
	)
	if err != nil {
		t.Fatalf("SendCodingMessage: %v", err)
	}
	want := "Perintah /plan tidak dikenali di mode chat."
	if got := strings.TrimSpace(result.Assistant.Content); got != want {
		t.Fatalf("unexpected assistant response: %q", got)
	}
	if strings.TrimSpace(result.Assistant.Actor) != "" {
		t.Fatalf("expected chat-only assistant actor to stay blank, got %q", result.Assistant.Actor)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_legacy_plan_cmd")
	if err != nil {
		t.Fatalf("GetCodingSession updated: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to remain unchanged, got %q", updated.Title)
	}
}

func TestSendCodingMessageStream_StartsFreshChatThreadWithoutResume(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	account := seedCodingTestAccount(t, st, cry, cfg, "acc_legacy_resume_log", "resume-log@example.com", false)
	activateCodingTestCLIAccount(t, svc, st, account)
	createCodingTestSession(t, st, cfg, "sess_legacy_resume_log")

	session, err := st.GetCodingSession(t.Context(), "sess_legacy_resume_log")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	session.WorkDir = cfg.CodexHome
	if err := st.UpdateCodingSession(t.Context(), session); err != nil {
		t.Fatalf("UpdateCodingSession: %v", err)
	}

	svc.Codex = provider.NewCodexAppServer(writeFakeCodexAppServerScript(t, `
if [ "${1:-}" = "app-server" ]; then
  while IFS= read -r line; do
    if printf '%s' "$line" | grep -q '"method":"initialize"'; then
      echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"'"${CODEX_HOME}"'","platformFamily":"unix","platformOs":"linux"}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"initialized"'; then
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/resume"'; then
      printf '%s\n' 'unexpected thread/resume during chat-only normalization' >&2
      exit 1
    fi
    if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
      echo '{"id":"2","result":{"thread":{"id":"thread_chat_resume_ignored"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_chat_resume_ignored"}}}'
      continue
    fi
    if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_chat_resume_ignored","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_chat_resume_ignored","turn":{"id":"turn_chat_resume_ignored"}}}'
      echo '{"method":"item/completed","params":{"threadId":"thread_chat_resume_ignored","turnId":"turn_chat_resume_ignored","item":{"id":"item_0","type":"agentMessage","text":"Fresh chat started without resuming legacy threads."}}}'
      echo '{"method":"turn/completed","params":{"threadId":"thread_chat_resume_ignored","turn":{"id":"turn_chat_resume_ignored","status":"completed"}}}'
      exit 0
    fi
  done
fi
exit 1
`))

	var logBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevLogWriter) })

	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_legacy_resume_log",
		"1",
		"",
		"",
		"",
		"",
		"chat",
		nil,
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream: %v", err)
	}

	logText := logBuf.String()
	if strings.Contains(logText, "[legacy-resume]") {
		t.Fatalf("expected chat-only send path to skip legacy resume logging, got %q", logText)
	}
	if len(result.Assistants) != 1 || strings.TrimSpace(result.Assistants[0].Actor) != "" {
		t.Fatalf("expected one chat-only assistant reply, got %#v", result.Assistants)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_legacy_resume_log")
	if err != nil {
		t.Fatalf("GetCodingSession updated: %v", err)
	}
	if updated.CodexThreadID != "thread_chat_resume_ignored" {
		t.Fatalf("expected fresh chat thread to be persisted as canonical thread id, got %q", updated.CodexThreadID)
	}
}

func TestPrepareCodingTurnSetup_PreservesReviewSlashAsRawChatText(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_review_chat_command")

	setup, err := svc.prepareCodingTurnSetup(
		t.Context(),
		"sess_review_chat_command",
		"/review focus auth",
		"",
		"",
		"",
		"",
		"chat",
	)
	if err != nil {
		t.Fatalf("prepareCodingTurnSetup: %v", err)
	}
	if got := strings.TrimSpace(setup.commandMode); got != "chat" {
		t.Fatalf("expected chat command mode, got %q", got)
	}
	if got := strings.TrimSpace(setup.promptInput); got != "/review focus auth" {
		t.Fatalf("expected raw /review prompt input to persist, got %q", got)
	}
	if got := strings.TrimSpace(setup.userVisibleContent); got != "/review focus auth" {
		t.Fatalf("expected raw /review user-visible content to persist, got %q", got)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_review_chat_command")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to remain unchanged, got %q", updated.Title)
	}
}

func TestSendCodingMessageStream_RejectsPlanSlashInChatMode(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_chat_plan_command_stream")

	var assistantEvents []string
	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_chat_plan_command_stream",
		"/plan tighten scope",
		"",
		"",
		"",
		"",
		"chat",
		func(event provider.ChatEvent) error {
			if strings.TrimSpace(event.Type) == "assistant_message" {
				assistantEvents = append(assistantEvents, strings.TrimSpace(event.Text))
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream: %v", err)
	}
	want := "Perintah /plan tidak dikenali di mode chat."
	if got := strings.TrimSpace(result.Assistant.Content); got != want {
		t.Fatalf("unexpected assistant response: %q", got)
	}
	if len(assistantEvents) != 1 || assistantEvents[0] != want {
		t.Fatalf("unexpected assistant events: %#v", assistantEvents)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_chat_plan_command_stream")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to remain unchanged, got %q", updated.Title)
	}
}

func TestSendCodingMessageStream_RejectsPlanAliasInChatMode(t *testing.T) {
	svc, st, _, cfg := newCodingTestService(t)
	createCodingTestSession(t, st, cfg, "sess_chat_plan_alias_stream")

	var assistantEvents []string
	result, err := svc.SendCodingMessageStream(
		t.Context(),
		"sess_chat_plan_alias_stream",
		"/plan tighten scope",
		"",
		"",
		"",
		"",
		"chat",
		func(event provider.ChatEvent) error {
			if strings.TrimSpace(event.Type) == "assistant_message" {
				assistantEvents = append(assistantEvents, strings.TrimSpace(event.Text))
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("SendCodingMessageStream: %v", err)
	}
	want := "Perintah /plan tidak dikenali di mode chat."
	if got := strings.TrimSpace(result.Assistant.Content); got != want {
		t.Fatalf("unexpected assistant response: %q", got)
	}
	if len(assistantEvents) != 1 || assistantEvents[0] != want {
		t.Fatalf("unexpected assistant events: %#v", assistantEvents)
	}
	updated, err := st.GetCodingSession(t.Context(), "sess_chat_plan_alias_stream")
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if updated.Title != "New Session" {
		t.Fatalf("expected title to remain unchanged, got %q", updated.Title)
	}
}

func TestIsRawSlashCommand_AllowsReviewText(t *testing.T) {
	if !isRawSlashCommand("/review focus auth") {
		t.Fatalf("expected /review command to be treated as raw slash text")
	}
	if !isRawSlashCommand("/review") {
		t.Fatalf("expected bare /review command to be treated as raw slash text")
	}
}

func TestSendCodingMessageStream_ModelCapacityRecoveryRetriesOnSameSession(t *testing.T) {
	if !codingRuntimeModelCapacity(errors.New("Selected model is at capacity. Please try a different model.")) {
		t.Fatalf("expected model capacity detection")
	}
	sanitized := sanitizeCodingRuntimeUserFacingError(errors.New("Selected model is at capacity. Please try a different model."))
	if sanitized == nil || !strings.Contains(strings.ToLower(sanitized.Error()), "capacity") {
		t.Fatalf("expected sanitized capacity runtime error, got %v", sanitized)
	}
}

func TestSendCodingMessageStream_ModelCapacityRecoveryStopsAfterRetryBudget(t *testing.T) {
	if !codingRuntimeModelCapacity(errors.New("Selected model is at capacity. Please try a different model.")) {
		t.Fatalf("expected model capacity detection")
	}
	sanitized := sanitizeCodingRuntimeUserFacingError(errors.New("Selected model is at capacity. Please try a different model."))
	if sanitized == nil || !strings.Contains(strings.ToLower(sanitized.Error()), "capacity") {
		t.Fatalf("expected sanitized capacity runtime error, got %v", sanitized)
	}
}

func TestClearCodingRunForceStopPreventsStaleForceKillAcrossActorHandoffs(t *testing.T) {
	svc, _, _, _ := newCodingTestService(t)
	release, err := svc.beginCodingRun("sess_force_stop_clear")
	if err != nil {
		t.Fatalf("beginCodingRun: %v", err)
	}
	defer release()

	_, cancel := context.WithCancel(t.Context())
	svc.setCodingRunCancel("sess_force_stop_clear", cancel)
	oldCalled := false
	svc.setCodingRunForceStop("sess_force_stop_clear", func() error {
		oldCalled = true
		return nil
	})
	svc.clearCodingRunForceStop("sess_force_stop_clear")
	svc.setCodingRunActor("sess_force_stop_clear", "executor")

	stopped := svc.StopCodingRun("sess_force_stop_clear", true)
	if !stopped {
		t.Fatalf("expected stop to succeed")
	}
	if oldCalled {
		t.Fatalf("expected stale forceKill handle to be cleared before actor handoff")
	}
}

func createCodingTestSession(t *testing.T, st *store.Store, cfg config.Config, sessionID string) {
	t.Helper()
	if err := os.MkdirAll(cfg.CodexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	_, err := st.CreateCodingSession(t.Context(), store.CodingSession{
		ID:             sessionID,
		Title:          "New Session",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        cfg.CodexHome,
		SandboxMode:    "workspace-write",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastMessageAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateCodingSession: %v", err)
	}
}

func writeFakeCodexAppServerScript(t *testing.T, body string) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "fake-codex.sh")
	content := "#!/usr/bin/env bash\nset -euo pipefail\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake codex script: %v", err)
	}
	return scriptPath
}
