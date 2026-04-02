package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

const (
	codingRuntimeRoleChat      = "chat"
	codingRuntimeRoleExecutor  = "executor"
	codingRuntimePendingPrefix = "pending_"
)

const (
	codingRuntimePendingTTL = 30 * time.Minute
	codingRuntimeOrphanTTL  = 10 * time.Minute
)

func defaultCodexSessDataRoot() string {
	homePath, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homePath) == "" {
		return filepath.Join(string(filepath.Separator), "tmp", ".codexsess")
	}
	return filepath.Join(homePath, ".codexsess")
}

func isPathWithin(basePath, targetPath string) bool {
	baseAbs, err := filepath.Abs(strings.TrimSpace(basePath))
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(strings.TrimSpace(targetPath))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	rel = filepath.Clean(rel)
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Service) codingDataRoot() string {
	root := strings.TrimSpace(s.Cfg.DataDir)
	fallback := defaultCodexSessDataRoot()
	if root == "" {
		return fallback
	}
	if !filepath.IsAbs(root) {
		return filepath.Join(fallback, root)
	}
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return root
	}
	if isPathWithin(cwd, root) {
		return fallback
	}
	return root
}

func (s *Service) codingRuntimesRoot() string {
	return filepath.Join(s.codingDataRoot(), "runtimes")
}

func (s *Service) codingTemplateHomeRoot() string {
	return filepath.Join(s.codingDataRoot(), "base-codex-home")
}

func (s *Service) codingSessionRuntimeRoot(sessionID string) string {
	return filepath.Join(s.codingRuntimesRoot(), strings.TrimSpace(sessionID))
}

func (s *Service) codingRuntimeHome(sessionID, role string) string {
	return filepath.Join(s.codingSessionRuntimeRoot(sessionID), strings.TrimSpace(role), "codex-home")
}

func (s *Service) CodingRuntimeHomeForTests(sessionID, role string) string {
	return s.codingRuntimeHome(sessionID, role)
}

type CodingRuntimeDebugRole struct {
	RuntimeHome       string `json:"runtime_home"`
	RuntimeHomeExists bool   `json:"runtime_home_exists"`
	StoredThreadID    string `json:"stored_thread_id"`
	ActiveAccountID   string `json:"active_account_id"`
	AuthJSONExists    bool   `json:"auth_json_exists"`
}

type CodingRuntimeDebugSnapshot struct {
	SessionID      string                            `json:"session_id"`
	ThreadID       string                            `json:"thread_id"`
	RestartPending bool                              `json:"restart_pending"`
	InFlight       bool                              `json:"in_flight"`
	RunnerRole     string                            `json:"runner_role"`
	Roles          map[string]CodingRuntimeDebugRole `json:"roles"`
}

func (s *Service) ensureCodingTemplateHome() (string, error) {
	root := s.codingTemplateHomeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	for _, subdir := range []string{"agents", "mcp", "skills"} {
		if err := os.MkdirAll(filepath.Join(root, subdir), 0o700); err != nil {
			return "", err
		}
	}
	if err := s.syncCodingTemplateHomeFromUserCodex(root); err != nil {
		return "", err
	}
	if err := seedBundledCodingTemplateAgents(root); err != nil {
		return "", err
	}
	if err := seedBundledCodingTemplateSkills(root); err != nil {
		return "", err
	}
	if err := ensureCodingTemplateSuperpowers(root); err != nil {
		return "", err
	}
	if err := ensureCodingTemplateConfig(root); err != nil {
		return "", err
	}
	return root, nil
}

