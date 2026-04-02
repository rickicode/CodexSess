package httpapi

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
)

type cliSwitchStatus struct {
	At         int64  `json:"at"`
	From       string `json:"from"`
	To         string `json:"to"`
	Reason     string `json:"reason"`
	Strategy   string `json:"strategy"`
	Error      string `json:"error"`
	Candidates int    `json:"candidates"`
}

type autoSwitchCandidate struct {
	account store.Account
	usage   store.UsageSnapshot
	score   int
}

func (s *Server) autoSwitchAPIIfNeeded(ctx context.Context, threshold int) error {
	active, err := s.svc.Store.ActiveAccount(ctx)
	if err != nil {
		return nil
	}
	score, err := s.usageScoreForDecision(ctx, active.ID)
	if err != nil {
		s.svc.AddSystemLog(ctx, "api_autoswitch", "API autoswitch check failed", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"threshold": threshold,
			"error":     err.Error(),
		})
		return err
	}
	if score > threshold {
		s.svc.AddSystemLog(ctx, "api_autoswitch", "API autoswitch skipped", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"reason":    "threshold_ok",
			"score":     score,
			"threshold": threshold,
		})
		return nil
	}

	candidates, err := s.findAutoSwitchCandidatesFromDB(ctx, active.ID)
	if err != nil {
		s.svc.AddSystemLog(ctx, "api_autoswitch", "API autoswitch check failed", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"threshold": threshold,
			"score":     score,
			"error":     err.Error(),
		})
		return err
	}
	if len(candidates) == 0 {
		s.svc.AddSystemLog(ctx, "api_autoswitch", "API autoswitch skipped", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"reason":    "no_backup_candidate",
			"score":     score,
			"threshold": threshold,
		})
		return nil
	}
	return s.switchAPIActiveToCandidate(ctx, active, candidates, score, threshold, "threshold", "autoswitch")
}

func (s *Server) autoSwitchCLIActiveIfNeeded(ctx context.Context, threshold int) error {
	active, err := s.svc.Store.ActiveCLIAccount(ctx)
	if err != nil {
		return nil
	}
	score, err := s.usageScoreForDecision(ctx, active.ID)
	if err != nil {
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI autoswitch check failed", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"threshold": threshold,
			"error":     err.Error(),
		})
		return err
	}
	if score > threshold {
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI autoswitch skipped", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"reason":    "threshold_ok",
			"score":     score,
			"threshold": threshold,
		})
		return nil
	}

	candidates, err := s.findAutoSwitchCandidatesFromDB(ctx, active.ID)
	if err != nil {
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI autoswitch check failed", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"threshold": threshold,
			"score":     score,
			"error":     err.Error(),
		})
		return err
	}
	if len(candidates) == 0 {
		s.setCLISwitchStatus(cliSwitchStatus{
			At:         time.Now().UTC().UnixMilli(),
			From:       accountSwitchDisplayValue(active),
			Reason:     "skip",
			Strategy:   "threshold",
			Error:      "no_backup_candidate",
			Candidates: 0,
		})
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI autoswitch skipped", map[string]any{
			"from":      strings.TrimSpace(active.ID),
			"reason":    "no_backup_candidate",
			"score":     score,
			"threshold": threshold,
			"strategy":  "threshold",
		})
		return nil
	}
	return s.switchCLIActiveToCandidate(ctx, active, candidates, score, threshold, "threshold", "autoswitch")
}

func (s *Server) setCLISwitchStatus(status cliSwitchStatus) {
	s.cliSwitchStatusMu.Lock()
	s.cliSwitchStatus = status
	s.cliSwitchStatusMu.Unlock()
}

func (s *Server) getCLISwitchStatus() cliSwitchStatus {
	s.cliSwitchStatusMu.Lock()
	defer s.cliSwitchStatusMu.Unlock()
	return s.cliSwitchStatus
}

func (s *Server) usageScoreForDecision(ctx context.Context, accountID string) (int, error) {
	usage, err := s.loadUsageForDecision(ctx, accountID)
	if err != nil {
		return 0, err
	}
	if !usageAvailable(usage) {
		return 0, nil
	}
	return usageScore(usage), nil
}

func (s *Server) loadUsageForDecision(ctx context.Context, accountID string) (store.UsageSnapshot, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return store.UsageSnapshot{}, fmt.Errorf("account id is required")
	}
	usage, err := s.svc.Store.GetUsage(ctx, accountID)
	if err == nil {
		return usage, nil
	}
	return store.UsageSnapshot{}, err
}

