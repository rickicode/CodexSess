package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/trafficlog"
	"github.com/ricki/codexsess/internal/util"
)

func TestHandleOpenAIV1Root_RejectsInvalidPayload(t *testing.T) {
	s := &Server{apiKey: "sk-test"}

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader("{"))
		req.Header.Set("Authorization", "Bearer sk-test")
		rec := httptest.NewRecorder()

		s.handleOpenAIV1Root(rec, req)

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

		s.handleOpenAIV1Root(rec, req)

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

func TestHandleWebSettings_ClaudeEndpointUsesV1Messages(t *testing.T) {
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

func TestAutoSwitchCLIIfNeeded_RoundRobinRotation(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	dbPath := filepath.Join(root, "data.db")
	st, err := store.Open(dbPath)
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
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")

	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	seedAccount := func(id string, active bool) store.Account {
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
			Email:        id + "@example.com",
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastUsedAt:   now,
			Active:       active,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
			t.Fatalf("write auth.json for %s: %v", id, err)
		}
		usage := store.UsageSnapshot{
			AccountID: id,
			HourlyPct: 80,
			WeeklyPct: 80,
			RawJSON:   "{}",
			FetchedAt: now,
		}
		if id == "acc-a" {
			usage.HourlyPct = 10
			usage.WeeklyPct = 10
		}
		if id == "acc-b" {
			usage.HourlyPct = 60
			usage.WeeklyPct = 60
		}
		if id == "acc-c" {
			usage.HourlyPct = 40
			usage.WeeklyPct = 40
		}
		if err := st.SaveUsage(ctx, usage); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
		return account
	}

	accA := seedAccount("acc-a", true)
	accB := seedAccount("acc-b", false)
	accC := seedAccount("acc-c", false)

	if _, err := svc.UseAccountCLI(ctx, accA.ID); err != nil {
		t.Fatalf("activate cli account a: %v", err)
	}
	restoreUsage := func() {
		t.Helper()
		_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: accA.ID, HourlyPct: 10, WeeklyPct: 10, RawJSON: "{}", FetchedAt: time.Now().UTC()})
		_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: accB.ID, HourlyPct: 60, WeeklyPct: 60, RawJSON: "{}", FetchedAt: time.Now().UTC()})
		_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: accC.ID, HourlyPct: 40, WeeklyPct: 40, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	}
	restoreUsage()

	restoreUsage()
	if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
		t.Fatalf("autoswitch round robin #1: %v", err)
	}
	active, err := svc.ActiveCLIAccountID(ctx)
	if err != nil {
		t.Fatalf("active cli after #1: %v", err)
	}
	if active == accA.ID {
		t.Fatalf("expected active cli to switch away from %s, got %s", accA.ID, active)
	}
	if active != accB.ID && active != accC.ID {
		t.Fatalf("expected active cli to be one of %s/%s, got %s", accB.ID, accC.ID, active)
	}
	prev := active

	restoreUsage()
	if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
		t.Fatalf("autoswitch round robin #2: %v", err)
	}
	active, err = svc.ActiveCLIAccountID(ctx)
	if err != nil {
		t.Fatalf("active cli after #2: %v", err)
	}
	if active == prev {
		t.Fatalf("expected active cli to rotate from %s, got %s", prev, active)
	}
	prev = active

	restoreUsage()
	if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
		t.Fatalf("autoswitch round robin #3: %v", err)
	}
	active, err = svc.ActiveCLIAccountID(ctx)
	if err != nil {
		t.Fatalf("active cli after #3: %v", err)
	}
	if active == prev {
		t.Fatalf("expected active cli to rotate from %s, got %s", prev, active)
	}
}

func TestAutoSwitchCLIIfNeeded_RoundRobinFallbackRotationOrder(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	dbPath := filepath.Join(root, "data.db")
	st, err := store.Open(dbPath)
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
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")

	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	now := time.Now().UTC()
	seedAccount := func(id string, active bool) store.Account {
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
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastUsedAt:   now,
			Active:       active,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
			t.Fatalf("write auth.json for %s: %v", id, err)
		}
		usage := store.UsageSnapshot{
			AccountID: id,
			HourlyPct: 0,
			WeeklyPct: 0,
			RawJSON:   "{}",
			FetchedAt: now,
		}
		if err := st.SaveUsage(ctx, usage); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
		return account
	}

	accA := seedAccount("acc-a", true)
	accB := seedAccount("acc-b", false)
	accC := seedAccount("acc-c", false)

	usageCandidates, err := s.listCLICandidatesWithUsage(ctx)
	if err != nil {
		t.Fatalf("list usage candidates: %v", err)
	}
	if len(usageCandidates) != 0 {
		t.Fatalf("expected no usage candidates, got %d", len(usageCandidates))
	}

	allCandidates, err := s.listCLICandidatesForRoundRobin(ctx)
	if err != nil {
		t.Fatalf("list round robin candidates: %v", err)
	}
	if len(allCandidates) != 0 {
		t.Fatalf("expected 0 round robin candidates when all usage=0, got %d", len(allCandidates))
	}

	if _, err := svc.UseAccountCLI(ctx, accB.ID); err != nil {
		t.Fatalf("activate cli account b: %v", err)
	}
	// Keep deterministic usage scores (all zero) for fallback round-robin path.
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: accA.ID, HourlyPct: 0, WeeklyPct: 0, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: accB.ID, HourlyPct: 0, WeeklyPct: 0, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: accC.ID, HourlyPct: 0, WeeklyPct: 0, RawJSON: "{}", FetchedAt: time.Now().UTC()})

	if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
		t.Fatalf("autoswitch round robin #1: %v", err)
	}
	active, err := svc.ActiveCLIAccountID(ctx)
	if err != nil {
		t.Fatalf("active cli after #1: %v", err)
	}
	if active != accB.ID {
		t.Fatalf("expected active cli unchanged (%s) when all candidates usage=0, got %s", accB.ID, active)
	}
	prev := active

	if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
		t.Fatalf("autoswitch round robin #2: %v", err)
	}
	active, err = svc.ActiveCLIAccountID(ctx)
	if err != nil {
		t.Fatalf("active cli after #2: %v", err)
	}
	if active != prev {
		t.Fatalf("expected active cli unchanged (%s) when all candidates usage=0, got %s", prev, active)
	}
}

