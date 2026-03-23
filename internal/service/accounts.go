package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

type Service struct {
	Cfg    config.Config
	Store  *store.Store
	Crypto *icrypto.Crypto
	Codex  *provider.CodexExec

	cliActiveMu       sync.RWMutex
	cliAuthStateMu    sync.Mutex
	cliActiveCachedID string
	cliActiveCachedAt time.Time
	codingRunMu       sync.Mutex
	codingRuns        map[string]*codingRunState
}

type codingRunState struct {
	startedAt time.Time
	cancel    context.CancelFunc
	forceKill func() error
}

type TokenSet struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

type AccountBackupEntry struct {
	ID             string               `json:"id"`
	Email          string               `json:"email"`
	Alias          string               `json:"alias"`
	PlanType       string               `json:"plan_type"`
	AccountID      string               `json:"account_id"`
	OrganizationID string               `json:"organization_id"`
	IDToken        string               `json:"id_token"`
	AccessToken    string               `json:"access_token"`
	RefreshToken   string               `json:"refresh_token,omitempty"`
	ActiveAPI      bool                 `json:"active_api"`
	ActiveCLI      bool                 `json:"active_cli"`
	Usage          *store.UsageSnapshot `json:"usage,omitempty"`
}

type AccountsBackupPayload struct {
	Version          string               `json:"version"`
	ExportedAt       time.Time            `json:"exported_at"`
	ActiveAPIAccount string               `json:"active_api_account_id,omitempty"`
	ActiveCLIAccount string               `json:"active_cli_account_id,omitempty"`
	Accounts         []AccountBackupEntry `json:"accounts"`
}

type RestoreAccountsResult struct {
	Restored int `json:"restored"`
	Skipped  int `json:"skipped"`
}

const accountsBackupVersion = "codexsess.accounts.backup.v1"
const cliActiveCacheTTL = 1 * time.Second

type cliSwitchReasonKey struct{}
type apiSwitchReasonKey struct{}

func WithCLISwitchReason(ctx context.Context, reason string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, cliSwitchReasonKey{}, strings.TrimSpace(reason))
}

func cliSwitchReason(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(cliSwitchReasonKey{}).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func WithAPISwitchReason(ctx context.Context, reason string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, apiSwitchReasonKey{}, strings.TrimSpace(reason))
}

func apiSwitchReason(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(apiSwitchReasonKey{}).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func New(cfg config.Config, st *store.Store, cry *icrypto.Crypto) *Service {
	bin := strings.TrimSpace(cfg.CodexBin)
	if bin == "" {
		bin = "codex"
	}
	return &Service{
		Cfg:        cfg,
		Store:      st,
		Crypto:     cry,
		Codex:      provider.NewCodexExec(bin),
		codingRuns: make(map[string]*codingRunState),
	}
}

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

func (s *Service) RefreshUsage(ctx context.Context, selector string) (store.UsageSnapshot, error) {
	// Refresh usage must not switch CLI active account in codex home.
	a, tk, err := s.resolveForRequest(ctx, selector, false)
	if err != nil {
		return store.UsageSnapshot{}, err
	}
	usage, err := provider.FetchUsage(ctx, tk.AccessToken, firstNonEmpty(a.AccountID, tk.AccountID))
	if err != nil {
		u := store.UsageSnapshot{
			AccountID: a.ID,
			FetchedAt: time.Now().UTC(),
			LastError: err.Error(),
			RawJSON:   "{}",
		}
		_ = s.Store.SaveUsage(ctx, u)
		if usageErrorLooksRevoked(err.Error()) {
			_ = s.Store.SetAccountRevoked(ctx, a.ID, true)
		}
		return u, err
	}
	u := store.UsageSnapshot{
		AccountID:       a.ID,
		HourlyPct:       usage.HourlyPct,
		WeeklyPct:       usage.WeeklyPct,
		HourlyResetAt:   usage.HourlyResetAt,
		WeeklyResetAt:   usage.WeeklyResetAt,
		RawJSON:         usage.RawJSON,
		FetchedAt:       time.Now().UTC(),
		LastError:       "",
		WindowPrimary:   usage.WindowPrimary,
		WindowSecondary: usage.WindowSecondary,
	}
	if err := s.Store.SaveUsage(ctx, u); err != nil {
		return store.UsageSnapshot{}, err
	}
	_ = s.Store.SetAccountRevoked(ctx, a.ID, false)
	return u, nil
}

func (s *Service) ImportTokenJSON(ctx context.Context, path string, alias string) (store.Account, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return store.Account{}, err
	}
	var anyJSON map[string]any
	if err := json.Unmarshal(b, &anyJSON); err != nil {
		return store.Account{}, err
	}
	tokens := TokenSet{}
	if tokObj, ok := anyJSON["tokens"].(map[string]any); ok {
		tokens.IDToken, _ = tokObj["id_token"].(string)
		tokens.AccessToken, _ = tokObj["access_token"].(string)
		tokens.RefreshToken, _ = tokObj["refresh_token"].(string)
		tokens.AccountID, _ = tokObj["account_id"].(string)
	} else {
		tokens.IDToken, _ = anyJSON["id_token"].(string)
		tokens.AccessToken, _ = anyJSON["access_token"].(string)
		tokens.RefreshToken, _ = anyJSON["refresh_token"].(string)
		tokens.AccountID, _ = anyJSON["account_id"].(string)
	}
	if strings.TrimSpace(tokens.IDToken) == "" || strings.TrimSpace(tokens.AccessToken) == "" {
		return store.Account{}, fmt.Errorf("invalid token JSON: id_token and access_token required")
	}
	return s.SaveAccountFromTokens(ctx, tokens, alias)
}