func (s *Service) CodingRuntimeDebugSnapshot(ctx context.Context, sessionID string) (CodingRuntimeDebugSnapshot, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return CodingRuntimeDebugSnapshot{}, fmt.Errorf("session_id is required")
	}
	session, err := s.Store.GetCodingSession(ctx, sid)
	if err != nil {
		return CodingRuntimeDebugSnapshot{}, err
	}
	inFlight, _, runnerRole := s.CodingRunStatus(sid)
	threadID := strings.TrimSpace(session.CodexThreadID)
	buildRole := func(role, threadID string) CodingRuntimeDebugRole {
		runtimeHome := s.codingRuntimeHome(sid, role)
		_, homeErr := os.Stat(runtimeHome)
		_, authErr := os.Stat(filepath.Join(runtimeHome, "auth.json"))
		accountID := strings.TrimSpace(s.readCodingRuntimeAccountMarker(sid, role))
		return CodingRuntimeDebugRole{
			RuntimeHome:       runtimeHome,
			RuntimeHomeExists: homeErr == nil,
			StoredThreadID:    strings.TrimSpace(threadID),
			ActiveAccountID:   accountID,
			AuthJSONExists:    authErr == nil,
		}
	}
	return CodingRuntimeDebugSnapshot{
		SessionID:      sid,
		ThreadID:       threadID,
		RestartPending: session.RestartPending,
		InFlight:       inFlight,
		RunnerRole:     normalizeCodingRunnerRole(runnerRole),
		Roles: map[string]CodingRuntimeDebugRole{
			codingRuntimeRoleChat: buildRole(codingRuntimeRoleChat, threadID),
		},
	}, nil
}

func (s *Service) EnsureCodingTemplateHome() (string, error) {
	return s.ensureCodingTemplateHome()
}

func (s *Service) RefreshCodingTemplateHome() (string, error) {
	return s.refreshCodingTemplateHome()
}

func (s *Service) CodingTemplateHomeStatus(ctx context.Context) (CodingTemplateHomeStatus, error) {
	_ = ctx
	return s.codingTemplateHomeStatus()
}

func (s *Service) refreshCodingTemplateHome() (string, error) {
	root := s.codingTemplateHomeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	for _, subdir := range []string{"agents", "mcp", "skills"} {
		if err := os.MkdirAll(filepath.Join(root, subdir), 0o700); err != nil {
			return "", err
		}
	}
	if err := s.syncCodingTemplateHomeFromUserCodex(root); err != nil {
		return "", err
	}
	if err := seedBundledCodingTemplateAgents(root); err != nil {
		return "", err
	}
	if err := seedBundledCodingTemplateSkills(root); err != nil {
		return "", err
	}
	if err := ensureCodingTemplateSuperpowers(root); err != nil {
		return "", err
	}
	if err := ensureCodingTemplateConfig(root); err != nil {
		return "", err
	}
	return root, nil
}

type CodingTemplateHomeStatus struct {
	RootPath              string          `json:"root_path"`
	ConfigPath            string          `json:"config_path"`
	ConfigExists          bool            `json:"config_exists"`
	Ready                 bool            `json:"ready"`
	SeededMCPServers      map[string]bool `json:"seeded_mcp_servers"`
	EnabledMCPServers     []string        `json:"enabled_mcp_servers"`
	DisabledMCPServers    []string        `json:"disabled_mcp_servers"`
	RuntimeHomeCount      int             `json:"runtime_home_count"`
	UpdatedAt             string          `json:"updated_at"`
	MissingBaselineFields []string        `json:"missing_baseline_fields"`
}