func TestAutoSwitchCLIIfNeeded_RoundRobinPrioritizesScore100(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	dbPath := filepath.Join(root, "data.db")
	st, err := store.Open(dbPath)
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
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	cfg.CodingCLIStrategy = "round_robin"

	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	now := time.Now().UTC()
	seedAccount := func(id string, score int) {
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
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastUsedAt:   now,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
			t.Fatalf("write auth.json for %s: %v", id, err)
		}
		usage := store.UsageSnapshot{
			AccountID: id,
			HourlyPct: score,
			WeeklyPct: score,
			RawJSON:   "{}",
			FetchedAt: now,
		}
		if err := st.SaveUsage(ctx, usage); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
	}

	seedAccount("acc-a", 100)
	seedAccount("acc-b", 100)
	seedAccount("acc-c", 60)

	if _, err := svc.UseAccountCLI(ctx, "acc-a"); err != nil {
		t.Fatalf("activate cli account a: %v", err)
	}
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: "acc-a", HourlyPct: 100, WeeklyPct: 100, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: "acc-b", HourlyPct: 100, WeeklyPct: 100, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: "acc-c", HourlyPct: 60, WeeklyPct: 60, RawJSON: "{}", FetchedAt: time.Now().UTC()})

	for i := 0; i < 20; i++ {
		if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
			t.Fatalf("autoswitch round robin #%d: %v", i+1, err)
		}
		active, err := svc.ActiveCLIAccountID(ctx)
		if err != nil {
			t.Fatalf("active cli after #%d: %v", i+1, err)
		}
		if active == "acc-c" {
			t.Fatalf("expected score-100 account only (acc-a/acc-b), got %s", active)
		}
	}
}

func TestAutoSwitchCLIIfNeeded_RoundRobinRefreshesActiveAuthWhenSingleScore100(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	dbPath := filepath.Join(root, "data.db")
	st, err := store.Open(dbPath)
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
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")
	cfg.CodingCLIStrategy = "round_robin"

	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	now := time.Now().UTC()
	seedAccount := func(id string, score int) {
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
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastUsedAt:   now,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
			t.Fatalf("write auth.json for %s: %v", id, err)
		}
		usage := store.UsageSnapshot{
			AccountID: id,
			HourlyPct: score,
			WeeklyPct: score,
			RawJSON:   "{}",
			FetchedAt: now,
		}
		if err := st.SaveUsage(ctx, usage); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
	}

	seedAccount("acc-a", 100)
	seedAccount("acc-b", 60)
	seedAccount("acc-c", 40)

	if _, err := svc.UseAccountCLI(ctx, "acc-a"); err != nil {
		t.Fatalf("activate cli account a: %v", err)
	}
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: "acc-a", HourlyPct: 100, WeeklyPct: 100, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: "acc-b", HourlyPct: 60, WeeklyPct: 60, RawJSON: "{}", FetchedAt: time.Now().UTC()})
	_ = st.SaveUsage(ctx, store.UsageSnapshot{AccountID: "acc-c", HourlyPct: 40, WeeklyPct: 40, RawJSON: "{}", FetchedAt: time.Now().UTC()})

	// Simulate corrupted/misaligned codex-home auth so round-robin must refresh it.
	if err := util.WriteAuthJSON(cfg.CodexHome, "id-token-acc-c", "access-token-acc-c", "refresh-token-acc-c", "acct-acc-c"); err != nil {
		t.Fatalf("seed mismatched codex-home auth.json: %v", err)
	}

	if err := s.autoSwitchCLIIfNeeded(ctx, 15, "round_robin"); err != nil {
		t.Fatalf("autoswitch round robin: %v", err)
	}

	active, err := svc.ActiveCLIAccountID(ctx)
	if err != nil {
		t.Fatalf("active cli after autoswitch: %v", err)
	}
	if active != "acc-a" {
		t.Fatalf("expected active cli remain on score-100 acc-a, got %s", active)
	}

	raw, err := os.ReadFile(filepath.Join(cfg.CodexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read codex-home auth.json: %v", err)
	}
	var f util.AuthFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode codex-home auth.json: %v", err)
	}
	if got := strings.TrimSpace(f.Tokens.IDToken); got != "id-token-acc-a" {
		t.Fatalf("expected codex-home auth sync to acc-a id token, got %q", got)
	}
	if got := strings.TrimSpace(f.Tokens.AccessToken); got != "access-token-acc-a" {
		t.Fatalf("expected codex-home auth sync to acc-a access token, got %q", got)
	}
}

