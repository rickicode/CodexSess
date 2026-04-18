package service

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricki/codexsess/internal/config"
)

func newOAuthTestService(t *testing.T) *Service {
	t.Helper()
	activeBrowserWebMu.Lock()
	activeBrowserWebState = nil
	activeBrowserWebMu.Unlock()
	t.Cleanup(func() {
		activeBrowserWebMu.Lock()
		activeBrowserWebState = nil
		activeBrowserWebMu.Unlock()
	})
	return &Service{Cfg: config.Config{DataDir: t.TempDir()}}
}

func TestStartBrowserLoginWeb_UsesLocalhostCallbackRedirect(t *testing.T) {
	svc := newOAuthTestService(t)
	login, err := svc.StartBrowserLoginWeb(context.Background(), "https://codexsess.example.com/base/", "alias-a")
	if err != nil {
		t.Fatalf("StartBrowserLoginWeb error: %v", err)
	}
	if strings.TrimSpace(login.LoginID) == "" {
		t.Fatalf("expected login id")
	}
	pending, err := svc.loadPending(login.LoginID)
	if err != nil {
		t.Fatalf("loadPending error: %v", err)
	}
	if pending.RedirectURI != "http://localhost:1455/auth/callback" {
		t.Fatalf("unexpected redirect uri: %q", pending.RedirectURI)
	}
	if pending.Alias != "alias-a" {
		t.Fatalf("expected alias to persist, got %q", pending.Alias)
	}
	u, err := url.Parse(login.AuthURL)
	if err != nil {
		t.Fatalf("auth url parse error: %v", err)
	}
	if got := u.Query().Get("redirect_uri"); got != pending.RedirectURI {
		t.Fatalf("auth url redirect mismatch: got %q want %q", got, pending.RedirectURI)
	}
	if got := u.Query().Get("redirect_uri"); got != "http://localhost:1455/auth/callback" {
		t.Fatalf("web auth url should use localhost redirect_uri, got %q", got)
	}
	if got := u.Query().Get("state"); got == "" || got != pending.State {
		t.Fatalf("auth url state mismatch: got %q want %q", got, pending.State)
	}
	if got := u.Query().Get("code_challenge"); got == "" {
		t.Fatalf("expected code challenge in auth url")
	}
}

func TestStartBrowserLoginWeb_ReusesActiveSession(t *testing.T) {
	svc := newOAuthTestService(t)
	first, err := svc.StartBrowserLoginWeb(context.Background(), "https://codexsess.example.com", "")
	if err != nil {
		t.Fatalf("first StartBrowserLoginWeb error: %v", err)
	}
	second, err := svc.StartBrowserLoginWeb(context.Background(), "https://ignored.example.com", "")
	if err != nil {
		t.Fatalf("second StartBrowserLoginWeb error: %v", err)
	}
	if first.LoginID != second.LoginID {
		t.Fatalf("expected login reuse, got %q and %q", first.LoginID, second.LoginID)
	}
	if first.AuthURL != second.AuthURL {
		t.Fatalf("expected auth url reuse")
	}
}

func TestCancelBrowserLoginWeb_RemovesPendingSession(t *testing.T) {
	svc := newOAuthTestService(t)
	login, err := svc.StartBrowserLoginWeb(context.Background(), "https://codexsess.example.com", "")
	if err != nil {
		t.Fatalf("StartBrowserLoginWeb error: %v", err)
	}
	if _, err := svc.loadPending(login.LoginID); err != nil {
		t.Fatalf("expected pending session before cancel: %v", err)
	}
	svc.CancelBrowserLoginWeb(login.LoginID)
	if _, err := svc.loadPending(login.LoginID); err == nil {
		t.Fatalf("expected pending session to be removed after cancel")
	}
	activeBrowserWebMu.Lock()
	defer activeBrowserWebMu.Unlock()
	if activeBrowserWebState != nil {
		t.Fatalf("expected active web session to be cleared")
	}
	if _, err := filepath.Abs(svc.pendingDir()); err != nil {
		t.Fatalf("pending dir should remain accessible: %v", err)
	}
}
