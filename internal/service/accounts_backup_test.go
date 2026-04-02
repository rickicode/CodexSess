package service

import (
	"strings"
	"testing"
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