func TestCurrentUsageSchedulerState_RoundRobinForcesFiveMinutes(t *testing.T) {
	cfg := config.Default()
	cfg.CodingCLIStrategy = "round_robin"
	cfg.UsageSchedulerInterval = 30
	cfg.UsageAutoSwitchThreshold = 15

	s := &Server{
		svc: &service.Service{
			Cfg: cfg,
		},
	}

	enabled, threshold, strategy, interval := s.currentUsageSchedulerState()
	if !enabled {
		t.Fatalf("expected scheduler enabled")
	}
	if threshold != 15 {
		t.Fatalf("expected threshold 15, got %d", threshold)
	}
	if strategy != "round_robin" {
		t.Fatalf("expected round_robin strategy, got %s", strategy)
	}
	if interval != 5 {
		t.Fatalf("expected interval 5 for round_robin, got %d", interval)
	}
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

func TestPickNextRoundRobinCLICandidate(t *testing.T) {
	candidates := []cliUsageCandidate{
		{account: store.Account{ID: "acc_a"}, score: 40},
		{account: store.Account{ID: "acc_b"}, score: 88},
		{account: store.Account{ID: "acc_c"}, score: 55},
	}
	next, ok := pickNextRoundRobinCLICandidate(candidates, "acc_b")
	if !ok {
		t.Fatalf("expected candidate to be selected")
	}
	if next.ID != "acc_c" {
		t.Fatalf("expected round robin next acc_c, got %s", next.ID)
	}
}

func TestParseToolCallsFromText_DropsMissingRequiredArguments(t *testing.T) {
	defs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	text := `{"tool_calls":[{"name":"Skill","arguments":{}}]}`
	calls, ok := parseToolCallsFromText(text, defs)
	if ok {
		t.Fatalf("expected invalid tool call to be dropped")
	}
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(calls))
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

func TestFilterToolCallsByDefs_DropsMissingRequiredArguments(t *testing.T) {
	defs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	calls := []ChatToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "Skill",
				Arguments: `{}`,
			},
		},
	}
	filtered, ok := filterToolCallsByDefs(calls, defs)
	if ok {
		t.Fatalf("expected invalid native tool call to be dropped")
	}
	if len(filtered) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(filtered))
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsInvalidToolUseAndPairedToolResult(t *testing.T) {
	s := &Server{}
	toolDefs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_1","name":"Skill","input":{}}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_1","is_error":true,"content":"missing skill"}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"final request"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, toolDefs, "session-test")
	if len(sanitized) != 2 {
		t.Fatalf("expected 2 messages after sanitize, got %d", len(sanitized))
	}
	if strings.Contains(promptFromClaudeMessages(sanitized), "assistant_tool_calls: Skill({})") {
		t.Fatalf("expected invalid tool call context to be removed")
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsSubsequentToolUseFromCachedInvalidPattern(t *testing.T) {
	s := &Server{}
	toolDefs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_1","name":"Skill","input":{}}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_2","name":"Skill","input":{"skill":"using-superpowers"}}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"final request"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, toolDefs, "session-cache")
	if len(sanitized) != 2 {
		t.Fatalf("expected 2 user-only messages after sanitize, got %d", len(sanitized))
	}
	if strings.Contains(promptFromClaudeMessages(sanitized), "assistant_tool_calls:") {
		t.Fatalf("expected cached invalid tool pattern to drop follow-up tool_use")
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsAssistantPolicyRefusalText(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"tolong cek bug ini"}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"Maaf, saya tidak bisa membantu memperbaiki sistem ini karena berpotensi disalahgunakan."}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"fokus ke bug parser output saja"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-refusal")
	got := promptFromClaudeMessages(sanitized)
	if strings.Contains(strings.ToLower(got), "maaf, saya tidak bisa membantu") {
		t.Fatalf("expected assistant refusal text to be removed from prompt")
	}
	if !strings.Contains(got, "fokus ke bug parser output saja") {
		t.Fatalf("expected latest user request to stay in prompt, got: %s", got)
	}
}

func TestSanitizeClaudeMessagesForPrompt_PreservesSkillActivityLines(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"● Skill(superpowers:brainstorming)\n⎿ Successfully loaded skill\nLangsung ke akar masalah parser."}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-skill-lines")
	got := promptFromClaudeMessages(sanitized)
	if !strings.Contains(got, "Skill(superpowers:brainstorming)") {
		t.Fatalf("expected skill activity line to stay, got: %s", got)
	}
	if !strings.Contains(got, "Successfully loaded skill") {
		t.Fatalf("expected skill loaded line to stay, got: %s", got)
	}
	if !strings.Contains(got, "Langsung ke akar masalah parser.") {
		t.Fatalf("expected substantive assistant text to remain, got: %s", got)
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsSkillToolUseAndResult(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_skill_1","name":"Skill","input":{"skill":"superpowers:systematic-debugging"}}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_skill_1","content":"Launching skill"}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"cek potensi bug di sistem ini"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-skill-drop")
	got := promptFromClaudeMessages(sanitized)
	if strings.Contains(got, "assistant_tool_calls: Skill(") {
		t.Fatalf("expected Skill tool_use to be removed from prompt")
	}
	if strings.Contains(strings.ToLower(got), "launching skill") {
		t.Fatalf("expected paired skill tool_result to be removed from prompt")
	}
	if !strings.Contains(got, "cek potensi bug di sistem ini") {
		t.Fatalf("expected real user request to stay in prompt, got: %s", got)
	}
}

func TestSanitizeClaudeMessagesForPrompt_StripsSystemReminderTextBlocks(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"<system-reminder>\nvery long reminder\n</system-reminder>"}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"analisis bug ini"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-system-reminder")
	got := promptFromClaudeMessages(sanitized)
	if strings.Contains(strings.ToLower(got), "system-reminder") {
		t.Fatalf("expected system-reminder block to be stripped, got: %s", got)
	}
	if !strings.Contains(got, "analisis bug ini") {
		t.Fatalf("expected real user text to remain, got: %s", got)
	}
}

func TestSanitizeClaudeAssistantText_DropsTraceAndKeepsSubstance(t *testing.T) {
	in := strings.Join([]string{
		"● Entered plan mode",
		"Skill(superpowers:systematic-debugging)",
		"⎿ Successfully loaded skill",
		"Explore(Explore browser flows issues)",
		"Read(/path/to/file.py · lines 1-2000)",
		"1 tasks (0 done, 1 in progress, 0 open)",
		"◼ Review code for potential bugs",
		"Potensi bug di flow callback ada race condition timeout OTP.",
		"(ctrl+b ctrl+b to run in background)",
	}, "\n")

	got := sanitizeClaudeAssistantText(in)
	if strings.Contains(strings.ToLower(got), "entered plan mode") {
		t.Fatalf("expected plan mode trace to be removed: %s", got)
	}
	if strings.Contains(got, "Skill(superpowers:systematic-debugging)") {
		t.Fatalf("expected skill trace to be removed: %s", got)
	}
	if strings.Contains(got, "Explore(") || strings.Contains(got, "Read(") {
		t.Fatalf("expected tool trace lines to be removed: %s", got)
	}
	if strings.Contains(strings.ToLower(got), "tasks (") || strings.Contains(got, "◼ ") {
		t.Fatalf("expected task status trace lines to be removed: %s", got)
	}
	if !strings.Contains(got, "Potensi bug di flow callback ada race condition timeout OTP.") {
		t.Fatalf("expected substantive text to remain: %s", got)
	}
}

func TestApplyClaudeResponseDefaults_PrependsGuidance(t *testing.T) {
	in := "user: analisis sistem ini apakah ada potensi bug?"
	got := applyClaudeResponseDefaults(in)
	if !strings.HasPrefix(got, "system: Response defaults:") {
		t.Fatalf("expected defaults preamble, got: %s", got)
	}
	if !strings.Contains(got, in) {
		t.Fatalf("expected original prompt to be preserved, got: %s", got)
	}
}

