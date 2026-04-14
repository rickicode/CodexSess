package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func configureSuperpowersFixtureEnv(t *testing.T) {
	t.Helper()
	fixtureRoot := t.TempDir()
	skillsRoot := filepath.Join(fixtureRoot, "skills")
	for _, name := range []string{
		"using-superpowers",
		"brainstorming",
		"writing-plans",
		"executing-plans",
		"subagent-driven-development",
		"systematic-debugging",
		"verification-before-completion",
		"using-git-worktrees",
	} {
		skillRoot := filepath.Join(skillsRoot, name)
		if err := os.MkdirAll(skillRoot, 0o700); err != nil {
			t.Fatalf("mkdir superpowers fixture skill %s: %v", name, err)
		}
		body := "---\nname: " + name + "\n---\n\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("write superpowers fixture skill %s: %v", name, err)
		}
	}
	t.Setenv("CODEXSESS_SUPERPOWERS_REPO_PATH", fixtureRoot)
	t.Setenv("CODEXSESS_SUPERPOWERS_REPO_URL", "https://example.invalid/superpowers.git")
}

func TestHandleOpenAIRoot_RejectsInvalidPayload(t *testing.T) {
	s := &Server{apiKey: "sk-test"}

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader("{"))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()

		s.handleOpenAIRoot(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "invalid JSON body") {
			t.Fatalf("expected invalid JSON message, got %s", body)
		}
	})

	t.Run("unsupported payload shape", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(`{"foo":"bar"}`))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()

		s.handleOpenAIRoot(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "unsupported /v1 payload") {
			t.Fatalf("expected unsupported payload message, got %s", body)
		}
	})
}