func (s *Service) ExportAccountsBackup(ctx context.Context) (AccountsBackupPayload, error) {
	accounts, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return AccountsBackupPayload{}, err
	}
	usageMap, _ := s.Store.ListUsageSnapshots(ctx)

	payload := AccountsBackupPayload{
		Version:    accountsBackupVersion,
		ExportedAt: time.Now().UTC(),
		Accounts:   make([]AccountBackupEntry, 0, len(accounts)),
	}

	for _, account := range accounts {
		idTokenRaw, err := s.Crypto.Decrypt(account.TokenID)
		if err != nil {
			return AccountsBackupPayload{}, err
		}
		accessTokenRaw, err := s.Crypto.Decrypt(account.TokenAccess)
		if err != nil {
			return AccountsBackupPayload{}, err
		}
		refreshTokenRaw, err := s.Crypto.Decrypt(account.TokenRefresh)
		if err != nil {
			return AccountsBackupPayload{}, err
		}

		entry := AccountBackupEntry{
			ID:             strings.TrimSpace(account.ID),
			Email:          strings.TrimSpace(account.Email),
			Alias:          strings.TrimSpace(account.Alias),
			PlanType:       strings.TrimSpace(account.PlanType),
			AccountID:      strings.TrimSpace(account.AccountID),
			OrganizationID: strings.TrimSpace(account.OrganizationID),
			IDToken:        string(idTokenRaw),
			AccessToken:    string(accessTokenRaw),
			RefreshToken:   string(refreshTokenRaw),
			ActiveAPI:      account.ActiveAPI,
			ActiveCLI:      account.ActiveCLI,
		}
		if entry.ActiveAPI {
			payload.ActiveAPIAccount = entry.ID
		}
		if entry.ActiveCLI {
			payload.ActiveCLIAccount = entry.ID
		}
		if usage, ok := usageMap[account.ID]; ok {
			u := usage
			entry.Usage = &u
		}
		payload.Accounts = append(payload.Accounts, entry)
	}

	return payload, nil
}

