package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func TestEnsureCodingCLIAccountForCoding_SwitchesToBetterCandidate(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	current := seedCodingTestAccount(t, st, cry, cfg, "acc_current", "current@example.com", true)
	better := seedCodingTestAccount(t, st, cry, cfg, "acc_better", "better@example.com", false)

	if _, err := svc.UseAccountCLI(t.Context(), current.ID); err != nil {
		t.Fatalf("set cli active current: %v", err)
	}
	now := time.Now().UTC()
	if err := st.SaveUsage(t.Context(), store.UsageSnapshot{
		AccountID: current.ID, HourlyPct: 10, WeeklyPct: 10, RawJSON: `{}`, FetchedAt: now,
	}); err != nil {
		t.Fatalf("save current usage: %v", err)
	}
	if err := st.SaveUsage(t.Context(), store.UsageSnapshot{
		AccountID: better.ID, HourlyPct: 75, WeeklyPct: 70, RawJSON: `{}`, FetchedAt: now,
	}); err != nil {
		t.Fatalf("save better usage: %v", err)
	}
	svc.Cfg.UsageAutoSwitchThreshold = 20

	if err := svc.ensureCodingCLIAccountForCoding(t.Context()); err != nil {
		t.Fatalf("ensureCodingCLIAccountForCoding: %v", err)
	}

	activeID, err := svc.ActiveCLIAccountID(t.Context())
	if err != nil {
		t.Fatalf("ActiveCLIAccountID: %v", err)
	}
	if activeID != better.ID {
		t.Fatalf("expected active cli %s, got %s", better.ID, activeID)
	}
}

func TestEnsureCodingCLIAccountForCoding_DoesNotDowngradeWhenFallbackOnlyWorse(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	current := seedCodingTestAccount(t, st, cry, cfg, "acc_current", "current@example.com", true)
	worse := seedCodingTestAccount(t, st, cry, cfg, "acc_worse", "worse@example.com", false)

	if _, err := svc.UseAccountCLI(t.Context(), current.ID); err != nil {
		t.Fatalf("set cli active current: %v", err)
	}
	now := time.Now().UTC()
	if err := st.SaveUsage(t.Context(), store.UsageSnapshot{
		AccountID: current.ID, HourlyPct: 19, WeeklyPct: 19, RawJSON: `{}`, FetchedAt: now,
	}); err != nil {
		t.Fatalf("save current usage: %v", err)
	}
	if err := st.SaveUsage(t.Context(), store.UsageSnapshot{
		AccountID: worse.ID, HourlyPct: 5, WeeklyPct: 5, RawJSON: `{}`, FetchedAt: now,
	}); err != nil {
		t.Fatalf("save worse usage: %v", err)
	}
	svc.Cfg.UsageAutoSwitchThreshold = 20

	if err := svc.ensureCodingCLIAccountForCoding(t.Context()); err != nil {
		t.Fatalf("ensureCodingCLIAccountForCoding: %v", err)
	}

	activeID, err := svc.ActiveCLIAccountID(t.Context())
	if err != nil {
		t.Fatalf("ActiveCLIAccountID: %v", err)
	}
	if activeID != current.ID {
		t.Fatalf("expected active cli tetap %s, got %s", current.ID, activeID)
	}
}

func TestResolveCommandContent_ReviewStripsPrefix(t *testing.T) {
	prompt, visible := resolveCommandContent("review", "/review focus auth middleware")
	if prompt != "focus auth middleware" {
		t.Fatalf("unexpected review prompt: %q", prompt)
	}
	if visible != "/review focus auth middleware" {
		t.Fatalf("unexpected user-visible content: %q", visible)
	}
}

func TestResolveCommandContent_ReviewWithNoArgs(t *testing.T) {
	prompt, visible := resolveCommandContent("review", "/review")
	if prompt != "" {
		t.Fatalf("expected empty prompt for bare /review, got: %q", prompt)
	}
	if visible != "/review" {
		t.Fatalf("unexpected user-visible content: %q", visible)
	}
}

func TestResolveCommandContent_ReviewWithoutSlashKeepsContent(t *testing.T) {
	prompt, visible := resolveCommandContent("review", "Check auth flow")
	if prompt != "Check auth flow" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if visible != "Check auth flow" {
		t.Fatalf("unexpected user-visible content: %q", visible)
	}
}

func newCodingTestService(t *testing.T) (*Service, *store.Store, *icrypto.Crypto, config.Config) {
	t.Helper()
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

func TestNormalizeCodingCommandMode_Review(t *testing.T) {
	if got := normalizeCodingCommandMode("review"); got != "review" {
		t.Fatalf("expected review, got %q", got)
	}
	if got := normalizeCodingCommandMode(" REVIEW "); got != "review" {
		t.Fatalf("expected review (case-insensitive), got %q", got)
	}
	if got := normalizeCodingCommandMode("chat"); got != "chat" {
		t.Fatalf("expected chat, got %q", got)
	}
}

func TestBuildCodingPrompt_ReviewBypassesWrapping(t *testing.T) {
	raw := "/review check race condition"
	prompt := buildCodingPrompt("review", raw)
	if got, want := prompt, raw; got != want {
		t.Fatalf("expected review prompt passthrough: got %q want %q", got, want)
	}
	if strings.Contains(strings.ToLower(prompt), "context hygiene rules") {
		t.Fatalf("review prompt should not include context hygiene wrapper: %q", prompt)
	}
}