func (s *Service) codingTemplateHomeStatus() (CodingTemplateHomeStatus, error) {
	root := s.codingTemplateHomeRoot()
	status := CodingTemplateHomeStatus{
		RootPath:           root,
		ConfigPath:         filepath.Join(root, "config.toml"),
		SeededMCPServers:   map[string]bool{},
		EnabledMCPServers:  []string{},
		DisabledMCPServers: []string{},
	}
	entries, err := os.ReadDir(s.codingRuntimesRoot())
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				status.RuntimeHomeCount++
			}
		}
	}
	raw, readErr := os.ReadFile(status.ConfigPath)
	if readErr == nil {
		status.ConfigExists = true
		configText := string(raw)
		status.SeededMCPServers["playwright"] = strings.Contains(configText, "[mcp_servers.playwright]")
		status.SeededMCPServers["filesystem"] = strings.Contains(configText, "[mcp_servers.filesystem]")
		status.SeededMCPServers["git"] = strings.Contains(configText, "[mcp_servers.git]")
		status.SeededMCPServers["exa"] = strings.Contains(configText, "[mcp_servers.exa]")
		status.SeededMCPServers["reftools"] = strings.Contains(configText, "[mcp_servers.reftools]")
		status.SeededMCPServers["github"] = strings.Contains(configText, "[mcp_servers.github]")
		for name, enabled := range status.SeededMCPServers {
			if enabled {
				status.EnabledMCPServers = append(status.EnabledMCPServers, name)
			} else {
				status.DisabledMCPServers = append(status.DisabledMCPServers, name)
			}
		}
		sort.Strings(status.EnabledMCPServers)
		sort.Strings(status.DisabledMCPServers)
		status.Ready = status.SeededMCPServers["playwright"] && status.SeededMCPServers["filesystem"] && status.SeededMCPServers["git"]
		status.UpdatedAt = codingTemplateConfigUpdatedAt(status.ConfigPath)
	}
	if !status.ConfigExists {
		status.MissingBaselineFields = []string{"approval_policy", "sandbox_mode", "mcp_servers.playwright", "mcp_servers.filesystem", "mcp_servers.git"}
		return status, nil
	}
	if !status.SeededMCPServers["playwright"] {
		status.MissingBaselineFields = append(status.MissingBaselineFields, "mcp_servers.playwright")
	}
	if !status.SeededMCPServers["filesystem"] {
		status.MissingBaselineFields = append(status.MissingBaselineFields, "mcp_servers.filesystem")
	}
	if !status.SeededMCPServers["git"] {
		status.MissingBaselineFields = append(status.MissingBaselineFields, "mcp_servers.git")
	}
	if len(status.MissingBaselineFields) == 0 && strings.TrimSpace(status.UpdatedAt) == "" {
		status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return status, nil
}

func codingTemplateConfigUpdatedAt(path string) string {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().UTC().Format(time.RFC3339)
	}
	return ""
}

func ensureCodingTemplateConfig(root string) error {
	configPath := filepath.Join(strings.TrimSpace(root), "config.toml")
	homePath, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homePath) == "" {
		homePath = "~"
	}
	body := buildDefaultCodingTemplateConfig(homePath)
	existing, readErr := os.ReadFile(configPath)
	switch {
	case readErr == nil:
		body = mergeMissingTemplateConfigSections(string(existing), homePath)
	case !os.IsNotExist(readErr):
		return readErr
	}
	return os.WriteFile(configPath, []byte(body), 0o600)
}

func (s *Service) syncCodingTemplateHomeFromUserCodex(root string) error {
	homePath, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homePath) == "" {
		homePath = "~"
	}
	sourceRoot := filepath.Join(homePath, ".codex")
	if err := ensureBundledTemplateAssetsInUserCodexHome(homePath); err != nil {
		return err
	}
	if info, err := os.Stat(sourceRoot); err != nil || !info.IsDir() {
		return nil
	}
	for _, name := range []string{"agents", "skills", "mcp"} {
		srcPath := filepath.Join(sourceRoot, name)
		if info, err := os.Stat(srcPath); err == nil && info.IsDir() {
			if err := copyMissingDirContents(srcPath, filepath.Join(root, name)); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		srcPath := filepath.Join(sourceRoot, name)
		if info, err := os.Stat(srcPath); err == nil && !info.IsDir() {
			if err := copyFile(srcPath, filepath.Join(root, name)); err != nil {
				return err
			}
		}
	}
	srcConfigPath := filepath.Join(sourceRoot, "config.toml")
	raw, err := os.ReadFile(srcConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	merged := mergeMissingTemplateConfigSections(string(raw), homePath)
	return os.WriteFile(filepath.Join(root, "config.toml"), []byte(merged), 0o600)
}

func ensureBundledTemplateAssetsInUserCodexHome(homePath string) error {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" || homePath == "~" {
		return nil
	}
	userCodexRoot := filepath.Join(homePath, ".codex")
	if err := seedBundledCodingTemplateAgents(userCodexRoot); err != nil {
		return err
	}
	if err := seedBundledCodingTemplateSkills(userCodexRoot); err != nil {
		return err
	}
	return nil
}