func TestApplyClaudeTokenBudgetGuard_NoChangeWhenUnderSoftLimit(t *testing.T) {
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT", "14000")
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT", "22000")
	msgs := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"analisis bug ringan"}]`)},
	}
	system := json.RawMessage(`"instruksi singkat"`)
	gotMsgs, gotSys := applyClaudeTokenBudgetGuard(msgs, system)
	if len(gotMsgs) != len(msgs) {
		t.Fatalf("expected messages unchanged under soft limit")
	}
	if string(gotSys) != string(system) {
		t.Fatalf("expected system unchanged under soft limit")
	}
}

func TestApplyClaudeTokenBudgetGuard_ProgressiveTrim(t *testing.T) {
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT", "4000")
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT", "5000")
	msgs := make([]ClaudeMessage, 0, 40)
	for i := 0; i < 40; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		msgs = append(msgs, ClaudeMessage{
			Role:    role,
			Content: json.RawMessage(fmt.Sprintf(`[{"type":"text","text":"msg-%d %s"}]`, i, strings.Repeat("x", 900))),
		})
	}
	systemText := strings.Repeat("SYSTEM ", 1200)
	systemRaw, _ := json.Marshal(systemText)
	gotMsgs, gotSys := applyClaudeTokenBudgetGuard(msgs, json.RawMessage(systemRaw))
	if len(gotMsgs) >= len(msgs) {
		t.Fatalf("expected message history to be trimmed, got %d from %d", len(gotMsgs), len(msgs))
	}
	if len([]rune(extractClaudeSystemText(gotSys))) >= len([]rune(systemText)) {
		t.Fatalf("expected system text to be compressed")
	}
	lastPrompt := promptFromClaudeMessages(gotMsgs)
	if !strings.Contains(lastPrompt, "msg-39") {
		t.Fatalf("expected latest context to remain after trimming")
	}
}

func TestMapClaudeToolsToChatTools_AllowsTaskToolsByDefault(t *testing.T) {
	in := []ClaudeToolDef{
		{Name: "TaskCreate"},
		{Name: "TaskOutput"},
		{Name: "Read"},
	}
	got := mapClaudeToolsToChatTools(in)
	if len(got) != 3 {
		t.Fatalf("expected all tools to remain by default, got=%d", len(got))
	}
	if got[0].Function.Name != "TaskCreate" {
		t.Fatalf("expected TaskCreate tool to remain, got=%q", got[0].Function.Name)
	}
}

func TestSanitizeClaudeClientToolCalls_NormalizesReadPagesAndKeepsTaskByDefault(t *testing.T) {
	calls := []ChatToolCall{
		{
			ID:   "a",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "TaskOutput",
				Arguments: `{"task_id":"??"}`,
			},
		},
		{
			ID:   "b",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "Read",
				Arguments: `{"file_path":"/tmp/x.py","limit":400,"offset":1,"pages":""}`,
			},
		},
	}
	got := sanitizeClaudeClientToolCalls(calls)
	if len(got) != 2 {
		t.Fatalf("expected both calls to remain by default, got=%d", len(got))
	}
	if got[0].Function.Name != "TaskOutput" {
		t.Fatalf("expected TaskOutput call to remain, got=%q", got[0].Function.Name)
	}
	if got[1].Function.Name != "Read" {
		t.Fatalf("expected Read call to remain, got=%q", got[1].Function.Name)
	}
	if strings.Contains(got[1].Function.Arguments, `"pages"`) {
		t.Fatalf("expected empty pages to be removed from arguments, got=%s", got[1].Function.Arguments)
	}
}

func TestSanitizeClaudeClientToolCalls_DropsTaskWhenEnvEnabled(t *testing.T) {
	t.Setenv("CODEXSESS_CLAUDE_BLOCK_TASK_TOOLS", "1")
	calls := []ChatToolCall{
		{
			ID:   "a",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "TaskOutput",
				Arguments: `{"task_id":"x"}`,
			},
		},
		{
			ID:   "b",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "Read",
				Arguments: `{"file_path":"README.md","pages":""}`,
			},
		},
	}
	got := sanitizeClaudeClientToolCalls(calls)
	if len(got) != 1 {
		t.Fatalf("expected Task* call to be dropped when env enabled, got=%d", len(got))
	}
	if got[0].Function.Name != "Read" {
		t.Fatalf("expected Read call to remain, got=%q", got[0].Function.Name)
	}
}

func TestSanitizeClaudeToolResultText_DropsKnownNoise(t *testing.T) {
	cases := []string{
		`<tool_use_error>Invalid pages parameter: "".</tool_use_error>`,
		`File content (18151 tokens) exceeds maximum allowed tokens (10000).`,
		`CRITICAL: This is a READ-ONLY task. You CANNOT edit, write, or create files.`,
	}
	for _, in := range cases {
		if got, keep := sanitizeClaudeToolResultText(in); keep || got != "" {
			t.Fatalf("expected noisy tool_result to be dropped, got keep=%v text=%q", keep, got)
		}
	}
}

func TestSanitizeClaudeToolResultText_TruncatesLongResult(t *testing.T) {
	in := strings.Repeat("x", 4000)
	got, keep := sanitizeClaudeToolResultText(in)
	if !keep {
		t.Fatalf("expected long tool_result to be kept with truncation")
	}
	if len(got) >= len(in) {
		t.Fatalf("expected truncated output, got len=%d", len(got))
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got: %q", got)
	}
}

func TestExtractSessionIDFromMetadata_SupportsMultipleShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "nested user_id json string",
			raw:  `{"user_id":"{\"session_id\":\"sid-123\"}"}`,
			want: "sid-123",
		},
		{
			name: "direct session_id",
			raw:  `{"session_id":"sid-234"}`,
			want: "sid-234",
		},
		{
			name: "direct sessionId",
			raw:  `{"sessionId":"sid-345"}`,
			want: "sid-345",
		},
		{
			name: "nested metadata sessionId",
			raw:  `{"metadata":{"sessionId":"sid-456"}}`,
			want: "sid-456",
		},
		{
			name: "userId plain string",
			raw:  `{"userId":"sid-567"}`,
			want: "sid-567",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSessionIDFromMetadata(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
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

func TestPickBestUsageCandidateAbove_SelectsBestOverThreshold(t *testing.T) {
	accounts := []store.Account{
		{ID: "acc_a", Email: "a@example.com"},
		{ID: "acc_b", Email: "b@example.com"},
		{ID: "acc_c", Email: "c@example.com"},
	}
	usageByID := map[string]store.UsageSnapshot{
		"acc_a": {AccountID: "acc_a", HourlyPct: 2, WeeklyPct: 10},
		"acc_b": {AccountID: "acc_b", HourlyPct: 40, WeeklyPct: 70},
		"acc_c": {AccountID: "acc_c", HourlyPct: 25, WeeklyPct: 80},
	}

	best, score, ok := pickBestUsageCandidateAbove(accounts, "acc_a", 2, func(accountID string) (store.UsageSnapshot, error) {
		return usageByID[accountID], nil
	})
	if !ok {
		t.Fatalf("expected candidate, got none")
	}
	if best.ID != "acc_b" {
		t.Fatalf("expected acc_b, got %s", best.ID)
	}
	if score != 40 {
		t.Fatalf("expected score 40, got %d", score)
	}
}

func TestPickBestUsageCandidateAbove_ReturnsNoneWhenNotBetter(t *testing.T) {
	accounts := []store.Account{
		{ID: "acc_a", Email: "a@example.com"},
		{ID: "acc_b", Email: "b@example.com"},
	}
	usageByID := map[string]store.UsageSnapshot{
		"acc_a": {AccountID: "acc_a", HourlyPct: 1, WeeklyPct: 10},
		"acc_b": {AccountID: "acc_b", HourlyPct: 1, WeeklyPct: 1},
	}

	_, _, ok := pickBestUsageCandidateAbove(accounts, "acc_a", 1, func(accountID string) (store.UsageSnapshot, error) {
		return usageByID[accountID], nil
	})
	if ok {
		t.Fatalf("expected no candidate when all scores are <= current threshold")
	}
}

func TestWithTrafficLog_CapturesRequestAndResponse(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("claude", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/messages",
		strings.NewReader(`{"model":"gpt-5.2-codex","messages":[{"role":"user","content":"hi"}],"stream":false}`),
	)
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got, _ := entry["path"].(string); got != "/v1/messages" {
		t.Fatalf("expected path /v1/messages, got %q", got)
	}
	if got, _ := entry["protocol"].(string); got != "claude" {
		t.Fatalf("expected protocol claude, got %q", got)
	}
	if got, _ := entry["model"].(string); got != "gpt-5.2-codex" {
		t.Fatalf("expected model gpt-5.2-codex, got %q", got)
	}
	if got, _ := entry["status"].(float64); int(got) != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %v", entry["status"])
	}
	if got, _ := entry["response_body"].(string); !strings.Contains(got, "bad request") {
		t.Fatalf("expected response body to be captured, got %q", got)
	}
}

func TestWithTrafficLog_CapturesTokenUsageOpenAI(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("openai", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.2-codex","stream":false}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := int(entry["request_tokens"].(float64)); got != 12 {
		t.Fatalf("expected request_tokens=12, got %d", got)
	}
	if got := int(entry["response_tokens"].(float64)); got != 8 {
		t.Fatalf("expected response_tokens=8, got %d", got)
	}
	if got := int(entry["total_tokens"].(float64)); got != 20 {
		t.Fatalf("expected total_tokens=20, got %d", got)
	}
}

func TestWithTrafficLog_CapturesTokenUsageClaude(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("claude", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","usage":{"input_tokens":5,"output_tokens":7}}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.2-codex","stream":false}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := int(entry["request_tokens"].(float64)); got != 5 {
		t.Fatalf("expected request_tokens=5, got %d", got)
	}
	if got := int(entry["response_tokens"].(float64)); got != 7 {
		t.Fatalf("expected response_tokens=7, got %d", got)
	}
	if got := int(entry["total_tokens"].(float64)); got != 12 {
		t.Fatalf("expected total_tokens=12, got %d", got)
	}
}

func TestWithTrafficLog_CapturesTokenUsageFromStream(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("openai", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7}}}\n\n"))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.2-codex","stream":true}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got := int(entry["request_tokens"].(float64)); got != 11 {
		t.Fatalf("expected request_tokens=11, got %d", got)
	}
	if got := int(entry["response_tokens"].(float64)); got != 7 {
		t.Fatalf("expected response_tokens=7, got %d", got)
	}
	if got := int(entry["total_tokens"].(float64)); got != 18 {
		t.Fatalf("expected total_tokens=18, got %d", got)
	}
}

func TestWithTrafficLog_CapturesResolvedAccountWithoutLeakingHeaders(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	s := &Server{traffic: logger}
	wrapped := s.withTrafficLog("openai", func(w http.ResponseWriter, _ *http.Request) {
		setResolvedAccountHeaders(w, store.Account{
			ID:    "acc_test_1",
			Email: "tester@example.com",
		})
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.2-codex","stream":false}`))
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	if got := rec.Header().Get("X-Codex-Resolved-Account-ID"); got != "" {
		t.Fatalf("expected no leaked account id header, got %q", got)
	}
	if got := rec.Header().Get("X-Codex-Resolved-Account-Email"); got != "" {
		t.Fatalf("expected no leaked account email header, got %q", got)
	}

	lines, err := logger.ReadTail(5)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected one log line, got %d", len(lines))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if got, _ := entry["account_id"].(string); got != "acc_test_1" {
		t.Fatalf("expected account_id acc_test_1, got %q", got)
	}
	if got, _ := entry["account_email"].(string); got != "tester@example.com" {
		t.Fatalf("expected account_email tester@example.com, got %q", got)
	}
}