func (s *Server) findBestUsageAccountAbove(ctx context.Context, skipID string, minScore int) (store.Account, int, bool, error) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil || len(accounts) == 0 {
		return store.Account{}, 0, false, err
	}
	best, bestScore, ok := pickBestUsageCandidateAbove(accounts, skipID, minScore, func(accountID string) (store.UsageSnapshot, error) {
		return s.loadUsageForDecision(ctx, accountID)
	})
	return best, bestScore, ok, nil
}

func (s *Server) findBestAutoSwitchAccountFromDB(ctx context.Context, skipID string) (store.Account, int, bool, error) {
	candidates, err := s.findAutoSwitchCandidatesFromDB(ctx, skipID)
	if err != nil {
		return store.Account{}, 0, false, err
	}
	if len(candidates) == 0 {
		return store.Account{}, 0, false, nil
	}
	return candidates[0].account, candidates[0].score, true, nil
}

func (s *Server) findAutoSwitchCandidatesFromDB(ctx context.Context, skipID string) ([]autoSwitchCandidate, error) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil || len(accounts) == 0 {
		return nil, err
	}
	return rankAutoSwitchCandidates(accounts, skipID, func(accountID string) (store.UsageSnapshot, error) {
		return s.loadUsageForDecision(ctx, accountID)
	}), nil
}

func pickBestUsageCandidateAbove(accounts []store.Account, skipID string, minScore int, loadUsage func(accountID string) (store.UsageSnapshot, error)) (store.Account, int, bool) {
	best := store.Account{}
	bestScore := -1
	for _, account := range accounts {
		if strings.TrimSpace(account.ID) == "" || account.ID == skipID || account.Revoked {
			continue
		}
		usage, err := loadUsage(account.ID)
		if err != nil || !usageAvailable(usage) || strings.TrimSpace(usage.LastError) != "" {
			continue
		}
		score := usageScore(usage)
		if score <= minScore || score <= bestScore {
			continue
		}
		best = account
		bestScore = score
	}
	if bestScore < 0 {
		return store.Account{}, 0, false
	}
	return best, bestScore, true
}

func autoSwitchBackupEligible(u store.UsageSnapshot) bool {
	if strings.TrimSpace(u.LastError) != "" {
		return false
	}
	return u.HourlyPct >= 80 || u.WeeklyPct >= 80
}

func autoSwitchBackupScore(u store.UsageSnapshot) int {
	if u.WeeklyPct > u.HourlyPct {
		return u.WeeklyPct
	}
	return u.HourlyPct
}

func pickBestAutoSwitchCandidate(accounts []store.Account, skipID string, loadUsage func(accountID string) (store.UsageSnapshot, error)) (store.Account, int, bool) {
	candidates := rankAutoSwitchCandidates(accounts, skipID, loadUsage)
	if len(candidates) == 0 {
		return store.Account{}, 0, false
	}
	return candidates[0].account, candidates[0].score, true
}

func rankAutoSwitchCandidates(accounts []store.Account, skipID string, loadUsage func(accountID string) (store.UsageSnapshot, error)) []autoSwitchCandidate {
	candidates := make([]autoSwitchCandidate, 0, len(accounts))
	for _, account := range accounts {
		if strings.TrimSpace(account.ID) == "" || account.ID == skipID || account.Revoked {
			continue
		}
		usage, err := loadUsage(account.ID)
		if err != nil || !autoSwitchBackupEligible(usage) {
			continue
		}
		candidates = append(candidates, autoSwitchCandidate{
			account: account,
			usage:   usage,
			score:   autoSwitchBackupScore(usage),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].usage.WeeklyPct != candidates[j].usage.WeeklyPct {
			return candidates[i].usage.WeeklyPct > candidates[j].usage.WeeklyPct
		}
		if candidates[i].usage.HourlyPct != candidates[j].usage.HourlyPct {
			return candidates[i].usage.HourlyPct > candidates[j].usage.HourlyPct
		}
		return strings.TrimSpace(candidates[i].account.ID) < strings.TrimSpace(candidates[j].account.ID)
	})
	return candidates
}

func accountSwitchDisplayValue(account store.Account) string {
	if email := strings.TrimSpace(account.Email); email != "" {
		return email
	}
	if alias := strings.TrimSpace(account.Alias); alias != "" {
		return alias
	}
	return strings.TrimSpace(account.ID)
}