func sanitizeCodingRuntimeHome(root string) error {
	runtimeRoot := strings.TrimSpace(root)
	if runtimeRoot == "" {
		return nil
	}
	configPath := filepath.Join(runtimeRoot, "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sanitized := mergeMissingTemplateConfigSections(string(raw), filepath.Dir(runtimeRoot))
	if strings.TrimSpace(sanitized) == strings.TrimSpace(string(raw)) {
		return nil
	}
	return os.WriteFile(configPath, []byte(sanitized), 0o600)
}

func syncCodingRuntimeRoleSkills(runtimeHome, templateHome, role string) error {
	roleSkills := codingRuntimeRoleSkills(role)
	runtimeSkillsRoot := filepath.Join(strings.TrimSpace(runtimeHome), "skills")
	if err := os.MkdirAll(runtimeSkillsRoot, 0o700); err != nil {
		return err
	}

	sourceSkillsRoot, err := resolveCodingRuntimeSuperpowersSkillsRoot(templateHome)
	if err != nil {
		return nil
	}

	allowed := map[string]struct{}{}
	if len(roleSkills) == 0 {
		return nil
	}
	for _, name := range roleSkills {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		allowed[name] = struct{}{}
	}

	entries, err := os.ReadDir(runtimeSkillsRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		if _, keep := allowed[name]; keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(runtimeSkillsRoot, name)); err != nil {
			return err
		}
	}

	for name := range allowed {
		src := filepath.Join(sourceSkillsRoot, name)
		if _, statErr := os.Stat(filepath.Join(src, "SKILL.md")); statErr != nil {
			continue
		}
		if err := linkOrCopySkillDir(src, filepath.Join(runtimeSkillsRoot, name), true); err != nil {
			return err
		}
	}

	return nil
}

func resolveCodingRuntimeSuperpowersSkillsRoot(templateHome string) (string, error) {
	templateRoot := strings.TrimSpace(templateHome)
	repoSkillsRoot, repoErr := resolveTemplateSuperpowersSkillsRoot(filepath.Join(templateRoot, "superpowers"))
	if repoErr == nil {
		return repoSkillsRoot, nil
	}

	fallbackRoot := filepath.Join(templateRoot, "skills")
	if info, err := os.Stat(fallbackRoot); err == nil {
		if info.IsDir() {
			return fallbackRoot, nil
		}
		return "", fmt.Errorf("template skills root is not directory: %s", fallbackRoot)
	}

	return "", repoErr
}

