package httpapi

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/store"
)

type activeUsageRefreshResult struct {
	Refreshed []string
	APIFailed store.Account
	APIErr    error
	CLIFailed store.Account
	CLIErr    error
}

func (s *Server) saveUsageSchedulerCursor(ctx context.Context, cursor int) error {
	if s.svc == nil || s.svc.Store == nil {
		return nil
	}
	if cursor < 0 {
		cursor = 0
	}
	return s.svc.Store.SetSetting(ctx, store.SettingUsageCursor, strconv.Itoa(cursor))
}

func (s *Server) currentUsageSchedulerState() (bool, int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enabled := s.svc.Cfg.UsageSchedulerEnabled
	threshold := s.svc.Cfg.UsageAutoSwitchThreshold
	intervalMinutes := config.NormalizeUsageSchedulerIntervalMinutes(s.svc.Cfg.UsageSchedulerInterval)
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 100 {
		threshold = 100
	}
	return enabled, threshold, intervalMinutes
}

func (s *Server) currentUsageSchedulerTimeouts() (time.Duration, time.Duration) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	refreshSec := config.NormalizeUsageRefreshTimeoutSeconds(s.svc.Cfg.UsageRefreshTimeoutSec)
	switchSec := config.NormalizeUsageSwitchTimeoutSeconds(s.svc.Cfg.UsageSwitchTimeoutSec)
	return time.Duration(refreshSec) * time.Second, time.Duration(switchSec) * time.Second
}

//nolint:unused // kept as a focused one-shot scheduler entrypoint
func (s *Server) runUsageAutoSwitchActiveOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	s.lastActiveCheckAt.Store(time.Now().UTC().UnixMilli())
	s.runActiveUsageAutoSwitchTick(ctx)
}

func (s *Server) refreshUsageForActiveAccounts(ctx context.Context) activeUsageRefreshResult {
	refreshed := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	result := activeUsageRefreshResult{}

	appendRefreshed := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		refreshed = append(refreshed, id)
	}

	if active, err := s.svc.Store.ActiveAccount(ctx); err == nil {
		id := strings.TrimSpace(active.ID)
		if id != "" {
			if _, refreshErr := s.svc.RefreshUsage(ctx, id); refreshErr != nil {
				s.markUsageLastError(ctx, id, refreshErr.Error())
				s.svc.AddSystemLog(ctx, "usage_refresh", "Active API usage refresh failed", map[string]any{
					"source":   "autoswitch",
					"selector": id,
					"email":    strings.TrimSpace(active.Email),
					"type":     "api",
					"error":    refreshErr.Error(),
				})
				log.Printf("[autoswitch] active api usage refresh failed for %s: %v", accountSwitchDisplayValue(active), refreshErr)
				result.APIFailed = active
				result.APIErr = refreshErr
			} else {
				appendRefreshed(id)
			}
		}
	}

	if active, err := s.svc.Store.ActiveCLIAccount(ctx); err == nil {
		id := strings.TrimSpace(active.ID)
		if id != "" {
			if _, refreshErr := s.svc.RefreshUsage(ctx, id); refreshErr != nil {
				s.markUsageLastError(ctx, id, refreshErr.Error())
				s.svc.AddSystemLog(ctx, "usage_refresh", "Active CLI usage refresh failed", map[string]any{
					"source":   "autoswitch",
					"selector": id,
					"email":    strings.TrimSpace(active.Email),
					"type":     "cli",
					"error":    refreshErr.Error(),
				})
				log.Printf("[autoswitch] active cli usage refresh failed for %s: %v", accountSwitchDisplayValue(active), refreshErr)
				result.CLIFailed = active
				result.CLIErr = refreshErr
			} else {
				appendRefreshed(id)
			}
		}
	}

	result.Refreshed = refreshed
	return result
}

func (s *Server) runUsageSchedulerLoop(ctx context.Context) {
	s.runUsageSchedulerTick(ctx)
	ticker := time.NewTicker(usageSchedulerLoopPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runUsageSchedulerTickIfDue(ctx)
		}
	}
}

func (s *Server) runUsageSchedulerTickIfDue(parent context.Context) {
	enabled, _, intervalMinutes := s.currentUsageSchedulerState()
	if !enabled {
		return
	}
	lastCompletedMS := s.lastAllCheckAt.Load()
	if lastCompletedMS > 0 {
		lastTick := time.UnixMilli(lastCompletedMS)
		if time.Since(lastTick) < time.Duration(intervalMinutes)*time.Minute {
			return
		}
	}
	s.runUsageSchedulerTick(parent)
}

