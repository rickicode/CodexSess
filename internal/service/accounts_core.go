package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

func (s *Service) SaveAccountFromTokens(ctx context.Context, t TokenSet, _ string) (store.Account, error) {
	claims, err := util.ParseClaims(t.IDToken, t.AccessToken)
	if err != nil {
		return store.Account{}, err
	}
	existing, _ := s.Store.ListAccounts(ctx)
	isFirstAccount := len(existing) == 0
	id := accountStorageID(claims.Email, claims.AccountID, claims.OrgID)
	if err := os.MkdirAll(s.Cfg.AuthStoreDir, 0o700); err != nil {
		return store.Account{}, err
	}
	accountID := firstNonEmpty(t.AccountID, claims.AccountID)
	if err := util.WriteAuthJSON(s.accountDir(id), t.IDToken, t.AccessToken, t.RefreshToken, accountID); err != nil {
		return store.Account{}, err
	}
	encID, err := s.Crypto.Encrypt([]byte(t.IDToken))
	if err != nil {
		return store.Account{}, err
	}
	encAccess, err := s.Crypto.Encrypt([]byte(t.AccessToken))
	if err != nil {
		return store.Account{}, err
	}
	encRefresh, err := s.Crypto.Encrypt([]byte(t.RefreshToken))
	if err != nil {
		return store.Account{}, err
	}
	a := store.Account{
		ID:             id,
		Email:          claims.Email,
		Alias:          claims.Email,
		PlanType:       claims.PlanType,
		AccountID:      accountID,
		OrganizationID: claims.OrgID,
		TokenID:        encID,
		TokenAccess:    encAccess,
		TokenRefresh:   encRefresh,
		CodexHome:      s.Cfg.CodexHome,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		LastUsedAt:     time.Now().UTC(),
		Active:         isFirstAccount,
		ActiveAPI:      isFirstAccount,
		ActiveCLI:      isFirstAccount,
	}
	if err := s.Store.UpsertAccount(ctx, a); err != nil {
		return store.Account{}, err
	}
	usage, usageErr := s.RefreshUsage(ctx, a.ID)
	if usageErrorLooksRevoked(usage.LastError) {
		return store.Account{}, fmt.Errorf("account cannot be added: token revoked")
	}
	if usageErr != nil {
		log.Printf("[usage-check] add account usage check failed for %s: %v", strings.TrimSpace(a.ID), usageErr)
	}
	if isFirstAccount {
		// Bootstrap UX: first account becomes both API and CLI active.
		if err := s.syncAccountAuthToCodexHome(a); err != nil {
			return store.Account{}, err
		}
		if err := s.Store.SetActiveAccount(ctx, a.ID); err != nil {
			return store.Account{}, err
		}
		if err := s.Store.SetActiveCLIAccount(ctx, a.ID); err != nil {
			return store.Account{}, err
		}
		s.setCLIActiveCache(a.ID)
	}
	return s.Store.FindAccountBySelector(ctx, a.ID)
}

func (s *Service) ListAccounts(ctx context.Context) ([]store.Account, error) {
	accounts, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].UpdatedAt.After(accounts[j].UpdatedAt) })
	return accounts, nil
}

func (s *Service) CountAccounts(ctx context.Context) (int, error) {
	return s.Store.CountAccounts(ctx)
}

func (s *Service) ListAccountsPaginated(ctx context.Context, page, limit int, filter store.AccountFilter) ([]store.Account, int, error) {
	return s.Store.ListAccountsPaginated(ctx, page, limit, filter)
}

func (s *Service) UseAccount(ctx context.Context, selector string) (store.Account, error) {
	a, err := s.UseAccountAPI(ctx, selector)
	if err != nil {
		return store.Account{}, err
	}
	if err := s.syncAccountAuthToCodexHome(a); err != nil {
		return store.Account{}, err
	}
	return s.Store.FindAccountBySelector(ctx, a.ID)
}

func (s *Service) UseAccountAPI(ctx context.Context, selector string) (store.Account, error) {
	prev, _ := s.Store.ActiveAccount(ctx)
	a, err := s.Store.FindAccountBySelector(ctx, selector)
	if err != nil {
		return store.Account{}, err
	}
	if err := s.ensureAccountUsableForActivation(ctx, a.ID); err != nil {
		return store.Account{}, err
	}
	if err := s.Store.SetActiveAccount(ctx, a.ID); err != nil {
		return store.Account{}, err
	}
	if prev.ID != "" && prev.ID != a.ID {
		reason := apiSwitchReason(ctx)
		if reason == "" {
			reason = "manual"
		}
		s.AddSystemLog(ctx, "account_switch", "API active switched", map[string]any{
			"from":   prev.ID,
			"to":     a.ID,
			"reason": reason,
			"type":   "api",
		})
	}
	return s.Store.FindAccountBySelector(ctx, a.ID)
}