func TestHandleWebLogs_DeleteClearsTrafficLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := trafficlog.New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new traffic logger: %v", err)
	}

	if err := logger.Append(trafficlog.Entry{Path: "/v1/messages", Method: http.MethodPost}); err != nil {
		t.Fatalf("append traffic log: %v", err)
	}

	s := &Server{traffic: logger}
	req := httptest.NewRequest(http.MethodDelete, "/api/logs", nil)
	rec := httptest.NewRecorder()

	s.handleWebLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	lines, err := logger.ReadTail(10)
	if err != nil {
		t.Fatalf("read traffic log: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected logs to be cleared, got %d line(s)", len(lines))
	}
}

func TestDetectTrafficModelAndStream_SupportsNewClaudePath(t *testing.T) {
	model, stream := detectTrafficModelAndStream("/v1/messages", []byte(`{"model":"gpt-5.2-codex","stream":true}`))
	if model != "gpt-5.2-codex" {
		t.Fatalf("expected model gpt-5.2-codex, got %q", model)
	}
	if !stream {
		t.Fatalf("expected stream=true")
	}
}

func TestParseToolCallsFromText_WrappedJSON(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Function: ChatToolFunctionDef{Name: "navigate_page"}},
	}
	text := `{"tool_calls":[{"name":"navigate_page","arguments":{"page":1,"action":"url","url":"https://www.speedtest.net"}}]}`
	calls, ok := parseToolCallsFromText(text, defs)
	if !ok {
		t.Fatalf("expected tool calls to parse")
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %s", calls[0].Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments must be valid json: %v", err)
	}
	if got, _ := args["url"].(string); !strings.Contains(got, "speedtest.net") {
		t.Fatalf("unexpected url argument: %q", got)
	}
}

