package service

import (
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/store"
)

func TestUseAccountAPI_BlockedWhenTokenRevoked(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	revoked := seedCodingTestAccount(t, st, cry, cfg, "acc_revoked", "revoked@example.com", false)

	now := time.Now().UTC()
	if err := st.SaveUsage(t.Context(), store.UsageSnapshot{
		AccountID: revoked.ID,
		HourlyPct: 0,
		WeeklyPct: 0,
		RawJSON:   `{}`,
		FetchedAt: now,
		LastError: `401 {"error":{"code":"token_revoked","message":"Encountered invalidated oauth token for user"}}`,
	}); err != nil {
		t.Fatalf("save revoked usage: %v", err)
	}

	if _, err := svc.UseAccountAPI(t.Context(), revoked.ID); err == nil {
		t.Fatalf("expected UseAccountAPI to reject revoked account")
	}
}

func TestUseAccountCLI_BlockedWhenTokenRevoked(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)
	revoked := seedCodingTestAccount(t, st, cry, cfg, "acc_revoked_cli", "revoked-cli@example.com", false)

	now := time.Now().UTC()
	if err := st.SaveUsage(t.Context(), store.UsageSnapshot{
		AccountID: revoked.ID,
		HourlyPct: 0,
		WeeklyPct: 0,
		RawJSON:   `{}`,
		FetchedAt: now,
		LastError: `401 {"error":{"code":"token_revoked","message":"Encountered invalidated oauth token for user"}}`,
	}); err != nil {
		t.Fatalf("save revoked usage: %v", err)
	}

	if _, err := svc.UseAccountCLI(t.Context(), revoked.ID); err == nil {
		t.Fatalf("expected UseAccountCLI to reject revoked account")
	}
}
