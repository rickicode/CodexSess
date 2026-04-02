package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/provider"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func TestCurrentUsageSchedulerState_UsesConfiguredInterval(t *testing.T) {
	cfg := config.Default()
	cfg.UsageSchedulerInterval = 30
	cfg.UsageAutoSwitchThreshold = 15

	s := &Server{
		svc: &service.Service{
			Cfg: cfg,
		},
	}

	enabled, threshold, interval := s.currentUsageSchedulerState()
	if !enabled {
		t.Fatalf("expected scheduler enabled")
	}
	if threshold != 15 {
		t.Fatalf("expected threshold 15, got %d", threshold)
	}
	if interval != 30 {
		t.Fatalf("expected configured interval 30, got %d", interval)
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

func TestPickBestAutoSwitchCandidate_RequiresHealthyBackupWindow(t *testing.T) {
	accounts := []store.Account{
		{ID: "acc_a", Email: "a@example.com"},
		{ID: "acc_b", Email: "b@example.com"},
		{ID: "acc_c", Email: "c@example.com"},
		{ID: "acc_d", Email: "d@example.com"},
	}
	usageByID := map[string]store.UsageSnapshot{
		"acc_a": {AccountID: "acc_a", HourlyPct: 5, WeeklyPct: 5},
		"acc_b": {AccountID: "acc_b", HourlyPct: 79, WeeklyPct: 95},
		"acc_c": {AccountID: "acc_c", HourlyPct: 85, WeeklyPct: 40},
		"acc_d": {AccountID: "acc_d", HourlyPct: 70, WeeklyPct: 70},
	}

	best, score, ok := pickBestAutoSwitchCandidate(accounts, "acc_a", func(accountID string) (store.UsageSnapshot, error) {
		return usageByID[accountID], nil
	})
	if !ok {
		t.Fatalf("expected candidate, got none")
	}
	if best.ID != "acc_b" {
		t.Fatalf("expected acc_b, got %s", best.ID)
	}
	if score != 95 {
		t.Fatalf("expected score 95, got %d", score)
	}
}

func TestRunUsageSchedulerTick_DoesNotSwitchActiveAccounts(t *testing.T) {
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
	cfg.UsageSchedulerEnabled = true

	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	seedAccount := func(id string, activeAPI bool, activeCLI bool, usage int) {
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
			Active:       activeAPI,
			ActiveAPI:    activeAPI,
			ActiveCLI:    activeCLI,
		}
		if err := st.UpsertAccount(ctx, account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := st.SaveUsage(ctx, store.UsageSnapshot{
			AccountID: id,
			HourlyPct: usage,
			WeeklyPct: usage,
			RawJSON:   "{}",
			FetchedAt: now,
		}); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
	}

	seedAccount("acc-a", true, true, 5)
	seedAccount("acc-b", false, false, 95)

	s.runUsageSchedulerTick(ctx)

	apiActive, err := svc.Store.ActiveAccount(ctx)
	if err != nil {
		t.Fatalf("active api after scheduler tick: %v", err)
	}
	if apiActive.ID != "acc-a" {
		t.Fatalf("expected background usage scheduler to keep active api acc-a, got %s", apiActive.ID)
	}

	cliActive, err := svc.Store.ActiveCLIAccount(ctx)
	if err != nil {
		t.Fatalf("active cli after scheduler tick: %v", err)
	}
	if cliActive.ID != "acc-a" {
		t.Fatalf("expected background usage scheduler to keep active cli acc-a, got %s", cliActive.ID)
	}
}

func TestAutoSwitchCLIActiveIfNeeded_FallsBackToNextCandidateWhenFirstActivationFails(t *testing.T) {
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

	seedAccount := func(id, email string, usage int) store.Account {
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
			AccountID:    "acct-" + id,
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
			t.Fatalf("write auth json %s: %v", id, err)
		}
		if err := st.SaveUsage(ctx, store.UsageSnapshot{
			AccountID: id,
			HourlyPct: usage,
			WeeklyPct: usage,
			RawJSON:   "{}",
			FetchedAt: now,
		}); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
		return account
	}

	active := seedAccount("acc-active", "active@example.com", 5)
	bad := seedAccount("acc-bad", "bad@example.com", 96)
	good := seedAccount("acc-good", "good@example.com", 92)

	if err := st.SetActiveCLIAccount(ctx, active.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}
	if err := st.SetActiveAccount(ctx, active.ID); err != nil {
		t.Fatalf("set active api: %v", err)
	}
	if err := util.WriteAuthJSON(cfg.CodexHome, "id-token-"+active.ID, "access-token-"+active.ID, "refresh-token-"+active.ID, "acct-"+active.ID); err != nil {
		t.Fatalf("seed codex home auth: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":5},"secondary_window":{"used_percent":5}}}`))
	}))
	defer server.Close()
	restoreUsage := overrideUsageHTTPClientForTests(rewriteHTTPClient(server))
	defer restoreUsage()
	if err := os.Remove(filepath.Join(cfg.AuthStoreDir, bad.ID, "auth.json")); err != nil {
		t.Fatalf("remove bad auth.json: %v", err)
	}

	if err := s.autoSwitchCLIActiveIfNeeded(ctx, 10); err != nil {
		t.Fatalf("autoSwitchCLIActiveIfNeeded: %v", err)
	}

	cliActive, err := svc.Store.ActiveCLIAccount(ctx)
	if err != nil {
		t.Fatalf("active cli after switch: %v", err)
	}
	if cliActive.ID != good.ID {
		t.Fatalf("expected fallback candidate %s, got %s", good.ID, cliActive.ID)
	}
}

func TestRunActiveUsageAutoSwitchTick_SwitchesCLIWhenActiveRefreshFails(t *testing.T) {
	ctx := context.Background()
	var logBuf bytes.Buffer
	prevLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prevLogWriter)

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
	cfg.UsageSchedulerEnabled = true
	cfg.UsageAutoSwitchThreshold = 10
	svc := service.New(cfg, st, cry)
	s := &Server{svc: svc}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Your refresh token has already been used to generate a new access token. Please try signing in again.","code":"refresh_token_reused"}}`))
		case "/backend-api/wham/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":5},"secondary_window":{"used_percent":5}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restoreOAuth := service.OverrideOAuthHTTPClientForTests(rewriteHTTPClient(server))
	defer restoreOAuth()
	restoreUsage := overrideUsageHTTPClientForTests(rewriteHTTPClient(server))
	defer restoreUsage()

	seedAccount := func(id, email, accessToken, refreshToken string, usage int) store.Account {
		t.Helper()
		tokenID, err := cry.Encrypt([]byte(testJWT(email, time.Now().UTC().Add(24 * time.Hour))))
		if err != nil {
			t.Fatalf("encrypt id token: %v", err)
		}
		tokenAccess, err := cry.Encrypt([]byte(accessToken))
		if err != nil {
			t.Fatalf("encrypt access token: %v", err)
		}
		tokenRefresh, err := cry.Encrypt([]byte(refreshToken))
		if err != nil {
			t.Fatalf("encrypt refresh token: %v", err)
		}
		now := time.Now().UTC()
		account := store.Account{
			ID:           id,
			Email:        email,
			Alias:        email,
			AccountID:    "acct-" + id,
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
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), testJWT(email, time.Now().UTC().Add(24*time.Hour)), accessToken, refreshToken, "acct-"+id); err != nil {
			t.Fatalf("write auth json %s: %v", id, err)
		}
		if err := st.SaveUsage(ctx, store.UsageSnapshot{
			AccountID: id,
			HourlyPct: usage,
			WeeklyPct: usage,
			RawJSON:   "{}",
			FetchedAt: now,
		}); err != nil {
			t.Fatalf("save usage %s: %v", id, err)
		}
		return account
	}

	active := seedAccount("codex_deadbeef", "active@example.com", testJWT("active@example.com", time.Now().UTC().Add(-time.Hour)), "refresh-reused", 95)
	good := seedAccount("acc-good", "good@example.com", "access-token-good", "refresh-good", 92)

	if err := st.SetActiveCLIAccount(ctx, active.ID); err != nil {
		t.Fatalf("set active cli: %v", err)
	}

	s.runActiveUsageAutoSwitchTick(ctx)

	cliActive, err := svc.Store.ActiveCLIAccount(ctx)
	if err != nil {
		t.Fatalf("active cli after refresh failure switch: %v", err)
	}
	if cliActive.ID != good.ID {
		t.Fatalf("expected CLI autoswitch to recover to %s, got %s", good.ID, cliActive.ID)
	}
	if !strings.Contains(logBuf.String(), "active@example.com") {
		t.Fatalf("expected autoswitch log to include active email, got %q", logBuf.String())
	}
	if strings.Contains(logBuf.String(), "codex_deadbeef") {
		t.Fatalf("expected autoswitch log to avoid raw codex id, got %q", logBuf.String())
	}
}

func rewriteHTTPClient(server *httptest.Server) *http.Client {
	target := server.URL
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = "http"
			clone.URL.Host = strings.TrimPrefix(target, "http://")
			clone.RequestURI = ""
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}
}

func overrideUsageHTTPClientForTests(client *http.Client) func() {
	prev := provider.TestingSwapUsageHTTPClient(client)
	return func() {
		provider.TestingSwapUsageHTTPClient(prev)
	}
}

func testJWT(email string, exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{
		"email": email,
		"exp":   exp.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-" + email,
		},
	})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