func TestParseToolCallsFromText_RejectsUnknownTool(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Function: ChatToolFunctionDef{Name: "navigate_page"}},
	}
	text := `{"name":"delete_all","arguments":{"confirm":true}}`
	calls, ok := parseToolCallsFromText(text, defs)
	if ok {
		t.Fatalf("expected parse to fail for unknown tool")
	}
	if len(calls) != 0 {
		t.Fatalf("expected no calls")
	}
}

func TestParseToolCallsFromText_AcceptsResponsesStyleToolDef(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Name: "navigate_page"},
	}
	text := `{"tool_calls":[{"name":"navigate_page","arguments":{"url":"https://example.com"}}]}`
	calls, ok := parseToolCallsFromText(text, defs)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one parsed tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %q", calls[0].Function.Name)
	}
}

func TestParseToolCallsFromText_AcceptsToolCallsObject(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Name: "glob"},
	}
	text := `{"tool_calls":{"name":"glob","arguments":{"pattern":"./CLAUDE.md","path":"/home/ricki/.claude"}}}`
	calls, ok := parseToolCallsFromText(text, defs)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one parsed tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "glob" {
		t.Fatalf("unexpected tool name: %q", calls[0].Function.Name)
	}
	if !strings.Contains(calls[0].Function.Arguments, "CLAUDE.md") {
		t.Fatalf("unexpected arguments: %q", calls[0].Function.Arguments)
	}
}

func TestParseToolCallsFromText_AcceptsConcatenatedJSONObjects(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Name: "glob"},
		{Type: "function", Name: "read"},
	}
	text := strings.Join([]string{
		`{"tool_calls":{"name":"glob","arguments":{"pattern":"./CLAUDE.md","path":"/home/ricki/.claude"}}}`,
		`{"tool_calls":{"name":"read","arguments":{"filePath":"/home/ricki/.claude/CLAUDE.md"}}}`,
	}, "")
	calls, ok := parseToolCallsFromText(text, defs)
	if !ok {
		t.Fatalf("expected parse to succeed for concatenated objects")
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Function.Name != "glob" || calls[1].Function.Name != "read" {
		t.Fatalf("unexpected call order/names: %q then %q", calls[0].Function.Name, calls[1].Function.Name)
	}
}

func TestStreamChatCompletionToolCalls_SSEShape(t *testing.T) {
	rec := httptest.NewRecorder()
	calls := []ChatToolCall{
		{
			ID:   "call_abc",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "navigate_page",
				Arguments: `{"url":"https://example.com"}`,
			},
		},
	}
	streamChatCompletionToolCalls(
		rec,
		rec,
		"chatcmpl-test",
		"gpt-5.2-codex",
		calls,
		Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		false,
	)

	frames := collectSSEDataFrames(rec.Body.Bytes())
	if len(frames) < 4 {
		t.Fatalf("expected at least 4 SSE frames, got %d", len(frames))
	}
	if strings.TrimSpace(frames[len(frames)-1]) != "[DONE]" {
		t.Fatalf("expected final [DONE] frame, got %q", frames[len(frames)-1])
	}

	var firstRaw map[string]any
	if err := json.Unmarshal([]byte(frames[0]), &firstRaw); err != nil {
		t.Fatalf("decode first raw chunk: %v", err)
	}
	if _, ok := firstRaw["usage"]; !ok {
		t.Fatalf("expected usage field to be present in stream chunk")
	}
	if firstRaw["usage"] != nil {
		t.Fatalf("expected non-final chunk usage=null, got %#v", firstRaw["usage"])
	}

	var first ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[0]), &first); err != nil {
		t.Fatalf("decode first chunk: %v", err)
	}
	if first.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("expected first delta role assistant, got %q", first.Choices[0].Delta.Role)
	}

	var nameChunk ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[1]), &nameChunk); err != nil {
		t.Fatalf("decode name chunk: %v", err)
	}
	if len(nameChunk.Choices) == 0 || len(nameChunk.Choices[0].Delta.ToolCalls) == 0 {
		t.Fatalf("expected tool_calls in name chunk")
	}
	tc := nameChunk.Choices[0].Delta.ToolCalls[0]
	if tc.Index == nil || *tc.Index != 0 {
		t.Fatalf("expected tool_call index 0, got %+v", tc.Index)
	}
	if tc.ID != "call_abc" || tc.Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool call identity: id=%q name=%q", tc.ID, tc.Function.Name)
	}

	var argChunk ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[2]), &argChunk); err != nil {
		t.Fatalf("decode arg chunk: %v", err)
	}
	if len(argChunk.Choices) == 0 || len(argChunk.Choices[0].Delta.ToolCalls) == 0 {
		t.Fatalf("expected tool_calls in argument chunk")
	}
	if !strings.Contains(argChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments, "example.com") {
		t.Fatalf("unexpected arguments delta: %q", argChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments)
	}

	var final ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[len(frames)-2]), &final); err != nil {
		t.Fatalf("decode final chunk: %v", err)
	}
	if final.Choices[0].FinishReason == nil || *final.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected finish_reason tool_calls, got %+v", final.Choices[0].FinishReason)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 3 {
		t.Fatalf("expected usage in final chunk")
	}
}

