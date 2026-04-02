package httpapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
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
