package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ricki/codexsess/internal/util"
)

func TestUseAccountCLI_SyncsAuthImmediately(t *testing.T) {
	svc, st, cry, cfg := newCodingTestService(t)

	first := seedCodingTestAccount(t, st, cry, cfg, "acc_cli_first", "first@example.com", false)
	second := seedCodingTestAccount(t, st, cry, cfg, "acc_cli_second", "second@example.com", false)

	if _, err := svc.UseAccountCLI(t.Context(), first.ID); err != nil {
		t.Fatalf("set initial active cli: %v", err)
	}

	rawBefore, err := os.ReadFile(filepath.Join(cfg.CodexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read initial auth.json: %v", err)
	}
	var authBefore util.AuthFile
	if err := json.Unmarshal(rawBefore, &authBefore); err != nil {
		t.Fatalf("decode initial auth.json: %v", err)
	}
	if got := authBefore.Tokens.IDToken; got != "id-token-"+first.ID {
		t.Fatalf("expected auth.json to reflect first account before switch, got %q", got)
	}

	if _, err := svc.UseAccountCLI(t.Context(), second.ID); err != nil {
		t.Fatalf("switch cli account: %v", err)
	}

	activeID, err := svc.ActiveCLIAccountID(t.Context())
	if err != nil {
		t.Fatalf("ActiveCLIAccountID: %v", err)
	}
	if activeID != second.ID {
		t.Fatalf("expected active cli %s, got %s", second.ID, activeID)
	}

	rawAfter, err := os.ReadFile(filepath.Join(cfg.CodexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json after switch: %v", err)
	}
	var authAfter util.AuthFile
	if err := json.Unmarshal(rawAfter, &authAfter); err != nil {
		t.Fatalf("decode auth.json after switch: %v", err)
	}
	if got := authAfter.Tokens.IDToken; got != "id-token-"+second.ID {
		t.Fatalf("expected auth.json to sync to second account, got %q", got)
	}
}