func TestResponsesFunctionCallOutputItems_Shape(t *testing.T) {
	calls := []ChatToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			},
		},
	}
	items := responsesFunctionCallOutputItems(calls)
	if len(items) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(items))
	}
	item := items[0]
	if item.Type != "function_call" {
		t.Fatalf("expected type function_call, got %q", item.Type)
	}
	if item.CallID != "call_1" || item.Name != "read_file" {
		t.Fatalf("unexpected function_call identity: call_id=%q name=%q", item.CallID, item.Name)
	}
	if !strings.Contains(item.Arguments, "README.md") {
		t.Fatalf("unexpected arguments: %q", item.Arguments)
	}
}

func TestResponsesMessageOutputItems_ContainsAnnotationsArray(t *testing.T) {
	items := responsesMessageOutputItems("ok")
	if len(items) != 1 || len(items[0].Content) != 1 {
		t.Fatalf("unexpected output shape")
	}
	if items[0].Content[0].Type != "output_text" {
		t.Fatalf("unexpected content type: %q", items[0].Content[0].Type)
	}
	if items[0].Content[0].Annotations == nil {
		t.Fatalf("annotations must be present as array for compatibility")
	}
}

func TestExtractResponsesInput_HandlesFunctionCallItems(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"Use tool now"}]},
		{"type":"function_call","call_id":"call_abc","name":"navigate","arguments":{"url":"https://example.com"}},
		{"type":"function_call_output","call_id":"call_abc","output":{"ok":true}}
	]`)
	text := extractResponsesInput(raw)
	if !strings.Contains(text, "user: Use tool now") {
		t.Fatalf("expected user text in prompt, got %q", text)
	}
	if !strings.Contains(text, "assistant_tool_calls: navigate") {
		t.Fatalf("expected function call summary in prompt, got %q", text)
	}
	if !strings.Contains(text, "tool(call_abc):") {
		t.Fatalf("expected function output summary in prompt, got %q", text)
	}
}

func TestExtractOpenAIContentText_ArrayParts(t *testing.T) {
	raw := []any{
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
		map[string]any{"type": "text", "text": "world"},
	}
	got := extractOpenAIContentText(raw)
	if got != "hello\nworld" {
		t.Fatalf("unexpected extracted text: %q", got)
	}
}

func TestPromptFromMessages_AcceptsOpenAIContentParts(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "system",
			Content: []any{
				map[string]any{"type": "text", "text": "system rules"},
			},
		},
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "please analyze file"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/b.png"}},
			},
		},
	}
	got := promptFromMessages(msgs)
	if !strings.Contains(got, "system: system rules") {
		t.Fatalf("missing system text in prompt: %q", got)
	}
	if !strings.Contains(got, "user: please analyze file") {
		t.Fatalf("missing user text in prompt: %q", got)
	}
	if strings.Contains(got, "image_url") {
		t.Fatalf("non-text part should not be injected verbatim into prompt: %q", got)
	}
}

func TestPromptFromClaudeMessages_EncodesToolSignals(t *testing.T) {
	msgs := []ClaudeMessage{
		{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"text","text":"I'll use a tool now"},
				{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"README.md"}}
			]`),
		},
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"file content"}]}
			]`),
		},
	}
	got := promptFromClaudeMessages(msgs)
	if !strings.Contains(got, "assistant_tool_calls: read_file(") {
		t.Fatalf("expected assistant tool calls in prompt, got %q", got)
	}
	if !strings.Contains(got, "tool(toolu_1): file content") {
		t.Fatalf("expected tool result line in prompt, got %q", got)
	}
}

func TestBuildClaudeResponseContent_ToolUse(t *testing.T) {
	content, stopReason := buildClaudeResponseContent(
		"",
		[]ChatToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: ChatToolFunctionCall{
					Name:      "navigate_page",
					Arguments: `{"url":"https://example.com"}`,
				},
			},
		},
	)
	if stopReason != "tool_use" {
		t.Fatalf("expected stop reason tool_use, got %q", stopReason)
	}
	if len(content) != 1 {
		t.Fatalf("expected single tool block, got %d", len(content))
	}
	if content[0].Type != "tool_use" || content[0].Name != "navigate_page" {
		t.Fatalf("unexpected content block: %+v", content[0])
	}
	input, ok := content[0].Input.(map[string]any)
	if !ok {
		t.Fatalf("expected tool input object, got %T", content[0].Input)
	}
	if got := strings.TrimSpace(coerceAnyText(input["url"])); got != "https://example.com" {
		t.Fatalf("unexpected tool input url: %q", got)
	}
}

func TestParseDirectResponseSSE_NativeFunctionCallWithoutText(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7},"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	res, err := parseDirectResponseSSE(strings.NewReader(sse), nil)
	if err != nil {
		t.Fatalf("parseDirectResponseSSE returned error: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 native tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool name: %q", res.ToolCalls[0].Function.Name)
	}
	if !strings.Contains(res.ToolCalls[0].Function.Arguments, "README.md") {
		t.Fatalf("unexpected tool args: %q", res.ToolCalls[0].Function.Arguments)
	}
	if res.InputTokens != 11 || res.OutputTokens != 7 {
		t.Fatalf("unexpected usage: in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
}

func TestParseDirectResponseSSE_NativeFunctionCallFromResponseDone(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.done","response":{"usage":{"input_tokens":5,"output_tokens":3},"output":[{"type":"function_call","id":"fc_2","call_id":"call_2","name":"navigate_page","arguments":"{\"url\":\"https://example.com\"}"}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	res, err := parseDirectResponseSSE(strings.NewReader(sse), nil)
	if err != nil {
		t.Fatalf("parseDirectResponseSSE returned error: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 native tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %q", res.ToolCalls[0].Function.Name)
	}
	if res.InputTokens != 5 || res.OutputTokens != 3 {
		t.Fatalf("unexpected usage: in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
}

func TestResolveToolCalls_PrefersNative(t *testing.T) {
	defs := []ChatToolDef{{Type: "function", Name: "navigate_page"}}
	native := []ChatToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "navigate_page",
				Arguments: `{"url":"https://example.com"}`,
			},
		},
	}
	calls, ok := resolveToolCalls(`plain text`, defs, native)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected native tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected resolved tool name: %q", calls[0].Function.Name)
	}
}

func TestResolveToolCalls_PrefersProviderNative(t *testing.T) {
	defs := []ChatToolDef{{Type: "function", Name: "read_file"}}
	native := mapProviderToolCalls([]provider.ToolCall{
		{
			ID:        "call_1",
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	})
	calls, ok := resolveToolCalls(`not json`, defs, native)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected provider-native tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool name: %q", calls[0].Function.Name)
	}
	if !strings.Contains(calls[0].Function.Arguments, "README.md") {
		t.Fatalf("unexpected tool args: %q", calls[0].Function.Arguments)
	}
}

func TestStreamResponsesText_EmitsOpenCodeRequiredFields(t *testing.T) {
	rec := httptest.NewRecorder()
	emit := func(event string, payload map[string]any) {
		writeSSE(rec, event, payload)
	}
	streamResponsesText(
		emit,
		"resp_test",
		"gpt-5.2-codex",
		"hello",
		ResponsesUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		1710000000,
	)

	frames := collectSSEDataFrames(rec.Body.Bytes())
	if len(frames) < 5 {
		t.Fatalf("expected at least 5 SSE frames, got %d", len(frames))
	}

	parsed := make([]map[string]any, 0, len(frames))
	for i := 0; i < len(frames); i++ {
		var evt map[string]any
		if err := json.Unmarshal([]byte(frames[i]), &evt); err != nil {
			t.Fatalf("decode frame[%d]: %v", i, err)
		}
		if _, exists := evt["response_id"]; exists {
			t.Fatalf("unexpected non-standard response_id field in frame[%d]: %+v", i, evt)
		}
		parsed = append(parsed, evt)
	}

	find := func(typ string) map[string]any {
		for _, evt := range parsed {
			if got, _ := evt["type"].(string); got == typ {
				return evt
			}
		}
		return nil
	}

	added := find("response.output_item.added")
	if added == nil {
		t.Fatalf("missing response.output_item.added event")
	}
	item, _ := added["item"].(map[string]any)
	itemID, _ := item["id"].(string)
	if strings.TrimSpace(itemID) == "" {
		t.Fatalf("expected item.id in output_item.added")
	}

	delta := find("response.output_text.delta")
	if delta == nil {
		t.Fatalf("missing response.output_text.delta event")
	}
	deltaItemID, _ := delta["item_id"].(string)
	if deltaItemID != itemID {
		t.Fatalf("expected response.output_text.delta.item_id=%q, got %q", itemID, deltaItemID)
	}

	textDone := find("response.output_text.done")
	if textDone == nil {
		t.Fatalf("missing response.output_text.done event")
	}
	if doneItemID, _ := textDone["item_id"].(string); doneItemID != itemID {
		t.Fatalf("expected response.output_text.done.item_id=%q, got %q", itemID, doneItemID)
	}

	itemDone := find("response.output_item.done")
	if itemDone == nil {
		t.Fatalf("missing response.output_item.done event")
	}

	completed := find("response.completed")
	if completed == nil {
		t.Fatalf("missing response.completed event")
	}
	if done := find("response.done"); done != nil {
		t.Fatalf("unexpected non-standard response.done event still emitted")
	}
	resp, _ := completed["response"].(map[string]any)
	if got, _ := resp["created_at"].(float64); int64(got) != 1710000000 {
		t.Fatalf("unexpected created_at in completed frame: %v", resp["created_at"])
	}
	usage, _ := resp["usage"].(map[string]any)
	if int(usage["input_tokens"].(float64)) != 1 || int(usage["output_tokens"].(float64)) != 2 {
		t.Fatalf("unexpected usage in completed frame: %+v", usage)
	}
	if got, _ := resp["output_text"].(string); got != "hello" {
		t.Fatalf("unexpected output_text in completed frame: %q", got)
	}
}

func TestWriteOpenAISSE_DataOnlyFrame(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOpenAISSE(rec, map[string]any{"type": "response.created"})

	raw := rec.Body.String()
	if strings.Contains(raw, "\nevent:") || strings.HasPrefix(raw, "event:") {
		t.Fatalf("expected data-only sse frame without event header, got %q", raw)
	}
	if !strings.Contains(raw, "data:") {
		t.Fatalf("expected data frame, got %q", raw)
	}
}

func collectSSEDataFrames(raw []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	frames := make([]string, 0, 8)
	dataLines := make([]string, 0, 4)
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		frames = append(frames, strings.TrimSpace(strings.Join(dataLines, "\n")))
		dataLines = dataLines[:0]
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	return frames
}

func TestOAuthBaseURLFromRequest_UsesRequestHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/auth/browser/start", nil)
	req.Host = "app.example.com:8443"

	base := oauthBaseURLFromRequest(req)
	if base != "http://app.example.com:8443" {
		t.Fatalf("expected base url from request host, got %q", base)
	}
}

func TestOAuthBaseURLFromRequest_UsesForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3061/api/auth/browser/start", nil)
	req.Host = "127.0.0.1:3061"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "codexsess.example.com")

	base := oauthBaseURLFromRequest(req)
	if base != "https://codexsess.example.com" {
		t.Fatalf("expected forwarded base url, got %q", base)
	}
}

func TestResolveSSEKeepAliveInterval_DefaultAndClamp(t *testing.T) {
	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "")
	if got := resolveSSEKeepAliveInterval(); got != 8*time.Second {
		t.Fatalf("expected default keepalive 8s, got %s", got)
	}

	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "1")
	if got := resolveSSEKeepAliveInterval(); got != 2*time.Second {
		t.Fatalf("expected min-clamped keepalive 2s, got %s", got)
	}

	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "99")
	if got := resolveSSEKeepAliveInterval(); got != 30*time.Second {
		t.Fatalf("expected max-clamped keepalive 30s, got %s", got)
	}
}

func TestResolveSSEKeepAliveInterval_ParsesValidValue(t *testing.T) {
	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "12")
	if got := resolveSSEKeepAliveInterval(); got != 12*time.Second {
		t.Fatalf("expected keepalive 12s, got %s", got)
	}
}