func (s *Service) RestoreAccountsBackup(ctx context.Context, payload AccountsBackupPayload) (RestoreAccountsResult, error) {
	result := RestoreAccountsResult{}
	if v := strings.TrimSpace(payload.Version); v != "" && v != accountsBackupVersion {
		return result, fmt.Errorf("unsupported backup version: %s", v)
	}
	entries := payload.Accounts
	if len(entries) == 0 {
		return result, fmt.Errorf("backup does not contain accounts")
	}

	var (
		apiCandidate  string
		cliCandidate  string
		firstRestored string
		now           = time.Now().UTC()
	)
	restoredIDs := map[string]struct{}{}

	for idx, entry := range entries {
		idToken := strings.TrimSpace(entry.IDToken)
		accessToken := strings.TrimSpace(entry.AccessToken)
		if idToken == "" || accessToken == "" {
			result.Skipped++
			continue
		}

		accountID := strings.TrimSpace(entry.AccountID)
		organizationID := strings.TrimSpace(entry.OrganizationID)
		email := strings.TrimSpace(entry.Email)
		accountStorage := strings.TrimSpace(entry.ID)
		if accountStorage == "" {
			seedAccountID := firstNonEmpty(accountID, fmt.Sprintf("restored-%d", idx+1))
			accountStorage = accountStorageID(firstNonEmpty(email, seedAccountID), seedAccountID, organizationID)
		}

		encID, err := s.Crypto.Encrypt([]byte(idToken))
		if err != nil {
			return result, err
		}
		encAccess, err := s.Crypto.Encrypt([]byte(accessToken))
		if err != nil {
			return result, err
		}
		encRefresh, err := s.Crypto.Encrypt([]byte(strings.TrimSpace(entry.RefreshToken)))
		if err != nil {
			return result, err
		}

		account := store.Account{
			ID:             accountStorage,
			Email:          firstNonEmpty(email, accountStorage),
			Alias:          firstNonEmpty(strings.TrimSpace(entry.Alias), email, accountStorage),
			PlanType:       strings.TrimSpace(entry.PlanType),
			AccountID:      accountID,
			OrganizationID: organizationID,
			TokenID:        encID,
			TokenAccess:    encAccess,
			TokenRefresh:   encRefresh,
			CodexHome:      s.Cfg.CodexHome,
			CreatedAt:      now,
			UpdatedAt:      now,
			LastUsedAt:     now,
			Active:         false,
			ActiveAPI:      false,
			ActiveCLI:      false,
		}
		if err := s.Store.UpsertAccount(ctx, account); err != nil {
			return result, err
		}
		if err := util.WriteAuthJSON(s.accountDir(account.ID), idToken, accessToken, strings.TrimSpace(entry.RefreshToken), accountID); err != nil {
			return result, err
		}
		if entry.Usage != nil {
			u := *entry.Usage
			u.AccountID = account.ID
			if u.FetchedAt.IsZero() {
				u.FetchedAt = now
			}
			_ = s.Store.SaveUsage(ctx, u)
		}

		if firstRestored == "" {
			firstRestored = account.ID
		}
		restoredIDs[account.ID] = struct{}{}
		if entry.ActiveAPI {
			apiCandidate = account.ID
		}
		if entry.ActiveCLI {
			cliCandidate = account.ID
		}
		result.Restored++
	}

	if result.Restored == 0 {
		return result, fmt.Errorf("no valid accounts restored from backup")
	}

	pickRestoredCandidate := func(candidate string) string {
		id := strings.TrimSpace(candidate)
		if id == "" {
			return ""
		}
		if _, ok := restoredIDs[id]; ok {
			return id
		}
		return ""
	}

	apiCandidate = pickRestoredCandidate(apiCandidate)
	if apiCandidate == "" {
		apiCandidate = pickRestoredCandidate(payload.ActiveAPIAccount)
	}
	if apiCandidate == "" {
		apiCandidate = firstRestored
	}
	if strings.TrimSpace(apiCandidate) != "" {
		_, activateErr := s.UseAccountAPI(ctx, apiCandidate)
		if activateErr != nil {
			if apiCandidate != firstRestored {
				_, activateErr = s.UseAccountAPI(ctx, firstRestored)
			}
		}
		if activateErr != nil {
			return result, activateErr
		}
	}

	cliCandidate = pickRestoredCandidate(cliCandidate)
	if cliCandidate == "" {
		cliCandidate = pickRestoredCandidate(payload.ActiveCLIAccount)
	}
	if cliCandidate == "" {
		cliCandidate = firstRestored
	}
	if strings.TrimSpace(cliCandidate) != "" {
		_, activateErr := s.UseAccountCLI(ctx, cliCandidate)
		if activateErr != nil {
			if cliCandidate != firstRestored {
				_, activateErr = s.UseAccountCLI(ctx, firstRestored)
			}
		}
		if activateErr != nil {
			return result, activateErr
		}
	}

	return result, nil
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

type resolvedTokens struct {
	account store.Account
	tokens  TokenSet
}

func (s *Service) ensureFreshTokens(ctx context.Context, a store.Account, tk TokenSet) (resolvedTokens, error) {
	exp, err := util.AccessTokenExpiry(tk.AccessToken)
	if err != nil {
		return resolvedTokens{account: a, tokens: tk}, nil
	}
	if time.Until(exp) > 2*time.Minute {
		return resolvedTokens{account: a, tokens: tk}, nil
	}
	if strings.TrimSpace(tk.RefreshToken) == "" {
		return resolvedTokens{}, fmt.Errorf("access token expired and refresh token missing")
	}
	newTk, err := refreshAccessToken(ctx, tk.RefreshToken)
	if err != nil {
		return resolvedTokens{}, err
	}
	if tk.AccountID != "" && newTk.AccountID == "" {
		newTk.AccountID = tk.AccountID
	}
	encID, err := s.Crypto.Encrypt([]byte(newTk.IDToken))
	if err != nil {
		return resolvedTokens{}, err
	}
	encAccess, err := s.Crypto.Encrypt([]byte(newTk.AccessToken))
	if err != nil {
		return resolvedTokens{}, err
	}
	encRefresh, err := s.Crypto.Encrypt([]byte(newTk.RefreshToken))
	if err != nil {
		return resolvedTokens{}, err
	}
	a.TokenID = encID
	a.TokenAccess = encAccess
	a.TokenRefresh = encRefresh
	a.UpdatedAt = time.Now().UTC()
	if err := util.WriteAuthJSON(s.accountDir(a.ID), newTk.IDToken, newTk.AccessToken, newTk.RefreshToken, firstNonEmpty(newTk.AccountID, a.AccountID)); err != nil {
		return resolvedTokens{}, err
	}
	if err := s.Store.UpsertAccount(ctx, a); err != nil {
		return resolvedTokens{}, err
	}
	return resolvedTokens{account: a, tokens: newTk}, nil
}

func (s *Service) accountDir(id string) string {
	return filepath.Join(s.Cfg.AuthStoreDir, id)
}

// APICodexHome returns isolated CODEX_HOME path for proxy API execution.
// This prevents API traffic from mutating global CLI auth context.
func (s *Service) APICodexHome(accountID string) string {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return strings.TrimSpace(s.Cfg.CodexHome)
	}
	return s.accountDir(id)
}

