package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

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