func buildDefaultCodingTemplateConfig(homePath string) string {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		homePath = "~"
	}
	var b strings.Builder
	b.WriteString("approval_policy = \"never\"\n")
	b.WriteString("sandbox_mode = \"danger-full-access\"\n\n")
	b.WriteString("[mcp_servers.playwright]\n")
	b.WriteString("command = \"npx\"\n")
	b.WriteString("args = [\"@playwright/mcp@latest\"]\n\n")
	b.WriteString("[mcp_servers.filesystem]\n")
	b.WriteString("command = \"npx\"\n")
	b.WriteString("args = [\"-y\", \"@modelcontextprotocol/server-filesystem\", ")
	b.WriteString(tomlString(homePath))
	b.WriteString("]\n\n")
	b.WriteString("[mcp_servers.git]\n")
	b.WriteString("command = \"uvx\"\n")
	b.WriteString("args = [\"mcp-server-git\", \"--repository\", ")
	b.WriteString(tomlString(homePath))
	b.WriteString("]\n\n")
	b.WriteString("[mcp_servers.exa]\n")
	b.WriteString("url = \"https://mcp.exa.ai/mcp\"\n")
	b.WriteString("enabled = false\n\n")
	b.WriteString("[mcp_servers.reftools]\n")
	b.WriteString("url = \"https://api.ref.tools/mcp\"\n")
	b.WriteString("enabled = false\n\n")
	b.WriteString("[mcp_servers.github]\n")
	b.WriteString("url = \"https://api.githubcopilot.com/mcp/\"\n")
	b.WriteString("bearer_token_env_var = \"GITHUB_PAT_TOKEN\"\n")
	b.WriteString("enabled = false\n")
	return b.String()
}

func mergeMissingTemplateConfigSections(existing, homePath string) string {
	body := stripTemplateConfigSection(strings.TrimSpace(existing), "[mcp_servers.memory]")
	if body == "" {
		return buildDefaultCodingTemplateConfig(homePath)
	}
	appendLine := func(key, value string) {
		if strings.Contains(body, key+" = ") {
			return
		}
		body += "\n" + key + " = " + value
	}
	appendSection := func(marker, section string) {
		if strings.Contains(body, marker) {
			return
		}
		body += "\n\n" + strings.TrimSpace(section)
	}
	appendLine("approval_policy", tomlString("never"))
	appendLine("sandbox_mode", tomlString("danger-full-access"))
	defaultBody := buildDefaultCodingTemplateConfig(homePath)
	for _, marker := range []string{
		"[mcp_servers.playwright]",
		"[mcp_servers.filesystem]",
		"[mcp_servers.git]",
		"[mcp_servers.exa]",
		"[mcp_servers.reftools]",
		"[mcp_servers.github]",
	} {
		start := strings.Index(defaultBody, marker)
		if start < 0 {
			continue
		}
		end := len(defaultBody)
		for _, nextMarker := range []string{
			"[mcp_servers.playwright]",
			"[mcp_servers.filesystem]",
			"[mcp_servers.git]",
			"[mcp_servers.exa]",
			"[mcp_servers.reftools]",
			"[mcp_servers.github]",
		} {
			if nextMarker == marker {
				continue
			}
			if idx := strings.Index(defaultBody[start+len(marker):], "\n\n"+nextMarker); idx >= 0 {
				candidateEnd := start + len(marker) + idx
				if candidateEnd < end {
					end = candidateEnd
				}
			}
		}
		appendSection(marker, defaultBody[start:end])
	}
	return strings.TrimSpace(body) + "\n"
}

