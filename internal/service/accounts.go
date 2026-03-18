package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	cliActiveCachedID string
	cliActiveCachedAt time.Time
}

type TokenSet struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

func New(cfg config.Config, st *store.Store, cry *icrypto.Crypto) *Service {
	return &Service{
		Cfg:    cfg,
		Store:  st,
		Crypto: cry,
		Codex:  provider.NewCodexExec("codex"),
	}
}

func (s *Service) SaveAccountFromTokens(ctx context.Context, t TokenSet, _ string) (store.Account, error) {
	claims, err := util.ParseClaims(t.IDToken, t.AccessToken)
	if err != nil {
		return store.Account{}, err
	}
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
	}
	if err := s.Store.UpsertAccount(ctx, a); err != nil {
		return store.Account{}, err
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
	a, err := s.Store.FindAccountBySelector(ctx, selector)
	if err != nil {
		return store.Account{}, err
	}
	if err := s.Store.SetActiveAccount(ctx, a.ID); err != nil {
		return store.Account{}, err
	}
	return s.Store.FindAccountBySelector(ctx, a.ID)
}

func (s *Service) UseAccountCLI(ctx context.Context, selector string) (store.Account, error) {
	a, err := s.Store.FindAccountBySelector(ctx, selector)
	if err != nil {
		return store.Account{}, err
	}
	if err := s.syncAccountAuthToCodexHome(a); err != nil {
		return store.Account{}, err
	}
	s.setCLIActiveCache(a.ID)
	return s.Store.FindAccountBySelector(ctx, a.ID)
}

func (s *Service) RemoveAccount(ctx context.Context, selector string) error {
	a, err := s.Store.FindAccountBySelector(ctx, selector)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAccount(ctx, a.ID); err != nil {
		return err
	}
	_ = os.RemoveAll(s.accountDir(a.ID))
	return nil
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

func (s *Service) syncAccountAuthToCodexHome(a store.Account) error {
	src := filepath.Join(s.accountDir(a.ID), "auth.json")
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Cfg.CodexHome, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Cfg.CodexHome, "auth.json"), b, 0o600); err != nil {
		return err
	}
	s.setCLIActiveCache(a.ID)
	return nil
}

func (s *Service) ActiveCLIAccountID(ctx context.Context) (string, error) {
	s.cliActiveMu.RLock()
	if s.cliActiveCachedID != "" && time.Since(s.cliActiveCachedAt) < 5*time.Second {
		id := s.cliActiveCachedID
		s.cliActiveMu.RUnlock()
		return id, nil
	}
	s.cliActiveMu.RUnlock()

	authPath := filepath.Join(s.Cfg.CodexHome, "auth.json")
	b, err := os.ReadFile(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
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
		return "", nil
	}
	idToken := firstNonEmpty(f.Tokens.IDToken, f.IDToken)
	accessToken := firstNonEmpty(f.Tokens.AccessToken, f.AccessToken)
	authAccountID := firstNonEmpty(f.Tokens.AccountID, f.AccountID)
	if strings.TrimSpace(idToken) == "" || strings.TrimSpace(accessToken) == "" {
		return "", nil
	}
	claims, err := util.ParseClaims(idToken, accessToken)
	if err != nil {
		return "", nil
	}
	claimAccountID := firstNonEmpty(authAccountID, claims.AccountID)
	accounts, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return "", err
	}
	// Match exact token pair first to avoid false-positive email/account-id matches.
	for _, a := range accounts {
		accIDTokenRaw, err := s.Crypto.Decrypt(a.TokenID)
		if err != nil {
			continue
		}
		accAccessTokenRaw, err := s.Crypto.Decrypt(a.TokenAccess)
		if err != nil {
			continue
		}
		if string(accIDTokenRaw) == idToken && string(accAccessTokenRaw) == accessToken {
			s.setCLIActiveCache(a.ID)
			return a.ID, nil
		}
	}
	for _, a := range accounts {
		if claimAccountID != "" && a.AccountID != "" && a.AccountID == claimAccountID {
			s.setCLIActiveCache(a.ID)
			return a.ID, nil
		}
	}
	for _, a := range accounts {
		if claims.Email != "" && strings.EqualFold(a.Email, claims.Email) {
			s.setCLIActiveCache(a.ID)
			return a.ID, nil
		}
	}
	return "", nil
}

func (s *Service) setCLIActiveCache(id string) {
	s.cliActiveMu.Lock()
	s.cliActiveCachedID = strings.TrimSpace(id)
	s.cliActiveCachedAt = time.Now()
	s.cliActiveMu.Unlock()
}