func (s *Server) switchAPIActiveToCandidate(ctx context.Context, active store.Account, candidates []autoSwitchCandidate, score, threshold int, strategy, reason string) error {
	var lastErr error
	for idx, candidate := range candidates {
		switched, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(ctx, reason), candidate.account.ID)
		if err != nil {
			lastErr = err
			continue
		}
		s.svc.AddSystemLog(ctx, "api_autoswitch", "API autoswitch switched", map[string]any{
			"from":         strings.TrimSpace(active.ID),
			"from_email":   strings.TrimSpace(active.Email),
			"to":           strings.TrimSpace(switched.ID),
			"to_email":     strings.TrimSpace(switched.Email),
			"reason":       reason,
			"strategy":     strategy,
			"score":        score,
			"target_score": candidate.score,
			"threshold":    threshold,
			"attempt":      idx + 1,
			"candidates":   len(candidates),
		})
		log.Printf("[autoswitch] api active switched from %s to %s (remaining %d%% -> %d%%, threshold=%d%%)", accountSwitchDisplayValue(active), accountSwitchDisplayValue(switched), score, candidate.score, threshold)
		return nil
	}
	if lastErr != nil {
		s.svc.AddSystemLog(ctx, "api_autoswitch", "API autoswitch failed", map[string]any{
			"from":       strings.TrimSpace(active.ID),
			"from_email": strings.TrimSpace(active.Email),
			"reason":     reason,
			"strategy":   strategy,
			"score":      score,
			"threshold":  threshold,
			"candidates": len(candidates),
			"error":      lastErr.Error(),
		})
	}
	return lastErr
}

func (s *Server) switchCLIActiveToCandidate(ctx context.Context, active store.Account, candidates []autoSwitchCandidate, score, threshold int, strategy, reason string) error {
	var lastErr error
	for idx, candidate := range candidates {
		switched, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(ctx, reason), candidate.account.ID)
		if err != nil {
			lastErr = err
			continue
		}
		s.setCLISwitchStatus(cliSwitchStatus{
			At:         time.Now().UTC().UnixMilli(),
			From:       accountSwitchDisplayValue(active),
			To:         accountSwitchDisplayValue(switched),
			Reason:     reason,
			Strategy:   strategy,
			Candidates: len(candidates),
		})
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI autoswitch switched", map[string]any{
			"from":         strings.TrimSpace(active.ID),
			"from_email":   strings.TrimSpace(active.Email),
			"to":           strings.TrimSpace(switched.ID),
			"to_email":     strings.TrimSpace(switched.Email),
			"reason":       reason,
			"strategy":     strategy,
			"score":        score,
			"target_score": candidate.score,
			"threshold":    threshold,
			"attempt":      idx + 1,
			"candidates":   len(candidates),
		})
		log.Printf("[autoswitch] cli active switched from %s to %s (remaining %d%% -> %d%%, threshold=%d%%)", accountSwitchDisplayValue(active), accountSwitchDisplayValue(switched), score, candidate.score, threshold)
		return nil
	}
	if lastErr != nil {
		s.setCLISwitchStatus(cliSwitchStatus{
			At:         time.Now().UTC().UnixMilli(),
			From:       accountSwitchDisplayValue(active),
			Reason:     "skip",
			Strategy:   strategy,
			Error:      lastErr.Error(),
			Candidates: len(candidates),
		})
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI autoswitch failed", map[string]any{
			"from":       strings.TrimSpace(active.ID),
			"from_email": strings.TrimSpace(active.Email),
			"reason":     reason,
			"strategy":   strategy,
			"score":      score,
			"threshold":  threshold,
			"candidates": len(candidates),
			"error":      lastErr.Error(),
		})
	}
	return lastErr
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Server) resolveAPIAccount(ctx context.Context, selector string) (store.Account, error) {
	account, _, err := s.svc.ResolveForRequest(ctx, selector)
	if err != nil {
		return store.Account{}, err
	}

	var usageErr error
	var usage store.UsageSnapshot

	if account.Revoked {
		// If DB already marks it as revoked, skip OpenAI checks and trigger switch directly
		usageErr = nil
		usage = store.UsageSnapshot{LastError: "token_invalidated"} // Mock to force usageErrorLooksRevoked
	} else {
		usage, usageErr = s.loadOrRefreshUsage(ctx, account.ID)
	}

	if usageErr == nil && usageErrorLooksRevoked(usage.LastError) || account.Revoked {
		// Explicit selector should stay strict and not auto-switch.
		if strings.TrimSpace(selector) != "" {
			return store.Account{}, fmt.Errorf("target account token revoked")
		}

		best, ok := s.findBestUsageAccount(ctx, account.ID)
		if !ok {
			return store.Account{}, fmt.Errorf("all API accounts are revoked or exhausted")
		}
		switched, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(ctx, "autoswitch"), best.ID)
		if err != nil {
			return store.Account{}, err
		}
		account = switched
	} else if usageErr == nil && !usageAvailable(usage) {
		// Explicit selector should stay strict and not auto-switch.
		if strings.TrimSpace(selector) != "" {
			return store.Account{}, fmt.Errorf("target account quota exhausted")
		}

		best, ok := s.findBestUsageAccount(ctx, account.ID)
		if !ok {
			return store.Account{}, fmt.Errorf("all API accounts are exhausted")
		}
		switched, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(ctx, "autoswitch"), best.ID)
		if err != nil {
			return store.Account{}, err
		}
		account = switched
	}

	// Ensure auth context always matches resolved API account before the runtime call.
	// API traffic must not mutate global CLI auth selection.
	resolved, _, err := s.svc.ResolveForRequest(ctx, account.ID)
	if err != nil {
		return store.Account{}, err
	}
	return resolved, nil
}

