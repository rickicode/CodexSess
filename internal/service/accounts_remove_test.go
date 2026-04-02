package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func TestRemoveAccount_FallbacksActiveAPIAndCLI(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "data.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	cry, err := icrypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.AuthStoreDir = filepath.Join(root, "auth-accounts")
	cfg.CodexHome = filepath.Join(root, "codex-home")

	svc := New(cfg, st, cry)
	seedAccount := func(id, email string, active bool) store.Account {
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
			TokenID:      tokenID,
			TokenAccess:  tokenAccess,
			TokenRefresh: tokenRefresh,
			CodexHome:    cfg.CodexHome,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastUsedAt:   now,
			Active:       active,
		}
		if err := st.UpsertAccount(t.Context(), account); err != nil {
			t.Fatalf("upsert account %s: %v", id, err)
		}
		if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, id), "id-token-"+id, "access-token-"+id, "refresh-token-"+id, "acct-"+id); err != nil {
			t.Fatalf("write auth.json for %s: %v", id, err)
		}
		return account
	}

	a := seedAccount("acc_a", "a@example.com", true)
	b := seedAccount("acc_b", "b@example.com", false)

	if _, err := svc.UseAccountCLI(t.Context(), a.ID); err != nil {
		t.Fatalf("activate cli account a: %v", err)
	}

	if err := svc.RemoveAccount(t.Context(), a.ID); err != nil {
		t.Fatalf("remove active account: %v", err)
	}

	activeAPI, err := st.ActiveAccount(t.Context())
	if err != nil {
		t.Fatalf("active api account: %v", err)
	}
	if activeAPI.ID != b.ID {
		t.Fatalf("expected active api %s, got %s", b.ID, activeAPI.ID)
	}

	activeCLI, err := svc.ActiveCLIAccountID(t.Context())
	if err != nil {
		t.Fatalf("active cli account: %v", err)
	}
	if activeCLI != b.ID {
		t.Fatalf("expected active cli %s, got %s", b.ID, activeCLI)
	}
}
