package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/store"
)

func TestRestoreAccountsBackup_RejectsUnsupportedVersion(t *testing.T) {
	svc := &Service{}
	_, err := svc.RestoreAccountsBackup(t.Context(), AccountsBackupPayload{
		Version:  "unsupported.version",
		Accounts: []AccountBackupEntry{{IDToken: "id", AccessToken: "access"}},
	})
	if err == nil {
		t.Fatalf("expected error for unsupported backup version")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unsupported backup version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRestoreAccountsBackup_RejectsEmptyAccounts(t *testing.T) {
	svc := &Service{}
	_, err := svc.RestoreAccountsBackup(t.Context(), AccountsBackupPayload{
		Version: accountsBackupVersion,
	})
	if err == nil {
		t.Fatalf("expected error for empty backup accounts")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "does not contain accounts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportAccountTokens_ReturnsEmailAndTokens(t *testing.T) {
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
	svc := New(cfg, st, cry)

	idToken, err := cry.Encrypt([]byte("id-token-1"))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	accessToken, err := cry.Encrypt([]byte("access-token-1"))
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	refreshToken, err := cry.Encrypt([]byte("refresh-token-1"))
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}

	account := store.Account{
		ID:           "acc-1",
		Email:        "user@example.com",
		TokenID:      idToken,
		TokenAccess:  accessToken,
		TokenRefresh: refreshToken,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		LastUsedAt:   time.Now().UTC(),
	}
	if err := st.UpsertAccount(t.Context(), account); err != nil {
		t.Fatalf("upsert account: %v", err)
	}

	entries, err := svc.ExportAccountTokens(t.Context())
	if err != nil {
		t.Fatalf("export account tokens: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 export entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Email != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %q", entry.Email)
	}
	if entry.AccessToken != "access-token-1" {
		t.Fatalf("expected access token access-token-1, got %q", entry.AccessToken)
	}
	if entry.RefreshToken != "refresh-token-1" {
		t.Fatalf("expected refresh token refresh-token-1, got %q", entry.RefreshToken)
	}
	if entry.IDToken != "id-token-1" {
		t.Fatalf("expected id token id-token-1, got %q", entry.IDToken)
	}
}

func TestExportAccountTokens_DeduplicatesByEmail(t *testing.T) {
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
	svc := New(cfg, st, cry)

	seed := func(id string, updatedAt time.Time, idTokenValue string, accessTokenValue string, refreshTokenValue string) {
		t.Helper()
		idToken, err := cry.Encrypt([]byte(idTokenValue))
		if err != nil {
			t.Fatalf("encrypt id token: %v", err)
		}
		accessToken, err := cry.Encrypt([]byte(accessTokenValue))
		if err != nil {
			t.Fatalf("encrypt access token: %v", err)
		}
		refreshToken, err := cry.Encrypt([]byte(refreshTokenValue))
		if err != nil {
			t.Fatalf("encrypt refresh token: %v", err)
		}
		if err := st.UpsertAccount(t.Context(), store.Account{
			ID:           id,
			Email:        "user@example.com",
			TokenID:      idToken,
			TokenAccess:  accessToken,
			TokenRefresh: refreshToken,
			CreatedAt:    updatedAt,
			UpdatedAt:    updatedAt,
			LastUsedAt:   updatedAt,
		}); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
	}

	now := time.Now().UTC()
	seed("acc-old", now.Add(-time.Hour), "id-token-old", "access-token-old", "refresh-token-old")
	seed("acc-new", now, "id-token-new", "access-token-new", "refresh-token-new")

	entries, err := svc.ExportAccountTokens(t.Context())
	if err != nil {
		t.Fatalf("export account tokens: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated export entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Email != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %q", entry.Email)
	}
	if entry.AccessToken != "access-token-new" {
		t.Fatalf("expected newest access token, got %q", entry.AccessToken)
	}
	if entry.RefreshToken != "refresh-token-new" {
		t.Fatalf("expected newest refresh token, got %q", entry.RefreshToken)
	}
	if entry.IDToken != "id-token-new" {
		t.Fatalf("expected newest id token, got %q", entry.IDToken)
	}
}