func (s *Server) loadOrRefreshUsage(ctx context.Context, accountID string) (store.UsageSnapshot, error) {
	u, err := s.svc.Store.GetUsage(ctx, accountID)
	if err == nil && !u.FetchedAt.IsZero() && time.Since(u.FetchedAt) <= autoSwitchUsageFreshness {
		return u, nil
	}
	refreshed, refreshErr := s.svc.RefreshUsage(ctx, accountID)
	if refreshErr == nil {
		return refreshed, nil
	}
	if err == nil {
		return u, nil
	}
	return store.UsageSnapshot{}, refreshErr
}

func usageErrorLooksRevoked(raw string) bool {
	msg := strings.ToLower(strings.TrimSpace(raw))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "token_revoked") || strings.Contains(msg, "invalidated oauth token") {
		return true
	}
	return strings.Contains(msg, "status=401") && strings.Contains(msg, "oauth")
}

func (s *Server) markUsageLastError(ctx context.Context, accountID, lastError string) {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return
	}
	u, err := s.svc.Store.GetUsage(ctx, id)
	if err != nil {
		u = store.UsageSnapshot{
			AccountID: id,
			RawJSON:   "{}",
		}
	}
	u.AccountID = id
	u.FetchedAt = time.Now().UTC()
	u.LastError = strings.TrimSpace(lastError)
	if strings.TrimSpace(u.RawJSON) == "" {
		u.RawJSON = "{}"
	}
	_ = s.svc.Store.SaveUsage(ctx, u)
}

func usageAvailable(u store.UsageSnapshot) bool {
	return u.HourlyPct > 0 || u.WeeklyPct > 0
}

func usageScore(u store.UsageSnapshot) int {
	min := u.HourlyPct
	if u.WeeklyPct < min {
		min = u.WeeklyPct
	}
	if min < 0 {
		return 0
	}
	return min
}

func (s *Server) findBestUsageAccount(ctx context.Context, skipID string) (store.Account, bool) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil || len(accounts) == 0 {
		return store.Account{}, false
	}

	usageMap, _ := s.svc.Store.ListUsageSnapshots(ctx)
	var best store.Account
	bestScore := -1
	found := false

	for _, a := range accounts {
		if strings.TrimSpace(a.ID) == "" || a.ID == skipID || a.Revoked {
			continue
		}
		u, ok := usageMap[a.ID]
		if !ok || u.FetchedAt.IsZero() || time.Since(u.FetchedAt) > autoSwitchUsageFreshness {
			refreshed, refreshErr := s.loadOrRefreshUsage(ctx, a.ID)
			if refreshErr != nil {
				continue
			}
			u = refreshed
		}
		if strings.TrimSpace(u.LastError) != "" {
			continue
		}
		if !usageAvailable(u) {
			continue
		}
		score := usageScore(u)
		if score > bestScore {
			best = a
			bestScore = score
			found = true
		}
	}

	if found {
		return best, true
	}
	return store.Account{}, false
}