func (s *Server) runUsageSchedulerTick(parent context.Context) {
	enabled, _, intervalMinutes := s.currentUsageSchedulerState()
	if !enabled {
		return
	}
	if !s.usageSchedulerRunning.CompareAndSwap(false, true) {
		return
	}
	defer s.usageSchedulerRunning.Store(false)
	startedAt := time.Now().UTC()
	refreshTimeout, _ := s.currentUsageSchedulerTimeouts()
	nowMS := time.Now().UTC().UnixMilli()
	s.lastAllAttemptAt.Store(nowMS)
	log.Printf("[usage-scheduler] refresh tick started: parallel=%d timeout=%s interval=%dm", usageSchedulerParallelWorkers, refreshTimeout.String(), intervalMinutes)

	var tickErr bool
	refreshCtx, refreshCancel := context.WithCancel(parent)
	refreshed, total, err := s.refreshUsageAllAccounts(refreshCtx, usageSchedulerParallelWorkers, refreshTimeout)
	if err != nil {
		tickErr = true
		s.svc.AddSystemLog(refreshCtx, "usage_refresh", "Scheduled usage refresh failed", map[string]any{
			"all":          true,
			"source":       "auto",
			"parallel":     usageSchedulerParallelWorkers,
			"tick_seconds": intervalMinutes * 60,
			"error":        err.Error(),
		})
		log.Printf("[usage-scheduler] refresh tick failed: %v", err)
		log.Printf("[usage-scheduler] refresh tick completed: status=failed refreshed=%d total=%d duration=%s", refreshed, total, time.Since(startedAt).Round(time.Millisecond).String())
	} else {
		s.svc.AddSystemLog(refreshCtx, "usage_refresh", "Scheduled usage refresh", map[string]any{
			"all":          true,
			"source":       "auto",
			"refreshed":    refreshed,
			"total":        total,
			"parallel":     usageSchedulerParallelWorkers,
			"tick_seconds": intervalMinutes * 60,
		})
		log.Printf("[usage-scheduler] refresh tick completed: status=ok refreshed=%d total=%d duration=%s", refreshed, total, time.Since(startedAt).Round(time.Millisecond).String())
	}
	refreshCancel()
	completedMS := time.Now().UTC().UnixMilli()
	s.lastAllCheckAt.Store(completedMS)
	if tickErr {
		s.lastAllFailureAt.Store(completedMS)
		return
	}
}

func (s *Server) runActiveUsageAutoSwitchLoop(ctx context.Context) {
	s.runActiveUsageAutoSwitchTick(ctx)
	ticker := time.NewTicker(usageSchedulerLoopPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runActiveUsageAutoSwitchTickIfDue(ctx)
		}
	}
}

func (s *Server) runActiveUsageAutoSwitchTickIfDue(parent context.Context) {
	lastTickMS := s.lastActiveCheckAt.Load()
	if lastTickMS > 0 {
		lastTick := time.UnixMilli(lastTickMS)
		if time.Since(lastTick) < activeUsageCheckInterval {
			return
		}
	}
	s.runActiveUsageAutoSwitchTick(parent)
}

func (s *Server) runActiveUsageAutoSwitchTick(parent context.Context) {
	enabled, threshold, _ := s.currentUsageSchedulerState()
	if !enabled {
		return
	}
	_, switchTimeout := s.currentUsageSchedulerTimeouts()
	s.lastActiveCheckAt.Store(time.Now().UTC().UnixMilli())

	switchCtx, switchCancel := context.WithTimeout(parent, switchTimeout)
	defer switchCancel()

	refreshResult := s.refreshUsageForActiveAccounts(switchCtx)
	if len(refreshResult.Refreshed) > 0 {
		s.svc.AddSystemLog(switchCtx, "usage_refresh", "Active account usage refresh", map[string]any{
			"source":    "autoswitch",
			"selectors": refreshResult.Refreshed,
			"count":     len(refreshResult.Refreshed),
		})
	} else {
		s.svc.AddSystemLog(switchCtx, "usage_refresh", "Active account usage refresh skipped", map[string]any{
			"source": "autoswitch",
			"reason": "no_active_accounts_refreshed",
		})
	}

	if refreshResult.APIErr != nil && strings.TrimSpace(refreshResult.APIFailed.ID) != "" {
		candidates, err := s.findAutoSwitchCandidatesFromDB(switchCtx, refreshResult.APIFailed.ID)
		if err != nil {
			s.svc.AddSystemLog(switchCtx, "api_autoswitch", "API refresh-failure switch lookup failed", map[string]any{
				"from":       strings.TrimSpace(refreshResult.APIFailed.ID),
				"from_email": strings.TrimSpace(refreshResult.APIFailed.Email),
				"reason":     "refresh_failed",
				"strategy":   "refresh_failure",
				"error":      err.Error(),
			})
		} else if switchErr := s.switchAPIActiveToCandidate(switchCtx, refreshResult.APIFailed, candidates, 0, threshold, "refresh_failure", "refresh_failed"); switchErr != nil {
			log.Printf("[autoswitch] api refresh-failure recovery failed: %v", switchErr)
		}
	}
	if refreshResult.CLIErr != nil && strings.TrimSpace(refreshResult.CLIFailed.ID) != "" {
		candidates, err := s.findAutoSwitchCandidatesFromDB(switchCtx, refreshResult.CLIFailed.ID)
		if err != nil {
			s.svc.AddSystemLog(switchCtx, "cli_autoswitch", "CLI refresh-failure switch lookup failed", map[string]any{
				"from":       strings.TrimSpace(refreshResult.CLIFailed.ID),
				"from_email": strings.TrimSpace(refreshResult.CLIFailed.Email),
				"reason":     "refresh_failed",
				"strategy":   "refresh_failure",
				"error":      err.Error(),
			})
		} else if switchErr := s.switchCLIActiveToCandidate(switchCtx, refreshResult.CLIFailed, candidates, 0, threshold, "refresh_failure", "refresh_failed"); switchErr != nil {
			log.Printf("[autoswitch] cli refresh-failure recovery failed: %v", switchErr)
		}
	}

	if err := s.autoSwitchAPIIfNeeded(switchCtx, threshold); err != nil {
		s.svc.AddSystemLog(switchCtx, "api_autoswitch", "API autoswitch job failed", map[string]any{
			"threshold": threshold,
			"error":     err.Error(),
		})
		log.Printf("[autoswitch] api active check failed: %v", err)
	}
	if err := s.autoSwitchCLIActiveIfNeeded(switchCtx, threshold); err != nil {
		s.svc.AddSystemLog(switchCtx, "cli_autoswitch", "CLI autoswitch job failed", map[string]any{
			"threshold": threshold,
			"strategy":  "threshold",
			"error":     err.Error(),
		})
		log.Printf("[autoswitch] cli active check failed: %v", err)
	}

	s.lastActiveCheckAt.Store(time.Now().UTC().UnixMilli())
}