func stripTemplateConfigSection(body, marker string) string {
	text := strings.TrimSpace(body)
	sectionMarker := strings.TrimSpace(marker)
	if text == "" || sectionMarker == "" || !strings.Contains(text, sectionMarker) {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == sectionMarker {
				skipping = true
				continue
			}
			if skipping {
				skipping = false
			}
		}
		if skipping {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func tomlString(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	return `"` + replacer.Replace(strings.TrimSpace(value)) + `"`
}

func (s *Service) ensureCodingRuntimeHome(ctx context.Context, sessionID, role string) (string, store.Account, error) {
	return s.ensureCodingRuntimeHomeExcluding(ctx, sessionID, role, nil)
}

func (s *Service) ensureCodingRuntimeHomeExcluding(ctx context.Context, sessionID, role string, excluded map[string]struct{}) (string, store.Account, error) {
	sid := strings.TrimSpace(sessionID)
	runtimeRole := strings.TrimSpace(role)
	if sid == "" {
		return "", store.Account{}, fmt.Errorf("session id is required")
	}
	if runtimeRole == "" {
		runtimeRole = codingRuntimeRoleChat
	}
	s.cleanupStaleCodingRuntimeHomes(ctx)
	templateHome, err := s.ensureCodingTemplateHome()
	if err != nil {
		return "", store.Account{}, err
	}
	runtimeRoleRoot := filepath.Join(s.codingSessionRuntimeRoot(sid), runtimeRole)
	runtimeHome := s.codingRuntimeHome(sid, runtimeRole)
	roleRootExisted := true
	if _, statErr := os.Stat(runtimeRoleRoot); os.IsNotExist(statErr) {
		roleRootExisted = false
	}
	cleanupRoleRoot := !roleRootExisted
	defer func() {
		if cleanupRoleRoot {
			_ = os.RemoveAll(runtimeRoleRoot)
			sessionRoot := s.codingSessionRuntimeRoot(sid)
			if entries, readErr := os.ReadDir(sessionRoot); readErr == nil && len(entries) == 0 {
				_ = os.Remove(sessionRoot)
			}
		}
	}()
	if err := os.MkdirAll(runtimeHome, 0o700); err != nil {
		return "", store.Account{}, err
	}
	if err := copyMissingDirContents(templateHome, runtimeHome); err != nil {
		return "", store.Account{}, err
	}
	if err := sanitizeCodingRuntimeHome(runtimeHome); err != nil {
		return "", store.Account{}, err
	}
	if err := syncCodingRuntimeRoleSkills(runtimeHome, templateHome, runtimeRole); err != nil {
		return "", store.Account{}, err
	}
	account, err := s.selectCodingRuntimeAccountExcluding(ctx, sid, runtimeRole, excluded)
	if err != nil {
		return "", store.Account{}, err
	}
	if runtimeRole == codingRuntimeRoleChat {
		updated, cliErr := s.UseAccountCLI(WithCLISwitchReason(ctx, "coding-runtime-recovery"), account.ID)
		if cliErr != nil {
			return "", store.Account{}, cliErr
		}
		account = updated
	}
	if err := s.syncCodingRuntimeAuth(ctx, sid, runtimeRole, runtimeHome, account); err != nil {
		return "", store.Account{}, err
	}
	cleanupRoleRoot = false
	return runtimeHome, account, nil
}

func (s *Service) cleanupStaleCodingRuntimeHomes(ctx context.Context) {
	root := strings.TrimSpace(s.codingRuntimesRoot())
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	liveSessions := map[string]struct{}{}
	sessions, err := s.Store.ListCodingSessions(ctx)
	if err == nil {
		for _, session := range sessions {
			sid := strings.TrimSpace(session.ID)
			if sid != "" {
				liveSessions[sid] = struct{}{}
			}
		}
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		fullPath := filepath.Join(root, name)
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			continue
		}
		age := now.Sub(info.ModTime().UTC())
		if strings.HasPrefix(name, codingRuntimePendingPrefix) {
			if age >= codingRuntimePendingTTL {
				provider.CloseAppServerClientsUnder(fullPath)
				_ = os.RemoveAll(fullPath)
			}
			continue
		}
		if _, exists := liveSessions[name]; exists {
			continue
		}
		if age < codingRuntimeOrphanTTL {
			continue
		}
		provider.CloseAppServerClientsUnder(fullPath)
		_ = os.RemoveAll(fullPath)
	}
}

func (s *Service) selectCodingRuntimeAccount(ctx context.Context, sessionID, role string) (store.Account, error) {
	return s.selectCodingRuntimeAccountExcluding(ctx, sessionID, role, nil)
}

func (s *Service) selectCodingRuntimeAccountExcluding(ctx context.Context, sessionID, role string, excluded map[string]struct{}) (store.Account, error) {
	if strings.TrimSpace(role) == codingRuntimeRoleChat {
		account, err := s.Store.ActiveCLIAccount(ctx)
		if err == nil && strings.TrimSpace(account.ID) != "" {
			if excluded == nil {
				return account, nil
			}
			if _, blocked := excluded[strings.TrimSpace(account.ID)]; !blocked {
				return account, nil
			}
		}
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		return store.Account{}, err
	}
	best := selectBestCodingRuntimeAccount(accounts, excluded)
	if strings.TrimSpace(best.ID) == "" {
		return store.Account{}, fmt.Errorf("no healthy codex account available for runtime")
	}
	return best, nil
}

func selectBestCodingRuntimeAccount(accounts []store.Account, excluded map[string]struct{}) store.Account {
	best := store.Account{}
	for _, account := range accounts {
		if strings.TrimSpace(account.ID) == "" || account.Revoked {
			continue
		}
		if _, blocked := excluded[strings.TrimSpace(account.ID)]; blocked {
			continue
		}
		if codingRuntimeAccountSelectionBlocked(account.UsageLastError) {
			continue
		}
		if !account.UsageFetchedAt.IsZero() && (account.UsageWeeklyPct <= 0 || account.UsageHourlyPct <= 0) {
			continue
		}
		if strings.TrimSpace(best.ID) == "" || compareCodingRuntimeAccounts(account, best) > 0 {
			best = account
		}
	}
	return best
}

func codingRuntimeAccountSelectionBlocked(lastError string) bool {
	errText := strings.TrimSpace(lastError)
	if errText == "" {
		return false
	}
	err := fmt.Errorf("%s", errText)
	return usageErrorLooksRevoked(errText) || codingRuntimeAccountDeactivated(err) || codingRuntimeUsageExhausted(err)
}

func (s *Service) markCodingRuntimeAccountUsageLimited(ctx context.Context, accountID string, cause error) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return
	}
	snapshot, err := s.Store.GetUsage(ctx, id)
	if err != nil {
		snapshot = store.UsageSnapshot{
			AccountID: id,
			RawJSON:   "{}",
		}
	}
	snapshot.AccountID = id
	snapshot.FetchedAt = time.Now().UTC()
	causeText := ""
	if cause != nil {
		causeText = cause.Error()
	}
	snapshot.LastError = strings.TrimSpace(firstNonEmpty(causeText, "runtime rate limited or quota exhausted (429)"))
	_ = s.Store.SaveUsage(ctx, snapshot)
}