func (s *Service) syncAccountAuthToCodexHome(a store.Account) error {
	s.cliAuthStateMu.Lock()
	defer s.cliAuthStateMu.Unlock()
	return s.syncAccountAuthToCodexHomeUnlocked(a)
}

func (s *Service) syncAccountAuthToCodexHomeUnlocked(a store.Account) error {
	src := filepath.Join(s.accountDir(a.ID), "auth.json")
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Cfg.CodexHome, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(s.Cfg.CodexHome, "auth.json")
	tmp, err := os.CreateTemp(s.Cfg.CodexHome, "auth.json.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	replaced := false
	var replaceErr error
	for attempt := 0; attempt < 5; attempt++ {
		replaceErr = os.Rename(tmpPath, dst)
		if replaceErr == nil {
			replaced = true
			break
		}
		if runtime.GOOS == "windows" {
			replaceErr = replaceFileWindowsPreservingExisting(tmpPath, dst)
			if replaceErr == nil {
				replaced = true
				break
			}
		}
		if replaced {
			break
		}
		if replaceErr == nil {
			replaceErr = fmt.Errorf("replace auth.json failed")
		}
		if !isTransientFileReplaceError(replaceErr) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	if !replaced {
		return replaceErr
	}
	if runtime.GOOS != "windows" {
		if err := syncDir(s.Cfg.CodexHome); err != nil {
			return err
		}
	}
	committed = true
	s.setCLIActiveCache(a.ID)
	return nil
}

func replaceFileWindowsPreservingExisting(tmpPath, dst string) error {
	backupPath := fmt.Sprintf("%s.bak-%d-%d", dst, os.Getpid(), time.Now().UnixNano())
	movedCurrent := false
	if _, statErr := os.Stat(dst); statErr == nil {
		if err := os.Rename(dst, backupPath); err != nil {
			return err
		}
		movedCurrent = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	replaceErr := os.Rename(tmpPath, dst)
	if replaceErr == nil {
		if movedCurrent {
			_ = os.Remove(backupPath)
		}
		return nil
	}
	if !movedCurrent {
		return replaceErr
	}
	if restoreErr := restoreFileWithRetry(backupPath, dst, 5); restoreErr != nil {
		return fmt.Errorf("%w (restore original auth.json: %v)", replaceErr, restoreErr)
	}
	return replaceErr
}

func restoreFileWithRetry(src, dst string, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
	}
	return err
}

func (s *Service) ActiveCLIAccountID(ctx context.Context) (string, error) {
	s.cliActiveMu.RLock()
	if s.cliActiveCachedID != "" && time.Since(s.cliActiveCachedAt) < cliActiveCacheTTL {
		id := s.cliActiveCachedID
		s.cliActiveMu.RUnlock()
		return id, nil
	}
	s.cliActiveMu.RUnlock()

	s.cliAuthStateMu.Lock()
	defer s.cliAuthStateMu.Unlock()

	selected, err := s.Store.ActiveCLIAccount(ctx)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "account not found") {
			return "", nil
		}
		return "", err
	}
	selectedID := strings.TrimSpace(selected.ID)
	if selectedID == "" {
		return "", nil
	}

	s.cliActiveMu.RLock()
	if s.cliActiveCachedID != "" && s.cliActiveCachedID == selectedID && time.Since(s.cliActiveCachedAt) < cliActiveCacheTTL {
		id := s.cliActiveCachedID
		s.cliActiveMu.RUnlock()
		return id, nil
	}
	s.cliActiveMu.RUnlock()

	needsHeal := false
	authPath := filepath.Join(s.Cfg.CodexHome, "auth.json")
	b, readErr := os.ReadFile(authPath)
	if readErr != nil {
		needsHeal = true
	} else {
		var f struct {
			IDToken     string `json:"id_token"`
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
			Tokens      struct {
				IDToken     string `json:"id_token"`
				AccessToken string `json:"access_token"`
				AccountID   string `json:"account_id"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(b, &f); err != nil {
			needsHeal = true
		} else {
			idToken := firstNonEmpty(f.Tokens.IDToken, f.IDToken)
			accessToken := firstNonEmpty(f.Tokens.AccessToken, f.AccessToken)
			authAccountID := firstNonEmpty(f.Tokens.AccountID, f.AccountID)
			if strings.TrimSpace(idToken) == "" || strings.TrimSpace(accessToken) == "" {
				needsHeal = true
			} else {
				matched := false
				accIDTokenRaw, decErr := s.Crypto.Decrypt(selected.TokenID)
				if decErr == nil {
					accAccessTokenRaw, decErr2 := s.Crypto.Decrypt(selected.TokenAccess)
					if decErr2 == nil && string(accIDTokenRaw) == idToken && string(accAccessTokenRaw) == accessToken {
						matched = true
					}
				}
				if !matched {
					claims, claimErr := util.ParseClaims(idToken, accessToken)
					if claimErr == nil {
						claimAccountID := firstNonEmpty(authAccountID, claims.AccountID)
						if claimAccountID != "" && selected.AccountID != "" && selected.AccountID == claimAccountID {
							matched = true
						}
						if claims.Email != "" && strings.EqualFold(selected.Email, claims.Email) {
							matched = true
						}
					}
				}
				needsHeal = !matched
			}
		}
	}
	if needsHeal {
		if err := s.syncAccountAuthToCodexHomeUnlocked(selected); err != nil {
			log.Printf("[cli-auth] active cli auth.json heal failed for %s: %v", selectedID, err)
		}
	}
	s.setCLIActiveCache(selected.ID)
	return selected.ID, nil
}

func (s *Service) setCLIActiveCache(id string) {
	s.cliActiveMu.Lock()
	s.cliActiveCachedID = strings.TrimSpace(id)
	s.cliActiveCachedAt = time.Now()
	s.cliActiveMu.Unlock()
}

func isTransientFileReplaceError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "resource busy") || strings.Contains(msg, "text file busy") {
		return true
	}
	if strings.Contains(msg, "used by another process") {
		return true
	}
	if strings.Contains(msg, "access is denied") {
		return true
	}
	if strings.Contains(msg, "permission denied") {
		return true
	}
	return false
}

func syncDir(dir string) error {
	path := strings.TrimSpace(dir)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (s *Service) notifyCLISwitch(ctx context.Context, fromID string, to store.Account) {
	cmd := strings.TrimSpace(s.Cfg.CLISwitchNotifyCmd)
	if cmd == "" {
		return
	}
	fromID = strings.TrimSpace(fromID)
	toID := strings.TrimSpace(to.ID)
	if fromID == "" || toID == "" || fromID == toID {
		return
	}
	reason := cliSwitchReason(ctx)
	if reason == "" {
		reason = "unknown"
	}
	env := []string{
		"CODEXSESS_CLI_SWITCH_FROM=" + fromID,
		"CODEXSESS_CLI_SWITCH_TO=" + toID,
		"CODEXSESS_CLI_SWITCH_REASON=" + reason,
	}
	if email := strings.TrimSpace(to.Email); email != "" {
		env = append(env, "CODEXSESS_CLI_SWITCH_TO_EMAIL="+email)
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := runNotifyCommand(runCtx, cmd, env); err != nil {
			log.Printf("[notify] cli switch command failed: %v", err)
		}
	}()
}

func runNotifyCommand(ctx context.Context, command string, extraEnv []string) error {
	cmdLine := strings.TrimSpace(command)
	if cmdLine == "" {
		return nil
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdLine)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdLine)
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