func (s *Server) refreshUsageAllAccounts(ctx context.Context, parallelWorkers int, perAccountTimeout time.Duration) (int, int, error) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil {
		return 0, 0, err
	}
	if len(accounts) == 0 {
		return 0, 0, nil
	}
	sort.Slice(accounts, func(i, j int) bool {
		return strings.TrimSpace(accounts[i].ID) < strings.TrimSpace(accounts[j].ID)
	})
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" || account.Revoked {
			continue
		}
		ids = append(ids, id)
	}
	total := len(ids)
	if total == 0 {
		return 0, 0, nil
	}
	if parallelWorkers < 1 {
		parallelWorkers = 1
	}
	if perAccountTimeout < 1*time.Second {
		perAccountTimeout = 15 * time.Second
	}

	groups := make([][]string, 0, parallelWorkers)
	for i := 0; i < parallelWorkers; i++ {
		groups = append(groups, make([]string, 0, total/parallelWorkers+1))
	}
	for i := 0; i < parallelWorkers; i++ {
		groups[i] = groups[i][:0]
	}
	for idx, id := range ids {
		workerIdx := idx % parallelWorkers
		groups[workerIdx] = append(groups[workerIdx], id)
	}
	planned := total
	if planned > 0 {
		s.svc.AddSystemLog(ctx, "usage_refresh_progress", "Scheduled usage refresh started", map[string]any{
			"source":    "auto",
			"total":     total,
			"planned":   planned,
			"checked":   0,
			"refreshed": 0,
			"parallel":  parallelWorkers,
		})
	}

	var wg sync.WaitGroup
	results := make(chan int, len(groups))
	var checked atomic.Int64
	var refreshedOK atomic.Int64
	for _, group := range groups {
		idsForWorker := group
		if len(idsForWorker) == 0 {
			continue
		}
		wg.Add(1)
		go func(workerIDs []string) {
			defer wg.Done()
			ok := 0
			for _, id := range workerIDs {
				if ctx.Err() != nil {
					break
				}
				accountCtx, cancel := context.WithTimeout(ctx, perAccountTimeout)
				_, err := s.svc.RefreshUsage(accountCtx, id)
				cancel()
				if err == nil {
					ok++
					refreshedOK.Add(1)
				} else {
					s.svc.AddSystemLog(ctx, "usage_refresh", "Scheduled usage refresh account failed", map[string]any{
						"source":     "auto",
						"account_id": id,
						"error":      err.Error(),
					})
				}
				done := checked.Add(1)
				if planned > 0 && (done == 1 || done == int64(planned) || done%10 == 0) {
					s.svc.AddSystemLog(ctx, "usage_refresh_progress", "Scheduled usage refresh in progress", map[string]any{
						"source":     "auto",
						"total":      total,
						"planned":    planned,
						"checked":    done,
						"refreshed":  refreshedOK.Load(),
						"account_id": id,
					})
				}
			}
			results <- ok
		}(idsForWorker)
	}
	wg.Wait()
	close(results)

	refreshed := 0
	for v := range results {
		refreshed += v
	}

	if ctx.Err() != nil {
		return refreshed, total, ctx.Err()
	}
	return refreshed, total, nil
}

func (s *Server) loadUsageSchedulerCursor(ctx context.Context) int {
	if s.svc == nil || s.svc.Store == nil {
		return 0
	}
	raw, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageCursor)
	if err != nil || !ok {
		return 0
	}
	v, parseErr := strconv.Atoi(strings.TrimSpace(raw))
	if parseErr != nil || v < 0 {
		return 0
	}
	return v
}
