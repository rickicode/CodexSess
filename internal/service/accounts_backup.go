package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

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

func (s *Service) ExportAccountTokens(ctx context.Context) ([]AccountTokenExportEntry, error) {
	accounts, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}

	type selectedEntry struct {
		entry    AccountTokenExportEntry
		freshest time.Time
	}

	selectFreshest := func(account store.Account) time.Time {
		if !account.LastUsedAt.IsZero() {
			return account.LastUsedAt
		}
		if !account.CreatedAt.IsZero() {
			return account.CreatedAt
		}
		return account.UpdatedAt
	}

	byEmail := make(map[string]selectedEntry, len(accounts))
	for _, account := range accounts {
		idTokenRaw, err := s.Crypto.Decrypt(account.TokenID)
		if err != nil {
			return nil, err
		}
		accessTokenRaw, err := s.Crypto.Decrypt(account.TokenAccess)
		if err != nil {
			return nil, err
		}
		refreshTokenRaw, err := s.Crypto.Decrypt(account.TokenRefresh)
		if err != nil {
			return nil, err
		}

		entry := AccountTokenExportEntry{
			Email:        strings.TrimSpace(account.Email),
			AccessToken:  string(accessTokenRaw),
			RefreshToken: string(refreshTokenRaw),
			IDToken:      string(idTokenRaw),
		}
		key := strings.ToLower(strings.TrimSpace(entry.Email))
		if key == "" {
			key = strings.TrimSpace(account.ID)
		}
		current := selectedEntry{
			entry:    entry,
			freshest: selectFreshest(account),
		}
		if existing, exists := byEmail[key]; exists && !current.freshest.After(existing.freshest) {
			continue
		}
		byEmail[key] = current
	}

	entries := make([]AccountTokenExportEntry, 0, len(byEmail))
	for _, selected := range byEmail {
		entries = append(entries, selected.entry)
	}

	return entries, nil
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