func TestHandleModels_Unauthorized(t *testing.T) {
	s := &Server{apiKey: "sk-test"}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	s.handleModels(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleClaudeMessages_Unauthorized(t *testing.T) {
	s := &Server{apiKey: "sk-test"}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.2-codex"}`))
	rec := httptest.NewRecorder()

	s.handleClaudeMessages(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleAPIAuthJSON_Unauthorized(t *testing.T) {
	s := &Server{apiKey: "sk-test"}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth.json", nil)
	rec := httptest.NewRecorder()

	s.handleAPIAuthJSON(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleAPIUsageStatus_Unauthorized(t *testing.T) {
	s := &Server{apiKey: "sk-test"}
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	rec := httptest.NewRecorder()

	s.handleAPIUsageStatus(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleWebSettings_ClaudeEndpointUsesClaudeMessagesPath(t *testing.T) {
	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings:         map[string]string{"default": "gpt-5.2-codex"},
				DirectAPIInjectPrompt: true,
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Host = "127.0.0.1:3052"
	rec := httptest.NewRecorder()

	s.handleWebSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claudeEndpoint, _ := body["claude_endpoint"].(string)
	if !strings.HasSuffix(claudeEndpoint, "/v1/messages") {
		t.Fatalf("expected claude endpoint to end with /v1/messages, got %q", claudeEndpoint)
	}
	usageStatusEndpoint, _ := body["usage_status_endpoint"].(string)
	if !strings.HasSuffix(usageStatusEndpoint, "/v1/usage") {
		t.Fatalf("expected usage status endpoint to end with /v1/usage, got %q", usageStatusEndpoint)
	}
	if inject, ok := body["direct_api_inject_prompt"].(bool); !ok || !inject {
		t.Fatalf("expected direct_api_inject_prompt=true, got %v", body["direct_api_inject_prompt"])
	}
}

func TestHandleWebSettings_UpdateDirectAPIInjectPrompt(t *testing.T) {
	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Cfg: config.Config{
				DirectAPIInjectPrompt: true,
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"direct_api_inject_prompt":false}`))
	rec := httptest.NewRecorder()
	s.handleWebSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if inject, ok := body["direct_api_inject_prompt"].(bool); !ok || !inject {
		t.Fatalf("expected direct_api_inject_prompt=true, got %v", body["direct_api_inject_prompt"])
	}
	if !s.svc.Cfg.DirectAPIInjectPrompt {
		t.Fatalf("expected server config direct_api_inject_prompt=true")
	}
}

func TestHandleWebSettings_DoesNotExposeLegacyZoModeSettings(t *testing.T) {
	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Cfg: config.Config{
				DirectAPIInjectPrompt: true,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Host = "127.0.0.1:3052"
	rec := httptest.NewRecorder()
	s.handleWebSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, key := range []string{
		"legacy_provider",
		"legacy_zo_key_id",
		"legacy_model",
		"legacy_persona_id",
		"legacy_planning_mode",
	} {
		if _, ok := body[key]; ok {
			t.Fatalf("expected legacy Zo legacy setting %q to be absent", key)
		}
	}
}

func TestHandleWebSettings_IgnoresLegacyZoModeSettings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings-legacy-mode.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Store: st,
			Cfg: config.Config{
				DirectAPIInjectPrompt: true,
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"legacy_zo_key_id":"legacy","legacy_model":"legacy"}`))
	rec := httptest.NewRecorder()
	s.handleWebSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleWebCodingTemplateHome_GetInitializeResync(t *testing.T) {
	configureSuperpowersFixtureEnv(t)
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "runtimes", "session-a"), 0o700); err != nil {
		t.Fatalf("seed runtime dir: %v", err)
	}

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Cfg: config.Config{
				DataDir: dataDir,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/coding/template-home", nil)
	rec := httptest.NewRecorder()
	s.handleWebCodingTemplateHome(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	status, ok := body["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected status object, got %T", body["status"])
	}
	if ready, _ := status["ready"].(bool); ready {
		t.Fatalf("expected template home to be unready before initialization")
	}
	if exists, _ := status["config_exists"].(bool); exists {
		t.Fatalf("expected config to be absent before initialization")
	}
	if count, _ := status["runtime_home_count"].(float64); count != 1 {
		t.Fatalf("expected runtime_home_count=1, got %v", status["runtime_home_count"])
	}

	req = httptest.NewRequest(http.MethodPost, "/api/coding/template-home", strings.NewReader(`{"action":"initialize"}`))
	rec = httptest.NewRecorder()
	s.handleWebCodingTemplateHome(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on initialize, got %d body=%s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(dataDir, "base-codex-home", "config.toml")); err != nil {
		t.Fatalf("expected seeded config.toml after initialize: %v", err)
	}
	for _, skill := range []string{"brainstorming", "writing-plans"} {
		if _, err := os.Stat(filepath.Join(dataDir, "base-codex-home", "skills", skill, "SKILL.md")); err != nil {
			t.Fatalf("expected required superpowers skill %q after initialize: %v", skill, err)
		}
	}

	body = map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	status, ok = body["status"].(map[string]any)
	if !ok {
		t.Fatalf("expected initialize status object, got %T", body["status"])
	}
	if ready, _ := status["ready"].(bool); !ready {
		t.Fatalf("expected template home ready after initialize")
	}
	if exists, _ := status["config_exists"].(bool); !exists {
		t.Fatalf("expected config_exists after initialize")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/coding/template-home", strings.NewReader(`{"action":"resync"}`))
	rec = httptest.NewRecorder()
	s.handleWebCodingTemplateHome(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on resync, got %d body=%s", rec.Code, rec.Body.String())
	}

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/coding/template-home", strings.NewReader("{"))
		rec := httptest.NewRecorder()
		s.handleWebCodingTemplateHome(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("invalid action", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/coding/template-home", strings.NewReader(`{"action":"other"}`))
		rec := httptest.NewRecorder()
		s.handleWebCodingTemplateHome(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestBootstrapCodingTemplateHome_SeedsTemplateHomeAtServerStartup(t *testing.T) {
	configureSuperpowersFixtureEnv(t)
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "server-bootstrap.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AuthStoreDir = filepath.Join(root, "auth")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	s.bootstrapCodingTemplateHome()

	templateRoot := filepath.Join(svc.Cfg.DataDir, "base-codex-home")
	if _, err := os.Stat(filepath.Join(templateRoot, "config.toml")); err != nil {
		t.Fatalf("expected startup bootstrap to seed template config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(templateRoot, "agents")); err != nil {
		t.Fatalf("expected startup bootstrap to seed template agents dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(templateRoot, "superpowers")); err != nil {
		t.Fatalf("expected startup bootstrap to install superpowers repo: %v", err)
	}
	for _, skill := range []string{"brainstorming", "writing-plans"} {
		if _, err := os.Stat(filepath.Join(templateRoot, "skills", skill, "SKILL.md")); err != nil {
			t.Fatalf("expected startup bootstrap to seed required superpowers skill %q: %v", skill, err)
		}
	}
}

func TestHandleWebCodingSkills_UsesTemplateSuperpowersOnly(t *testing.T) {
	configureSuperpowersFixtureEnv(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, ".codex", "skills", "local-only-skill"), 0o700); err != nil {
		t.Fatalf("seed local-only skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".codex", "skills", "local-only-skill", "SKILL.md"), []byte("# local only\n"), 0o600); err != nil {
		t.Fatalf("write local-only skill file: %v", err)
	}

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3052",
		svc: &service.Service{
			Cfg: config.Config{
				DataDir: t.TempDir(),
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/coding/skills", nil)
	rec := httptest.NewRecorder()
	s.handleWebCodingSkills(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := body["skills"].([]any)
	skills := make([]string, 0, len(items))
	for _, item := range items {
		skills = append(skills, strings.TrimSpace(item.(string)))
	}
	if len(skills) == 0 {
		t.Fatalf("expected skills from superpowers template, got none")
	}
	for _, required := range []string{"brainstorming", "writing-plans", "using-superpowers"} {
		found := false
		for _, skill := range skills {
			if skill == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected required superpowers skill %q in %#v", required, skills)
		}
	}
	for _, skill := range skills {
		if skill == "local-only-skill" {
			t.Fatalf("expected local home skill to be ignored, got %#v", skills)
		}
	}
}

func TestHandleWebCodingSessions_CreatesChatOnlySession(t *testing.T) {
	configureSuperpowersFixtureEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "coding-sessions.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AuthStoreDir = filepath.Join(root, "auth")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)
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
      echo '{"id":"2","result":{"thread":{"id":"thread_http_legacy_mode_create"}}}'
      echo '{"method":"thread/started","params":{"thread":{"id":"thread_http_legacy_mode_create"}}}'
      exit 0
    fi
  done
fi
exit 1
`)
	s := &Server{svc: svc}

	tokenID, err := cry.Encrypt([]byte("id-token-legacy-mode"))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	tokenAccess, err := cry.Encrypt([]byte("access-token-legacy-mode"))
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	tokenRefresh, err := cry.Encrypt([]byte("refresh-token-legacy-mode"))
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	now := time.Now().UTC()
	if err := st.UpsertAccount(ctx, store.Account{
		ID:           "acc-coding-legacy-mode",
		Email:        "coding-legacy-mode@example.com",
		AccountID:    "acct-coding-legacy-mode",
		TokenID:      tokenID,
		TokenAccess:  tokenAccess,
		TokenRefresh: tokenRefresh,
		CodexHome:    cfg.CodexHome,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
	}); err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, "acc-coding-legacy-mode"), "id-token-legacy-mode", "access-token-legacy-mode", "refresh-token-legacy-mode", "acct-coding-legacy-mode"); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	if err := st.SaveUsage(ctx, store.UsageSnapshot{
		AccountID: "acc-coding-legacy-mode",
		HourlyPct: 90,
		WeeklyPct: 90,
		RawJSON:   "{}",
		FetchedAt: now,
	}); err != nil {
		t.Fatalf("save usage: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/coding/sessions", strings.NewReader(`{
		"title":"Fresh Session",
		"model":"gpt-5.2-codex",
		"reasoning_level":"medium",
		"work_dir":"~/",
		"sandbox_mode":"workspace-write"
	}`))
	rec := httptest.NewRecorder()

	s.handleWebCodingSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("expected ok response")
	}
	sessionPayload, _ := body["session"].(map[string]any)
	if strings.TrimSpace(stringFromAny(sessionPayload["id"])) == "" {
		t.Fatalf("expected created session id")
	}
	expectedKeys := map[string]struct{}{
		"id":                     {},
		"thread_id":              {},
		"title":                  {},
		"model":                  {},
		"reasoning_level":        {},
		"work_dir":               {},
		"sandbox_mode":           {},
		"last_applied_event_seq": {},
		"created_at":             {},
		"updated_at":             {},
		"last_message_at":        {},
	}
	for key := range sessionPayload {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected compact create response, found unexpected key %q", key)
		}
	}

	stored, err := st.GetCodingSession(ctx, stringFromAny(sessionPayload["id"]))
	if err != nil {
		t.Fatalf("GetCodingSession: %v", err)
	}
	if strings.TrimSpace(stored.CodexThreadID) == "" {
		t.Fatalf("expected stored session to keep a canonical chat thread id")
	}
}

func TestHandleWebCodingSessions_ListUsesSingleThreadID(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coding-session-list-contract.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	if _, err := st.CreateCodingSession(ctx, store.CodingSession{
		ID:             "sess_http_list_contract",
		Title:          "HTTP List Contract",
		Model:          "gpt-5.2-codex",
		ReasoningLevel: "medium",
		WorkDir:        "~/",
		SandboxMode:    "workspace-write",
		CodexThreadID:  "thread_chat_only",
		CreatedAt:      now,
		UpdatedAt:      now,
		LastMessageAt:  now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{svc: &service.Service{Store: st}}
	req := httptest.NewRequest(http.MethodGet, "/api/coding/sessions", nil)
	rec := httptest.NewRecorder()

	s.handleWebCodingSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := body["sessions"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one session, got %d", len(items))
	}
	sessionPayload, _ := items[0].(map[string]any)
	expectedKeys := map[string]struct{}{
		"id":                     {},
		"thread_id":              {},
		"title":                  {},
		"model":                  {},
		"reasoning_level":        {},
		"work_dir":               {},
		"sandbox_mode":           {},
		"last_applied_event_seq": {},
		"created_at":             {},
		"updated_at":             {},
		"last_message_at":        {},
	}
	for key := range sessionPayload {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected compact list response, found unexpected key %q", key)
		}
	}
	if got := stringFromAny(sessionPayload["thread_id"]); got != "thread_chat_only" {
		t.Fatalf("expected list response to keep chat thread as thread_id, got %q", got)
	}
}

func TestHandleWebSystemLogs_ListExportAndClear(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "system-logs.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	s := &Server{
		svc: &service.Service{
			Store: st,
		},
	}

	now := time.Now().UTC()
	if err := st.AddSystemLog(ctx, store.SystemLogEntry{
		ID:        "log-1",
		Kind:      "switch",
		Message:   "first",
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("add log 1: %v", err)
	}
	if err := st.AddSystemLog(ctx, store.SystemLogEntry{
		ID:        "log-2",
		Kind:      "usage",
		Message:   "second",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("add log 2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/logs?limit=1", nil)
	rec := httptest.NewRecorder()
	s.handleWebSystemLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	logs, _ := body["logs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log item, got %d", len(logs))
	}
	if total, _ := body["total"].(float64); int(total) != 2 {
		t.Fatalf("expected total=2, got %v", body["total"])
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/system/logs", nil)
	rec = httptest.NewRecorder()
	s.handleWebSystemLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 delete, got %d body=%s", rec.Code, rec.Body.String())
	}
	count, err := st.CountSystemLogs(ctx)
	if err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 logs after delete, got %d", count)
	}
}

func TestHandleWebAccounts_ReturnsGlobalActiveIDsWhenActiveRowsAreOutsidePage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "accounts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	seed := func(id string, updatedAt time.Time) {
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
		account := store.Account{
			ID:           id,
			Email:        id + "@example.com",
			AccountID:    "acct-" + id,
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    updatedAt,
			UpdatedAt:    updatedAt,
			LastUsedAt:   updatedAt,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
			t.Fatalf("write auth.json for %s: %v", id, err)
		}
		if err := st.SaveUsage(ctx, store.UsageSnapshot{
			AccountID: id,
			HourlyPct: 90,
			WeeklyPct: 90,
			RawJSON:   "{}",
			FetchedAt: updatedAt,
		}); err != nil {
			t.Fatalf("save usage for %s: %v", id, err)
		}
	}

	now := time.Now().UTC()
	seed("acc-active", now.Add(-time.Minute))
	seed("acc-other", now)

	if err := st.SetActiveAccount(ctx, "acc-active"); err != nil {
		t.Fatalf("set active api: %v", err)
	}
	if _, err := svc.UseAccountCLI(ctx, "acc-active"); err != nil {
		t.Fatalf("set active cli: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts?page=1&limit=1", nil)
	rec := httptest.NewRecorder()
	s.handleWebAccounts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := body["active_api_account_id"].(string); got != "acc-active" {
		t.Fatalf("expected active_api_account_id=acc-active, got %q", got)
	}
	if got, _ := body["active_api_account_email"].(string); got != "acc-active@example.com" {
		t.Fatalf("expected active_api_account_email=acc-active@example.com, got %q", got)
	}
	if got, _ := body["active_cli_account_id"].(string); got != "acc-active" {
		t.Fatalf("expected active_cli_account_id=acc-active, got %q", got)
	}
	if got, _ := body["active_cli_account_email"].(string); got != "acc-active@example.com" {
		t.Fatalf("expected active_cli_account_email=acc-active@example.com, got %q", got)
	}
	if got, _ := body["invalid_accounts_total"].(float64); int(got) != 0 {
		t.Fatalf("expected invalid_accounts_total=0, got %v", got)
	}
	if got, _ := body["revoked_accounts_total"].(float64); int(got) != 0 {
		t.Fatalf("expected revoked_accounts_total=0, got %v", got)
	}
	if got, _ := body["active_api_invalid"].(bool); got {
		t.Fatalf("expected active_api_invalid=false, got true")
	}
	if got, _ := body["active_cli_invalid"].(bool); got {
		t.Fatalf("expected active_cli_invalid=false, got true")
	}
	accounts, _ := body["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("expected 1 paginated account, got %d", len(accounts))
	}
	row, _ := accounts[0].(map[string]any)
	if got, _ := row["id"].(string); got != "acc-other" {
		t.Fatalf("expected current page row acc-other, got %q", got)
	}
	if activeCLI, _ := row["active_cli"].(bool); activeCLI {
		t.Fatalf("expected paginated row acc-other not to be marked active_cli")
	}
}

func TestHandleWebExportAccountTokens_ReturnsJSONDownload(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "accounts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	tokenID, err := cry.Encrypt([]byte("id-token-export"))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	tokenAccess, err := cry.Encrypt([]byte("access-token-export"))
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	tokenRefresh, err := cry.Encrypt([]byte("refresh-token-export"))
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	if err := st.UpsertAccount(ctx, store.Account{
		ID:           "acc-export",
		Email:        "user@example.com",
		TokenID:      tokenID,
		TokenAccess:  tokenAccess,
		TokenRefresh: tokenRefresh,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		LastUsedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert account: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts/export-tokens", nil)
	rec := httptest.NewRecorder()
	s.handleWebExportAccountTokens(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("expected json content type, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment;") {
		t.Fatalf("expected attachment content disposition, got %q", got)
	}

	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 export entry, got %d", len(body))
	}
	row := body[0]
	if got, _ := row["email"].(string); got != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %q", got)
	}
	if got, _ := row["access_token"].(string); got != "access-token-export" {
		t.Fatalf("expected access token, got %q", got)
	}
	if got, _ := row["refresh_token"].(string); got != "refresh-token-export" {
		t.Fatalf("expected refresh token, got %q", got)
	}
	if got, _ := row["id_token"].(string); got != "id-token-export" {
		t.Fatalf("expected id token, got %q", got)
	}
	if _, exists := row["account_id"]; exists {
		t.Fatalf("did not expect account_id field in export payload")
	}
}

func TestHandleWebAccounts_InvalidAccountsTotalIncludesUsageErrorPattern(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "accounts-invalid.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	now := time.Now().UTC()
	tokenID, _ := cry.Encrypt([]byte("id-token-acc-ok"))
	tokenAccess, _ := cry.Encrypt([]byte("access-token-acc-ok"))
	tokenRefresh, _ := cry.Encrypt([]byte("refresh-token-acc-ok"))
	if err := st.UpsertAccount(ctx, store.Account{
		ID:           "acc-ok",
		Email:        "ok@example.com",
		AccountID:    "acct-ok",
		TokenID:      tokenID,
		TokenAccess:  tokenAccess,
		TokenRefresh: tokenRefresh,
		CodexHome:    cfg.CodexHome,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
	}); err != nil {
		t.Fatalf("upsert acc-ok: %v", err)
	}
	if err := st.SaveUsage(ctx, store.UsageSnapshot{
		AccountID: "acc-ok",
		HourlyPct: 90,
		WeeklyPct: 90,
		RawJSON:   "{}",
		FetchedAt: now,
	}); err != nil {
		t.Fatalf("save usage acc-ok: %v", err)
	}

	tokenID2, _ := cry.Encrypt([]byte("id-token-acc-bad"))
	tokenAccess2, _ := cry.Encrypt([]byte("access-token-acc-bad"))
	tokenRefresh2, _ := cry.Encrypt([]byte("refresh-token-acc-bad"))
	if err := st.UpsertAccount(ctx, store.Account{
		ID:           "acc-bad",
		Email:        "bad@example.com",
		AccountID:    "acct-bad",
		TokenID:      tokenID2,
		TokenAccess:  tokenAccess2,
		TokenRefresh: tokenRefresh2,
		CodexHome:    cfg.CodexHome,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
	}); err != nil {
		t.Fatalf("upsert acc-bad: %v", err)
	}
	if err := st.SaveUsage(ctx, store.UsageSnapshot{
		AccountID: "acc-bad",
		HourlyPct: 0,
		WeeklyPct: 0,
		RawJSON:   "{}",
		FetchedAt: now,
		LastError: `usage API error: 401 {"error":{"code":"token_revoked","message":"Encountered invalidated oauth token for user"}}`,
	}); err != nil {
		t.Fatalf("save usage acc-bad: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/accounts?page=1&limit=1", nil)
	rec := httptest.NewRecorder()
	s.handleWebAccounts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := body["invalid_accounts_total"].(float64); int(got) != 1 {
		t.Fatalf("expected invalid_accounts_total=1, got %v", got)
	}
	if got, _ := body["revoked_accounts_total"].(float64); int(got) != 0 {
		t.Fatalf("expected revoked_accounts_total=0, got %v", got)
	}
}

func TestHandleWebAccountTypes_ReturnsGlobalDedupedTypes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "accounts-types.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	seed := func(id, planType string) {
		now := time.Now().UTC()
		tokenID, _ := cry.Encrypt([]byte("id-token-" + id))
		tokenAccess, _ := cry.Encrypt([]byte("access-token-" + id))
		tokenRefresh, _ := cry.Encrypt([]byte("refresh-token-" + id))
		if err := st.UpsertAccount(ctx, store.Account{
			ID:           id,
			Email:        id + "@example.com",
			PlanType:     planType,
			AccountID:    "acct-" + id,
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastUsedAt:   now,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	seed("acc-free", "Free")
	seed("acc-pro-a", "pro")
	seed("acc-pro-b", "PRO")

	req := httptest.NewRequest(http.MethodGet, "/api/accounts/types", nil)
	rec := httptest.NewRecorder()
	s.handleWebAccountTypes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		AccountTypes []string       `json:"account_types"`
		Counts       map[string]int `json:"account_type_counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.AccountTypes) != 2 {
		t.Fatalf("expected 2 account types, got %d (%v)", len(body.AccountTypes), body.AccountTypes)
	}
	if !(containsString(body.AccountTypes, "free") && containsString(body.AccountTypes, "pro")) {
		t.Fatalf("expected account types to include free/pro, got %v", body.AccountTypes)
	}
	if body.Counts["free"] != 1 {
		t.Fatalf("expected free count=1, got %d", body.Counts["free"])
	}
	if body.Counts["pro"] != 2 {
		t.Fatalf("expected pro count=2, got %d", body.Counts["pro"])
	}
}

func containsString(list []string, target string) bool {
	needle := strings.TrimSpace(strings.ToLower(target))
	for _, item := range list {
		if strings.TrimSpace(strings.ToLower(item)) == needle {
			return true
		}
	}
	return false
}

func TestHandleWebClaudeCodeSettings_EnableWritesEnvAndModelPreset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/claude-code", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()

	s.handleWebClaudeCodeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	cc, _ := body["claude_code"].(map[string]any)
	if cc == nil {
		t.Fatalf("expected claude_code object in response")
	}
	if connected, _ := cc["connected"].(bool); !connected {
		t.Fatalf("expected connected=true")
	}

	envPath := filepath.Join(home, ".codexsess", "claude-code.env")
	envContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	envText := string(envContent)
	if !strings.Contains(envText, `ANTHROPIC_BASE_URL="http://127.0.0.1:3061"`) {
		t.Fatalf("expected env file to include base url, got: %s", envText)
	}
	if !strings.Contains(envText, `ANTHROPIC_AUTH_TOKEN="sk-test"`) {
		t.Fatalf("expected env file to include auth token, got: %s", envText)
	}

	bashrcPath := filepath.Join(home, ".bashrc")
	bashrc, err := os.ReadFile(bashrcPath)
	if err != nil {
		t.Fatalf("read .bashrc: %v", err)
	}
	bashrcText := string(bashrc)
	if !strings.Contains(bashrcText, "claude-code.env") {
		t.Fatalf("expected .bashrc to source claude-code.env, got: %s", bashrcText)
	}
	if strings.Contains(bashrcText, "export ANTHROPIC_BASE_URL=") || strings.Contains(bashrcText, "export ANTHROPIC_API_KEY=") || strings.Contains(bashrcText, "export ANTHROPIC_AUTH_TOKEN=") {
		t.Fatalf("expected .bashrc to avoid hardcoded ANTHROPIC exports and only source env file, got: %s", bashrcText)
	}

	profilePath := filepath.Join(home, ".profile")
	profileTextRaw, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read .profile: %v", err)
	}
	profileText := string(profileTextRaw)
	if !strings.Contains(profileText, "CodexSess Claude Code") {
		t.Fatalf("expected .profile to include CodexSess integration block, got: %s", profileText)
	}

	claudeSettingsPath := filepath.Join(home, ".claude", "settings.json")
	claudeSettingsRaw, err := os.ReadFile(claudeSettingsPath)
	if err != nil {
		t.Fatalf("read claude settings: %v", err)
	}
	var claudeSettings map[string]any
	if err := json.Unmarshal(claudeSettingsRaw, &claudeSettings); err != nil {
		t.Fatalf("decode claude settings: %v", err)
	}
	permissions, _ := claudeSettings["permissions"].(map[string]any)
	if permissions == nil {
		t.Fatalf("expected permissions object in claude settings")
	}
	if got, _ := permissions["defaultMode"].(string); got != "bypassPermissions" {
		t.Fatalf("expected defaultMode bypassPermissions, got %q", got)
	}
	envMap, _ := claudeSettings["env"].(map[string]any)
	if envMap == nil {
		t.Fatalf("expected env object in claude settings")
	}
	if got := strings.TrimSpace(asString(envMap["ANTHROPIC_BASE_URL"])); got != "http://127.0.0.1:3061" {
		t.Fatalf("expected env ANTHROPIC_BASE_URL, got %q", got)
	}
	if got := strings.TrimSpace(asString(envMap["ANTHROPIC_AUTH_TOKEN"])); got != "sk-test" {
		t.Fatalf("expected env ANTHROPIC_AUTH_TOKEN, got %q", got)
	}
	if _, exists := envMap["ANTHROPIC_API_KEY"]; exists {
		t.Fatalf("expected env ANTHROPIC_API_KEY to be removed")
	}

	if got := s.svc.Cfg.ModelMappings["claude-opus-4-1"]; got != "gpt-5.3-codex" {
		t.Fatalf("expected claude-opus-4-1 mapping gpt-5.3-codex, got %q", got)
	}
	if got := s.svc.Cfg.ModelMappings["claude-sonnet-4-6"]; got != "gpt-5.2-codex" {
		t.Fatalf("expected claude-sonnet-4-6 mapping gpt-5.2-codex, got %q", got)
	}
	if got := s.svc.Cfg.ModelMappings["claude-3-5-haiku-20241022"]; got != "gpt-5.1-codex-max" {
		t.Fatalf("expected lightweight mapping gpt-5.1-codex-max, got %q", got)
	}
	if got := s.svc.Cfg.ModelMappings["claude-haiku-4-5-20251001"]; got != "gpt-5.1-codex-max" {
		t.Fatalf("expected haiku-4-5 mapping gpt-5.1-codex-max, got %q", got)
	}

	claudeJSONPath := filepath.Join(home, ".claude.json")
	claudeJSONRaw, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	var claudeJSONDoc map[string]any
	if err := json.Unmarshal(claudeJSONRaw, &claudeJSONDoc); err != nil {
		t.Fatalf("decode .claude.json: %v", err)
	}
	if got, _ := claudeJSONDoc["hasCompletedOnboarding"].(bool); !got {
		t.Fatalf("expected hasCompletedOnboarding=true in .claude.json")
	}
}

func TestHandleWebClaudeCodeSettings_EnablePreservesExistingModelMapping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{
					"claude-sonnet-4-6": "gpt-5.4",
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/claude-code", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()

	s.handleWebClaudeCodeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	if got := s.svc.Cfg.ModelMappings["claude-sonnet-4-6"]; got != "gpt-5.4" {
		t.Fatalf("expected existing mapping preserved, got %q", got)
	}
}

func TestHandleWebClaudeCodeSettings_EnableOverridesExistingClaudeDefaultMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	seed := `{"permissions":{"defaultMode":"default"},"enabledPlugins":{"x":true},"env":{"FOO":"bar"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(seed), 0o600); err != nil {
		t.Fatalf("write claude settings seed: %v", err)
	}

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/claude-code", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()
	s.handleWebClaudeCodeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	updated, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read updated claude settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(updated, &doc); err != nil {
		t.Fatalf("decode updated claude settings: %v", err)
	}
	permissions, _ := doc["permissions"].(map[string]any)
	if got := strings.TrimSpace(asString(permissions["defaultMode"])); got != "bypassPermissions" {
		t.Fatalf("expected defaultMode bypassPermissions, got %q", got)
	}
	envMap, _ := doc["env"].(map[string]any)
	if got := strings.TrimSpace(asString(envMap["FOO"])); got != "bar" {
		t.Fatalf("expected existing env value preserved, got %q", got)
	}
	if got := strings.TrimSpace(asString(envMap["ANTHROPIC_BASE_URL"])); got != "http://127.0.0.1:3061" {
		t.Fatalf("expected env ANTHROPIC_BASE_URL merged, got %q", got)
	}
	if got := strings.TrimSpace(asString(envMap["ANTHROPIC_AUTH_TOKEN"])); got != "sk-test" {
		t.Fatalf("expected env ANTHROPIC_AUTH_TOKEN merged, got %q", got)
	}
	if _, exists := envMap["ANTHROPIC_API_KEY"]; exists {
		t.Fatalf("expected env ANTHROPIC_API_KEY removed on merge")
	}
}

func TestHandleWebClaudeCodeSettings_EnablePreservesExistingClaudeJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	seedPath := filepath.Join(home, ".claude.json")
	seed := `{"theme":"dark","hasCompletedOnboarding":false}`
	if err := os.WriteFile(seedPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("write claude json seed: %v", err)
	}

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/claude-code", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()
	s.handleWebClaudeCodeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	updatedRaw, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read updated .claude.json: %v", err)
	}
	var updated map[string]any
	if err := json.Unmarshal(updatedRaw, &updated); err != nil {
		t.Fatalf("decode updated .claude.json: %v", err)
	}
	if got := strings.TrimSpace(asString(updated["theme"])); got != "dark" {
		t.Fatalf("expected existing theme preserved, got %q", got)
	}
	if got, _ := updated["hasCompletedOnboarding"].(bool); !got {
		t.Fatalf("expected hasCompletedOnboarding=true, got %v", updated["hasCompletedOnboarding"])
	}
}

func TestHandleWebClaudeCodeSettings_EnableIgnoresInvalidClaudeSettingsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"permissions":`), 0o600); err != nil {
		t.Fatalf("write invalid claude settings: %v", err)
	}

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/claude-code", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()
	s.handleWebClaudeCodeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	envPath := filepath.Join(home, ".codexsess", "claude-code.env")
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf("expected env file to still be written, stat err=%v", err)
	}
}

func TestHandleWebClaudeCodeSettings_EnableRemovesLegacyHeaderLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bashrcPath := filepath.Join(home, ".bashrc")
	legacy := "# Added by CodexSess for Claude Code\n"
	if err := os.WriteFile(bashrcPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy bashrc: %v", err)
	}

	s := &Server{
		apiKey:   "sk-test",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/claude-code", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()
	s.handleWebClaudeCodeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(bashrcPath)
	if err != nil {
		t.Fatalf("read updated bashrc: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "# Added by CodexSess for Claude Code") {
		t.Fatalf("expected legacy header removed, got: %s", text)
	}
}

func TestResolveMappedModel_UsesClaudePresetFallback(t *testing.T) {
	s := &Server{
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	if got := s.resolveMappedModel("claude-sonnet-4-6"); got != "gpt-5.2-codex" {
		t.Fatalf("expected fallback mapping to gpt-5.2-codex, got %q", got)
	}
	if got := s.resolveMappedModel("claude-haiku-4-5-20251001"); got != "gpt-5.1-codex-max" {
		t.Fatalf("expected fallback mapping to gpt-5.1-codex-max, got %q", got)
	}
}

func TestHandleWebUpdateAPIKey_SyncsClaudeEnvAndSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := &Server{
		apiKey:   "sk-old",
		bindAddr: "127.0.0.1:3061",
		svc: &service.Service{
			Cfg: config.Config{
				ProxyAPIKey: "sk-old",
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/api-key", strings.NewReader(`{"api_key":"sk-new"}`))
	req.Host = "127.0.0.1:3061"
	rec := httptest.NewRecorder()
	s.handleWebUpdateAPIKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	envPath := filepath.Join(home, ".codexsess", "claude-code.env")
	envRaw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read claude-code.env: %v", err)
	}
	envText := string(envRaw)
	if !strings.Contains(envText, `ANTHROPIC_BASE_URL="http://127.0.0.1:3061"`) {
		t.Fatalf("expected ANTHROPIC_BASE_URL in env file, got: %s", envText)
	}
	if !strings.Contains(envText, `ANTHROPIC_AUTH_TOKEN="sk-new"`) {
		t.Fatalf("expected ANTHROPIC_AUTH_TOKEN in env file, got: %s", envText)
	}
	if strings.Contains(envText, "ANTHROPIC_API_KEY=") {
		t.Fatalf("expected ANTHROPIC_API_KEY removed from env file, got: %s", envText)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	settingsRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatalf("decode settings.json: %v", err)
	}
	envMap, _ := settings["env"].(map[string]any)
	if got := strings.TrimSpace(asString(envMap["ANTHROPIC_AUTH_TOKEN"])); got != "sk-new" {
		t.Fatalf("expected settings env ANTHROPIC_AUTH_TOKEN=sk-new, got %q", got)
	}
	if _, exists := envMap["ANTHROPIC_API_KEY"]; exists {
		t.Fatalf("expected settings env ANTHROPIC_API_KEY removed")
	}
}

func TestHandleWebModelMappings_CRUD(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := &Server{
		svc: &service.Service{
			Cfg: config.Config{
				ModelMappings: map[string]string{},
			},
		},
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/model-mappings", strings.NewReader(`{"alias":"default","model":"gpt-5.2-codex"}`))
	postRec := httptest.NewRecorder()
	s.handleWebModelMappings(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("post expected 200, got %d body=%s", postRec.Code, postRec.Body.String())
	}
	if got := s.resolveMappedModel("default"); got != "gpt-5.2-codex" {
		t.Fatalf("expected mapped model gpt-5.2-codex, got %q", got)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/model-mappings?alias=default", nil)
	delRec := httptest.NewRecorder()
	s.handleWebModelMappings(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d body=%s", delRec.Code, delRec.Body.String())
	}
	if got := s.resolveMappedModel("default"); got != "default" {
		t.Fatalf("expected mapping to be removed, got %q", got)
	}
}