func (s *Service) UseAccountCLI(ctx context.Context, selector string) (store.Account, error) {
	prevID, _ := s.ActiveCLIAccountID(ctx)
	a, err := s.Store.FindAccountBySelector(ctx, selector)
	if err != nil {
		return store.Account{}, err
	}
	if err := s.ensureAccountUsableForActivation(ctx, a.ID); err != nil {
		return store.Account{}, err
	}
	s.cliAuthStateMu.Lock()
	if err := s.syncAccountAuthToCodexHomeUnlocked(a); err != nil {
		s.cliAuthStateMu.Unlock()
		return store.Account{}, err
	}
	if err := s.Store.SetActiveCLIAccount(ctx, a.ID); err != nil {
		s.cliAuthStateMu.Unlock()
		return store.Account{}, err
	}
	s.setCLIActiveCache(a.ID)
	s.cliAuthStateMu.Unlock()
	s.notifyCLISwitch(ctx, prevID, a)
	if strings.TrimSpace(prevID) != strings.TrimSpace(a.ID) {
		reason := cliSwitchReason(ctx)
		if reason == "" {
			reason = "manual"
		}
		s.AddSystemLog(ctx, "account_switch", "CLI active switched", map[string]any{
			"from":   strings.TrimSpace(prevID),
			"to":     strings.TrimSpace(a.ID),
			"reason": reason,
			"type":   "cli",
		})
	}
	return s.Store.FindAccountBySelector(ctx, a.ID)
}

func (s *Service) RemoveAccount(ctx context.Context, selector string) error {
	a, err := s.Store.FindAccountBySelector(ctx, selector)
	if err != nil {
		return err
	}
	activeAPI, _ := s.Store.ActiveAccount(ctx)
	activeCLI, _ := s.Store.ActiveCLIAccount(ctx)
	wasAPIActive := strings.TrimSpace(activeAPI.ID) == strings.TrimSpace(a.ID)
	wasCLIActive := strings.TrimSpace(activeCLI.ID) == strings.TrimSpace(a.ID)

	if err := s.Store.DeleteAccount(ctx, a.ID); err != nil {
		return err
	}
	_ = os.RemoveAll(s.accountDir(a.ID))

	if !wasAPIActive && !wasCLIActive {
		return nil
	}

	remaining, err := s.ListAccounts(ctx)
	if err != nil || len(remaining) == 0 {
		return err
	}

	fallbackID := strings.TrimSpace(remaining[0].ID)
	if fallbackID == "" {
		return nil
	}

	if wasAPIActive {
		if _, err := s.UseAccountAPI(ctx, fallbackID); err != nil {
			return err
		}
	}
	if wasCLIActive {
		if _, err := s.UseAccountCLI(ctx, fallbackID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) DeleteRevokedAccounts(ctx context.Context) (int, error) {
	return s.Store.DeleteRevokedAccounts(ctx)
}

func (s *Service) ResolveForRequest(ctx context.Context, selector string) (store.Account, TokenSet, error) {
	return s.resolveForRequest(ctx, selector, false)
}

func (s *Service) ResolveForRequestWithCLISync(ctx context.Context, selector string) (store.Account, TokenSet, error) {
	return s.resolveForRequest(ctx, selector, true)
}

func (s *Service) resolveForRequest(ctx context.Context, selector string, syncCLIAuth bool) (store.Account, TokenSet, error) {
	var a store.Account
	var err error
	if strings.TrimSpace(selector) == "" {
		a, err = s.Store.ActiveAccount(ctx)
	} else {
		a, err = s.Store.FindAccountBySelector(ctx, selector)
	}
	if err != nil {
		return store.Account{}, TokenSet{}, err
	}
	idToken, err := s.Crypto.Decrypt(a.TokenID)
	if err != nil {
		return store.Account{}, TokenSet{}, err
	}
	accessToken, err := s.Crypto.Decrypt(a.TokenAccess)
	if err != nil {
		return store.Account{}, TokenSet{}, err
	}
	refreshToken, err := s.Crypto.Decrypt(a.TokenRefresh)
	if err != nil {
		return store.Account{}, TokenSet{}, err
	}
	tk := TokenSet{
		IDToken:      string(idToken),
		AccessToken:  string(accessToken),
		RefreshToken: string(refreshToken),
		AccountID:    a.AccountID,
	}
	refreshed, err := s.ensureFreshTokens(ctx, a, tk)
	if err != nil {
		return store.Account{}, TokenSet{}, err
	}
	if syncCLIAuth {
		if err := s.syncAccountAuthToCodexHome(refreshed.account); err != nil {
			return store.Account{}, TokenSet{}, err
		}
	}
	return refreshed.account, refreshed.tokens, nil
}

func (s *Service) ensureAccountUsableForActivation(ctx context.Context, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("account not found")
	}
	stored, storedErr := s.Store.GetUsage(ctx, accountID)
	if storedErr == nil && usageErrorLooksRevoked(stored.LastError) {
		return fmt.Errorf("account cannot be activated: token revoked")
	}
	usage, refreshErr := s.RefreshUsage(ctx, accountID)
	if usageErrorLooksRevoked(usage.LastError) {
		return fmt.Errorf("account cannot be activated: token revoked")
	}
	if refreshErr != nil {
		log.Printf("[usage-check] activation usage check failed for %s: %v", accountID, refreshErr)
	}
	return nil
}

func usageErrorLooksRevoked(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "token_revoked") ||
		strings.Contains(msg, "invalidated oauth token") ||
		strings.Contains(msg, "token_invalidated") {
		return true
	}
	if strings.Contains(msg, "account_suspended") || strings.Contains(msg, "account_deactivated") || strings.Contains(msg, "suspended") {
		return true
	}
	if strings.Contains(msg, "status=401") && strings.Contains(msg, "oauth") {
		return true
	}
	if (strings.Contains(msg, `"status":401`) || strings.Contains(msg, `"status": 401`)) && strings.Contains(msg, "token") {
		return true
	}
	return false
}

func accountStorageID(email, accountID, orgID string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email)) + "|" + accountID + "|" + orgID))
	return "codex_" + hex.EncodeToString(sum[:])
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