func (s *Service) syncCodingRuntimeAuth(ctx context.Context, sessionID, role, runtimeHome string, account store.Account) error {
	_ = ctx
	if err := s.syncAccountAuthToPath(account, runtimeHome); err != nil {
		return err
	}
	accountMarkerPath := s.codingRuntimeAccountMarkerPath(sessionID, role)
	if err := os.MkdirAll(filepath.Dir(accountMarkerPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(accountMarkerPath, []byte(strings.TrimSpace(account.ID)+"\n"), 0o600); err != nil {
		return err
	}
	return nil
}

func (s *Service) currentCodingRuntimeAccount(sessionID, role string) string {
	return strings.TrimSpace(s.readCodingRuntimeAccountMarker(sessionID, role))
}

func (s *Service) switchCodingRuntimeAccount(ctx context.Context, sessionID, role string, excluded map[string]struct{}, markRevoked bool) (string, store.Account, error) {
	sid := strings.TrimSpace(sessionID)
	runtimeRole := strings.TrimSpace(role)
	if excluded == nil {
		excluded = map[string]struct{}{}
	}
	if currentID := s.currentCodingRuntimeAccount(sid, runtimeRole); currentID != "" {
		excluded[currentID] = struct{}{}
		if markRevoked {
			_ = s.Store.SetAccountRevoked(ctx, currentID, true)
		}
	}
	runtimeHome := s.codingRuntimeHome(sid, runtimeRole)
	if err := os.MkdirAll(runtimeHome, 0o700); err != nil {
		return "", store.Account{}, err
	}
	provider.CloseAppServerClientsUnder(runtimeHome)
	account, err := s.selectCodingRuntimeAccountExcluding(ctx, sid, runtimeRole, excluded)
	if err != nil {
		return "", store.Account{}, err
	}
	if err := s.syncCodingRuntimeAuth(ctx, sid, runtimeRole, runtimeHome, account); err != nil {
		return "", store.Account{}, err
	}
	return runtimeHome, account, nil
}

func codingRuntimeFailureRetryable(err error) bool {
	return codingRuntimeAccountDeactivated(err) || codingRuntimeUsageExhausted(err)
}

func codingRuntimeFailureRecoveryCode(err error) string {
	switch {
	case codingRuntimeAccountDeactivated(err):
		return "auth_failure"
	case codingRuntimeUsageExhausted(err):
		return "usage_limit"
	default:
		return "runtime_failure"
	}
}

func codingRuntimeRecoveryStepText(step, role string, fields ...string) string {
	base := strings.TrimSpace(step)
	if base == "" {
		return ""
	}
	role = normalizeCodingRunnerRole(role)
	if role != "" {
		base += " role=" + role
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		base += " " + field
	}
	return strings.TrimSpace(base)
}

func codingRuntimeRecoveryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(attempt) * 150 * time.Millisecond
}

func compareCodingRuntimeAccounts(a, b store.Account) int {
	aKnown := !a.UsageFetchedAt.IsZero()
	bKnown := !b.UsageFetchedAt.IsZero()
	if aKnown != bKnown {
		if aKnown {
			return 1
		}
		return -1
	}
	if a.UsageWeeklyPct != b.UsageWeeklyPct {
		if a.UsageWeeklyPct > b.UsageWeeklyPct {
			return 1
		}
		return -1
	}
	if a.UsageHourlyPct != b.UsageHourlyPct {
		if a.UsageHourlyPct > b.UsageHourlyPct {
			return 1
		}
		return -1
	}
	if !a.UsageFetchedAt.Equal(b.UsageFetchedAt) {
		if a.UsageFetchedAt.After(b.UsageFetchedAt) {
			return 1
		}
		return -1
	}
	aKey := codingRuntimeAccountSortKey(a)
	bKey := codingRuntimeAccountSortKey(b)
	switch {
	case aKey < bKey:
		return 1
	case aKey > bKey:
		return -1
	default:
		return 0
	}
}

func codingRuntimeAccountSortKey(account store.Account) string {
	return strings.ToLower(firstNonEmpty(strings.TrimSpace(account.ID), strings.TrimSpace(account.Email), strings.TrimSpace(account.Alias)))
}

func (s *Service) codingRuntimeAccountMarkerPath(sessionID, role string) string {
	return filepath.Join(s.codingSessionRuntimeRoot(sessionID), strings.TrimSpace(role), "state", "active-account-id")
}

func (s *Service) readCodingRuntimeAccountMarker(sessionID, role string) string {
	raw, err := os.ReadFile(s.codingRuntimeAccountMarkerPath(sessionID, role))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func copyMissingDirContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if info, lerr := os.Lstat(srcPath); lerr == nil && (info.Mode()&os.ModeSymlink) != 0 {
			if targetInfo, serr := os.Stat(srcPath); serr == nil && targetInfo.IsDir() {
				if _, err := os.Stat(dstPath); err != nil {
					if err := os.MkdirAll(dstPath, 0o700); err != nil {
						return err
					}
				}
				if err := copyMissingDirContents(srcPath, dstPath); err != nil {
					return err
				}
				continue
			}
		}
		if _, err := os.Stat(dstPath); err == nil {
			if entry.IsDir() {
				if err := copyMissingDirContents(srcPath, dstPath); err != nil {
					return err
				}
			}
			continue
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o700); err != nil {
				return err
			}
			if err := copyMissingDirContents(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
