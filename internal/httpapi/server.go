package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/trafficlog"
	"github.com/ricki/codexsess/internal/webui"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

type Server struct {
	svc               *service.Service
	apiKey            string
	bindAddr          string
	adminUsername     string
	adminPasswordHash string
	traffic           *trafficlog.Logger
	appVersion        string
	codexVersion      string

	updateMu              sync.Mutex
	updateCheckedAt       time.Time
	updateLatestVersion   string
	updateAvailable       bool
	updateReleaseURL      string
	updateLatestChangelog string
	updateCheckErrMessage string
	mu                    sync.RWMutex
	directRoundRobin      atomic.Uint64
	invalidToolCacheMu    sync.Mutex
	invalidToolCache      map[string]map[string]time.Time
	lastActiveCheckAt     atomic.Int64
	lastAllAttemptAt      atomic.Int64
	lastAllFailureAt      atomic.Int64
	lastAllCheckAt        atomic.Int64
	cliSwitchStatusMu     sync.Mutex
	cliSwitchStatus       cliSwitchStatus
}

const (
	maxTrafficRequestCaptureBytes  = 8 * 1024 * 1024
	maxTrafficResponseCaptureBytes = 8 * 1024 * 1024
	usageSchedulerWorkerPercent    = 10
	usageSchedulerParallelWorkers  = 2
	usageSchedulerLoopPollInterval = 1 * time.Minute
	usageSchedulerRetryCooldown    = 2 * time.Minute
	autoSwitchUsageFreshness       = 5 * time.Minute
	invalidToolCacheTTL            = 20 * time.Minute
	claudeTokenSoftLimitDefault    = 14000
	claudeTokenHardLimitDefault    = 22000
)

var (
	claudeTraceCallLinePattern = regexp.MustCompile(`(?i)^(?:explore|read|skill)\([^)]*\)\s*$`)
	claudeTaskCountLinePattern = regexp.MustCompile(`^\d+\s+tasks\s*\(`)
)

func New(svc *service.Service, bindAddr string, apiKey string, adminUsername string, adminPasswordHash string, traffic *trafficlog.Logger, appVersion string, codexVersion string) *Server {
	return &Server{
		svc:               svc,
		bindAddr:          bindAddr,
		apiKey:            apiKey,
		adminUsername:     strings.TrimSpace(adminUsername),
		adminPasswordHash: strings.TrimSpace(adminPasswordHash),
		traffic:           traffic,
		appVersion:        normalizeVersionString(appVersion),
		codexVersion:      strings.TrimSpace(codexVersion),
		invalidToolCache:  make(map[string]map[string]time.Time),
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.bootstrapSettingsFromStore(ctx); err != nil {
		log.Printf("settings store bootstrap failed: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts", s.handleWebAccounts)
	mux.HandleFunc("/api/accounts/total", s.handleWebAccountsTotal)
	mux.HandleFunc("/api/accounts/revoked", s.handleDeleteRevokedAccounts)
	mux.HandleFunc("/api/account/use", s.handleWebUseAccount)
	mux.HandleFunc("/api/account/use-api", s.handleWebUseAPIAccount)
	mux.HandleFunc("/api/account/use-cli", s.handleWebUseCLIAccount)
	mux.HandleFunc("/api/account/remove", s.handleWebRemoveAccount)
	mux.HandleFunc("/api/account/import", s.handleWebImportAccount)
	mux.HandleFunc("/api/accounts/backup", s.handleWebBackupAccounts)
	mux.HandleFunc("/api/accounts/restore", s.handleWebRestoreAccounts)
	mux.HandleFunc("/api/usage/refresh", s.handleWebRefreshUsage)
	mux.HandleFunc("/api/usage/automation", s.handleWebUsageAutomationStatus)
	mux.HandleFunc("/api/system/logs", s.handleWebSystemLogs)
	mux.HandleFunc("/api/settings", s.handleWebSettings)
	mux.HandleFunc("/api/settings/claude-code", s.handleWebClaudeCodeSettings)
	mux.HandleFunc("/api/settings/api-key", s.handleWebUpdateAPIKey)
	mux.HandleFunc("/api/zo/keys", s.handleWebZoKeys)
	mux.HandleFunc("/api/zo/keys/activate", s.handleWebZoKeyActivate)
	mux.HandleFunc("/api/zo/keys/delete", s.handleWebZoKeyDelete)
	mux.HandleFunc("/api/zo/keys/reset", s.handleWebZoKeyReset)
	mux.HandleFunc("/api/zo/keys/strategy", s.handleWebZoKeyStrategy)
	mux.HandleFunc("/api/version/check", s.handleWebVersionCheck)
	mux.HandleFunc("/api/model-mappings", s.handleWebModelMappings)
	mux.HandleFunc("/api/logs", s.handleWebLogs)
	mux.HandleFunc("/api/coding/sessions", s.handleWebCodingSessions)
	mux.HandleFunc("/api/coding/messages", s.handleWebCodingMessages)
	mux.HandleFunc("/api/coding/status", s.handleWebCodingStatus)
	mux.HandleFunc("/api/coding/stop", s.handleWebCodingStop)
	mux.HandleFunc("/api/coding/ws", s.handleWebCodingWS)
	mux.HandleFunc("/api/coding/chat", s.handleWebCodingChat)
	mux.HandleFunc("/api/coding/sessions/runtime", s.handleWebCodingSessionRuntime)
	mux.HandleFunc("/api/coding/runtime/status", s.handleWebCodingRuntimeStatus)
	mux.HandleFunc("/api/coding/runtime/restart", s.handleWebCodingRuntimeRestart)
	mux.HandleFunc("/api/coding/path-suggestions", s.handleWebCodingPathSuggestions)
	mux.HandleFunc("/api/coding/skills", s.handleWebCodingSkills)
	mux.HandleFunc("/api/auth/browser/start", s.handleWebBrowserStart)
	mux.HandleFunc("/api/auth/browser/cancel", s.handleWebBrowserCancel)
	mux.HandleFunc("/api/auth/browser/callback", s.handleWebBrowserCallback)
	mux.HandleFunc("/api/auth/browser/complete", s.handleWebBrowserComplete)
	mux.HandleFunc("/api/auth/login", s.handleAPIAuthLogin)
	mux.HandleFunc("/auth/callback", s.handleWebBrowserCallback)
	mux.HandleFunc("/auth/login", s.handleWebAuthLogin)
	mux.HandleFunc("/auth/logout", s.handleWebAuthLogout)
	mux.HandleFunc("/api/auth/device/start", s.handleWebDeviceStart)
	mux.HandleFunc("/api/auth/device/poll", s.handleWebDevicePoll)
	mux.HandleFunc("/api/events/log", s.handleWebClientEventLog)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/v1/models", s.withTrafficLog("openai", s.handleModels))
	mux.HandleFunc("/v1", s.withTrafficLog("openai", s.handleOpenAIV1Root))
	mux.HandleFunc("/v1/chat/completions", s.withTrafficLog("openai", s.handleChatCompletions))
	mux.HandleFunc("/v1/responses", s.withTrafficLog("openai", s.handleResponses))
	mux.HandleFunc("/v1/auth.json", s.handleAPIAuthJSON)
	mux.HandleFunc("/v1/usage", s.withTrafficLog("openai", s.handleAPIUsageStatus))
	mux.HandleFunc("/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.HandleFunc("/claude/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.HandleFunc("/zo/v1/models", s.withTrafficLog("zo", s.handleZoModels))
	mux.HandleFunc("/zo/v1", s.withTrafficLog("zo", s.handleZoV1Root))
	mux.HandleFunc("/zo/v1/chat/completions", s.withTrafficLog("zo", s.handleZoChatCompletions))
	mux.HandleFunc("/zo/v1/responses", s.withTrafficLog("zo", s.handleZoNotSupported))
	mux.HandleFunc("/zo/v1/messages", s.withTrafficLog("zo", s.handleZoNotSupported))
	mux.Handle("/", webui.Handler())
	handler := s.withAccessLog(withCORS(s.withManagementAuth(mux)))
	srv := &http.Server{Addr: s.bindAddr, Handler: handler}
	go s.runUsageAutoSwitchLoop(ctx)
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}

func (s *Server) runUsageAutoSwitchLoop(ctx context.Context) {
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
	enabled, _, _, intervalMinutes := s.currentUsageSchedulerState()
	if !enabled {
		return
	}
	lastFailureMS := s.lastAllFailureAt.Load()
	lastSuccessMS := s.lastAllCheckAt.Load()
	if lastFailureMS > 0 && (lastSuccessMS == 0 || lastFailureMS > lastSuccessMS) {
		lastFailure := time.UnixMilli(lastFailureMS)
		if time.Since(lastFailure) < usageSchedulerRetryCooldown {
			return
		}
	}
	lastTickMS := s.lastAllCheckAt.Load()
	if lastTickMS > 0 {
		lastTick := time.UnixMilli(lastTickMS)
		if time.Since(lastTick) < time.Duration(intervalMinutes)*time.Minute {
			return
		}
	}
	s.runUsageSchedulerTick(parent)
}

func (s *Server) runUsageSchedulerTick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	enabled, threshold, cliStrategy, intervalMinutes := s.currentUsageSchedulerState()
	if !enabled {
		return
	}
	nowMS := time.Now().UTC().UnixMilli()
	s.lastAllAttemptAt.Store(nowMS)
	s.lastActiveCheckAt.Store(nowMS)

	var tickErr bool
	refreshed, total, nextCursor, err := s.refreshUsageBatchedByCursor(ctx, usageSchedulerWorkerPercent, usageSchedulerParallelWorkers)
	if err != nil {
		tickErr = true
		log.Printf("[usage-scheduler] refresh tick failed: %v", err)
	} else {
		s.svc.AddSystemLog(ctx, "usage_refresh", "Scheduled usage refresh", map[string]any{
			"all":          false,
			"source":       "auto",
			"refreshed":    refreshed,
			"total":        total,
			"cursor_next":  nextCursor,
			"parallel":     usageSchedulerParallelWorkers,
			"batch_pct":    usageSchedulerWorkerPercent * usageSchedulerParallelWorkers,
			"tick_seconds": intervalMinutes * 60,
		})
	}

	if err := s.autoSwitchAPIIfNeeded(ctx, threshold); err != nil {
		tickErr = true
		log.Printf("[autoswitch] api check failed: %v", err)
	}
	if err := s.autoSwitchCLIIfNeeded(ctx, threshold, cliStrategy); err != nil {
		tickErr = true
		log.Printf("[autoswitch] cli check failed: %v", err)
	}
	if tickErr {
		s.lastAllFailureAt.Store(time.Now().UTC().UnixMilli())
		return
	}
	successMS := time.Now().UTC().UnixMilli()
	s.lastAllCheckAt.Store(successMS)
	s.lastActiveCheckAt.Store(successMS)
}

func (s *Server) refreshUsageBatchedByCursor(ctx context.Context, workerPercent int, parallelWorkers int) (int, int, int, error) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(accounts) == 0 {
		return 0, 0, 0, nil
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
		return 0, 0, 0, nil
	}
	if workerPercent <= 0 {
		workerPercent = 10
	}
	if parallelWorkers < 1 {
		parallelWorkers = 1
	}
	chunkSize := (total*workerPercent + 99) / 100
	if chunkSize < 1 {
		chunkSize = 1
	}
	cursor := s.loadUsageSchedulerCursor(ctx)
	if cursor < 0 {
		cursor = 0
	}
	cursor = cursor % total

	groups := make([][]string, 0, parallelWorkers)
	used := map[string]struct{}{}
	offset := cursor
	advance := 0
	for i := 0; i < parallelWorkers; i++ {
		group := make([]string, 0, chunkSize)
		for step := 0; step < chunkSize; step++ {
			idx := (offset + step) % total
			id := ids[idx]
			if _, exists := used[id]; exists {
				continue
			}
			used[id] = struct{}{}
			group = append(group, id)
		}
		groups = append(groups, group)
		offset = (offset + chunkSize) % total
		advance += chunkSize
	}
	planned := len(used)
	if planned > 0 {
		s.svc.AddSystemLog(ctx, "usage_refresh_progress", "Scheduled usage refresh started", map[string]any{
			"source":      "auto",
			"total":       total,
			"planned":     planned,
			"checked":     0,
			"refreshed":   0,
			"cursor_from": cursor,
			"parallel":    parallelWorkers,
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
				if _, err := s.svc.RefreshUsage(ctx, id); err == nil {
					ok++
					refreshedOK.Add(1)
				}
				done := checked.Add(1)
				if planned > 0 && (done == 1 || done == int64(planned) || done%10 == 0) {
					s.svc.AddSystemLog(ctx, "usage_refresh_progress", "Scheduled usage refresh in progress", map[string]any{
						"source":      "auto",
						"total":       total,
						"planned":     planned,
						"checked":     done,
						"refreshed":   refreshedOK.Load(),
						"cursor_from": cursor,
						"account_id":  id,
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
	nextCursor := (cursor + advance) % total
	if err := s.saveUsageSchedulerCursor(ctx, nextCursor); err != nil {
		return refreshed, total, cursor, err
	}
	return refreshed, total, nextCursor, nil
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

func (s *Server) saveUsageSchedulerCursor(ctx context.Context, cursor int) error {
	if s.svc == nil || s.svc.Store == nil {
		return nil
	}
	if cursor < 0 {
		cursor = 0
	}
	return s.svc.Store.SetSetting(ctx, store.SettingUsageCursor, strconv.Itoa(cursor))
}

func (s *Server) currentUsageSchedulerState() (bool, int, string, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enabled := true
	threshold := s.svc.Cfg.UsageAutoSwitchThreshold
	cliStrategy := config.NormalizeCodingCLIStrategy(s.svc.Cfg.CodingCLIStrategy)
	intervalMinutes := config.NormalizeUsageSchedulerIntervalMinutes(s.svc.Cfg.UsageSchedulerInterval)
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 100 {
		threshold = 100
	}
	return enabled, threshold, cliStrategy, intervalMinutes
}

func (s *Server) runUsageAutoSwitchActiveOnce(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	s.lastActiveCheckAt.Store(time.Now().UTC().UnixMilli())
	enabled, threshold, cliStrategy, _ := s.currentUsageSchedulerState()
	if !enabled {
		return
	}

	if err := s.autoSwitchAPIIfNeeded(ctx, threshold); err != nil {
		log.Printf("[autoswitch] api check failed: %v", err)
	}
	if err := s.autoSwitchCLIIfNeeded(ctx, threshold, cliStrategy); err != nil {
		log.Printf("[autoswitch] cli check failed: %v", err)
	}
}

func (s *Server) refreshUsageForActiveAccounts(ctx context.Context) {
	_ = ctx
}

func (s *Server) autoSwitchAPIIfNeeded(ctx context.Context, threshold int) error {
	active, err := s.svc.Store.ActiveAccount(ctx)
	if err != nil {
		return nil
	}
	score, err := s.usageScoreForDecision(ctx, active.ID)
	if err != nil {
		return err
	}
	if score > threshold {
		return nil
	}

	target, targetScore, ok, err := s.findBestUsageAccountAbove(ctx, active.ID, maxInt(threshold, score))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(ctx, "autoswitch"), target.ID); err != nil {
		return err
	}
	log.Printf("[autoswitch] api active switched from %s to %s (remaining %d%% -> %d%%, threshold=%d%%)", active.ID, target.ID, score, targetScore, threshold)
	return nil
}

func (s *Server) autoSwitchCLIIfNeeded(ctx context.Context, threshold int, cliStrategy string) error {
	cliID, err := s.svc.ActiveCLIAccountID(ctx)
	if err != nil {
		return err
	}
	activeID := strings.TrimSpace(cliID)
	active := store.Account{}
	hasActive := false
	if activeID != "" {
		if found, findErr := s.svc.Store.FindAccountBySelector(ctx, activeID); findErr == nil {
			active = found
			hasActive = true
		}
	}
	strategy := config.NormalizeCodingCLIStrategy(cliStrategy)
	if strategy == "round_robin" {
		candidates, loadErr := s.listCLICandidatesForRoundRobin(ctx)
		if loadErr != nil {
			return loadErr
		}
		if len(candidates) <= 1 {
			if !hasActive && len(candidates) == 1 && strings.TrimSpace(candidates[0].account.ID) != "" {
				target := candidates[0].account
				if _, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(ctx, "autoswitch"), target.ID); err == nil {
					s.setCLISwitchStatus(cliSwitchStatus{
						At:         time.Now().UTC().UnixMilli(),
						From:       "",
						To:         strings.TrimSpace(target.ID),
						Reason:     "recover_empty_active_cli",
						Strategy:   "round_robin",
						Candidates: 1,
					})
					s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI active recovered", map[string]any{
						"from":       "",
						"to":         strings.TrimSpace(target.ID),
						"reason":     "recover_empty_active_cli",
						"strategy":   "round_robin",
						"candidates": 1,
					})
				} else {
					s.setCLISwitchStatus(cliSwitchStatus{
						At:         time.Now().UTC().UnixMilli(),
						From:       "",
						To:         strings.TrimSpace(target.ID),
						Reason:     "recover_empty_active_cli",
						Strategy:   "round_robin",
						Error:      err.Error(),
						Candidates: 1,
					})
					s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI active recovery failed", map[string]any{
						"from":       "",
						"to":         strings.TrimSpace(target.ID),
						"reason":     "recover_empty_active_cli",
						"strategy":   "round_robin",
						"error":      err.Error(),
						"candidates": 1,
					})
				}
				return nil
			}
			s.setCLISwitchStatus(cliSwitchStatus{
				At:         time.Now().UTC().UnixMilli(),
				From:       strings.TrimSpace(activeID),
				Reason:     "skip",
				Strategy:   "round_robin",
				Error:      "candidates<=1",
				Candidates: len(candidates),
			})
			s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI round robin skipped", map[string]any{
				"from":       strings.TrimSpace(activeID),
				"reason":     "candidates<=1",
				"strategy":   "round_robin",
				"candidates": len(candidates),
			})
			log.Printf("[autoswitch] cli round_robin skipped: candidates=%d", len(candidates))
			return nil
		}
		if len(candidates) > 1 {
			log.Printf("[autoswitch] cli round_robin candidates: %s", formatCLICandidates(candidates))
			tried := map[string]struct{}{}
			var lastErr error
			switched := false
			for len(tried) < len(candidates) {
				target, ok := pickWeightedRandomCLICandidateExcluding(candidates, activeID, tried)
				if !ok || strings.TrimSpace(target.ID) == "" {
					break
				}
				tried[strings.TrimSpace(target.ID)] = struct{}{}
				if hasActive && strings.TrimSpace(target.ID) == strings.TrimSpace(active.ID) {
					continue
				}
				if _, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(ctx, "autoswitch"), target.ID); err != nil {
					lastErr = err
					continue
				}
				reason := "autoswitch"
				if !hasActive {
					reason = "recover_empty_active_cli"
				}
				s.setCLISwitchStatus(cliSwitchStatus{
					At:         time.Now().UTC().UnixMilli(),
					From:       strings.TrimSpace(activeID),
					To:         strings.TrimSpace(target.ID),
					Reason:     reason,
					Strategy:   "round_robin",
					Candidates: len(candidates),
				})
				s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI round robin switched", map[string]any{
					"from":       strings.TrimSpace(activeID),
					"to":         strings.TrimSpace(target.ID),
					"reason":     reason,
					"strategy":   "round_robin",
					"candidates": len(candidates),
				})
				log.Printf("[autoswitch] cli active switched from %s to %s (strategy=round_robin)", activeID, target.ID)
				switched = true
				break
			}
			if switched {
				return nil
			}
			if lastErr != nil {
				s.setCLISwitchStatus(cliSwitchStatus{
					At:         time.Now().UTC().UnixMilli(),
					From:       strings.TrimSpace(activeID),
					Reason:     "skip",
					Strategy:   "round_robin",
					Error:      lastErr.Error(),
					Candidates: len(candidates),
				})
				s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI round robin switch failed", map[string]any{
					"from":       strings.TrimSpace(activeID),
					"reason":     "switch_failed_all_candidates",
					"error":      lastErr.Error(),
					"strategy":   "round_robin",
					"candidates": len(candidates),
				})
				return nil
			}
			target, ok := pickWeightedRandomCLICandidate(candidates, activeID)
			if !ok || strings.TrimSpace(target.ID) == "" {
				s.setCLISwitchStatus(cliSwitchStatus{
					At:         time.Now().UTC().UnixMilli(),
					From:       strings.TrimSpace(activeID),
					Reason:     "skip",
					Strategy:   "round_robin",
					Error:      "no target",
					Candidates: len(candidates),
				})
				s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI round robin skipped", map[string]any{
					"from":       strings.TrimSpace(activeID),
					"reason":     "no_target",
					"strategy":   "round_robin",
					"candidates": len(candidates),
				})
				log.Printf("[autoswitch] cli round_robin skipped: no target")
				return nil
			}
			if hasActive && strings.TrimSpace(target.ID) == strings.TrimSpace(active.ID) {
				s.setCLISwitchStatus(cliSwitchStatus{
					At:         time.Now().UTC().UnixMilli(),
					From:       strings.TrimSpace(activeID),
					To:         strings.TrimSpace(target.ID),
					Reason:     "skip",
					Strategy:   "round_robin",
					Error:      "target is current",
					Candidates: len(candidates),
				})
				s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI round robin skipped", map[string]any{
					"from":       strings.TrimSpace(activeID),
					"to":         strings.TrimSpace(target.ID),
					"reason":     "target_is_current",
					"strategy":   "round_robin",
					"candidates": len(candidates),
				})
				log.Printf("[autoswitch] cli round_robin skipped: target is current (%s)", strings.TrimSpace(active.ID))
				return nil
			}
			return nil
		}
		return nil
	}
	if !hasActive {
		candidates, loadErr := s.listCLICandidatesWithUsage(ctx)
		if loadErr != nil {
			return loadErr
		}
		if len(candidates) == 0 {
			candidates, loadErr = s.listCLICandidatesForRoundRobin(ctx)
			if loadErr != nil {
				return loadErr
			}
		}
		tried := map[string]struct{}{}
		var target store.Account
		var lastErr error
		switched := false
		for len(tried) < len(candidates) {
			next, ok := pickWeightedRandomCLICandidateExcluding(candidates, "", tried)
			if !ok || strings.TrimSpace(next.ID) == "" {
				break
			}
			tried[strings.TrimSpace(next.ID)] = struct{}{}
			target = next
			if _, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(ctx, "autoswitch"), target.ID); err != nil {
				lastErr = err
				continue
			}
			switched = true
			break
		}
		if !switched {
			if lastErr != nil && strings.TrimSpace(target.ID) != "" {
				s.setCLISwitchStatus(cliSwitchStatus{
					At:         time.Now().UTC().UnixMilli(),
					From:       "",
					To:         strings.TrimSpace(target.ID),
					Reason:     "recover_empty_active_cli",
					Strategy:   "threshold",
					Error:      lastErr.Error(),
					Candidates: len(candidates),
				})
				s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI active recovery failed", map[string]any{
					"from":       "",
					"to":         strings.TrimSpace(target.ID),
					"reason":     "recover_empty_active_cli",
					"strategy":   "threshold",
					"error":      lastErr.Error(),
					"candidates": len(candidates),
				})
			}
			return nil
		}
		s.setCLISwitchStatus(cliSwitchStatus{
			At:         time.Now().UTC().UnixMilli(),
			From:       "",
			To:         strings.TrimSpace(target.ID),
			Reason:     "recover_empty_active_cli",
			Strategy:   "threshold",
			Candidates: len(candidates),
		})
		s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI active recovered", map[string]any{
			"from":       "",
			"to":         strings.TrimSpace(target.ID),
			"reason":     "recover_empty_active_cli",
			"strategy":   "threshold",
			"candidates": len(candidates),
		})
		return nil
	}

	score, err := s.usageScoreForDecision(ctx, active.ID)
	if err != nil {
		return err
	}
	if score > threshold {
		return nil
	}

	target, targetScore, ok, err := s.findBestUsageAccountAbove(ctx, active.ID, maxInt(threshold, score))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(ctx, "autoswitch"), target.ID); err != nil {
		return err
	}
	s.setCLISwitchStatus(cliSwitchStatus{
		At:         time.Now().UTC().UnixMilli(),
		From:       strings.TrimSpace(active.ID),
		To:         strings.TrimSpace(target.ID),
		Reason:     "autoswitch",
		Strategy:   "threshold",
		Candidates: 0,
	})
	s.svc.AddSystemLog(ctx, "cli_autoswitch", "CLI threshold switched", map[string]any{
		"from":     strings.TrimSpace(active.ID),
		"to":       strings.TrimSpace(target.ID),
		"reason":   "autoswitch",
		"strategy": "threshold",
		"score":    targetScore,
	})
	log.Printf("[autoswitch] cli active switched from %s to %s (remaining %d%% -> %d%%, threshold=%d%%)", active.ID, target.ID, score, targetScore, threshold)
	return nil
}

type cliUsageCandidate struct {
	account store.Account
	score   int
}

type cliSwitchStatus struct {
	At         int64  `json:"at"`
	From       string `json:"from"`
	To         string `json:"to"`
	Reason     string `json:"reason"`
	Strategy   string `json:"strategy"`
	Error      string `json:"error"`
	Candidates int    `json:"candidates"`
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

func (s *Server) listCLICandidatesWithUsage(ctx context.Context) ([]cliUsageCandidate, error) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]cliUsageCandidate, 0, len(accounts))
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" || account.Revoked {
			continue
		}
		score, scoreErr := s.usageScoreForDecision(ctx, id)
		if scoreErr != nil || score <= 0 {
			continue
		}
		candidates = append(candidates, cliUsageCandidate{
			account: account,
			score:   score,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		a := candidates[i].account
		b := candidates[j].account
		if !a.CreatedAt.IsZero() && !b.CreatedAt.IsZero() && !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return strings.TrimSpace(a.ID) < strings.TrimSpace(b.ID)
	})
	return candidates, nil
}

func (s *Server) listCLICandidatesForRoundRobin(ctx context.Context) ([]cliUsageCandidate, error) {
	accounts, err := s.svc.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]cliUsageCandidate, 0, len(accounts))
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" || account.Revoked {
			continue
		}
		candidates = append(candidates, cliUsageCandidate{
			account: account,
			score:   1,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		a := candidates[i].account
		b := candidates[j].account
		if !a.CreatedAt.IsZero() && !b.CreatedAt.IsZero() && !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return strings.TrimSpace(a.ID) < strings.TrimSpace(b.ID)
	})
	return candidates, nil
}

func pickNextRoundRobinCLICandidate(candidates []cliUsageCandidate, currentID string) (store.Account, bool) {
	if len(candidates) == 0 {
		return store.Account{}, false
	}
	current := strings.TrimSpace(currentID)
	start := -1
	for idx, item := range candidates {
		if strings.TrimSpace(item.account.ID) == current {
			start = idx
			break
		}
	}
	for step := 1; step <= len(candidates); step++ {
		idx := (start + step + len(candidates)) % len(candidates)
		item := candidates[idx]
		if strings.TrimSpace(item.account.ID) == current {
			continue
		}
		if strings.TrimSpace(item.account.ID) == "" {
			continue
		}
		return item.account, true
	}
	return store.Account{}, false
}

func pickPreferredCLICandidate(candidates []cliUsageCandidate, currentID string) (store.Account, bool) {
	if len(candidates) == 0 {
		return store.Account{}, false
	}
	minScore := candidates[0].score
	maxScore := candidates[0].score
	for _, item := range candidates[1:] {
		if item.score < minScore {
			minScore = item.score
		}
		if item.score > maxScore {
			maxScore = item.score
		}
	}
	if minScore == maxScore {
		return pickNextRoundRobinCLICandidate(candidates, currentID)
	}
	current := strings.TrimSpace(currentID)
	sorted := make([]cliUsageCandidate, 0, len(candidates))
	for _, item := range candidates {
		if strings.TrimSpace(item.account.ID) == "" {
			continue
		}
		sorted = append(sorted, item)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].score == sorted[j].score {
			return strings.TrimSpace(sorted[i].account.ID) < strings.TrimSpace(sorted[j].account.ID)
		}
		return sorted[i].score > sorted[j].score
	})
	for _, item := range sorted {
		if strings.TrimSpace(item.account.ID) == current {
			continue
		}
		return item.account, true
	}
	return store.Account{}, false
}

func pickWeightedRandomCLICandidate(candidates []cliUsageCandidate, currentID string) (store.Account, bool) {
	return pickWeightedRandomCLICandidateExcluding(candidates, currentID, nil)
}

func pickWeightedRandomCLICandidateExcluding(candidates []cliUsageCandidate, currentID string, exclude map[string]struct{}) (store.Account, bool) {
	current := strings.TrimSpace(currentID)
	filtered := make([]cliUsageCandidate, 0, len(candidates))
	total := int64(0)
	for _, item := range candidates {
		id := strings.TrimSpace(item.account.ID)
		if id == "" || id == current {
			continue
		}
		if exclude != nil {
			if _, skip := exclude[id]; skip {
				continue
			}
		}
		score := item.score
		if score <= 0 {
			score = 1
		}
		filtered = append(filtered, cliUsageCandidate{
			account: item.account,
			score:   score,
		})
		total += int64(score)
	}
	if len(filtered) == 0 {
		return store.Account{}, false
	}
	if total <= 0 {
		return filtered[0].account, true
	}
	n, err := rand.Int(rand.Reader, big.NewInt(total))
	if err != nil {
		return filtered[0].account, true
	}
	pick := n.Int64()
	acc := int64(0)
	for _, item := range filtered {
		acc += int64(item.score)
		if pick < acc {
			return item.account, true
		}
	}
	return filtered[len(filtered)-1].account, true
}

type structuredOutputSpec struct {
	Name   string
	Schema json.RawMessage
	Strict bool
}

func normalizeResponseFormat(format *ResponseFormat) (*structuredOutputSpec, error) {
	if format == nil {
		return nil, nil
	}
	typ := strings.TrimSpace(strings.ToLower(format.Type))
	if typ == "" {
		return nil, fmt.Errorf("response_format.type is required")
	}
	if typ != "json_schema" {
		return nil, fmt.Errorf("unsupported response_format.type: %s", typ)
	}
	name := strings.TrimSpace(format.Name)
	schema := bytes.TrimSpace(format.Schema)
	strictPtr := format.Strict
	if format.JSONSchema != nil {
		if name == "" {
			name = strings.TrimSpace(format.JSONSchema.Name)
		}
		if len(schema) == 0 {
			schema = bytes.TrimSpace(format.JSONSchema.Schema)
		}
		if strictPtr == nil {
			strictPtr = format.JSONSchema.Strict
		}
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("json_schema.schema is required")
	}
	strict := true
	if strictPtr != nil {
		strict = *strictPtr
	}
	return &structuredOutputSpec{
		Name:   name,
		Schema: schema,
		Strict: strict,
	}, nil
}

func responseFormatPayload(format *ResponseFormat) (map[string]any, error) {
	spec, err := normalizeResponseFormat(format)
	if err != nil || spec == nil {
		return nil, err
	}
	payload := map[string]any{
		"type":   "json_schema",
		"schema": json.RawMessage(spec.Schema),
		"strict": spec.Strict,
	}
	if spec.Name != "" {
		payload["name"] = spec.Name
	}
	return payload, nil
}

func validateStructuredOutput(spec *structuredOutputSpec, output string) error {
	if spec == nil {
		return nil
	}
	raw := strings.TrimSpace(output)
	if raw == "" {
		return fmt.Errorf("output is empty")
	}
	var data any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return fmt.Errorf("output is not valid JSON: %w", err)
	}
	if !spec.Strict {
		return nil
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(spec.Schema)); err != nil {
		return fmt.Errorf("invalid json_schema: %w", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("invalid json_schema: %w", err)
	}
	if err := schema.Validate(data); err != nil {
		return fmt.Errorf("output does not match json_schema: %w", err)
	}
	return nil
}

func findCLICandidateScore(candidates []cliUsageCandidate, accountID string) int {
	needle := strings.TrimSpace(accountID)
	for _, item := range candidates {
		if strings.TrimSpace(item.account.ID) == needle {
			return item.score
		}
	}
	return -1
}

func formatCLICandidates(candidates []cliUsageCandidate) string {
	if len(candidates) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(candidates))
	for _, item := range candidates {
		id := strings.TrimSpace(item.account.ID)
		if id == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(score=%d)", id, item.score))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := strings.TrimSpace(r.URL.Path)
		if path == "" {
			path = "/"
		}

		// WebSocket upgrade paths should bypass response writer wrapping to avoid
		// hijack/upgrade incompatibilities in middleware wrappers.
		if path == "/api/coding/ws" {
			next.ServeHTTP(w, r)
			remote := strings.TrimSpace(r.RemoteAddr)
			if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
				remote = host
			}
			accountHint := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Codex-Account")), "-")
			apiAuth := classifyAuthSource(r)
			ua := firstNonEmpty(truncateForLog(strings.TrimSpace(r.UserAgent()), 72), "-")
			log.Printf(
				"[ACCESS] %-7s %-38s status=%3s latency=%4dms from=%s kind=%s auth=%s account=%s ua=%s",
				strings.ToUpper(strings.TrimSpace(r.Method)),
				path,
				"WS",
				time.Since(start).Milliseconds(),
				firstNonEmpty(remote, "-"),
				requestKind(path),
				apiAuth,
				accountHint,
				ua,
			)
			return
		}

		rec := &accessLogRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		next.ServeHTTP(rec, r)

		remote := strings.TrimSpace(r.RemoteAddr)
		if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
			remote = host
		}
		accountHint := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Codex-Account")), "-")
		apiAuth := classifyAuthSource(r)
		ua := firstNonEmpty(truncateForLog(strings.TrimSpace(r.UserAgent()), 72), "-")
		log.Printf(
			"[ACCESS] %-7s %-38s status=%3d latency=%4dms from=%s kind=%s auth=%s account=%s ua=%s",
			strings.ToUpper(strings.TrimSpace(r.Method)),
			path,
			rec.status,
			time.Since(start).Milliseconds(),
			firstNonEmpty(remote, "-"),
			requestKind(path),
			apiAuth,
			accountHint,
			ua,
		)
	})
}

func requestKind(path string) string {
	p := strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(p, "/v1"), strings.HasPrefix(p, "/claude/v1"):
		return "proxy-api"
	case strings.HasPrefix(p, "/api/"):
		return "web-api"
	case strings.HasPrefix(p, "/auth/"):
		return "auth"
	case strings.HasPrefix(p, "/assets/"), strings.HasPrefix(p, "/sounds/"), p == "/favicon.svg":
		return "asset"
	default:
		return "web-ui"
	}
}

func classifyAuthSource(r *http.Request) string {
	bearer := strings.TrimSpace(BearerToken(r.Header.Get("Authorization")))
	xAPIKey := strings.TrimSpace(r.Header.Get("x-api-key"))
	switch {
	case bearer != "":
		return "bearer:" + maskSecret(bearer)
	case xAPIKey != "":
		return "x-api-key:" + maskSecret(xAPIKey)
	default:
		return "none"
	}
}

func maskSecret(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return "-"
	}
	if len(s) <= 6 {
		return s[:1] + "***"
	}
	return s[:3] + "..." + s[len(s)-2:]
}

func ptrString(v string) *string {
	return &v
}

func parseBoolQuery(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

	// Ensure auth context always matches resolved API account before `codex exec`.
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
	if u.HourlyPct > 0 {
		return u.HourlyPct
	}
	if u.WeeklyPct > 0 {
		return u.WeeklyPct
	}
	return 0
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

type accessLogRecorder struct {
	http.ResponseWriter
	status int
}

func (r *accessLogRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *accessLogRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}

func (r *accessLogRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *accessLogRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacker unsupported")
	}
	return hj.Hijack()
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withManagementAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		target := "/auth/login"
		path := strings.TrimSpace(r.URL.Path)
		if path != "" && path != "/" {
			target += "?next=" + url.QueryEscape(path)
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
}

func (s *Server) isPublicPath(path string) bool {
	p := strings.TrimSpace(path)
	switch {
	case p == "/healthz":
		return true
	case p == "/favicon.svg":
		return true
	case strings.HasPrefix(p, "/v1"):
		return true
	case strings.HasPrefix(p, "/claude/v1"):
		return true
	case strings.HasPrefix(p, "/zo/v1"):
		return true
	case p == "/auth/callback":
		return true
	case p == "/api/auth/browser/callback":
		return true
	case p == "/auth/login":
		return true
	case p == "/api/auth/login":
		return true
	}
	return false
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	ck, err := r.Cookie("codexsess_auth")
	if err != nil || strings.TrimSpace(ck.Value) == "" {
		return false
	}
	return s.validateAuthCookie(ck.Value)
}

func (s *Server) validateAuthCookie(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	parts := strings.Split(string(b), "|")
	if len(parts) != 3 {
		return false
	}
	username := strings.TrimSpace(parts[0])
	expRaw := strings.TrimSpace(parts[1])
	sig := strings.TrimSpace(parts[2])
	if username == "" || expRaw == "" || sig == "" {
		return false
	}
	expUnix, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expUnix {
		return false
	}
	if username != s.adminUsername {
		return false
	}
	expect := s.cookieSignature(username, expRaw)
	return sig == expect
}

func (s *Server) cookieSignature(username, expRaw string) string {
	sum := sha256.Sum256([]byte(username + "|" + expRaw + "|" + s.currentAdminPasswordHash()))
	return hex.EncodeToString(sum[:])
}

func (s *Server) issueAuthCookieValue() string {
	expRaw := strconv.FormatInt(time.Now().Add(30*24*time.Hour).Unix(), 10)
	sig := s.cookieSignature(s.adminUsername, expRaw)
	payload := s.adminUsername + "|" + expRaw + "|" + sig
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func (s *Server) setAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "codexsess_auth",
		Value:    s.issueAuthCookieValue(),
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "codexsess_auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) verifyAdminCredentials(username, password string) bool {
	user := strings.TrimSpace(username)
	pass := strings.TrimSpace(password)
	if user == "" || pass == "" {
		return false
	}
	if user != s.adminUsername {
		return false
	}
	return config.VerifyPassword(pass, s.currentAdminPasswordHash())
}

func (s *Server) handleAPIAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if !s.verifyAdminCredentials(req.Username, req.Password) {
		respondErr(w, http.StatusUnauthorized, "unauthorized", "invalid username or password")
		return
	}
	s.setAuthCookie(w)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		nextPath := strings.TrimSpace(r.URL.Query().Get("next"))
		if nextPath == "" {
			nextPath = "/"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>CodexSess Login</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@500;600&family=IBM+Plex+Sans:wght@400;500;600;700&display=swap" rel="stylesheet" />
<style>
:root{
  --bg:#0f1115;
  --panel:#171717;
  --border:rgba(148,163,184,.23);
  --text:#f8fafc;
  --muted:#94a3b8;
  --primary:#00d4aa;
}
*{box-sizing:border-box}
body{
  margin:0;
  min-height:100vh;
  display:grid;
  place-items:center;
  background:radial-gradient(1200px 500px at 10% -20%,rgba(0,212,170,.10),transparent),var(--bg);
  color:var(--text);
  font-family:"IBM Plex Sans",sans-serif;
}
.login-shell{
  width:min(420px,92vw);
  background:var(--panel);
  border:1px solid var(--border);
  border-radius:14px;
  padding:18px;
  display:grid;
  gap:14px;
}
.brand{
  display:grid;
  gap:2px;
  justify-items:center;
  text-align:center;
}
.brand strong{
  font-family:"IBM Plex Mono",monospace;
  letter-spacing:.02em;
  font-size:20px;
}
.brand span{
  color:var(--muted);
  font-size:12px;
}
form{
  display:grid;
  gap:10px;
}
label{
  color:var(--muted);
  font-size:12px;
}
input{
  width:100%;
  border:1px solid var(--border);
  border-radius:9px;
  padding:10px 11px;
  background:#131313;
  color:var(--text);
  outline:none;
}
input:focus{
  border-color:rgba(0,212,170,.7);
  box-shadow:0 0 0 2px rgba(0,212,170,.16);
}
button{
  margin-top:4px;
  border:none;
  border-radius:10px;
  padding:10px 12px;
  background:var(--primary);
  color:#02251f;
  font-weight:700;
  cursor:pointer;
  display:inline-flex;
  align-items:center;
  justify-content:center;
  gap:8px;
}
button[disabled]{
  opacity:.8;
  cursor:wait;
}
.spin{
  width:14px;
  height:14px;
  border-radius:50%;
  border:2px solid rgba(2,37,31,.25);
  border-top-color:#02251f;
  animation:spin .8s linear infinite;
}
@keyframes spin{
  to{transform:rotate(360deg)}
}
.foot{
  color:var(--muted);
  font-size:12px;
  text-align:center;
}
.err{
  margin:0;
  min-height:20px;
  border:1px solid rgba(239,68,68,.35);
  background:rgba(239,68,68,.12);
  color:#fecaca;
  border-radius:9px;
  padding:8px 10px;
  font-size:12px;
}
.err[hidden]{
  display:none;
}
.foot a{
  color:#7fead6;
  text-decoration:none;
}
.foot a:hover{
  text-decoration:underline;
}
</style>
</head>
<body>
<section class="login-shell">
  <div class="brand">
    <strong>CodexSess</strong>
    <span>Codex Account Management</span>
  </div>
  <form id="loginForm" method="post" action="/auth/login">
    <input type="hidden" name="next" value="` + templateEscape(nextPath) + `" />
    <label for="username">Username</label>
    <input id="username" name="username" autocomplete="username" placeholder="admin" />
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password" placeholder="Enter password" />
    <p id="loginError" class="err" role="alert" aria-live="polite" hidden></p>
    <button id="loginButton" type="submit">Sign In</button>
  </form>
  <div class="foot">
    Session will be remembered for 30 days.<br/>
    <a href="https://hijinetwork.net" target="_blank" rel="noopener noreferrer">Powered by HIJINETWORK</a>
  </div>
</section>
<script>
(() => {
  const form = document.getElementById("loginForm");
  const btn = document.getElementById("loginButton");
  const err = document.getElementById("loginError");
  if (!form || !btn) return;
  const defaultButtonHTML = 'Sign In';
  const loadingButtonHTML = '<span class="spin" aria-hidden="true"></span><span>Signing in...</span>';
  const setError = (message) => {
    if (!err) return;
    if (!message) {
      err.hidden = true;
      err.textContent = "";
      return;
    }
    err.hidden = false;
    err.textContent = message;
  };
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (btn.disabled) return;
    setError("");
    btn.disabled = true;
    btn.innerHTML = loadingButtonHTML;
    const fd = new FormData(form);
    const username = String(fd.get("username") || "");
    const password = String(fd.get("password") || "");
    const next = String(fd.get("next") || "/");
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Accept": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ username, password }),
      });
      if (res.ok) {
        window.location.assign(next || "/");
        return;
      }
      let msg = "Invalid username or password";
      try {
        const body = await res.json();
        if (body && body.error && typeof body.error.message === "string" && body.error.message.trim()) {
          msg = body.error.message;
        }
      } catch (_) {}
      setError(msg);
    } catch (_) {
      setError("Unable to sign in. Please try again.");
    } finally {
      btn.disabled = false;
      btn.innerHTML = defaultButtonHTML;
    }
  });
})();
</script>
</body>
</html>`))
		return
	}
	if r.Method != http.MethodPost {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if err := r.ParseForm(); err != nil {
		respondErr(w, http.StatusBadRequest, "bad_request", "invalid form")
		return
	}
	username := r.Form.Get("username")
	password := r.Form.Get("password")
	nextPath := strings.TrimSpace(r.Form.Get("next"))
	if nextPath == "" {
		nextPath = "/"
	}
	if !s.verifyAdminCredentials(username, password) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid username or password"))
		return
	}
	s.setAuthCookie(w)
	http.Redirect(w, r, nextPath, http.StatusFound)
}

func (s *Server) handleWebAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	s.clearAuthCookie(w)
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

func templateEscape(v string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(v)
}

func (s *Server) handleWebAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	
	qPage := r.URL.Query().Get("page")
	qLimit := r.URL.Query().Get("limit")
	
	filter := store.AccountFilter{
		Query:    r.URL.Query().Get("q"),
		PlanType: r.URL.Query().Get("type"),
		Status:   r.URL.Query().Get("status"),
		Usage:    r.URL.Query().Get("usage"),
	}

	var accounts []store.Account
	var totalFiltered int
	var err error

	page, _ := strconv.Atoi(qPage)
	limit, _ := strconv.Atoi(qLimit)

	if qPage != "" || qLimit != "" {
		accounts, totalFiltered, err = s.svc.ListAccountsPaginated(r.Context(), page, limit, filter)
	} else {
		accounts, err = s.svc.ListAccounts(r.Context())
		totalFiltered = len(accounts)
	}
	
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	type webAccount struct {
		ID            string               `json:"id"`
		Email         string               `json:"email"`
		Alias         string               `json:"alias"`
		PlanType      string               `json:"plan_type"`
		Active        bool                 `json:"active"`
		ActiveAPI     bool                 `json:"active_api"`
		ActiveCLI     bool                 `json:"active_cli"`
		Usage         *store.UsageSnapshot `json:"usage,omitempty"`
		Revoked       bool                 `json:"revoked"`
		RevokedReason string               `json:"revoked_reason,omitempty"`
	}
	resp := struct {
		Accounts      []webAccount `json:"accounts"`
		TotalFiltered int          `json:"total_filtered"`
	}{
		TotalFiltered: totalFiltered,
	}
	usageMap, err := s.svc.Store.ListUsageSnapshots(r.Context())
	if err != nil {
		usageMap = map[string]store.UsageSnapshot{}
	}
	cliActiveID, err := s.svc.ActiveCLIAccountID(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}

	activeAPIID := ""
	for _, a := range accounts {
		if a.Active {
			activeAPIID = strings.TrimSpace(a.ID)
			break
		}
	}
	apiRevoked := false
	cliRevoked := false
	if u, ok := usageMap[activeAPIID]; ok && usageErrorLooksRevoked(u.LastError) {
		apiRevoked = true
	}
	if u, ok := usageMap[strings.TrimSpace(cliActiveID)]; ok && usageErrorLooksRevoked(u.LastError) {
		cliRevoked = true
	}
	if apiRevoked || cliRevoked {
		if apiRevoked {
			if best, ok := s.findBestUsageAccount(r.Context(), activeAPIID); ok {
				_, _ = s.svc.UseAccountAPI(service.WithAPISwitchReason(r.Context(), "revoked"), best.ID)
			}
		}
		if cliRevoked {
			if best, ok := s.findBestUsageAccount(r.Context(), strings.TrimSpace(cliActiveID)); ok {
				_, _ = s.svc.UseAccountCLI(service.WithCLISwitchReason(r.Context(), "revoked"), best.ID)
			}
		}
		if qPage != "" || qLimit != "" {
			accounts, totalFiltered, err = s.svc.ListAccountsPaginated(r.Context(), page, limit, filter)
		} else {
			accounts, err = s.svc.ListAccounts(r.Context())
			totalFiltered = len(accounts)
		}
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		usageMap, err = s.svc.Store.ListUsageSnapshots(r.Context())
		if err != nil {
			usageMap = map[string]store.UsageSnapshot{}
		}
		cliActiveID, err = s.svc.ActiveCLIAccountID(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
	}

	for _, a := range accounts {
		isAPI := a.Active
		isCLI := cliActiveID != "" && a.ID == cliActiveID
		item := webAccount{
			ID:        a.ID,
			Email:     a.Email,
			Alias:     a.Alias,
			PlanType:  a.PlanType,
			Active:    isAPI && isCLI,
			ActiveAPI: isAPI,
			ActiveCLI: isCLI,
		}
		item.Revoked = a.Revoked
		if u, ok := usageMap[a.ID]; ok {
			ux := u
			item.Usage = &ux
			if usageErrorLooksRevoked(ux.LastError) {
				item.Revoked = true
				item.RevokedReason = strings.TrimSpace(ux.LastError)
			} else if a.Revoked {
				item.RevokedReason = "Marked as revoked in database"
			}
		} else if a.Revoked {
			item.RevokedReason = "Marked as revoked in database"
		}
		resp.Accounts = append(resp.Accounts, item)
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleWebAccountsTotal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	total, err := s.svc.CountAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]int{"total": total})
}

func (s *Server) handleDeleteRevokedAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	n, err := s.svc.DeleteRevokedAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"deleted": n})
}

func (s *Server) handleWebUseAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(r.Context(), "manual"), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	cliErr := ""
	if _, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(r.Context(), "manual"), req.Selector); err != nil {
		cliErr = err.Error()
	}
	respondJSON(w, 200, map[string]any{
		"ok":        true,
		"cli_ok":    cliErr == "",
		"cli_error": cliErr,
		"account":   map[string]any{"id": acc.ID, "email": acc.Email},
	})
}

func (s *Server) handleWebUseAPIAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(r.Context(), "manual"), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebUseCLIAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(r.Context(), "manual"), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebRemoveAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if err := s.svc.RemoveAccount(r.Context(), req.Selector); err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebImportAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Path  string `json:"path"`
		Alias string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.ImportTokenJSON(r.Context(), req.Path, req.Alias)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebBackupAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	payload, err := s.svc.ExportAccountsBackup(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}

	name := "codexsess-accounts-backup-" + time.Now().UTC().Format("20060102-150405") + ".json"
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, _ = w.Write(b)
}

func (s *Server) handleWebRestoreAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	var payload service.AccountsBackupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.RestoreAccountsBackup(r.Context(), payload)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok":       true,
		"restored": result.Restored,
		"skipped":  result.Skipped,
	})
}

func (s *Server) handleWebRefreshUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
		All      bool   `json:"all"`
		Source   string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	source := strings.TrimSpace(strings.ToLower(req.Source))
	switch source {
	case "auto", "manual":
	default:
		source = "manual"
	}
	if req.All {
		respondErr(w, 400, "bad_request", "bulk usage refresh is disabled; refresh per account only")
		return
	}
	if strings.TrimSpace(req.Selector) == "" {
		respondErr(w, 400, "bad_request", "selector required")
		return
	}
	u, err := s.svc.RefreshUsage(r.Context(), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	msg := "Manual usage refresh"
	if source == "auto" {
		msg = "Automatic usage refresh"
	}
	s.svc.AddSystemLog(r.Context(), "usage_refresh", msg, map[string]any{
		"all":      false,
		"selector": strings.TrimSpace(req.Selector),
		"hourly":   u.HourlyPct,
		"weekly":   u.WeeklyPct,
		"source":   source,
	})
	respondJSON(w, 200, map[string]any{"ok": true, "usage": u})
}

func (s *Server) handleWebUsageAutomationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	enabled, threshold, cliStrategy, intervalMinutes := s.currentUsageSchedulerState()
	status := s.getCLISwitchStatus()
	respondJSON(w, http.StatusOK, map[string]any{
		"usage_scheduler_enabled":          enabled,
		"usage_auto_switch_threshold":      threshold,
		"usage_scheduler_interval_minutes": intervalMinutes,
		"retry_cooldown_seconds":           int(usageSchedulerRetryCooldown.Seconds()),
		"coding_cli_strategy":              cliStrategy,
		"active_check_interval_seconds":    intervalMinutes * 60,
		"all_check_interval_seconds":       intervalMinutes * 60,
		"last_all_attempt_at":              s.lastAllAttemptAt.Load(),
		"last_all_failure_at":              s.lastAllFailureAt.Load(),
		"last_active_check_at":             s.lastActiveCheckAt.Load(),
		"last_all_check_at":                s.lastAllCheckAt.Load(),
		"last_cli_switch_at":               status.At,
		"last_cli_switch_from":             status.From,
		"last_cli_switch_to":               status.To,
		"last_cli_switch_reason":           status.Reason,
		"last_cli_switch_strategy":         status.Strategy,
		"last_cli_switch_error":            status.Error,
		"last_cli_switch_candidates":       status.Candidates,
	})
}

func (s *Server) handleWebSystemLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				if v > 2000 {
					v = 2000
				}
				limit = v
			}
		}
		entries, err := s.svc.Store.ListSystemLogs(r.Context(), limit)
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		total, _ := s.svc.Store.CountSystemLogs(r.Context())
		items := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			items = append(items, map[string]any{
				"id":         e.ID,
				"kind":       e.Kind,
				"message":    e.Message,
				"meta_json":  e.MetaJSON,
				"created_at": e.CreatedAt.Format(time.RFC3339),
			})
		}
		respondJSON(w, 200, map[string]any{
			"logs":  items,
			"total": total,
		})
		return
	case http.MethodDelete:
		if err := s.svc.Store.ClearSystemLogs(r.Context()); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	now := time.Now().Unix()
	available := codexAvailableModels()
	data := make([]ModelInfo, 0, len(available))
	for _, id := range available {
		data = append(data, ModelInfo{ID: id, Object: "model", Created: now, OwnedBy: "codexsess"})
	}
	resp := ModelsResponse{
		Object: "list",
		Data:   data,
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleAPIAuthJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}

	account, err := s.resolveAPIAccount(r.Context(), "")
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondErr(w, http.StatusNotFound, "account_not_found", err.Error())
		case strings.Contains(msg, "exhausted"):
			respondErr(w, http.StatusTooManyRequests, "quota_exhausted", "target account quota exhausted")
		default:
			respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	authPath := filepath.Join(s.svc.APICodexHome(account.ID), "auth.json")
	content, err := os.ReadFile(authPath)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "internal_error", "failed to load auth.json for active API account")
		return
	}
	if !json.Valid(content) {
		respondErr(w, http.StatusInternalServerError, "internal_error", "invalid auth.json content for active API account")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleAPIUsageStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}

	type activeUsageAccount struct {
		ID        string               `json:"id"`
		Email     string               `json:"email"`
		Alias     string               `json:"alias"`
		PlanType  string               `json:"plan_type"`
		ActiveAPI bool                 `json:"active_api"`
		ActiveCLI bool                 `json:"active_cli"`
		Usage     *store.UsageSnapshot `json:"usage,omitempty"`
		Available bool                 `json:"available"`
		Score     int                  `json:"score"`
	}
	type usageStatus struct {
		Object    string              `json:"object"`
		Generated string              `json:"generated_at"`
		APIActive *activeUsageAccount `json:"api_active,omitempty"`
		CLIActive *activeUsageAccount `json:"cli_active,omitempty"`
	}

	accounts, err := s.svc.ListAccounts(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	usageMap, err := s.svc.Store.ListUsageSnapshots(r.Context())
	if err != nil {
		usageMap = map[string]store.UsageSnapshot{}
	}
	cliActiveID, err := s.svc.ActiveCLIAccountID(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	findByID := func(id string) (store.Account, bool) {
		needle := strings.TrimSpace(id)
		if needle == "" {
			return store.Account{}, false
		}
		for _, account := range accounts {
			if strings.TrimSpace(account.ID) == needle {
				return account, true
			}
		}
		return store.Account{}, false
	}
	build := func(account store.Account, isAPI bool, isCLI bool) *activeUsageAccount {
		if strings.TrimSpace(account.ID) == "" {
			return nil
		}
		item := &activeUsageAccount{
			ID:        account.ID,
			Email:     account.Email,
			Alias:     account.Alias,
			PlanType:  account.PlanType,
			ActiveAPI: isAPI,
			ActiveCLI: isCLI,
		}
		if usage, ok := usageMap[account.ID]; ok {
			ux := usage
			item.Usage = &ux
			item.Available = usageAvailable(usage)
			item.Score = usageScore(usage)
		}
		return item
	}

	var apiActive *activeUsageAccount
	for _, account := range accounts {
		if account.Active {
			apiActive = build(account, true, strings.TrimSpace(cliActiveID) != "" && strings.TrimSpace(cliActiveID) == strings.TrimSpace(account.ID))
			break
		}
	}

	var cliActive *activeUsageAccount
	if account, ok := findByID(cliActiveID); ok {
		cliActive = build(account, account.Active, true)
	}

	respondJSON(w, http.StatusOK, usageStatus{
		Object:    "usage_status",
		Generated: time.Now().UTC().Format(time.RFC3339),
		APIActive: apiActive,
		CLIActive: cliActive,
	})
}

func (s *Server) handleOpenAIV1Root(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleModels(w, r)
		return
	case http.MethodPost:
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON body")
			return
		}
		if _, ok := anyBody["messages"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleChatCompletions(w, r)
			return
		}
		if _, ok := anyBody["input"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleResponses(w, r)
			return
		}
		respondErr(w, 400, "bad_request", "unsupported /v1 payload, use /v1/chat/completions or /v1/responses")
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	structuredSpec, err := normalizeResponseFormat(req.ResponseFormat)
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return
	}
	injectPrompt := s.shouldInjectDirectAPIPrompt() && s.currentAPIMode() != "direct_api"
	prompt := promptFromMessages(req.Messages)
	if injectPrompt {
		prompt = promptFromMessagesWithTools(req.Messages, req.Tools, req.ToolChoice)
	}
	directOpts := directCodexRequestOptions{
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		TextFormat: req.ResponseFormat,
	}
	account, tk, err := s.resolveAPIAccountWithTokens(r.Context(), selector)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondErr(w, 404, "account_not_found", err.Error())
		case strings.Contains(msg, "exhausted"):
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
		default:
			respondErr(w, 500, "internal_error", err.Error())
		}
		return
	}
	setResolvedAccountHeaders(w, account)
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    req.Stream,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		bufferedToolsMode := len(req.Tools) > 0 || structuredSpec != nil
		includeUsageChunk := req.StreamOpts != nil && req.StreamOpts.IncludeUsage
		if s.currentAPIMode() == "direct_api" {
			var streamedText strings.Builder
			stopKeepAlive := func() {}
			if bufferedToolsMode {
				stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
			}
			res, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, func(delta string) error {
				if bufferedToolsMode {
					streamedText.WriteString(delta)
					return nil
				}
				chunk := ChatCompletionsChunk{
					ID:      "chatcmpl-" + reqID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: delta}}},
				}
				writeChatCompletionsChunk(w, flusher, chunk)
				return nil
			}, !bufferedToolsMode)
			stopKeepAlive()
			if err != nil {
				code, errType := classifyDirectUpstreamError(err)
				status = code
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"`+escape(errType)+`"}}`)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			usage := Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens}
			if bufferedToolsMode {
				toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, res.ToolCalls)
				if structuredSpec != nil {
					if hasToolCalls {
						status = 400
						_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"tool_calls not allowed when response_format is set","type":"invalid_response_format"}}`)
						_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
						flusher.Flush()
						return
					}
					text := strings.TrimSpace(streamedText.String())
					if text == "" {
						text = res.Text
					}
					if err := validateStructuredOutput(structuredSpec, text); err != nil {
						status = 400
						_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"invalid_response_format"}}`)
						_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
						flusher.Flush()
						return
					}
					streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
					return
				}
				if hasToolCalls {
					streamChatCompletionToolCalls(w, flusher, "chatcmpl-"+reqID, req.Model, toolCalls, usage, includeUsageChunk)
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = res.Text
				}
				streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
				return
			}
			streamChatCompletionFinalStop(w, flusher, "chatcmpl-"+reqID, req.Model, usage, includeUsageChunk)
			return
		}

		var streamedText strings.Builder
		stopKeepAlive := func() {}
		if bufferedToolsMode {
			stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
		}
		res, err := s.svc.Codex.StreamChat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt, func(evt provider.ChatEvent) error {
			if evt.Type != "delta" {
				return nil
			}
			if bufferedToolsMode {
				streamedText.WriteString(evt.Text)
				return nil
			}
			chunk := ChatCompletionsChunk{
				ID:      "chatcmpl-" + reqID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   req.Model,
				Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: evt.Text}}},
			}
			writeChatCompletionsChunk(w, flusher, chunk)
			return nil
		})
		stopKeepAlive()
		if err != nil {
			status = 500
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`"}}`)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		usage := Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens}
		if bufferedToolsMode {
			toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, mapProviderToolCalls(res.ToolCalls))
			if structuredSpec != nil {
				if hasToolCalls {
					status = 400
					_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"tool_calls not allowed when response_format is set","type":"invalid_response_format"}}`)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = res.Text
				}
				if err := validateStructuredOutput(structuredSpec, text); err != nil {
					status = 400
					_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"invalid_response_format"}}`)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
				return
			}
			if hasToolCalls {
				streamChatCompletionToolCalls(w, flusher, "chatcmpl-"+reqID, req.Model, toolCalls, usage, includeUsageChunk)
				return
			}
			text := strings.TrimSpace(streamedText.String())
			if text == "" {
				text = res.Text
			}
			streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
			return
		}
		streamChatCompletionFinalStop(w, flusher, "chatcmpl-"+reqID, req.Model, usage, includeUsageChunk)
		return
	}

	if s.currentAPIMode() == "direct_api" {
		res, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, nil, false)
		if err != nil {
			status = 500
			code, errType := classifyDirectUpstreamError(err)
			status = code
			respondErr(w, code, errType, err.Error())
			return
		}
		toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, res.ToolCalls)
		if structuredSpec != nil {
			if hasToolCalls {
				status = 400
				respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when response_format is set")
				return
			}
			if err := validateStructuredOutput(structuredSpec, res.Text); err != nil {
				status = 400
				respondErr(w, 400, "invalid_response_format", err.Error())
				return
			}
		}
		choice := ChatChoice{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: res.Text},
			FinishReason: "stop",
		}
		if hasToolCalls {
			choice.Message = ChatMessage{
				Role:      "assistant",
				Content:   "",
				ToolCalls: toolCalls,
			}
			choice.FinishReason = "tool_calls"
		}
		resp := ChatCompletionsResponse{
			ID:      "chatcmpl-" + reqID,
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []ChatChoice{choice},
			Usage:   Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
		}
		respondJSON(w, 200, resp)
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, mapProviderToolCalls(res.ToolCalls))
	if structuredSpec != nil {
		if hasToolCalls {
			status = 400
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when response_format is set")
			return
		}
		if err := validateStructuredOutput(structuredSpec, res.Text); err != nil {
			status = 400
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	choice := ChatChoice{
		Index:        0,
		Message:      ChatMessage{Role: "assistant", Content: res.Text},
		FinishReason: "stop",
	}
	if hasToolCalls {
		choice.Message = ChatMessage{
			Role:      "assistant",
			Content:   "",
			ToolCalls: toolCalls,
		}
		choice.FinishReason = "tool_calls"
	}
	resp := ChatCompletionsResponse{
		ID:      "chatcmpl-" + reqID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{choice},
		Usage:   Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	structuredSpec, err := normalizeResponseFormat(nil)
	if req.Text != nil {
		structuredSpec, err = normalizeResponseFormat(req.Text.Format)
	}
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	injectPrompt := s.shouldInjectDirectAPIPrompt() && s.currentAPIMode() != "direct_api"
	prompt := promptFromResponsesInput(req.Input, nil, nil)
	if injectPrompt {
		prompt = promptFromResponsesInput(req.Input, req.Tools, req.ToolChoice)
	}
	directOpts := directCodexRequestOptions{
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		TextFormat: nil,
	}
	if req.Text != nil {
		directOpts.TextFormat = req.Text.Format
	}
	if strings.TrimSpace(prompt) == "" {
		respondErr(w, 400, "bad_request", "input is required")
		return
	}
	account, tk, err := s.resolveAPIAccountWithTokens(r.Context(), selector)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondErr(w, 404, "account_not_found", err.Error())
		case strings.Contains(msg, "exhausted"):
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
		default:
			respondErr(w, 500, "internal_error", err.Error())
		}
		return
	}
	setResolvedAccountHeaders(w, account)
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    req.Stream,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		responseCreatedAt := time.Now().Unix()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		seq := 0
		emit := func(event string, payload map[string]any) {
			if payload == nil {
				return
			}
			if _, exists := payload["sequence_number"]; !exists {
				seq++
				payload["sequence_number"] = seq
			}
			_ = event
			writeOpenAISSE(w, payload)
			flusher.Flush()
		}
		createdEvent := map[string]any{
			"type":     "response.created",
			"response": buildResponseObject(reqID, req.Model, "in_progress", []any{}, nil, responseCreatedAt),
		}
		emit("response.created", createdEvent)

		bufferedToolsMode := len(req.Tools) > 0 || structuredSpec != nil
		if s.currentAPIMode() == "direct_api" {
			var streamedText strings.Builder
			textItemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			stopKeepAlive := func() {}
			if bufferedToolsMode {
				stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
			}
			if !bufferedToolsMode {
				emit("response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"output_index": 0,
					"item": map[string]any{
						"type":    "message",
						"id":      textItemID,
						"status":  "in_progress",
						"role":    "assistant",
						"content": []any{},
					},
				})
			}
			result, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, func(delta string) error {
				if bufferedToolsMode {
					streamedText.WriteString(delta)
					return nil
				}
				streamedText.WriteString(delta)
				deltaEvent := map[string]any{
					"type":          "response.output_text.delta",
					"item_id":       textItemID,
					"output_index":  0,
					"content_index": 0,
					"delta":         delta,
					"logprobs":      []any{},
				}
				emit("response.output_text.delta", deltaEvent)
				return nil
			}, !bufferedToolsMode)
			stopKeepAlive()
			if err != nil {
				code, errType := classifyDirectUpstreamError(err)
				status = code
				emit("error", map[string]any{
					"type":    "error",
					"code":    errType,
					"message": err.Error(),
					"param":   nil,
				})
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			usage := ResponsesUsage{
				InputTokens:  result.InputTokens,
				OutputTokens: result.OutputTokens,
				TotalTokens:  result.InputTokens + result.OutputTokens,
			}
			if bufferedToolsMode {
				toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, result.ToolCalls)
				if structuredSpec != nil {
					if hasToolCalls {
						status = 400
						emit("error", map[string]any{
							"type":    "error",
							"code":    "invalid_response_format",
							"message": "tool_calls not allowed when text.format is set",
							"param":   nil,
						})
						_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
						flusher.Flush()
						return
					}
					text := strings.TrimSpace(streamedText.String())
					if text == "" {
						text = strings.TrimSpace(result.Text)
					}
					if err := validateStructuredOutput(structuredSpec, text); err != nil {
						status = 400
						emit("error", map[string]any{
							"type":    "error",
							"code":    "invalid_response_format",
							"message": err.Error(),
							"param":   nil,
						})
						_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
						flusher.Flush()
						return
					}
					streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				if hasToolCalls {
					streamResponsesFunctionCalls(emit, reqID, req.Model, toolCalls, usage, responseCreatedAt)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = strings.TrimSpace(result.Text)
				}
				streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			finalText := strings.TrimSpace(result.Text)
			if finalText == "" {
				finalText = strings.TrimSpace(streamedText.String())
			}
			emit("response.output_text.done", map[string]any{
				"type":          "response.output_text.done",
				"item_id":       textItemID,
				"output_index":  0,
				"content_index": 0,
				"text":          finalText,
				"logprobs":      []any{},
			})
			outputItem := map[string]any{
				"type":   "message",
				"id":     textItemID,
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": finalText, "annotations": []any{}},
				},
			}
			emit("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         outputItem,
			})
			completedEvent := map[string]any{
				"type": "response.completed",
				"response": buildResponseObject(reqID, req.Model, "completed", []any{outputItem}, map[string]any{
					"input_tokens":  usage.InputTokens,
					"output_tokens": usage.OutputTokens,
					"total_tokens":  usage.TotalTokens,
				}, responseCreatedAt),
			}
			emit("response.completed", completedEvent)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		var streamedText strings.Builder
		textItemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		stopKeepAlive := func() {}
		if bufferedToolsMode {
			stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
		}
		if !bufferedToolsMode {
			emit("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"type":    "message",
					"id":      textItemID,
					"status":  "in_progress",
					"role":    "assistant",
					"content": []any{},
				},
			})
		}
		result, err := s.svc.Codex.StreamChat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt, func(evt provider.ChatEvent) error {
			if evt.Type != "delta" {
				return nil
			}
			if bufferedToolsMode {
				streamedText.WriteString(evt.Text)
				return nil
			}
			streamedText.WriteString(evt.Text)
			deltaEvent := map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       textItemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         evt.Text,
				"logprobs":      []any{},
			}
			emit("response.output_text.delta", deltaEvent)
			return nil
		})
		stopKeepAlive()
		if err != nil {
			status = 500
			emit("error", map[string]any{
				"type":    "error",
				"code":    "upstream_error",
				"message": err.Error(),
				"param":   nil,
			})
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		usage := ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		}
		if bufferedToolsMode {
			toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, mapProviderToolCalls(result.ToolCalls))
			if structuredSpec != nil {
				if hasToolCalls {
					status = 400
					emit("error", map[string]any{
						"type":    "error",
						"code":    "invalid_response_format",
						"message": "tool_calls not allowed when text.format is set",
						"param":   nil,
					})
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = strings.TrimSpace(result.Text)
				}
				if err := validateStructuredOutput(structuredSpec, text); err != nil {
					status = 400
					emit("error", map[string]any{
						"type":    "error",
						"code":    "invalid_response_format",
						"message": err.Error(),
						"param":   nil,
					})
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			if hasToolCalls {
				streamResponsesFunctionCalls(emit, reqID, req.Model, toolCalls, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			text := strings.TrimSpace(streamedText.String())
			if text == "" {
				text = strings.TrimSpace(result.Text)
			}
			streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		finalText := strings.TrimSpace(result.Text)
		if finalText == "" {
			finalText = strings.TrimSpace(streamedText.String())
		}
		emit("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"text":          finalText,
			"logprobs":      []any{},
		})
		outputItem := map[string]any{
			"type":   "message",
			"id":     textItemID,
			"status": "completed",
			"role":   "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": finalText, "annotations": []any{}},
			},
		}
		emit("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item":         outputItem,
		})
		completedEvent := map[string]any{
			"type": "response.completed",
			"response": buildResponseObject(reqID, req.Model, "completed", []any{outputItem}, map[string]any{
				"input_tokens":  usage.InputTokens,
				"output_tokens": usage.OutputTokens,
				"total_tokens":  usage.TotalTokens,
			}, responseCreatedAt),
		}
		emit("response.completed", completedEvent)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	if s.currentAPIMode() == "direct_api" {
		result, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, nil, false)
		if err != nil {
			code, errType := classifyDirectUpstreamError(err)
			status = code
			respondErr(w, code, errType, err.Error())
			return
		}
		toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, result.ToolCalls)
		if structuredSpec != nil {
			if hasToolCalls {
				status = 400
				respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when text.format is set")
				return
			}
			if err := validateStructuredOutput(structuredSpec, result.Text); err != nil {
				status = 400
				respondErr(w, 400, "invalid_response_format", err.Error())
				return
			}
		}
		output := responsesMessageOutputItems(result.Text)
		outputText := strings.TrimSpace(result.Text)
		if hasToolCalls {
			output = responsesFunctionCallOutputItems(toolCalls)
			outputText = ""
		}
		completedAt := time.Now().Unix()
		textPayload := map[string]any{"format": map[string]any{"type": "text"}}
		if req.Text != nil && req.Text.Format != nil {
			if formatPayload, err := responseFormatPayload(req.Text.Format); err == nil && formatPayload != nil {
				textPayload = map[string]any{"format": formatPayload}
			}
		}
		resp := ResponsesResponse{
			ID:                 reqID,
			Object:             "response",
			CreatedAt:          completedAt,
			OutputText:         outputText,
			Status:             "completed",
			CompletedAt:        &completedAt,
			Error:              nil,
			IncompleteDetails:  nil,
			Instructions:       nil,
			MaxOutputTokens:    nil,
			Model:              req.Model,
			Output:             output,
			ParallelToolCalls:  true,
			PreviousResponseID: nil,
			Reasoning:          map[string]any{"effort": nil, "summary": nil},
			Store:              true,
			Temperature:        1.0,
			Text:               textPayload,
			ToolChoice:         "auto",
			Tools:              []any{},
			TopP:               1.0,
			Truncation:         "disabled",
			Usage: ResponsesUsage{
				InputTokens:  result.InputTokens,
				OutputTokens: result.OutputTokens,
				TotalTokens:  result.InputTokens + result.OutputTokens,
			},
			User:     nil,
			Metadata: map[string]any{},
		}
		respondJSON(w, 200, resp)
		return
	}

	result, err := s.svc.Codex.Chat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, mapProviderToolCalls(result.ToolCalls))
	if structuredSpec != nil {
		if hasToolCalls {
			status = 400
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when text.format is set")
			return
		}
		if err := validateStructuredOutput(structuredSpec, result.Text); err != nil {
			status = 400
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	output := responsesMessageOutputItems(result.Text)
	outputText := strings.TrimSpace(result.Text)
	if hasToolCalls {
		output = responsesFunctionCallOutputItems(toolCalls)
		outputText = ""
	}
	completedAt := time.Now().Unix()
	textPayload := map[string]any{"format": map[string]any{"type": "text"}}
	if req.Text != nil && req.Text.Format != nil {
		if formatPayload, err := responseFormatPayload(req.Text.Format); err == nil && formatPayload != nil {
			textPayload = map[string]any{"format": formatPayload}
		}
	}
	resp := ResponsesResponse{
		ID:                 reqID,
		Object:             "response",
		CreatedAt:          completedAt,
		OutputText:         outputText,
		Status:             "completed",
		CompletedAt:        &completedAt,
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       nil,
		MaxOutputTokens:    nil,
		Model:              req.Model,
		Output:             output,
		ParallelToolCalls:  true,
		PreviousResponseID: nil,
		Reasoning:          map[string]any{"effort": nil, "summary": nil},
		Store:              true,
		Temperature:        1.0,
		Text:               textPayload,
		ToolChoice:         "auto",
		Tools:              []any{},
		TopP:               1.0,
		Truncation:         "disabled",
		Usage: ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		},
		User:     nil,
		Metadata: map[string]any{},
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	anthropicVersion := normalizeAnthropicVersion(r.Header.Get("anthropic-version"))
	if r.Method != http.MethodPost {
		respondClaudeErr(w, 405, "invalid_request_error", "method not allowed", reqID)
		return
	}
	if !s.isValidAPIKey(r) {
		respondClaudeErr(w, 401, "authentication_error", "invalid API key", reqID)
		return
	}
	w.Header().Set("anthropic-version", anthropicVersion)
	w.Header().Set("request-id", reqID)
	selector := ""
	var req ClaudeMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondClaudeErr(w, 400, "invalid_request_error", "invalid JSON body", reqID)
		return
	}
	s.enforceClaudeCodexOnlyConfig()
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	if strings.Contains(strings.TrimSpace(req.Model), ":") {
		req.Model = "gpt-5.2-codex"
	}
	if req.MaxTokens <= 0 {
		respondClaudeErr(w, 400, "invalid_request_error", "max_tokens must be greater than 0", reqID)
		return
	}
	req.Model = s.resolveMappedModel(req.Model)
	toolDefs := mapClaudeToolsToChatTools(req.Tools)
	sessionKey := deriveClaudeSessionKey(req, r)
	sanitizedMessages := s.sanitizeClaudeMessagesForPrompt(req.Messages, toolDefs, sessionKey)
	budgetedMessages, budgetedSystem := applyClaudeTokenBudgetGuard(sanitizedMessages, req.System)
	injectPrompt := s.shouldInjectDirectAPIPrompt() && s.currentAPIMode() != "direct_api"
	prompt := promptFromClaudeMessagesWithSystemAndTools(budgetedMessages, budgetedSystem, nil, nil)
	if injectPrompt {
		prompt = promptFromClaudeMessagesWithSystemAndTools(budgetedMessages, budgetedSystem, toolDefs, req.ToolChoice)
	}
	prompt = applyClaudeResponseDefaults(prompt)
	directOpts := directCodexRequestOptions{
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   req.StopSequences,
		Tools:           toolDefs,
		ToolChoice:      req.ToolChoice,
		ClaudeProtocol:  true,
		AnthropicVer:    anthropicVersion,
	}
	if strings.TrimSpace(prompt) == "" {
		respondClaudeErr(w, 400, "invalid_request_error", "messages are required", reqID)
		return
	}
	account, tk, err := s.resolveAPIAccountWithTokens(r.Context(), selector)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondClaudeErr(w, 404, "not_found_error", err.Error(), reqID)
		case strings.Contains(msg, "exhausted"):
			respondClaudeErr(w, 429, "rate_limit_error", "target account quota exhausted", reqID)
		default:
			respondClaudeErr(w, 500, "api_error", err.Error(), reqID)
		}
		return
	}
	setResolvedAccountHeaders(w, account)
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    req.Stream,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondClaudeErr(w, 500, "api_error", "streaming not supported", reqID)
			return
		}

		writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            reqID,
				"type":          "message",
				"role":          "assistant",
				"model":         req.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
		flusher.Flush()

		var (
			contentIdx   = 0
			streamRes    provider.ChatResult
			directRes    directAPIResult
			usage        ClaudeMessagesUsage
			toolCalls    []ChatToolCall
			streamedText strings.Builder
			streamMu     sync.Mutex
		)
		onDelta := func(delta string) error {
			if strings.TrimSpace(delta) == "" {
				return nil
			}
			streamMu.Lock()
			streamedText.WriteString(delta)
			streamMu.Unlock()
			return nil
		}

		if s.currentAPIMode() == "direct_api" {
			directRes, err = s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, onDelta, false)
			if err != nil {
				code, errType := classifyDirectUpstreamClaudeError(err)
				status = code
				writeSSE(w, "error", map[string]any{
					"type": "error",
					"error": map[string]any{
						"type":    errType,
						"message": err.Error(),
					},
				})
				flusher.Flush()
				return
			}
			usage = ClaudeMessagesUsage{
				InputTokens:  directRes.InputTokens,
				OutputTokens: directRes.OutputTokens,
			}
			toolCalls, _ = resolveToolCalls(directRes.Text, toolDefs, directRes.ToolCalls)
			toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
		} else {
			streamRes, err = s.svc.Codex.StreamChat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt, func(evt provider.ChatEvent) error {
				if evt.Type != "delta" {
					return nil
				}
				return onDelta(evt.Text)
			})
			if err != nil {
				status = 500
				writeSSE(w, "error", map[string]any{
					"type": "error",
					"error": map[string]any{
						"type":    "api_error",
						"message": err.Error(),
					},
				})
				flusher.Flush()
				return
			}
			usage = ClaudeMessagesUsage{
				InputTokens:  streamRes.InputTokens,
				OutputTokens: streamRes.OutputTokens,
			}
			toolCalls, _ = resolveToolCalls(streamRes.Text, toolDefs, mapProviderToolCalls(streamRes.ToolCalls))
			toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
		}

		streamMu.Lock()
		finalText := strings.TrimSpace(streamedText.String())
		streamMu.Unlock()
		if finalText == "" {
			if s.currentAPIMode() == "direct_api" {
				finalText = strings.TrimSpace(directRes.Text)
			} else {
				finalText = strings.TrimSpace(streamRes.Text)
			}
		}
		finalText = sanitizeClaudeAssistantText(finalText)
		if finalText != "" {
			writeSSE(w, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": contentIdx,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			})
			writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": contentIdx,
				"delta": map[string]any{"type": "text_delta", "text": finalText},
			})
			writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": contentIdx,
			})
			flusher.Flush()
			contentIdx++
		}

		for _, call := range toolCalls {
			toolInputJSON := strings.TrimSpace(call.Function.Arguments)
			if toolInputJSON == "" {
				toolInputJSON = "{}"
			}
			if !json.Valid([]byte(toolInputJSON)) {
				toolInputJSON = "{}"
			}
			writeSSE(w, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": contentIdx,
				"content_block": map[string]any{
					"type": "tool_use",
					"id":   strings.TrimSpace(call.ID),
					"name": strings.TrimSpace(call.Function.Name),
					// Claude SSE tool-use payload should be materialized via input_json_delta.
					"input": map[string]any{},
				},
			})
			writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": contentIdx,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": toolInputJSON,
				},
			})
			writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": contentIdx,
			})
			flusher.Flush()
			contentIdx++
		}

		stopReason := "end_turn"
		if len(toolCalls) > 0 {
			stopReason = "tool_use"
		}
		writeSSE(w, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"input_tokens":  usage.InputTokens,
				"output_tokens": usage.OutputTokens,
			},
		})
		writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
		flusher.Flush()
		return
	}

	if s.currentAPIMode() == "direct_api" {
		res, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, nil, false)
		if err != nil {
			code, errType := classifyDirectUpstreamClaudeError(err)
			status = code
			respondClaudeErr(w, code, errType, err.Error(), reqID)
			return
		}
		sanitizedText := sanitizeClaudeAssistantText(res.Text)
		toolCalls, _ := resolveToolCalls(sanitizedText, toolDefs, res.ToolCalls)
		toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
		content, stopReason := buildClaudeResponseContent(sanitizedText, toolCalls)
		resp := ClaudeMessagesResponse{
			ID:         reqID,
			Type:       "message",
			Role:       "assistant",
			Model:      req.Model,
			Content:    content,
			StopReason: stopReason,
			Usage: ClaudeMessagesUsage{
				InputTokens:  res.InputTokens,
				OutputTokens: res.OutputTokens,
			},
		}
		respondJSON(w, 200, resp)
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt)
	if err != nil {
		status = 500
		respondClaudeErr(w, 500, "api_error", err.Error(), reqID)
		return
	}
	sanitizedText := sanitizeClaudeAssistantText(res.Text)
	toolCalls, _ := resolveToolCalls(sanitizedText, toolDefs, mapProviderToolCalls(res.ToolCalls))
	toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
	content, stopReason := buildClaudeResponseContent(sanitizedText, toolCalls)
	resp := ClaudeMessagesResponse{
		ID:         reqID,
		Type:       "message",
		Role:       "assistant",
		Model:      req.Model,
		Content:    content,
		StopReason: stopReason,
		Usage: ClaudeMessagesUsage{
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		},
	}
	respondJSON(w, 200, resp)
}

func normalizeAnthropicVersion(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "2023-06-01"
	}
	return trimmed
}

func respondClaudeErr(w http.ResponseWriter, code int, errType, msg, requestID string) {
	body := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(msg),
		},
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		body["request_id"] = rid
	}
	respondJSON(w, code, body)
}

func classifyDirectUpstreamError(err error) (int, string) {
	if err == nil {
		return 500, "upstream_error"
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 400:
			return 400, "bad_request"
		case 401:
			return 401, "unauthorized"
		case 403:
			return 403, "forbidden"
		case 404:
			return 404, "not_found"
		case 408:
			return 408, "timeout"
		case 409:
			return 409, "conflict"
		case 422:
			return 422, "unprocessable_entity"
		case 429:
			return 429, "quota_exhausted"
		default:
			if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
				return httpErr.StatusCode, "upstream_error"
			}
			if httpErr.StatusCode > 0 {
				return httpErr.StatusCode, "upstream_error"
			}
		}
	}
	return 500, "upstream_error"
}

func classifyDirectUpstreamClaudeError(err error) (int, string) {
	if err == nil {
		return 500, "api_error"
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 400:
			return 400, "invalid_request_error"
		case 401:
			return 401, "authentication_error"
		case 403:
			return 403, "permission_error"
		case 404:
			return 404, "not_found_error"
		case 408:
			return 408, "timeout_error"
		case 429:
			return 429, "rate_limit_error"
		default:
			if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
				return httpErr.StatusCode, "api_error"
			}
			if httpErr.StatusCode > 0 {
				return httpErr.StatusCode, "api_error"
			}
		}
	}
	return 500, "api_error"
}

func mapClaudeToolsToChatTools(tools []ClaudeToolDef) []ChatToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ChatToolDef, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if shouldBlockClaudeClientToolName(name) {
			continue
		}
		out = append(out, ChatToolDef{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:        name,
				Description: strings.TrimSpace(t.Description),
				Parameters:  t.InputSchema,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func shouldBlockClaudeClientToolName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if !shouldBlockClaudeTaskTools() {
		return false
	}
	return strings.HasPrefix(lower, "task")
}

func shouldBlockClaudeTaskTools() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_BLOCK_TASK_TOOLS")))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func sanitizeClaudeClientToolCalls(calls []ChatToolCall) []ChatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		if shouldBlockClaudeClientToolName(name) {
			continue
		}
		call.Function.Arguments = sanitizeClaudeToolCallArguments(name, call.Function.Arguments)
		out = append(out, call)
	}
	return out
}

func sanitizeClaudeToolCallArguments(name string, raw string) string {
	args := strings.TrimSpace(raw)
	if args == "" || !json.Valid([]byte(args)) {
		return raw
	}
	if strings.ToLower(strings.TrimSpace(name)) != "read" {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil || obj == nil {
		return raw
	}
	if pages := strings.TrimSpace(coerceAnyText(obj["pages"])); pages == "" {
		delete(obj, "pages")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return string(b)
}

func buildClaudeResponseContent(text string, calls []ChatToolCall) ([]ClaudeContentBlock, string) {
	content := make([]ClaudeContentBlock, 0, len(calls)+1)
	trimmedText := strings.TrimSpace(text)
	if trimmedText != "" {
		content = append(content, ClaudeContentBlock{
			Type: "text",
			Text: trimmedText,
		})
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		content = append(content, ClaudeContentBlock{
			Type:  "tool_use",
			ID:    callID,
			Name:  name,
			Input: parseToolArgumentsForClaude(call.Function.Arguments),
		})
	}
	if len(content) == 0 {
		content = append(content, ClaudeContentBlock{
			Type: "text",
			Text: "",
		})
		return content, "end_turn"
	}
	if len(calls) > 0 {
		return content, "tool_use"
	}
	return content, "end_turn"
}

func parseToolArgumentsForClaude(arguments string) any {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return map[string]any{}
	}
	var out any
	if json.Unmarshal([]byte(raw), &out) == nil {
		return out
	}
	return map[string]any{"raw": raw}
}

func promptFromMessages(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		if role == "tool" && strings.TrimSpace(m.ToolCallID) != "" {
			sb.WriteString("(")
			sb.WriteString(strings.TrimSpace(m.ToolCallID))
			sb.WriteString(")")
		}
		sb.WriteString(": ")
		sb.WriteString(extractOpenAIContentText(m.Content))
		if len(m.ToolCalls) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			for i, tc := range m.ToolCalls {
				if i > 0 {
					sb.WriteString(" | ")
				}
				sb.WriteString(strings.TrimSpace(tc.Function.Name))
				sb.WriteString("(")
				sb.WriteString(strings.TrimSpace(tc.Function.Arguments))
				sb.WriteString(")")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func extractOpenAIContentText(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, it := range v {
			obj, ok := it.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			if t != "" && t != "text" {
				continue
			}
			text, _ := obj["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		text, _ := v["text"].(string)
		return strings.TrimSpace(text)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return strings.TrimSpace(s)
		}
		var items []map[string]any
		if err := json.Unmarshal(b, &items); err == nil {
			var parts []string
			for _, it := range items {
				t, _ := it["type"].(string)
				if t != "" && t != "text" {
					continue
				}
				text, _ := it["text"].(string)
				if strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
		return ""
	}
}

func promptFromMessagesWithTools(msgs []ChatMessage, tools []ChatToolDef, toolChoice json.RawMessage) string {
	base := promptFromMessages(msgs)
	return promptFromTextWithTools(base, tools, toolChoice)
}

func promptFromTextWithTools(base string, tools []ChatToolDef, toolChoice json.RawMessage) string {
	if len(tools) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\nAVAILABLE_TOOLS_JSON:\n")
	sb.WriteString("[\n")
	for i, t := range tools {
		if i > 0 {
			sb.WriteString(",\n")
		}
		b, _ := json.Marshal(toolDefForPrompt(t))
		sb.WriteString(string(b))
	}
	sb.WriteString("\n]\n")
	if len(bytes.TrimSpace(toolChoice)) > 0 {
		sb.WriteString("TOOL_CHOICE_JSON:\n")
		sb.WriteString(strings.TrimSpace(string(toolChoice)))
		sb.WriteString("\n")
	}
	sb.WriteString("TOOL_OUTPUT_RULES:\n")
	sb.WriteString("- If a tool is required, respond with JSON only.\n")
	sb.WriteString("- JSON format must be exactly: {\"tool_calls\":[{\"name\":\"<tool_name>\",\"arguments\":{...}}]}.\n")
	sb.WriteString("- Do not wrap JSON in markdown fences.\n")
	sb.WriteString("- If no tool is needed, respond normally with plain assistant text.\n")
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessages(msgs []ClaudeMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		textParts, toolCallParts, toolResultParts := extractClaudeMessageParts(m.Content)
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(strings.TrimSpace(strings.Join(textParts, "\n")))
		if len(toolCallParts) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			sb.WriteString(strings.Join(toolCallParts, " | "))
		}
		for _, tr := range toolResultParts {
			sb.WriteString("\n")
			sb.WriteString(tr)
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessagesWithSystemAndTools(
	msgs []ClaudeMessage,
	system json.RawMessage,
	tools []ChatToolDef,
	toolChoice json.RawMessage,
) string {
	base := promptFromClaudeMessages(msgs)
	if systemText := extractClaudeSystemText(system); systemText != "" {
		if strings.TrimSpace(base) != "" {
			base = "system: " + systemText + "\n" + base
		} else {
			base = "system: " + systemText
		}
	}
	return promptFromTextWithTools(base, tools, toolChoice)
}

func extractClaudeContentText(raw json.RawMessage) string {
	textParts, _, _ := extractClaudeMessageParts(raw)
	return strings.TrimSpace(strings.Join(textParts, "\n"))
}

func (s *Server) sanitizeClaudeMessagesForPrompt(messages []ClaudeMessage, toolDefs []ChatToolDef, sessionKey string) []ClaudeMessage {
	if len(messages) == 0 {
		return messages
	}
	droppedToolUseIDs := map[string]struct{}{}
	out := make([]ClaudeMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		contentRaw := bytes.TrimSpace(msg.Content)
		if len(contentRaw) == 0 {
			continue
		}
		var items []map[string]any
		if err := json.Unmarshal(contentRaw, &items); err != nil {
			out = append(out, msg)
			continue
		}
		kept := make([]map[string]any, 0, len(items))
		for _, item := range items {
			typ := strings.ToLower(strings.TrimSpace(coerceAnyText(item["type"])))
			if typ == "" || typ == "text" {
				text := coerceAnyText(item["text"])
				cleaned, keep := sanitizeClaudePromptText(role, text)
				if !keep {
					continue
				}
				item["text"] = cleaned
			}
			if role == "assistant" && typ == "tool_use" {
				name := strings.TrimSpace(coerceAnyText(item["name"]))
				toolUseID := strings.TrimSpace(coerceAnyText(item["id"]))
				if strings.EqualFold(name, "skill") {
					if toolUseID != "" {
						droppedToolUseIDs[toolUseID] = struct{}{}
					}
					continue
				}
				args := coerceAnyJSON(item["input"])
				if def, ok := findToolDefByName(toolDefs, name); ok {
					missing := missingRequiredToolFields(def, args)
					if len(missing) > 0 {
						if toolUseID != "" {
							droppedToolUseIDs[toolUseID] = struct{}{}
						}
						s.rememberInvalidToolPattern(sessionKey, name, missing)
						continue
					}
				}
				if s.hasInvalidToolPattern(sessionKey, name, nil) {
					if toolUseID != "" {
						droppedToolUseIDs[toolUseID] = struct{}{}
					}
					continue
				}
			}
			if role == "user" && typ == "tool_result" {
				toolUseID := strings.TrimSpace(coerceAnyText(item["tool_use_id"]))
				if _, dropped := droppedToolUseIDs[toolUseID]; dropped {
					continue
				}
				cleanedResult, keep := sanitizeClaudeToolResultText(extractClaudeToolResultValue(item["content"]))
				if !keep {
					continue
				}
				item["content"] = cleanedResult
				content := strings.ToLower(strings.TrimSpace(cleanedResult))
				if strings.Contains(content, "required parameter") && strings.Contains(content, "missing") {
					continue
				}
			}
			kept = append(kept, item)
		}
		if len(kept) == 0 {
			continue
		}
		b, err := json.Marshal(kept)
		if err != nil {
			continue
		}
		out = append(out, ClaudeMessage{
			Role:    msg.Role,
			Content: json.RawMessage(b),
		})
	}
	if len(out) == 0 {
		return messages
	}
	return out
}

func sanitizeClaudePromptText(role, text string) (string, bool) {
	cleaned := stripSystemReminderBlocks(text)
	if strings.TrimSpace(cleaned) == "" {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(role), "assistant") && isLikelyPolicyRefusalText(cleaned) {
		return "", false
	}
	return cleaned, true
}

func stripSystemReminderBlocks(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	for {
		start := strings.Index(lower, "<system-reminder>")
		if start < 0 {
			break
		}
		endRel := strings.Index(lower[start:], "</system-reminder>")
		if endRel < 0 {
			raw = strings.TrimSpace(raw[:start])
			break
		}
		end := start + endRel + len("</system-reminder>")
		raw = strings.TrimSpace(raw[:start] + "\n" + raw[end:])
		lower = strings.ToLower(raw)
	}
	return strings.TrimSpace(raw)
}

func isLikelyPolicyRefusalText(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	patterns := []string{
		"maaf, saya tidak bisa membantu",
		"maaf, saya tidak dapat membantu",
		"i can't help with",
		"i cannot help with",
		"i'm sorry, i can't help",
		"berpotensi disalahgunakan",
		"could be misused",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func sanitizeClaudeAssistantText(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if shouldDropClaudeTraceLine(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func sanitizeClaudeToolResultText(text string) (string, bool) {
	cleaned := stripSystemReminderBlocks(text)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", false
	}
	if isNoisyToolResultText(cleaned) {
		return "", false
	}
	const maxToolResultChars = 2800
	if len(cleaned) > maxToolResultChars {
		cleaned = strings.TrimSpace(cleaned[:maxToolResultChars]) + "\n...[truncated]"
	}
	return cleaned, true
}

func isNoisyToolResultText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "invalid pages parameter") {
		return true
	}
	if strings.Contains(lower, "exceeds maximum allowed tokens") {
		return true
	}
	if strings.Contains(lower, "this is a read-only task") {
		return true
	}
	return false
}

func shouldDropClaudeTraceLine(line string) bool {
	normalized := strings.TrimSpace(line)
	lower := strings.ToLower(normalized)
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "entered plan mode") {
		return true
	}
	if strings.Contains(lower, "successfully loaded skill") {
		return true
	}
	if strings.Contains(lower, "ctrl+b") {
		return true
	}
	if strings.HasPrefix(lower, "1 tasks (") || claudeTaskCountLinePattern.MatchString(lower) {
		return true
	}
	if strings.HasPrefix(lower, "◼ ") {
		return true
	}
	if strings.HasPrefix(lower, "● skill(") || strings.HasPrefix(lower, "skill(") {
		return true
	}
	if strings.HasPrefix(lower, "⎿") {
		return true
	}
	if claudeTraceCallLinePattern.MatchString(normalized) {
		return true
	}
	return false
}

func applyClaudeResponseDefaults(prompt string) string {
	base := strings.TrimSpace(prompt)
	if base == "" {
		return ""
	}
	defaults := strings.Join([]string{
		"system: Response defaults:",
		"- For broad analysis/debug requests, begin with a best-effort system analysis immediately.",
		"- Make reasonable assumptions and proceed; avoid asking scope-first questions unless truly blocked.",
		"- Prefer actionable findings first, then assumptions/open questions.",
	}, "\n")
	return strings.TrimSpace(defaults + "\n\n" + base)
}

func applyClaudeTokenBudgetGuard(messages []ClaudeMessage, system json.RawMessage) ([]ClaudeMessage, json.RawMessage) {
	msgs := cloneClaudeMessages(messages)
	sys := system
	softLimit := resolveClaudeTokenSoftLimit()
	hardLimit := resolveClaudeTokenHardLimit(softLimit)
	if len(msgs) == 0 {
		return msgs, sys
	}
	estimated := estimateClaudePromptTokens(msgs, sys)
	if estimated <= softLimit {
		return msgs, sys
	}

	// Stage 1: light trim, keep a generous recent history.
	msgs = trimClaudeMessagesTail(msgs, 24)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	// Stage 2: tighter history window.
	msgs = trimClaudeMessagesTail(msgs, 16)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	// Stage 3: shrink oversized system payload.
	sys = compressClaudeSystem(system, 2800)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	// Stage 4: final non-aggressive trim.
	msgs = trimClaudeMessagesTail(msgs, 12)
	sys = compressClaudeSystem(sys, 1800)
	return msgs, sys
}

func resolveClaudeTokenSoftLimit() int {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT"))
	if raw == "" {
		return claudeTokenSoftLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 4000 {
		return claudeTokenSoftLimitDefault
	}
	return n
}

func resolveClaudeTokenHardLimit(softLimit int) int {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT"))
	if raw == "" {
		if softLimit+4000 > claudeTokenHardLimitDefault {
			return softLimit + 4000
		}
		return claudeTokenHardLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < softLimit+2000 {
		return maxInt(softLimit+2000, claudeTokenHardLimitDefault)
	}
	return n
}

func estimateClaudePromptTokens(messages []ClaudeMessage, system json.RawMessage) int {
	text := promptFromClaudeMessagesWithSystemAndTools(messages, system, nil, nil)
	if strings.TrimSpace(text) == "" {
		return 0
	}
	chars := len([]rune(text))
	return (chars + 3) / 4
}

func trimClaudeMessagesTail(messages []ClaudeMessage, keep int) []ClaudeMessage {
	if keep <= 0 || len(messages) <= keep {
		return cloneClaudeMessages(messages)
	}
	start := len(messages) - keep
	out := make([]ClaudeMessage, 0, keep)
	for _, msg := range messages[start:] {
		out = append(out, msg)
	}
	return out
}

func cloneClaudeMessages(messages []ClaudeMessage) []ClaudeMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ClaudeMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg)
	}
	return out
}

func compressClaudeSystem(system json.RawMessage, maxChars int) json.RawMessage {
	text := strings.TrimSpace(extractClaudeSystemText(system))
	if text == "" || maxChars <= 0 {
		return system
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return system
	}
	truncated := strings.TrimSpace(string(runes[:maxChars]))
	if truncated == "" {
		return system
	}
	b, err := json.Marshal(truncated + "\n...[system context truncated]")
	if err != nil {
		return system
	}
	return json.RawMessage(b)
}

func deriveClaudeSessionKey(req ClaudeMessagesRequest, r *http.Request) string {
	if metadataSession := extractSessionIDFromMetadata(req.Metadata); metadataSession != "" {
		return metadataSession
	}
	if fromHeader := strings.TrimSpace(r.Header.Get("x-claude-session-id")); fromHeader != "" {
		return fromHeader
	}
	ua := strings.TrimSpace(r.UserAgent())
	addr := strings.TrimSpace(r.RemoteAddr)
	if ua == "" && addr == "" {
		return "unknown"
	}
	return ua + "|" + addr
}

func extractSessionIDFromMetadata(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	return extractSessionIDFromMetadataAny(data, 0)
}

func extractSessionIDFromMetadataAny(data any, depth int) string {
	if depth > 3 || data == nil {
		return ""
	}
	switch v := data.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return ""
		}
		var nested any
		if err := json.Unmarshal([]byte(text), &nested); err != nil {
			return text
		}
		return extractSessionIDFromMetadataAny(nested, depth+1)
	case map[string]any:
		for _, key := range []string{"session_id", "sessionId"} {
			if id := strings.TrimSpace(coerceAnyText(v[key])); id != "" && id != "null" && id != "{}" {
				return id
			}
		}
		for _, key := range []string{"user_id", "userId"} {
			if id := extractSessionIDFromMetadataAny(v[key], depth+1); id != "" {
				return id
			}
		}
		for _, key := range []string{"metadata", "meta"} {
			if id := extractSessionIDFromMetadataAny(v[key], depth+1); id != "" {
				return id
			}
		}
		return ""
	default:
		return ""
	}
}

func (s *Server) rememberInvalidToolPattern(sessionKey string, name string, missing []string) {
	session := strings.TrimSpace(sessionKey)
	tool := strings.ToLower(strings.TrimSpace(name))
	if session == "" || tool == "" {
		return
	}
	now := time.Now()
	signature := invalidToolPatternSignature(tool, missing)
	s.invalidToolCacheMu.Lock()
	defer s.invalidToolCacheMu.Unlock()
	if s.invalidToolCache == nil {
		s.invalidToolCache = make(map[string]map[string]time.Time)
	}
	s.pruneInvalidToolCacheLocked(now)
	entry := s.invalidToolCache[session]
	if entry == nil {
		entry = make(map[string]time.Time)
		s.invalidToolCache[session] = entry
	}
	exp := now.Add(invalidToolCacheTTL)
	entry[signature] = exp
	entry[invalidToolPatternSignature(tool, nil)] = exp
}

func (s *Server) hasInvalidToolPattern(sessionKey string, name string, missing []string) bool {
	session := strings.TrimSpace(sessionKey)
	tool := strings.ToLower(strings.TrimSpace(name))
	if session == "" || tool == "" {
		return false
	}
	now := time.Now()
	signature := invalidToolPatternSignature(tool, missing)
	anySignature := invalidToolPatternSignature(tool, nil)
	s.invalidToolCacheMu.Lock()
	defer s.invalidToolCacheMu.Unlock()
	s.pruneInvalidToolCacheLocked(now)
	entry := s.invalidToolCache[session]
	if entry == nil {
		return false
	}
	if exp, ok := entry[signature]; ok && exp.After(now) {
		return true
	}
	if exp, ok := entry[anySignature]; ok && exp.After(now) {
		return true
	}
	return false
}

func invalidToolPatternSignature(tool string, missing []string) string {
	if len(missing) == 0 {
		return tool + "|any"
	}
	clean := make([]string, 0, len(missing))
	for _, item := range missing {
		field := strings.ToLower(strings.TrimSpace(item))
		if field != "" {
			clean = append(clean, field)
		}
	}
	if len(clean) == 0 {
		return tool + "|any"
	}
	sort.Strings(clean)
	return tool + "|" + strings.Join(clean, ",")
}

func (s *Server) pruneInvalidToolCacheLocked(now time.Time) {
	if s.invalidToolCache == nil {
		return
	}
	for session, entries := range s.invalidToolCache {
		for sig, exp := range entries {
			if !exp.After(now) {
				delete(entries, sig)
			}
		}
		if len(entries) == 0 {
			delete(s.invalidToolCache, session)
		}
	}
}

func extractClaudeSystemText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	textParts, _, _ := extractClaudeMessageParts(raw)
	return strings.TrimSpace(strings.Join(textParts, "\n"))
}

func extractClaudeMessageParts(raw json.RawMessage) ([]string, []string, []string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		text := strings.TrimSpace(asString)
		if text == "" {
			return nil, nil, nil
		}
		return []string{text}, nil, nil
	}

	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err != nil {
		return nil, nil, nil
	}
	textParts := make([]string, 0, len(asItems))
	toolCalls := make([]string, 0, 2)
	toolResults := make([]string, 0, 2)
	for _, it := range asItems {
		typ := strings.ToLower(strings.TrimSpace(coerceAnyText(it["type"])))
		switch typ {
		case "", "text":
			if text := strings.TrimSpace(coerceAnyText(it["text"])); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			name := strings.TrimSpace(coerceAnyText(it["name"]))
			if name == "" {
				continue
			}
			args := coerceAnyJSON(it["input"])
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", name, args))
		case "tool_result":
			toolUseID := strings.TrimSpace(coerceAnyText(it["tool_use_id"]))
			if toolUseID == "" {
				toolUseID = "unknown"
			}
			result := extractClaudeToolResultValue(it["content"])
			toolResults = append(toolResults, fmt.Sprintf("tool(%s): %s", toolUseID, result))
		}
	}
	return textParts, toolCalls, toolResults
}

func extractClaudeToolResultValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "{}"
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return "{}"
		}
		return trimmed
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t := strings.ToLower(strings.TrimSpace(coerceAnyText(obj["type"])))
			if t != "" && t != "text" {
				continue
			}
			text := strings.TrimSpace(coerceAnyText(obj["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		b, _ := json.Marshal(x)
		return strings.TrimSpace(string(b))
	default:
		b, _ := json.Marshal(x)
		raw := strings.TrimSpace(string(b))
		if raw == "" {
			return "{}"
		}
		return raw
	}
}

func respondErr(w http.ResponseWriter, code int, errType, msg string) {
	normalizedType := strings.TrimSpace(errType)
	if normalizedType == "" {
		normalizedType = "error"
	}
	respondJSON(w, code, map[string]any{
		"error": map[string]any{
			"type":    normalizedType,
			"message": msg,
			"code":    normalizedType,
			"param":   nil,
		},
	})
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		x := strings.TrimSpace(v)
		if x != "" {
			return x
		}
	}
	return ""
}

func promptFromResponsesInput(raw json.RawMessage, tools []ChatToolDef, toolChoice json.RawMessage) string {
	base := extractResponsesInput(raw)
	return promptFromTextWithTools(base, tools, toolChoice)
}

func extractResponsesInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err == nil {
		var parts []string
		for _, it := range asItems {
			typ, _ := it["type"].(string)
			switch strings.TrimSpace(strings.ToLower(typ)) {
			case "function_call":
				callID, _ := it["call_id"].(string)
				name, _ := it["name"].(string)
				args := coerceAnyJSON(it["arguments"])
				line := "assistant_tool_calls: " + strings.TrimSpace(name) + "(" + args + ")"
				if strings.TrimSpace(callID) != "" {
					line += " [id=" + strings.TrimSpace(callID) + "]"
				}
				parts = append(parts, strings.TrimSpace(line))
				continue
			case "function_call_output":
				callID, _ := it["call_id"].(string)
				output := strings.TrimSpace(coerceAnyText(it["output"]))
				if output == "" {
					output = "{}"
				}
				label := "tool"
				if strings.TrimSpace(callID) != "" {
					label += "(" + strings.TrimSpace(callID) + ")"
				}
				parts = append(parts, label+": "+output)
				continue
			}
			if role, _ := it["role"].(string); role != "" {
				if content, ok := it["content"].(string); ok {
					parts = append(parts, role+": "+content)
					continue
				}
				if arr, ok := it["content"].([]any); ok {
					for _, c := range arr {
						obj, _ := c.(map[string]any)
						if text, _ := obj["text"].(string); text != "" {
							parts = append(parts, role+": "+text)
						}
					}
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func coerceAnyText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return strings.TrimSpace(string(b))
	}
}

func coerceAnyJSON(v any) string {
	switch x := v.(type) {
	case nil:
		return "{}"
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return "{}"
		}
		if json.Valid([]byte(t)) {
			return t
		}
		b, _ := json.Marshal(t)
		return string(b)
	default:
		b, _ := json.Marshal(x)
		if len(bytes.TrimSpace(b)) == 0 {
			return "{}"
		}
		return string(b)
	}
}

func parseToolCallsFromText(text string, defs []ChatToolDef) ([]ChatToolCall, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil, false
	}
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start := strings.IndexAny(raw, "{[")
	end := strings.LastIndexAny(raw, "}]")
	if start < 0 || end < start {
		return nil, false
	}
	candidate := strings.TrimSpace(raw[start : end+1])
	allowed := map[string]struct{}{}
	for _, d := range defs {
		if strings.EqualFold(strings.TrimSpace(d.Type), "function") {
			name := strings.TrimSpace(toolDefName(d))
			if name != "" {
				allowed[name] = struct{}{}
			}
		}
	}
	type simpleCall struct {
		ID        string
		Name      string
		Arguments json.RawMessage
	}
	anyToRaw := func(v any) json.RawMessage {
		if v == nil {
			return json.RawMessage("{}")
		}
		if s, ok := v.(string); ok {
			trimmed := strings.TrimSpace(s)
			if trimmed == "" {
				return json.RawMessage("{}")
			}
			if json.Valid([]byte(trimmed)) {
				return json.RawMessage(trimmed)
			}
			b, _ := json.Marshal(trimmed)
			return json.RawMessage(b)
		}
		b, _ := json.Marshal(v)
		if len(bytes.TrimSpace(b)) == 0 || string(bytes.TrimSpace(b)) == "null" {
			return json.RawMessage("{}")
		}
		return json.RawMessage(b)
	}
	stringFromAny := func(v any) string {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	calls := make([]simpleCall, 0, 4)
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case []any:
			for _, item := range t {
				walk(item)
			}
		case map[string]any:
			if tc, ok := t["tool_calls"]; ok {
				walk(tc)
			}
			name := stringFromAny(t["name"])
			id := firstNonEmpty(stringFromAny(t["call_id"]), stringFromAny(t["id"]))
			args, hasArgs := t["arguments"]
			typ := strings.ToLower(strings.TrimSpace(stringFromAny(t["type"])))
			// Claude-style tool_use blocks carry args in `input`, not `arguments`.
			if !hasArgs {
				if v, ok := t["input"]; ok {
					args = v
					hasArgs = true
				}
			}
			if fn, ok := t["function"].(map[string]any); ok {
				name = firstNonEmpty(name, stringFromAny(fn["name"]))
				if v, ok := fn["arguments"]; ok {
					args = v
					hasArgs = true
				}
				if !hasArgs {
					if v, ok := fn["input"]; ok {
						args = v
						hasArgs = true
					}
				}
				typ = firstNonEmpty(typ, strings.ToLower(strings.TrimSpace(stringFromAny(fn["type"]))))
			}
			// Never synthesize empty `{}` arguments from random JSON snippets:
			// only treat as a call when arguments are explicitly present.
			looksLikeToolCall := name != "" && hasArgs
			if looksLikeToolCall {
				calls = append(calls, simpleCall{
					ID:        id,
					Name:      name,
					Arguments: anyToRaw(args),
				})
			}
		}
	}
	dec := json.NewDecoder(strings.NewReader(candidate))
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			break
		}
		walk(v)
	}
	if len(calls) == 0 {
		return nil, false
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		args := normalizeToolArguments(c.Arguments)
		if def, ok := findToolDefByName(defs, name); ok {
			if !isToolCallArgumentsValid(def, args) {
				continue
			}
		}
		callID := strings.TrimSpace(c.ID)
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		out = append(out, ChatToolCall{
			ID:   callID,
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func resolveToolCalls(text string, defs []ChatToolDef, native []ChatToolCall) ([]ChatToolCall, bool) {
	if len(native) > 0 {
		filtered, _ := filterToolCallsByDefs(native, defs)
		if len(filtered) == 0 {
			return nil, false
		}
		return filtered, true
	}
	if len(defs) == 0 {
		return nil, false
	}
	return parseToolCallsFromText(text, defs)
}

func mapProviderToolCalls(calls []provider.ToolCall) []ChatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(c.ID)
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		args := normalizeToolArguments(json.RawMessage(c.Arguments))
		out = append(out, ChatToolCall{
			ID:   callID,
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterToolCallsByDefs(calls []ChatToolCall, defs []ChatToolDef) ([]ChatToolCall, bool) {
	if len(calls) == 0 {
		return nil, false
	}
	allowed := map[string]struct{}{}
	for _, d := range defs {
		if strings.EqualFold(strings.TrimSpace(d.Type), "function") {
			name := strings.TrimSpace(toolDefName(d))
			if name != "" {
				allowed[name] = struct{}{}
			}
		}
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Function.Name)
		if name == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		if strings.TrimSpace(c.ID) == "" {
			c.ID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		if strings.TrimSpace(c.Type) == "" {
			c.Type = "function"
		}
		c.Function.Arguments = normalizeToolArguments(json.RawMessage(c.Function.Arguments))
		if def, ok := findToolDefByName(defs, name); ok {
			if !isToolCallArgumentsValid(def, c.Function.Arguments) {
				continue
			}
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func toolDefName(def ChatToolDef) string {
	if n := strings.TrimSpace(def.Function.Name); n != "" {
		return n
	}
	return strings.TrimSpace(def.Name)
}

func findToolDefByName(defs []ChatToolDef, name string) (ChatToolDef, bool) {
	target := strings.TrimSpace(name)
	if target == "" {
		return ChatToolDef{}, false
	}
	for _, def := range defs {
		if strings.EqualFold(strings.TrimSpace(toolDefName(def)), target) {
			return def, true
		}
	}
	return ChatToolDef{}, false
}

func isToolCallArgumentsValid(def ChatToolDef, argsRaw string) bool {
	return len(missingRequiredToolFields(def, argsRaw)) == 0
}

func missingRequiredToolFields(def ChatToolDef, argsRaw string) []string {
	required := toolDefRequiredFields(def)
	if len(required) == 0 {
		return nil
	}
	argsText := strings.TrimSpace(argsRaw)
	if argsText == "" {
		return append([]string(nil), required...)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(argsText), &parsed); err != nil || parsed == nil {
		return append([]string(nil), required...)
	}
	missing := make([]string, 0, len(required))
	for _, key := range required {
		v, ok := parsed[key]
		if !ok || v == nil {
			missing = append(missing, key)
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func toolDefRequiredFields(def ChatToolDef) []string {
	raw := bytes.TrimSpace(def.Function.Parameters)
	if len(raw) == 0 {
		raw = bytes.TrimSpace(def.Parameters)
	}
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil
	}
	requiredRaw, ok := obj["required"]
	if !ok {
		return nil
	}
	switch items := requiredRaw.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			name := strings.TrimSpace(coerceAnyText(item))
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			name := strings.TrimSpace(item)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func toolDefForPrompt(def ChatToolDef) any {
	name := toolDefName(def)
	typ := strings.TrimSpace(def.Type)
	if typ == "" {
		typ = "function"
	}
	if strings.TrimSpace(def.Function.Name) != "" {
		return def
	}
	out := map[string]any{
		"type": typ,
		"name": name,
	}
	if desc := strings.TrimSpace(def.Description); desc != "" {
		out["description"] = desc
	}
	if len(bytes.TrimSpace(def.Parameters)) > 0 {
		var params any
		if err := json.Unmarshal(def.Parameters, &params); err == nil {
			out["parameters"] = params
		}
	}
	return out
}

func normalizeToolArguments(raw json.RawMessage) string {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || string(b) == "null" {
		return "{}"
	}
	if json.Valid(b) {
		return string(b)
	}
	enc, _ := json.Marshal(string(b))
	return string(enc)
}

func streamChatCompletionText(w http.ResponseWriter, flusher http.Flusher, chunkID string, model string, text string, usage Usage, includeUsageChunk bool) {
	text = strings.TrimSpace(text)
	if text != "" {
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: text}}},
		})
	}
	streamChatCompletionFinalStop(w, flusher, chunkID, model, usage, includeUsageChunk)
}

func streamChatCompletionFinalStop(w http.ResponseWriter, flusher http.Flusher, chunkID string, model string, usage Usage, includeUsageChunk bool) {
	finalUsage := &usage
	if includeUsageChunk {
		finalUsage = nil
	}
	writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant"}, FinishReason: ptrString("stop")}},
		Usage:   finalUsage,
	})
	if includeUsageChunk {
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{},
			Usage:   &usage,
		})
	}
	writeChatCompletionsDone(w, flusher)
}

func streamChatCompletionToolCalls(w http.ResponseWriter, flusher http.Flusher, chunkID string, model string, calls []ChatToolCall, usage Usage, includeUsageChunk bool) {
	writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant"}}},
	})
	for i, call := range calls {
		idx := i
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{{
				Index: 0,
				Delta: ChatMessage{
					ToolCalls: []ChatToolCall{{
						Index: &idx,
						ID:    call.ID,
						Type:  "function",
						Function: ChatToolFunctionCall{
							Name: call.Function.Name,
						},
					}},
				},
			}},
		})
		if strings.TrimSpace(call.Function.Arguments) != "" {
			writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
				ID:      chunkID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   model,
				Choices: []ChatChunkChoice{{
					Index: 0,
					Delta: ChatMessage{
						ToolCalls: []ChatToolCall{{
							Index: &idx,
							Function: ChatToolFunctionCall{
								Arguments: call.Function.Arguments,
							},
						}},
					},
				}},
			})
		}
	}
	finalUsage := &usage
	if includeUsageChunk {
		finalUsage = nil
	}
	writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{}, FinishReason: ptrString("tool_calls")}},
		Usage:   finalUsage,
	})
	if includeUsageChunk {
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{},
			Usage:   &usage,
		})
	}
	writeChatCompletionsDone(w, flusher)
}

func responsesMessageOutputItems(text string) []ResponsesItem {
	return []ResponsesItem{
		{
			Type:   "message",
			ID:     "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Status: "completed",
			Role:   "assistant",
			Content: []ResponsesText{
				{Type: "output_text", Text: text, Annotations: []ResponsesRef{}},
			},
		},
	}
}

func responsesFunctionCallOutputItems(calls []ChatToolCall) []ResponsesItem {
	items := make([]ResponsesItem, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		items = append(items, ResponsesItem{
			Type:      "function_call",
			ID:        "fc_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Status:    "completed",
			CallID:    callID,
			Name:      strings.TrimSpace(call.Function.Name),
			Arguments: strings.TrimSpace(call.Function.Arguments),
		})
	}
	return items
}

func streamResponsesText(
	emit func(string, map[string]any),
	reqID string,
	model string,
	text string,
	usage ResponsesUsage,
	createdAt int64,
) {
	text = strings.TrimSpace(text)
	textItemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	emit("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"type":    "message",
			"id":      textItemID,
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	})
	if text != "" {
		emit("response.output_text.delta", map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"delta":         text,
			"logprobs":      []any{},
		})
		emit("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"text":          text,
			"logprobs":      []any{},
		})
	}
	outputItem := map[string]any{
		"type":   "message",
		"id":     textItemID,
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]any{
			{"type": "output_text", "text": text, "annotations": []any{}},
		},
	}
	emit("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item":         outputItem,
	})
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": buildResponseObject(reqID, model, "completed", []any{outputItem}, map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.TotalTokens,
		}, createdAt),
	}
	emit("response.completed", completedEvent)
}

func streamResponsesFunctionCalls(
	emit func(string, map[string]any),
	reqID string,
	model string,
	calls []ChatToolCall,
	usage ResponsesUsage,
	createdAt int64,
) {
	output := responsesFunctionCallOutputItems(calls)
	for i, item := range output {
		emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": i,
			"item":         item,
		})
		if strings.TrimSpace(item.Arguments) != "" {
			emit("response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      item.ID,
				"output_index": i,
				"delta":        item.Arguments,
			})
			emit("response.function_call_arguments.done", map[string]any{
				"type":         "response.function_call_arguments.done",
				"item_id":      item.ID,
				"output_index": i,
				"arguments":    item.Arguments,
				"name":         item.Name,
			})
		}
		emit("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": i,
			"item":         item,
		})
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": buildResponseObject(reqID, model, "completed", anySlice(output), map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.TotalTokens,
		}, createdAt),
	}
	emit("response.completed", completedEvent)
}

func writeChatCompletionsChunk(w http.ResponseWriter, flusher http.Flusher, chunk ChatCompletionsChunk) {
	b, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func writeChatCompletionsDone(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	b, _ := json.Marshal(payload)
	if strings.TrimSpace(event) != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeOpenAISSE(w http.ResponseWriter, payload any) {
	b, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func startSSEKeepAlive(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, interval time.Duration) func() {
	if interval <= 0 {
		interval = 8 * time.Second
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keep-alive\n\n")
				flusher.Flush()
			}
		}
	}()
	return func() {
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
		<-doneCh
	}
}

func resolveSSEKeepAliveInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_SSE_KEEPALIVE_SECONDS"))
	if raw == "" {
		return 8 * time.Second
	}
	sec, err := strconv.Atoi(raw)
	if err != nil {
		return 8 * time.Second
	}
	if sec < 2 {
		sec = 2
	}
	if sec > 30 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

func cloneSSEType(event map[string]any, typ string) map[string]any {
	if event == nil {
		return map[string]any{"type": strings.TrimSpace(typ)}
	}
	out := make(map[string]any, len(event))
	for k, v := range event {
		if k == "sequence_number" {
			continue
		}
		out[k] = v
	}
	out["type"] = strings.TrimSpace(typ)
	return out
}

func anySlice[T any](items []T) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func buildResponseObject(reqID, model, status string, output []any, usage any, createdAt int64) map[string]any {
	now := createdAt
	if now <= 0 {
		now = time.Now().Unix()
	}
	completedAt := any(nil)
	if strings.TrimSpace(status) == "completed" {
		completedAt = now
	}
	return map[string]any{
		"id":                   reqID,
		"object":               "response",
		"created_at":           now,
		"output_text":          responseOutputText(output),
		"status":               firstNonEmpty(strings.TrimSpace(status), "completed"),
		"completed_at":         completedAt,
		"error":                nil,
		"incomplete_details":   nil,
		"instructions":         nil,
		"max_output_tokens":    nil,
		"model":                model,
		"output":               output,
		"parallel_tool_calls":  true,
		"previous_response_id": nil,
		"reasoning": map[string]any{
			"effort":  nil,
			"summary": nil,
		},
		"store":       true,
		"temperature": 1.0,
		"text": map[string]any{
			"format": map[string]any{"type": "text"},
		},
		"tool_choice": "auto",
		"tools":       []any{},
		"top_p":       1.0,
		"truncation":  "disabled",
		"usage":       usage,
		"user":        nil,
		"metadata":    map[string]any{},
	}
}

func responseOutputText(output []any) string {
	if len(output) == 0 {
		return ""
	}
	parts := make([]string, 0, len(output))
	for _, raw := range output {
		switch item := raw.(type) {
		case ResponsesItem:
			if strings.TrimSpace(strings.ToLower(item.Type)) != "message" {
				continue
			}
			for _, c := range item.Content {
				if strings.TrimSpace(strings.ToLower(c.Type)) != "output_text" {
					continue
				}
				if text := strings.TrimSpace(c.Text); text != "" {
					parts = append(parts, text)
				}
			}
		case map[string]any:
			if strings.TrimSpace(strings.ToLower(asString(item["type"]))) != "message" {
				continue
			}
			switch content := item["content"].(type) {
			case []any:
				for _, cRaw := range content {
					c, _ := cRaw.(map[string]any)
					if c == nil {
						continue
					}
					if strings.TrimSpace(strings.ToLower(asString(c["type"]))) != "output_text" {
						continue
					}
					if text := strings.TrimSpace(asString(c["text"])); text != "" {
						parts = append(parts, text)
					}
				}
			case []map[string]any:
				for _, c := range content {
					if strings.TrimSpace(strings.ToLower(asString(c["type"]))) != "output_text" {
						continue
					}
					if text := strings.TrimSpace(asString(c["text"])); text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func setResolvedAccountHeaders(w http.ResponseWriter, account store.Account) {
	if w == nil {
		return
	}
	if rec, ok := w.(*trafficRecorder); ok {
		rec.accountID = strings.TrimSpace(account.ID)
		rec.accountEmail = strings.TrimSpace(account.Email)
		return
	}
}

func (s *Server) currentAPIKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apiKey
}

func (s *Server) isValidAPIKey(r *http.Request) bool {
	key := s.currentAPIKey()
	if BearerToken(r.Header.Get("Authorization")) == key {
		return true
	}
	return strings.TrimSpace(r.Header.Get("x-api-key")) == key
}

func (s *Server) setAPIKey(v string) {
	s.mu.Lock()
	s.apiKey = v
	s.mu.Unlock()
}

func (s *Server) setAdminPasswordHash(v string) {
	s.mu.Lock()
	s.adminPasswordHash = strings.TrimSpace(v)
	s.mu.Unlock()
}

func (s *Server) currentAdminPasswordHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.adminPasswordHash)
}

func (s *Server) bootstrapSettingsFromStore(ctx context.Context) error {
	if s.svc == nil || s.svc.Store == nil {
		return nil
	}
	cfg := s.svc.Cfg
	var errs []string

	legacyAPIKey, legacyExists, legacyErr := s.svc.Store.MustGetSetting(ctx, store.SettingLegacyAPIKey)
	if legacyErr != nil {
		errs = append(errs, legacyErr.Error())
	}
	apiKey, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingAPIKey)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		apiKey = strings.TrimSpace(apiKey)
		if apiKey != "" {
			cfg.ProxyAPIKey = apiKey
			s.setAPIKey(apiKey)
		}
	} else if legacyExists {
		legacyAPIKey = strings.TrimSpace(legacyAPIKey)
		if legacyAPIKey != "" {
			cfg.ProxyAPIKey = legacyAPIKey
			if err := s.svc.Store.SetSetting(ctx, store.SettingAPIKey, legacyAPIKey); err != nil {
				errs = append(errs, err.Error())
			}
			_ = s.svc.Store.DeleteSetting(ctx, store.SettingLegacyAPIKey)
			s.setAPIKey(legacyAPIKey)
		}
	} else {
		if strings.TrimSpace(cfg.ProxyAPIKey) == "" {
			if k, genErr := randomProxyKey(); genErr == nil {
				cfg.ProxyAPIKey = k
			} else {
				errs = append(errs, genErr.Error())
			}
		}
		if strings.TrimSpace(cfg.ProxyAPIKey) != "" {
			if err := s.svc.Store.SetSetting(ctx, store.SettingAPIKey, cfg.ProxyAPIKey); err != nil {
				errs = append(errs, err.Error())
			}
			s.setAPIKey(cfg.ProxyAPIKey)
		}
	}

	apiMode, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingAPIMode)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.APIMode = config.NormalizeAPIMode(apiMode)
	} else {
		cfg.APIMode = config.NormalizeAPIMode(cfg.APIMode)
		if err := s.svc.Store.SetSetting(ctx, store.SettingAPIMode, cfg.APIMode); err != nil {
			errs = append(errs, err.Error())
		}
	}

	cliStrategy, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingCodingCLIStrategy)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.CodingCLIStrategy = config.NormalizeCodingCLIStrategy(cliStrategy)
	} else {
		cfg.CodingCLIStrategy = config.NormalizeCodingCLIStrategy(cfg.CodingCLIStrategy)
		if err := s.svc.Store.SetSetting(ctx, store.SettingCodingCLIStrategy, cfg.CodingCLIStrategy); err != nil {
			errs = append(errs, err.Error())
		}
	}

	directStrategy, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingDirectAPIStrategy)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(directStrategy)
	} else {
		cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(cfg.DirectAPIStrategy)
		if err := s.svc.Store.SetSetting(ctx, store.SettingDirectAPIStrategy, cfg.DirectAPIStrategy); err != nil {
			errs = append(errs, err.Error())
		}
	}

	zoStrategy, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingZoAPIStrategy)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(zoStrategy)
	} else {
		cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(cfg.ZoAPIStrategy)
		if err := s.svc.Store.SetSetting(ctx, store.SettingZoAPIStrategy, cfg.ZoAPIStrategy); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageAlertThreshold, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageAlertThreshold)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageAlertThreshold)); parseErr == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAlertThreshold = parsed
		}
	} else {
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageAlertThreshold, strconv.Itoa(cfg.UsageAlertThreshold)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageAutoSwitchThreshold, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageAutoSwitchThreshold)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageAutoSwitchThreshold)); parseErr == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAutoSwitchThreshold = parsed
		}
	} else {
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageAutoSwitchThreshold, strconv.Itoa(cfg.UsageAutoSwitchThreshold)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageSchedulerInterval, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageSchedulerInterval)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(usageSchedulerInterval)); parseErr == nil {
			cfg.UsageSchedulerInterval = config.NormalizeUsageSchedulerIntervalMinutes(parsed)
		}
	} else {
		cfg.UsageSchedulerInterval = config.NormalizeUsageSchedulerIntervalMinutes(cfg.UsageSchedulerInterval)
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageSchedulerInterval, strconv.Itoa(cfg.UsageSchedulerInterval)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	usageSchedulerEnabled, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingUsageSchedulerEnabled)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok && strings.EqualFold(strings.TrimSpace(usageSchedulerEnabled), "false") {
		if err := s.svc.Store.SetSetting(ctx, store.SettingUsageSchedulerEnabled, "true"); err != nil {
			errs = append(errs, err.Error())
		}
	}
	cfg.UsageSchedulerEnabled = true

	codexHome, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingCodexHome)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		codexHome = strings.TrimSpace(codexHome)
		if codexHome != "" {
			cfg.CodexHome = codexHome
		}
	} else {
		if strings.TrimSpace(cfg.CodexHome) != "" {
			if err := s.svc.Store.SetSetting(ctx, store.SettingCodexHome, strings.TrimSpace(cfg.CodexHome)); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}

	mappingsRaw, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingModelMappings)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok && strings.TrimSpace(mappingsRaw) != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(mappingsRaw), &parsed); err != nil {
			errs = append(errs, err.Error())
		} else {
			cfg.ModelMappings = parsed
		}
	} else {
		if cfg.ModelMappings == nil {
			cfg.ModelMappings = map[string]string{}
		}
		if raw, err := json.Marshal(cfg.ModelMappings); err == nil {
			if err := s.svc.Store.SetSetting(ctx, store.SettingModelMappings, string(raw)); err != nil {
				errs = append(errs, err.Error())
			}
		} else {
			errs = append(errs, err.Error())
		}
	}

	passwordHash, ok, err := s.svc.Store.MustGetSetting(ctx, store.SettingAdminPasswordHash)
	if err != nil {
		errs = append(errs, err.Error())
	} else if ok {
		passwordHash = strings.TrimSpace(passwordHash)
		if passwordHash != "" {
			cfg.AdminPasswordHash = passwordHash
			s.setAdminPasswordHash(passwordHash)
		}
	} else if strings.TrimSpace(cfg.AdminPasswordHash) != "" {
		if err := s.svc.Store.SetSetting(ctx, store.SettingAdminPasswordHash, strings.TrimSpace(cfg.AdminPasswordHash)); err != nil {
			errs = append(errs, err.Error())
		}
	}

	s.svc.Cfg = cfg
	if len(errs) > 0 {
		return fmt.Errorf("settings bootstrap: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *Server) saveSetting(ctx context.Context, key string, value string) error {
	if s != nil && s.svc != nil && s.svc.Store != nil {
		return s.svc.Store.SetSetting(ctx, key, value)
	}
	cfg, err := config.LoadOrInit()
	if err != nil {
		return err
	}
	switch key {
	case store.SettingAPIKey:
		cfg.ProxyAPIKey = strings.TrimSpace(value)
	case store.SettingAPIMode:
		cfg.APIMode = config.NormalizeAPIMode(value)
	case store.SettingDirectAPIStrategy:
		cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(value)
	case store.SettingCodingCLIStrategy:
		cfg.CodingCLIStrategy = config.NormalizeCodingCLIStrategy(value)
	case store.SettingZoAPIStrategy:
		cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(value)
	case store.SettingCodexHome:
		cfg.CodexHome = strings.TrimSpace(value)
	case store.SettingUsageAlertThreshold:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAlertThreshold = parsed
		}
	case store.SettingUsageAutoSwitchThreshold:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			if parsed < 0 {
				parsed = 0
			}
			if parsed > 100 {
				parsed = 100
			}
			cfg.UsageAutoSwitchThreshold = parsed
		}
	case store.SettingUsageSchedulerEnabled:
		cfg.UsageSchedulerEnabled = true
	case store.SettingUsageSchedulerInterval:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			cfg.UsageSchedulerInterval = config.NormalizeUsageSchedulerIntervalMinutes(parsed)
		}
	case store.SettingModelMappings:
		mappings := map[string]string{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &mappings); err != nil {
			return err
		}
		cfg.ModelMappings = mappings
	case store.SettingAdminPasswordHash:
		cfg.AdminPasswordHash = strings.TrimSpace(value)
	default:
		return nil
	}
	return config.Save(cfg)
}

func (s *Server) currentAPIMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return config.NormalizeAPIMode(s.svc.Cfg.APIMode)
}

func (s *Server) currentDirectAPIStrategy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return config.NormalizeDirectAPIStrategy(s.svc.Cfg.DirectAPIStrategy)
}

func (s *Server) currentModelMappings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for k, v := range config.Default().ModelMappings {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	for k, v := range s.svc.Cfg.ModelMappings {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func (s *Server) resolveMappedModel(requested string) string {
	model := strings.TrimSpace(requested)
	if model == "" {
		return model
	}
	modelLower := strings.ToLower(model)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if target, ok := s.svc.Cfg.ModelMappings[model]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	if target, ok := s.svc.Cfg.ModelMappings[modelLower]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	// Default Claude alias fallback so proxy remains usable before explicit UI seeding.
	if target, ok := claudeCodeModelPresetDefaults[modelLower]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	return model
}

func (s *Server) upsertModelMapping(alias, model string) error {
	s.mu.Lock()
	cfg := s.svc.Cfg
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	cfg.ModelMappings[strings.TrimSpace(alias)] = strings.TrimSpace(model)
	raw, err := json.Marshal(cfg.ModelMappings)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	return s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
}

func (s *Server) deleteModelMapping(alias string) error {
	s.mu.Lock()
	cfg := s.svc.Cfg
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	delete(cfg.ModelMappings, strings.TrimSpace(alias))
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	raw, err := json.Marshal(cfg.ModelMappings)
	if err != nil {
		return err
	}
	return s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
}

func codexAvailableModels() []string {
	return []string{
		"gpt-5.1-codex-max",
		"gpt-5.2",
		"gpt-5.2-codex",
		"gpt-5.3-codex",
		"gpt-5.4-mini",
		"gpt-5.4",
	}
}

func isValidCodexModel(model string) bool {
	m := strings.TrimSpace(model)
	if m == "" {
		return false
	}
	for _, v := range codexAvailableModels() {
		if v == m {
			return true
		}
	}
	return false
}

func (s *Server) handleWebSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		base := externalBaseURLFromRequest(r, s.bindAddr)
		modelMappings := s.currentModelMappings()
		s.mu.RLock()
		usageAlertThreshold := s.svc.Cfg.UsageAlertThreshold
		usageAutoSwitchThreshold := s.svc.Cfg.UsageAutoSwitchThreshold
		usageSchedulerEnabled := true
		usageSchedulerInterval := config.NormalizeUsageSchedulerIntervalMinutes(s.svc.Cfg.UsageSchedulerInterval)
		directAPIStrategy := config.NormalizeDirectAPIStrategy(s.svc.Cfg.DirectAPIStrategy)
		codingCLIStrategy := config.NormalizeCodingCLIStrategy(s.svc.Cfg.CodingCLIStrategy)
		zoStrategy := config.NormalizeZoAPIStrategy(s.svc.Cfg.ZoAPIStrategy)
		s.mu.RUnlock()
		updateInfo := s.getUpdateInfo(r.Context(), false)
		claudeCodeStatus := s.claudeCodeIntegrationStatus(base)
		respondJSON(w, 200, map[string]any{
			"api_key":                          s.currentAPIKey(),
			"api_mode":                         s.currentAPIMode(),
			"openai_endpoint":                  strings.TrimRight(base, "/") + "/v1/chat/completions",
			"claude_endpoint":                  strings.TrimRight(base, "/") + "/v1/messages",
			"auth_json_endpoint":               strings.TrimRight(base, "/") + "/v1/auth.json",
			"usage_status_endpoint":            strings.TrimRight(base, "/") + "/v1/usage",
			"openai_models_url":                strings.TrimRight(base, "/") + "/v1/models",
			"openai_chat_url":                  strings.TrimRight(base, "/") + "/v1/chat/completions",
			"openai_responses_url":             strings.TrimRight(base, "/") + "/v1/responses",
			"zo_models_url":                    strings.TrimRight(base, "/") + "/zo/v1/models",
			"zo_chat_url":                      strings.TrimRight(base, "/") + "/zo/v1/chat/completions",
			"available_models":                 codexAvailableModels(),
			"model_mappings":                   modelMappings,
			"usage_alert_threshold":            usageAlertThreshold,
			"usage_auto_switch_threshold":      usageAutoSwitchThreshold,
			"usage_scheduler_enabled":          usageSchedulerEnabled,
			"usage_scheduler_interval_minutes": usageSchedulerInterval,
			"direct_api_strategy":              directAPIStrategy,
			"coding_cli_strategy":              codingCLIStrategy,
			"zo_api_strategy":                  zoStrategy,
			"direct_api_inject_prompt":         true,
			"claude_code":                      claudeCodeStatus,
			"app_version":                      updateInfo.CurrentVersion,
			"codex_version":                    firstNonEmpty(s.codexVersion, "unknown"),
			"latest_version":                   updateInfo.LatestVersion,
			"release_url":                      updateInfo.ReleaseURL,
			"latest_changelog":                 updateInfo.LatestChangelog,
			"update_available":                 updateInfo.UpdateAvailable,
			"update_checked_at":                updateInfo.CheckedAt,
			"update_check_error":               updateInfo.CheckError,
		})
		return
	case http.MethodPost:
		var req struct {
			APIMode                  *string `json:"api_mode"`
			UsageAlertThreshold      *int    `json:"usage_alert_threshold"`
			UsageAutoSwitchThreshold *int    `json:"usage_auto_switch_threshold"`
			UsageSchedulerEnabled    *bool   `json:"usage_scheduler_enabled"`
			UsageSchedulerInterval   *int    `json:"usage_scheduler_interval_minutes"`
			DirectAPIStrategy        *string `json:"direct_api_strategy"`
			CodingCLIStrategy        *string `json:"coding_cli_strategy"`
			ZoAPIStrategy            *string `json:"zo_api_strategy"`
			AdminPassword            *string `json:"admin_password"`
			DirectAPIInjectPrompt    *bool   `json:"direct_api_inject_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		s.mu.Lock()
		cfg := s.svc.Cfg
		before := cfg
		if req.APIMode != nil {
			cfg.APIMode = config.NormalizeAPIMode(strings.TrimSpace(*req.APIMode))
		}
		if req.UsageAlertThreshold != nil {
			v := *req.UsageAlertThreshold
			if v < 0 || v > 100 {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "usage_alert_threshold must be in range 0..100")
				return
			}
			cfg.UsageAlertThreshold = v
		}
		if req.UsageAutoSwitchThreshold != nil {
			v := *req.UsageAutoSwitchThreshold
			if v < 0 || v > 100 {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "usage_auto_switch_threshold must be in range 0..100")
				return
			}
			cfg.UsageAutoSwitchThreshold = v
		}
		if req.UsageSchedulerEnabled != nil {
			cfg.UsageSchedulerEnabled = true
		}
		if req.UsageSchedulerInterval != nil {
			v := config.NormalizeUsageSchedulerIntervalMinutes(*req.UsageSchedulerInterval)
			cfg.UsageSchedulerInterval = v
		}
		if req.DirectAPIStrategy != nil {
			cfg.DirectAPIStrategy = config.NormalizeDirectAPIStrategy(strings.TrimSpace(*req.DirectAPIStrategy))
		}
		if req.CodingCLIStrategy != nil {
			cfg.CodingCLIStrategy = config.NormalizeCodingCLIStrategy(strings.TrimSpace(*req.CodingCLIStrategy))
		}
		if req.ZoAPIStrategy != nil {
			cfg.ZoAPIStrategy = config.NormalizeZoAPIStrategy(strings.TrimSpace(*req.ZoAPIStrategy))
		}
		var adminPasswordChanged bool
		if req.AdminPassword != nil {
			plain := strings.TrimSpace(*req.AdminPassword)
			if plain == "" {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "admin_password cannot be empty")
				return
			}
			cfg.AdminPasswordHash = config.HashPassword(plain)
			adminPasswordChanged = true
		}
		cfg.DirectAPIInjectPrompt = true
		cfg.UsageSchedulerEnabled = true
		s.svc.Cfg = cfg
		s.adminPasswordHash = strings.TrimSpace(cfg.AdminPasswordHash)
		s.mu.Unlock()
		if req.APIMode != nil {
			if err := s.saveSetting(r.Context(), store.SettingAPIMode, cfg.APIMode); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.CodingCLIStrategy != nil {
			if err := s.saveSetting(r.Context(), store.SettingCodingCLIStrategy, cfg.CodingCLIStrategy); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.DirectAPIStrategy != nil {
			if err := s.saveSetting(r.Context(), store.SettingDirectAPIStrategy, cfg.DirectAPIStrategy); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.ZoAPIStrategy != nil {
			if err := s.saveSetting(r.Context(), store.SettingZoAPIStrategy, cfg.ZoAPIStrategy); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageAlertThreshold != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageAlertThreshold, strconv.Itoa(cfg.UsageAlertThreshold)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageAutoSwitchThreshold != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageAutoSwitchThreshold, strconv.Itoa(cfg.UsageAutoSwitchThreshold)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageSchedulerEnabled != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageSchedulerEnabled, "true"); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if req.UsageSchedulerInterval != nil {
			if err := s.saveSetting(r.Context(), store.SettingUsageSchedulerInterval, strconv.Itoa(cfg.UsageSchedulerInterval)); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}
		if adminPasswordChanged {
			if err := s.saveSetting(r.Context(), store.SettingAdminPasswordHash, cfg.AdminPasswordHash); err != nil {
				respondErr(w, 500, "internal_error", err.Error())
				return
			}
		}

		changed := map[string]map[string]any{}
		if before.APIMode != cfg.APIMode {
			changed["api_mode"] = map[string]any{"from": before.APIMode, "to": cfg.APIMode}
		}
		if before.UsageAlertThreshold != cfg.UsageAlertThreshold {
			changed["usage_alert_threshold"] = map[string]any{"from": before.UsageAlertThreshold, "to": cfg.UsageAlertThreshold}
		}
		if before.UsageAutoSwitchThreshold != cfg.UsageAutoSwitchThreshold {
			changed["usage_auto_switch_threshold"] = map[string]any{"from": before.UsageAutoSwitchThreshold, "to": cfg.UsageAutoSwitchThreshold}
		}
		if before.UsageSchedulerEnabled != cfg.UsageSchedulerEnabled {
			changed["usage_scheduler_enabled"] = map[string]any{"from": before.UsageSchedulerEnabled, "to": cfg.UsageSchedulerEnabled}
		}
		if before.UsageSchedulerInterval != cfg.UsageSchedulerInterval {
			changed["usage_scheduler_interval_minutes"] = map[string]any{"from": before.UsageSchedulerInterval, "to": cfg.UsageSchedulerInterval}
		}
		if before.DirectAPIStrategy != cfg.DirectAPIStrategy {
			changed["direct_api_strategy"] = map[string]any{"from": before.DirectAPIStrategy, "to": cfg.DirectAPIStrategy}
		}
		if before.CodingCLIStrategy != cfg.CodingCLIStrategy {
			changed["coding_cli_strategy"] = map[string]any{"from": before.CodingCLIStrategy, "to": cfg.CodingCLIStrategy}
		}
		if before.ZoAPIStrategy != cfg.ZoAPIStrategy {
			changed["zo_api_strategy"] = map[string]any{"from": before.ZoAPIStrategy, "to": cfg.ZoAPIStrategy}
		}
		if req.AdminPassword != nil {
			changed["admin_password"] = map[string]any{"from": "updated", "to": "updated"}
		}
		if len(changed) > 0 {
			s.svc.AddSystemLog(r.Context(), "settings_change", "Settings updated", map[string]any{
				"changed": changed,
				"source":  "ui",
			})
		}

		respondJSON(w, 200, map[string]any{
			"api_mode":                         cfg.APIMode,
			"direct_api_strategy":              cfg.DirectAPIStrategy,
			"coding_cli_strategy":              cfg.CodingCLIStrategy,
			"zo_api_strategy":                  cfg.ZoAPIStrategy,
			"direct_api_inject_prompt":         true,
			"ok":                               true,
			"usage_alert_threshold":            cfg.UsageAlertThreshold,
			"usage_auto_switch_threshold":      cfg.UsageAutoSwitchThreshold,
			"usage_scheduler_enabled":          cfg.UsageSchedulerEnabled,
			"usage_scheduler_interval_minutes": config.NormalizeUsageSchedulerIntervalMinutes(cfg.UsageSchedulerInterval),
		})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) shouldInjectDirectAPIPrompt() bool {
	return true
}

func (s *Server) handleWebClaudeCodeSettings(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURLFromRequest(r, s.bindAddr)
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"claude_code": s.claudeCodeIntegrationStatus(base),
		})
		return
	case http.MethodPost:
		status, err := s.enableClaudeCodeIntegration(base, claudeCodeEnableOptions{})
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"claude_code": status,
		})
		return
	default:
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
}

type claudeCodeIntegrationStatus struct {
	Connected       bool              `json:"connected"`
	BaseURL         string            `json:"base_url"`
	APIKey          string            `json:"api_key"`
	Provider        string            `json:"provider"`
	EnvFilePath     string            `json:"env_file_path"`
	Profiles        []string          `json:"profiles"`
	ModelPreset     map[string]string `json:"model_preset"`
	ActivateCommand string            `json:"activate_command"`
}

var claudeCodeModelPresetDefaults = map[string]string{
	"claude-opus-4-1":            "gpt-5.3-codex",
	"claude-opus-4-0":            "gpt-5.3-codex",
	"claude-3-opus-20240229":     "gpt-5.3-codex",
	"claude-sonnet-4-6":          "gpt-5.2-codex",
	"claude-sonnet-4-5":          "gpt-5.2-codex",
	"claude-sonnet-4-1":          "gpt-5.2-codex",
	"claude-sonnet-4-0":          "gpt-5.2-codex",
	"claude-sonnet-4":            "gpt-5.2-codex",
	"claude-3-7-sonnet-latest":   "gpt-5.2-codex",
	"claude-3-5-sonnet-latest":   "gpt-5.1-codex-max",
	"claude-3-5-sonnet-20241022": "gpt-5.1-codex-max",
	"claude-haiku-4-5-20251001":  "gpt-5.1-codex-max",
	"claude-haiku-4-5":           "gpt-5.1-codex-max",
	"claude-3-5-haiku-latest":    "gpt-5.1-codex-max",
	"claude-3-5-haiku-20241022":  "gpt-5.1-codex-max",
	"claude-haiku-3-5":           "gpt-5.1-codex-max",
}

func (s *Server) claudeCodeIntegrationStatus(base string) claudeCodeIntegrationStatus {
	baseURL := strings.TrimRight(strings.TrimSpace(base), "/")
	provider := "codex"
	if baseURL != "" {
		if apiKey := strings.TrimSpace(s.currentAPIKey()); apiKey != "" {
			_ = ensureClaudeEnvFile(baseURL, apiKey)
		}
	}
	envPath, profilePaths := claudeCodeIntegrationPaths()
	currentMappings := s.currentModelMappings()
	profilesConfigured := make([]string, 0, len(profilePaths))
	for _, p := range profilePaths {
		ok, err := profileHasCodexsessSource(p, envPath)
		if err == nil && ok {
			profilesConfigured = append(profilesConfigured, p)
		}
	}
	connected := false
	if len(profilesConfigured) > 0 {
		if b, err := os.ReadFile(envPath); err == nil {
			content := string(b)
			hasBase := strings.Contains(content, "ANTHROPIC_BASE_URL=")
			hasToken := strings.Contains(content, "ANTHROPIC_AUTH_TOKEN=")
			connected = hasBase && hasToken
		}
	}
	return claudeCodeIntegrationStatus{
		Connected:       connected,
		BaseURL:         baseURL,
		APIKey:          s.currentAPIKey(),
		Provider:        provider,
		EnvFilePath:     envPath,
		Profiles:        profilesConfigured,
		ModelPreset:     mergedClaudeModelPreset(currentMappings),
		ActivateCommand: claudeCodeActivateCommand(envPath),
	}
}

func claudeCodeActivateCommand(envPath string) string {
	escaped := strings.ReplaceAll(strings.TrimSpace(envPath), `"`, `\"`)
	if escaped == "" {
		return ""
	}
	return `. "` + escaped + `"`
}

type claudeCodeEnableOptions struct {
}

func (s *Server) enableClaudeCodeIntegration(base string, opts claudeCodeEnableOptions) (claudeCodeIntegrationStatus, error) {
	_ = opts
	baseURL := strings.TrimRight(strings.TrimSpace(base), "/")
	if baseURL == "" {
		return claudeCodeIntegrationStatus{}, fmt.Errorf("failed to resolve CodexSess base URL")
	}
	apiKey := strings.TrimSpace(s.currentAPIKey())
	if apiKey == "" {
		return claudeCodeIntegrationStatus{}, fmt.Errorf("api key is empty")
	}
	if err := ensureClaudeSettings(baseURL, apiKey, ""); err != nil {
		return claudeCodeIntegrationStatus{}, err
	}
	if err := ensureClaudeOnboardingCompleted(); err != nil {
		return claudeCodeIntegrationStatus{}, err
	}

	envPath, profilePaths := claudeCodeIntegrationPaths()
	if err := ensureClaudeEnvFile(baseURL, apiKey); err != nil {
		return claudeCodeIntegrationStatus{}, err
	}

	for _, profile := range profilePaths {
		if err := ensureCodexsessProfileIntegration(profile, envPath); err != nil {
			return claudeCodeIntegrationStatus{}, err
		}
	}

	s.mu.Lock()
	cfg := s.svc.Cfg
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	for alias, model := range claudeCodeModelPresetDefaults {
		key := strings.TrimSpace(alias)
		if key == "" {
			continue
		}
		existing := strings.TrimSpace(cfg.ModelMappings[key])
		if existing == "" || strings.Contains(existing, ":") || !isValidCodexModel(existing) {
			cfg.ModelMappings[key] = strings.TrimSpace(model)
		}
	}
	s.svc.Cfg = cfg
	s.mu.Unlock()
	if raw, err := json.Marshal(cfg.ModelMappings); err == nil {
		_ = s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
	}

	return s.claudeCodeIntegrationStatus(baseURL), nil
}

func ensureClaudeEnvFile(baseURL string, apiKey string) error {
	envPath, _ := claudeCodeIntegrationPaths()
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		return err
	}
	envContent := fmt.Sprintf(
		"# Managed by CodexSess\nunset ANTHROPIC_API_KEY\nexport ANTHROPIC_BASE_URL=%q\nexport ANTHROPIC_AUTH_TOKEN=%q\n",
		strings.TrimSpace(baseURL),
		strings.TrimSpace(apiKey),
	)
	return os.WriteFile(envPath, []byte(envContent), 0o600)
}

func ensureClaudeSettings(baseURL string, apiKey string, forcedModel string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	doc := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			log.Printf("claude settings parse skipped: path=%s err=%v", settingsPath, err)
			return nil
		}
	}

	permissions, ok := doc["permissions"].(map[string]any)
	if !ok {
		if existing, exists := doc["permissions"]; exists && existing != nil {
			log.Printf("claude settings permissions skipped: path=%s unexpected_type=%T", settingsPath, existing)
			return nil
		}
		permissions = map[string]any{}
	}
	permissions["defaultMode"] = "bypassPermissions"
	doc["permissions"] = permissions

	envMap, ok := doc["env"].(map[string]any)
	if !ok {
		if existing, exists := doc["env"]; exists && existing != nil {
			log.Printf("claude settings env skipped: path=%s unexpected_type=%T", settingsPath, existing)
			return nil
		}
		envMap = map[string]any{}
	}
	envMap["ANTHROPIC_BASE_URL"] = strings.TrimSpace(baseURL)
	envMap["ANTHROPIC_AUTH_TOKEN"] = strings.TrimSpace(apiKey)
	delete(envMap, "ANTHROPIC_API_KEY")
	doc["env"] = envMap

	model := strings.TrimSpace(forcedModel)
	if model != "" {
		doc["model"] = model
	} else {
		delete(doc, "model")
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(settingsPath, out, 0o600)
}

func ensureClaudeOnboardingCompleted() error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	doc := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			log.Printf("claude onboarding parse skipped: path=%s err=%v", path, err)
			return nil
		}
	}
	doc["hasCompletedOnboarding"] = true
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o600)
}

func removeLegacyClaudeSettingsModel() error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	doc := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil
		}
	}
	if _, ok := doc["model"]; !ok {
		return nil
	}
	delete(doc, "model")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(settingsPath, out, 0o600)
}

func (s *Server) enforceClaudeCodexOnlyConfig() {
	if err := removeLegacyClaudeSettingsModel(); err != nil {
		log.Printf("claude settings legacy model cleanup skipped: %v", err)
	}

	s.mu.Lock()
	cfg := s.svc.Cfg
	changed := false
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	for alias, fallback := range claudeCodeModelPresetDefaults {
		key := strings.TrimSpace(alias)
		if key == "" {
			continue
		}
		current := strings.TrimSpace(cfg.ModelMappings[key])
		if current == "" {
			continue
		}
		if isValidCodexModel(current) {
			continue
		}
		cfg.ModelMappings[key] = strings.TrimSpace(fallback)
		changed = true
	}
	if changed {
		s.svc.Cfg = cfg
		if raw, err := json.Marshal(cfg.ModelMappings); err == nil {
			_ = s.saveSetting(context.Background(), store.SettingModelMappings, string(raw))
		}
	}
	s.mu.Unlock()
}

func claudeCodeIntegrationPaths() (string, []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	envPath := filepath.Join(home, ".codexsess", "claude-code.env")
	profiles := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".zprofile"),
	}
	return envPath, profiles
}

func profileHasCodexsessSource(profilePath string, envPath string) (bool, error) {
	b, err := os.ReadFile(profilePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sourceLine := codexsessProfileSourceLine(envPath)
	content := string(b)
	if strings.Contains(content, codexsessProfileBlockStart) && strings.Contains(content, codexsessProfileBlockEnd) {
		return true, nil
	}
	return strings.Contains(content, sourceLine), nil
}

const (
	codexsessProfileBlockStart = "# >>> CodexSess Claude Code >>>"
	codexsessProfileBlockEnd   = "# <<< CodexSess Claude Code <<<"
)

func ensureCodexsessProfileIntegration(profilePath string, envPath string) error {
	b, err := os.ReadFile(profilePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(b)
	content = removeCodexsessProfileBlock(content)
	content = removeLegacyCodexsessHeader(content)

	var builder strings.Builder
	builder.WriteString(strings.TrimRight(content, "\n"))
	if strings.TrimSpace(content) != "" {
		builder.WriteString("\n")
	}
	builder.WriteString(codexsessProfileBlockStart)
	builder.WriteString("\n")
	builder.WriteString(codexsessProfileSourceLine(envPath))
	builder.WriteString("\n")
	builder.WriteString(codexsessProfileBlockEnd)
	builder.WriteString("\n")
	return os.WriteFile(profilePath, []byte(builder.String()), 0o600)
}

func codexsessProfileSourceLine(envPath string) string {
	escaped := strings.ReplaceAll(envPath, `"`, `\"`)
	return `[ -f "` + escaped + `" ] && . "` + escaped + `"`
}

func removeCodexsessProfileBlock(content string) string {
	text := strings.TrimRight(content, "\n")
	for {
		start := strings.Index(text, codexsessProfileBlockStart)
		if start < 0 {
			break
		}
		endRel := strings.Index(text[start:], codexsessProfileBlockEnd)
		if endRel < 0 {
			text = strings.TrimSpace(text[:start])
			break
		}
		end := start + endRel + len(codexsessProfileBlockEnd)
		text = strings.TrimSpace(text[:start] + "\n" + text[end:])
	}
	return text
}

func removeLegacyCodexsessHeader(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 0 {
		return strings.TrimSpace(content)
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "# Added by CodexSess for Claude Code" {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func copyModelPresetMap() map[string]string {
	out := make(map[string]string, len(claudeCodeModelPresetDefaults))
	for alias, model := range claudeCodeModelPresetDefaults {
		out[alias] = model
	}
	return out
}

func mergedClaudeModelPreset(current map[string]string) map[string]string {
	merged := copyModelPresetMap()
	for alias, fallback := range merged {
		override := strings.TrimSpace(current[alias])
		if override != "" {
			merged[alias] = override
			continue
		}
		merged[alias] = strings.TrimSpace(fallback)
	}
	return merged
}

type updateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	ReleaseURL      string `json:"release_url"`
	LatestChangelog string `json:"latest_changelog"`
	UpdateAvailable bool   `json:"update_available"`
	CheckedAt       string `json:"checked_at"`
	CheckError      string `json:"check_error"`
}

func (s *Server) handleWebVersionCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	info := s.getUpdateInfo(r.Context(), true)
	respondJSON(w, 200, map[string]any{
		"ok":                 true,
		"app_version":        info.CurrentVersion,
		"latest_version":     info.LatestVersion,
		"release_url":        info.ReleaseURL,
		"latest_changelog":   info.LatestChangelog,
		"update_available":   info.UpdateAvailable,
		"update_checked_at":  info.CheckedAt,
		"update_check_error": info.CheckError,
	})
}

func (s *Server) getUpdateInfo(ctx context.Context, force bool) updateInfo {
	current := normalizeVersionString(s.appVersion)
	if current == "" {
		current = "dev"
	}
	now := time.Now().UTC()
	const ttl = 30 * time.Minute

	s.updateMu.Lock()
	stale := s.updateCheckedAt.IsZero() || now.Sub(s.updateCheckedAt) > ttl
	needCheck := force || stale
	s.updateMu.Unlock()

	var latest, releaseURL, latestChangelog, checkErrMsg string
	if needCheck {
		checkCtx, cancel := context.WithTimeout(ctx, 1800*time.Millisecond)
		latestV, releaseURLV, latestChangelogV, err := fetchLatestReleaseVersion(checkCtx)
		cancel()
		latest = normalizeVersionString(latestV)
		releaseURL = strings.TrimSpace(releaseURLV)
		latestChangelog = strings.TrimSpace(latestChangelogV)
		if err != nil {
			checkErrMsg = err.Error()
		}
	}

	s.updateMu.Lock()
	if needCheck {
		s.updateCheckedAt = now
		s.updateLatestVersion = latest
		s.updateReleaseURL = releaseURL
		s.updateCheckErrMessage = checkErrMsg
		if len(latestChangelog) > 20000 {
			latestChangelog = latestChangelog[:20000]
		}
		s.updateLatestChangelog = latestChangelog
		s.updateAvailable = compareSemver(s.updateLatestVersion, current) > 0
	}
	info := updateInfo{
		CurrentVersion:  current,
		LatestVersion:   s.updateLatestVersion,
		ReleaseURL:      s.updateReleaseURL,
		LatestChangelog: s.updateLatestChangelog,
		UpdateAvailable: s.updateAvailable,
		CheckedAt:       s.updateCheckedAt.Format(time.RFC3339),
		CheckError:      s.updateCheckErrMessage,
	}
	s.updateMu.Unlock()
	return info
}

func fetchLatestReleaseVersion(ctx context.Context) (string, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/rickicode/CodexSess/releases/latest", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "codexsess-update-checker")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", "", fmt.Errorf("github latest release check failed: status %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", "", err
	}
	return strings.TrimSpace(payload.TagName), strings.TrimSpace(payload.HTMLURL), strings.TrimSpace(payload.Body), nil
}

func normalizeVersionString(v string) string {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(strings.ToLower(s), "v")
	if s == "" {
		return ""
	}
	return s
}

func compareSemver(a, b string) int {
	parse := func(v string) ([3]int, bool) {
		var out [3]int
		clean := normalizeVersionString(v)
		if clean == "" || clean == "dev" {
			return out, false
		}
		re := regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?`)
		m := re.FindStringSubmatch(clean)
		if len(m) < 2 {
			return out, false
		}
		out[0], _ = strconv.Atoi(m[1])
		if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
			out[1], _ = strconv.Atoi(m[2])
		}
		if len(m) > 3 && strings.TrimSpace(m[3]) != "" {
			out[2], _ = strconv.Atoi(m[3])
		}
		return out, true
	}
	aa, aok := parse(a)
	bb, bok := parse(b)
	if aok && !bok {
		return 1
	}
	if !aok && bok {
		return -1
	}
	for i := 0; i < 3; i++ {
		if aa[i] > bb[i] {
			return 1
		}
		if aa[i] < bb[i] {
			return -1
		}
	}
	return 0
}

func (s *Server) handleWebLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				if v > 1000 {
					v = 1000
				}
				limit = v
			}
		}
		if s.traffic == nil {
			respondJSON(w, 200, map[string]any{"ok": true, "lines": []string{}})
			return
		}
		lines, err := s.traffic.ReadTail(limit)
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "lines": lines})
		return
	case http.MethodDelete:
		if s.traffic == nil {
			respondJSON(w, 200, map[string]any{"ok": true})
			return
		}
		if err := s.traffic.Clear(); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebCodingSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := s.svc.ListCodingSessions(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":       true,
			"sessions": mapCodingSessions(sessions),
		})
		return
	case http.MethodPost:
		var req struct {
			Title       string `json:"title"`
			Model       string `json:"model"`
			WorkDir     string `json:"work_dir"`
			SandboxMode string `json:"sandbox_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		session, err := s.svc.CreateCodingSession(r.Context(), req.Title, req.Model, req.WorkDir, req.SandboxMode)
		if err != nil {
			respondErr(w, 400, "bad_request", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":      true,
			"session": mapCodingSession(session),
		})
		return
	case http.MethodPut:
		var req struct {
			SessionID   string `json:"session_id"`
			Model       string `json:"model"`
			WorkDir     string `json:"work_dir"`
			SandboxMode string `json:"sandbox_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		session, err := s.svc.UpdateCodingSessionPreferences(r.Context(), req.SessionID, req.Model, req.WorkDir, req.SandboxMode)
		if err != nil {
			respondErr(w, 400, "bad_request", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{
			"ok":      true,
			"session": mapCodingSession(session),
		})
		return
	case http.MethodDelete:
		sessionID := strings.TrimSpace(r.URL.Query().Get("id"))
		if sessionID == "" {
			respondErr(w, 400, "bad_request", "session id is required")
			return
		}
		if err := s.svc.DeleteCodingSession(r.Context(), sessionID); err != nil {
			respondErr(w, 400, "bad_request", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebCodingMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			respondErr(w, 400, "bad_request", "limit must be an integer")
			return
		}
		if v < 1 {
			v = 1
		}
		if v > 200 {
			v = 200
		}
		limit = v
	}
	beforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	messages, hasMore, err := s.svc.GetCodingMessagesPage(r.Context(), sessionID, limit, beforeID)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	oldestID := ""
	newestID := ""
	if len(messages) > 0 {
		oldestID = messages[0].ID
		newestID = messages[len(messages)-1].ID
	}
	respondJSON(w, 200, map[string]any{
		"ok":        true,
		"messages":  mapCodingMessages(messages),
		"has_more":  hasMore,
		"oldest_id": oldestID,
		"newest_id": newestID,
	})
}

func (s *Server) handleWebCodingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	runtime, err := s.svc.CodingRuntimeStatusDetail(r.Context(), sessionID)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	startedAtValue := ""
	if runtime.InFlight {
		startedAtValue = runtime.StartedAt.UTC().Format(time.RFC3339)
	}
	respondJSON(w, 200, map[string]any{
		"ok":              true,
		"session_id":      sessionID,
		"in_flight":       runtime.InFlight,
		"started_at":      startedAtValue,
		"runtime_mode":    runtime.RuntimeMode,
		"runtime_status":  runtime.RuntimeStatus,
		"restart_pending": runtime.RestartPending,
	})
}

func (s *Server) handleWebCodingStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	stopped := s.svc.StopCodingRun(sessionID)
	respondJSON(w, 200, map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"stopped":    stopped,
	})
}

func (s *Server) handleWebCodingWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(req *http.Request) bool {
			return wsOriginAllowed(req)
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	connClosed := atomic.Bool{}
	var writeMu sync.Mutex
	writeJSON := func(payload map[string]any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if connClosed.Load() {
			return websocket.ErrCloseSent
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := conn.WriteJSON(payload)
		if err != nil {
			connClosed.Store(true)
		}
		return err
	}

	type codingWSRequest struct {
		Type        string `json:"type"`
		SessionID   string `json:"session_id"`
		Content     string `json:"content"`
		Model       string `json:"model"`
		WorkDir     string `json:"work_dir"`
		SandboxMode string `json:"sandbox_mode"`
		Command     string `json:"command"`
	}

	inFlight := atomic.Bool{}
	for {
		var req codingWSRequest
		if err := conn.ReadJSON(&req); err != nil {
			connClosed.Store(true)
			return
		}
		reqType := strings.ToLower(strings.TrimSpace(req.Type))
		switch reqType {
		case "ping":
			_ = writeJSON(map[string]any{"event": "pong"})
		case "stop":
			sessionID := strings.TrimSpace(req.SessionID)
			if sessionID == "" {
				_ = writeJSON(map[string]any{"event": "error", "message": "session_id is required", "code": "bad_request"})
				continue
			}
			stopped := s.svc.StopCodingRun(sessionID)
			if !stopped {
				_ = writeJSON(map[string]any{"event": "error", "message": "session not running", "code": "not_running"})
				continue
			}
			_ = writeJSON(map[string]any{"event": "activity", "text": "stop requested", "session_id": sessionID})
		case "send":
			if inFlight.Load() {
				_ = writeJSON(map[string]any{"event": "error", "message": service.ErrCodingSessionBusy.Error(), "code": "session_busy"})
				continue
			}
			sessionID := strings.TrimSpace(req.SessionID)
			if sessionID == "" {
				_ = writeJSON(map[string]any{"event": "error", "message": "session_id is required", "code": "bad_request"})
				continue
			}
			inFlight.Store(true)
			_ = writeJSON(map[string]any{"event": "started", "session_id": sessionID})
			_ = writeJSON(map[string]any{"event": "runtime_started", "session_id": sessionID})
			if runtime, err := s.svc.CodingRuntimeStatusDetail(context.Background(), sessionID); err == nil {
				_ = writeJSON(map[string]any{
					"event":           "runtime_status",
					"session_id":      sessionID,
					"runtime_mode":    runtime.RuntimeMode,
					"runtime_status":  runtime.RuntimeStatus,
					"restart_pending": runtime.RestartPending,
					"in_flight":       runtime.InFlight,
				})
			}
			go func(payload codingWSRequest) {
				defer inFlight.Store(false)
				bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
				defer cancel()
				result, runErr := s.svc.SendCodingMessageStream(
					bgCtx,
					payload.SessionID,
					payload.Content,
					payload.Model,
					payload.WorkDir,
					payload.SandboxMode,
					payload.Command,
					func(evt provider.ChatEvent) error {
						eventType := strings.TrimSpace(strings.ToLower(evt.Type))
						switch eventType {
						case "assistant_message", "activity", "raw_event", "stderr", "delta":
							if !connClosed.Load() {
								_ = writeJSON(map[string]any{
									"event": eventType,
									"text":  evt.Text,
								})
							}
						}
						return nil
					},
				)
				if runErr != nil {
					if runtime, err := s.svc.CodingRuntimeStatusDetail(context.Background(), strings.TrimSpace(payload.SessionID)); err == nil {
						_ = writeJSON(map[string]any{
							"event":           "runtime_error",
							"session_id":      payload.SessionID,
							"runtime_mode":    runtime.RuntimeMode,
							"runtime_status":  runtime.RuntimeStatus,
							"restart_pending": runtime.RestartPending,
							"in_flight":       runtime.InFlight,
							"message":         runErr.Error(),
						})
					}
					_ = writeJSON(map[string]any{"event": "error", "message": runErr.Error()})
					return
				}
				if connClosed.Load() {
					return
				}
				_ = writeJSON(map[string]any{
					"event":              "done",
					"session":            mapCodingSession(result.Session),
					"user":               mapCodingMessage(result.User),
					"assistant":          mapCodingMessage(result.Assistant),
					"event_messages":     mapCodingMessages(result.EventMessages),
					"assistant_messages": mapCodingMessages(result.Assistants),
				})
				if runtime, err := s.svc.CodingRuntimeStatusDetail(context.Background(), strings.TrimSpace(payload.SessionID)); err == nil {
					_ = writeJSON(map[string]any{
						"event":           "runtime_status",
						"session_id":      payload.SessionID,
						"runtime_mode":    runtime.RuntimeMode,
						"runtime_status":  runtime.RuntimeStatus,
						"restart_pending": runtime.RestartPending,
						"in_flight":       runtime.InFlight,
					})
				}
			}(req)
		default:
			_ = writeJSON(map[string]any{"event": "error", "message": "unsupported request type", "code": "bad_request"})
		}
	}
}

func wsOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || strings.TrimSpace(originURL.Host) == "" {
		return false
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return false
	}
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	return strings.EqualFold(originURL.Host, host)
}

func (s *Server) handleWebCodingChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID   string `json:"session_id"`
		Content     string `json:"content"`
		Model       string `json:"model"`
		WorkDir     string `json:"work_dir"`
		SandboxMode string `json:"sandbox_mode"`
		Command     string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.SendCodingMessage(r.Context(), req.SessionID, req.Content, req.Model, req.WorkDir, req.SandboxMode, req.Command)
	if err != nil {
		if errors.Is(err, service.ErrCodingSessionBusy) {
			respondErr(w, 409, "conflict", err.Error())
			return
		}
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok":                 true,
		"session":            mapCodingSession(result.Session),
		"user":               mapCodingMessage(result.User),
		"assistant":          mapCodingMessage(result.Assistant),
		"event_messages":     mapCodingMessages(result.EventMessages),
		"assistant_messages": mapCodingMessages(result.Assistants),
	})
}

func (s *Server) handleWebCodingSessionRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID   string `json:"session_id"`
		RuntimeMode string `json:"runtime_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	session, err := s.svc.UpdateCodingSessionRuntimeMode(r.Context(), req.SessionID, req.RuntimeMode)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok":      true,
		"session": mapCodingSession(session),
	})
}

func (s *Server) handleWebCodingRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		respondErr(w, 400, "bad_request", "session_id is required")
		return
	}
	runtime, err := s.svc.CodingRuntimeStatusDetail(r.Context(), sessionID)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	startedAt := ""
	if runtime.InFlight {
		startedAt = runtime.StartedAt.UTC().Format(time.RFC3339)
	}
	respondJSON(w, 200, map[string]any{
		"ok":              true,
		"session_id":      runtime.SessionID,
		"runtime_mode":    runtime.RuntimeMode,
		"runtime_status":  runtime.RuntimeStatus,
		"restart_pending": runtime.RestartPending,
		"in_flight":       runtime.InFlight,
		"started_at":      startedAt,
	})
}

func (s *Server) handleWebCodingRuntimeRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Force     bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	deferred, err := s.svc.RestartCodingRuntime(r.Context(), req.SessionID, req.Force)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	runtime, _ := s.svc.CodingRuntimeStatusDetail(r.Context(), req.SessionID)
	respondJSON(w, 200, map[string]any{
		"ok":              true,
		"accepted":        true,
		"deferred":        deferred,
		"session_id":      strings.TrimSpace(req.SessionID),
		"runtime_mode":    runtime.RuntimeMode,
		"runtime_status":  runtime.RuntimeStatus,
		"restart_pending": runtime.RestartPending,
		"in_flight":       runtime.InFlight,
	})
}

func (s *Server) handleWebCodingPathSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("prefix"))
	if raw == "" {
		raw = "~/"
	}
	expanded := expandSuggestionPath(raw)
	parent := expanded
	if !strings.HasSuffix(raw, "/") {
		parent = filepath.Dir(expanded)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		respondJSON(w, 200, map[string]any{"ok": true, "suggestions": []string{raw}})
		return
	}
	out := make([]string, 0, len(entries)+1)
	needle := strings.ToLower(strings.TrimSpace(filepath.Base(expanded)))
	prefixRoot := raw
	if !strings.HasSuffix(prefixRoot, "/") {
		prefixRoot = filepath.Dir(raw)
	}
	prefixRoot = strings.TrimSuffix(prefixRoot, "/")
	if prefixRoot == "." {
		prefixRoot = ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if needle != "" && !strings.HasPrefix(strings.ToLower(name), needle) {
			continue
		}
		var suggestion string
		switch {
		case strings.HasPrefix(raw, "~/"):
			base := strings.TrimPrefix(prefixRoot, "~")
			suggestion = filepath.ToSlash(filepath.Join("~", base, name)) + "/"
		case strings.HasPrefix(raw, "/"):
			suggestion = filepath.ToSlash(filepath.Join(prefixRoot, name)) + "/"
		default:
			if prefixRoot == "" {
				suggestion = name + "/"
			} else {
				suggestion = filepath.ToSlash(filepath.Join(prefixRoot, name)) + "/"
			}
		}
		out = append(out, suggestion)
	}
	if len(out) == 0 {
		out = append(out, raw)
	}
	sort.Strings(out)
	respondJSON(w, 200, map[string]any{"ok": true, "suggestions": out})
}

func (s *Server) handleWebCodingSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	home, _ := os.UserHomeDir()
	searchRoots := []string{
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(".", ".codex", "skills"),
	}
	searchRoots = append(searchRoots, additionalSkillRootsFromEnv()...)
	seen := map[string]struct{}{}
	out := make([]string, 0, 64)
	for _, root := range searchRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := strings.TrimSpace(entry.Name())
			if name == "" {
				continue
			}
			skillPath := filepath.Join(root, name, "SKILL.md")
			if _, err := os.Stat(skillPath); err != nil {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	respondJSON(w, 200, map[string]any{
		"ok":     true,
		"skills": out,
	})
}

func additionalSkillRootsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_SKILL_DIRS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out = append(out, expandSuggestionPath(p))
	}
	return out
}

func mapCodingSessions(items []store.CodingSession) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mapCodingSession(item))
	}
	return out
}

func mapCodingSession(item store.CodingSession) map[string]any {
	return map[string]any{
		"id":              item.ID,
		"display_id":      firstNonEmpty(strings.TrimSpace(item.CodexThreadID), item.ID),
		"codex_thread_id": item.CodexThreadID,
		"title":           item.Title,
		"model":           item.Model,
		"work_dir":        item.WorkDir,
		"sandbox_mode":    item.SandboxMode,
		"runtime_mode":    firstNonEmpty(strings.TrimSpace(item.RuntimeMode), "spawn"),
		"runtime_status":  firstNonEmpty(strings.TrimSpace(item.RuntimeStatus), "idle"),
		"restart_pending": item.RestartPending,
		"created_at":      item.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":      item.UpdatedAt.UTC().Format(time.RFC3339),
		"last_message_at": item.LastMessageAt.UTC().Format(time.RFC3339),
	}
}

func mapCodingMessages(items []store.CodingMessage) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mapCodingMessage(item))
	}
	return out
}

func mapCodingMessage(item store.CodingMessage) map[string]any {
	return map[string]any{
		"id":            item.ID,
		"session_id":    item.SessionID,
		"role":          item.Role,
		"content":       item.Content,
		"input_tokens":  item.InputTokens,
		"output_tokens": item.OutputTokens,
		"created_at":    item.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Server) handleWebClientEventLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Type    string         `json:"type"`
		Source  string         `json:"source"`
		Level   string         `json:"level"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	eventType := firstNonEmpty(strings.TrimSpace(req.Type), "event")
	eventSource := firstNonEmpty(strings.TrimSpace(req.Source), "web-console")
	eventLevel := firstNonEmpty(strings.TrimSpace(req.Level), "info")
	eventMessage := firstNonEmpty(strings.TrimSpace(req.Message), "-")
	meta := "-"
	if len(req.Data) > 0 {
		if b, err := json.Marshal(req.Data); err == nil {
			meta = string(b)
		}
	}
	log.Printf("[EVENT] source=%s type=%s level=%s message=%s meta=%s", eventSource, eventType, eventLevel, eventMessage, meta)
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebModelMappings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, 200, map[string]any{
			"ok":               true,
			"available_models": codexAvailableModels(),
			"mappings":         s.currentModelMappings(),
		})
		return
	case http.MethodPost:
		var req struct {
			Alias string `json:"alias"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		alias := strings.TrimSpace(req.Alias)
		model := strings.TrimSpace(req.Model)
		if alias == "" || model == "" {
			respondErr(w, 400, "bad_request", "alias and model are required")
			return
		}
		if !isValidCodexModel(model) {
			respondErr(w, 400, "bad_request", "invalid target model")
			return
		}
		if err := s.upsertModelMapping(alias, model); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	case http.MethodDelete:
		alias := strings.TrimSpace(r.URL.Query().Get("alias"))
		if alias == "" {
			respondErr(w, 400, "bad_request", "alias is required")
			return
		}
		if err := s.deleteModelMapping(alias); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) withTrafficLog(protocol string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.traffic == nil {
			next(w, r)
			return
		}
		captureBody := &limitedCaptureReadCloser{
			rc:    r.Body,
			limit: maxTrafficRequestCaptureBytes,
		}
		r.Body = captureBody
		start := time.Now()
		rec := &trafficRecorder{
			ResponseWriter:    w,
			status:            http.StatusOK,
			responseBodyLimit: maxTrafficResponseCaptureBytes,
		}
		next(rec, r)
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}

		bodyBytes := captureBody.Captured()
		model, stream := detectTrafficModelAndStream(r.URL.Path, bodyBytes)
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			remote = host
		}
		responseBody := strings.TrimSpace(string(rec.responseBody))
		requestTokens, responseTokens, totalTokens := parseTrafficUsageTokens(responseBody)
		_ = s.traffic.Append(trafficlog.Entry{
			Timestamp:         time.Now().UTC(),
			Protocol:          protocol,
			Method:            r.Method,
			Path:              r.URL.Path,
			Status:            rec.status,
			LatencyMS:         time.Since(start).Milliseconds(),
			RemoteAddr:        strings.TrimSpace(remote),
			UserAgent:         strings.TrimSpace(r.UserAgent()),
			AccountHint:       strings.TrimSpace(r.Header.Get("X-Codex-Account")),
			AccountID:         strings.TrimSpace(rec.accountID),
			AccountEmail:      strings.TrimSpace(rec.accountEmail),
			Model:             model,
			Stream:            stream,
			RequestBody:       strings.TrimSpace(string(bodyBytes)),
			ResponseBody:      responseBody,
			RequestTokens:     requestTokens,
			ResponseTokens:    responseTokens,
			TotalTokens:       totalTokens,
			RequestTruncated:  captureBody.Truncated(),
			ResponseTruncated: rec.bodyTruncated,
		})
	}
}

type limitedCaptureReadCloser struct {
	rc        io.ReadCloser
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (l *limitedCaptureReadCloser) Read(p []byte) (int, error) {
	if l.rc == nil {
		return 0, io.EOF
	}
	n, err := l.rc.Read(p)
	if n > 0 {
		l.capture(p[:n])
	}
	return n, err
}

func (l *limitedCaptureReadCloser) Close() error {
	if l.rc == nil {
		return nil
	}
	return l.rc.Close()
}

func (l *limitedCaptureReadCloser) capture(p []byte) {
	if l.limit <= 0 || l.truncated || len(p) == 0 {
		if l.limit <= 0 {
			l.truncated = true
		}
		return
	}
	remaining := l.limit - l.buf.Len()
	if remaining <= 0 {
		l.truncated = true
		return
	}
	if len(p) > remaining {
		_, _ = l.buf.Write(p[:remaining])
		l.truncated = true
		return
	}
	_, _ = l.buf.Write(p)
}

func (l *limitedCaptureReadCloser) Captured() []byte {
	return l.buf.Bytes()
}

func (l *limitedCaptureReadCloser) Truncated() bool {
	return l.truncated
}

type trafficRecorder struct {
	http.ResponseWriter
	status            int
	responseBody      []byte
	responseBodyLimit int
	bodyTruncated     bool
	accountID         string
	accountEmail      string
}

func (r *trafficRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *trafficRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.responseBodyLimit <= 0 {
		r.responseBody = append(r.responseBody, p...)
	} else if !r.bodyTruncated {
		remaining := r.responseBodyLimit - len(r.responseBody)
		if remaining > 0 {
			if len(p) <= remaining {
				r.responseBody = append(r.responseBody, p...)
			} else {
				r.responseBody = append(r.responseBody, p[:remaining]...)
				r.bodyTruncated = true
			}
		} else {
			r.bodyTruncated = true
		}
	}
	return r.ResponseWriter.Write(p)
}

func (r *trafficRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func detectTrafficModelAndStream(path string, body []byte) (string, bool) {
	switch strings.TrimSpace(path) {
	case "/v1":
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err == nil {
			model, _ := anyBody["model"].(string)
			stream, _ := anyBody["stream"].(bool)
			return strings.TrimSpace(model), stream
		}
	case "/zo/v1":
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err == nil {
			model, _ := anyBody["model"].(string)
			stream, _ := anyBody["stream"].(bool)
			return strings.TrimSpace(model), stream
		}
	case "/v1/chat/completions":
		var req ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/zo/v1/chat/completions":
		var req ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/responses":
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/zo/v1/responses":
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/messages", "/claude/v1/messages":
		var req ClaudeMessagesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/zo/v1/messages":
		var req ClaudeMessagesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	}
	return "", false
}

func parseTrafficUsageTokens(body string) (int, int, int) {
	body = strings.TrimSpace(body)
	if body == "" {
		return 0, 0, 0
	}
	if strings.Contains(body, "data:") {
		return parseTrafficUsageTokensFromSSE(body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return 0, 0, 0
	}
	return extractTrafficUsageTokens(payload)
}

func parseTrafficUsageTokensFromSSE(body string) (int, int, int) {
	lines := strings.Split(body, "\n")
	requestTokens := 0
	responseTokens := 0
	totalTokens := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}
		req, resp, total := extractTrafficUsageTokens(payload)
		if req != 0 || resp != 0 || total != 0 {
			requestTokens = req
			responseTokens = resp
			totalTokens = total
		}
	}
	return requestTokens, responseTokens, totalTokens
}

func extractTrafficUsageTokens(payload map[string]any) (int, int, int) {
	if usage, ok := payload["usage"].(map[string]any); ok {
		return normalizeTrafficUsageTokens(usage)
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if usage, ok := response["usage"].(map[string]any); ok {
			return normalizeTrafficUsageTokens(usage)
		}
	}
	if message, ok := payload["message"].(map[string]any); ok {
		if usage, ok := message["usage"].(map[string]any); ok {
			return normalizeTrafficUsageTokens(usage)
		}
	}
	return 0, 0, 0
}

func normalizeTrafficUsageTokens(usage map[string]any) (int, int, int) {
	requestTokens := intFromAny(usage["prompt_tokens"])
	responseTokens := intFromAny(usage["completion_tokens"])
	if requestTokens == 0 {
		requestTokens = intFromAny(usage["input_tokens"])
	}
	if responseTokens == 0 {
		responseTokens = intFromAny(usage["output_tokens"])
	}
	totalTokens := intFromAny(usage["total_tokens"])
	if totalTokens == 0 && (requestTokens != 0 || responseTokens != 0) {
		totalTokens = requestTokens + responseTokens
	}
	return requestTokens, responseTokens, totalTokens
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

func truncateForLog(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func (s *Server) handleWebUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		APIKey     string `json:"api_key"`
		Regenerate bool   `json:"regenerate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	newKey := strings.TrimSpace(req.APIKey)
	if req.Regenerate {
		k, err := randomProxyKey()
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		newKey = k
	}
	if newKey == "" {
		respondErr(w, 400, "bad_request", "api_key required")
		return
	}
	if err := s.saveSetting(r.Context(), store.SettingAPIKey, newKey); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	s.mu.Lock()
	cfg := s.svc.Cfg
	cfg.ProxyAPIKey = newKey
	s.svc.Cfg = cfg
	s.mu.Unlock()
	s.setAPIKey(newKey)
	baseURL := externalBaseURLFromRequest(r, s.bindAddr)
	if strings.TrimSpace(baseURL) != "" {
		if err := ensureClaudeSettings(baseURL, newKey, ""); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		if err := ensureClaudeOnboardingCompleted(); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		if err := ensureClaudeEnvFile(baseURL, newKey); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
	}
	respondJSON(w, 200, map[string]any{"ok": true, "api_key": newKey})
}

func (s *Server) handleWebBrowserStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	base := oauthBaseURLFromRequest(r)
	login, err := s.svc.StartBrowserLoginWeb(r.Context(), base, "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "login_id": login.LoginID, "auth_url": login.AuthURL})
}

func (s *Server) handleWebBrowserCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.svc.CancelBrowserLoginWeb(strings.TrimSpace(req.LoginID))
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebBrowserCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	loginID := strings.TrimSpace(r.URL.Query().Get("login_id"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))

	var err error
	if loginID != "" {
		_, err = s.svc.CompleteBrowserLoginCode(r.Context(), loginID, code, state)
	} else {
		_, err = s.svc.CompleteBrowserLoginCodeByState(r.Context(), code, state)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("authentication failed: " + err.Error()))
		return
	}
	s.svc.CancelBrowserLoginWeb(loginID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><body style='font-family: sans-serif;background:#0f172a;color:#f8fafc;padding:20px'>Login success. You can close this tab and return to codexsess.</body></html>"))
}

func (s *Server) handleWebBrowserComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID     string `json:"login_id"`
		CallbackURL string `json:"callback_url"`
		Alias       string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	loginID := strings.TrimSpace(req.LoginID)
	callbackURL := strings.TrimSpace(req.CallbackURL)
	if loginID == "" {
		respondErr(w, 400, "bad_request", "login_id required")
		return
	}
	if callbackURL == "" {
		respondErr(w, 400, "bad_request", "callback_url required")
		return
	}
	acc, err := s.svc.CompleteFromManualCallback(r.Context(), loginID, callbackURL, strings.TrimSpace(req.Alias))
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	s.svc.CancelBrowserLoginWeb(loginID)
	respondJSON(w, 200, map[string]any{
		"ok": true,
		"account": map[string]any{
			"id":    acc.ID,
			"email": acc.Email,
		},
	})
}

func (s *Server) handleWebDeviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	login, err := s.svc.StartDeviceLogin(r.Context(), "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok": true,
		"login": map[string]any{
			"login_id":                  login.LoginID,
			"user_code":                 login.UserCode,
			"verification_uri":          login.VerificationURI,
			"verification_uri_complete": login.VerificationURIComplete,
			"interval_seconds":          login.IntervalSeconds,
			"expires_at":                login.ExpiresAt,
		},
	})
}

func (s *Server) handleWebDevicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.PollDeviceLogin(r.Context(), strings.TrimSpace(req.LoginID))
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func randomProxyKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(buf), nil
}

func oauthBaseURLFromRequest(r *http.Request) string {
	scheme := "http"
	if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); raw != "" {
		parts := strings.Split(raw, ",")
		if p := strings.ToLower(strings.TrimSpace(parts[0])); p == "http" || p == "https" {
			scheme = p
		}
	}
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return scheme + "://localhost:3061"
	}
	parts := strings.Split(host, ",")
	return scheme + "://" + strings.TrimSpace(parts[0])
}

func externalBaseURLFromRequest(r *http.Request, bindAddr string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host != "" {
		return scheme + "://" + host
	}
	return scheme + "://" + bindAddr
}

func expandSuggestionPath(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		clean = "~/"
	}
	if strings.HasPrefix(clean, "~/") || clean == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			suffix := strings.TrimPrefix(clean, "~")
			return filepath.Clean(filepath.Join(home, suffix))
		}
	}
	if filepath.IsAbs(clean) {
		return filepath.Clean(clean)
	}
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Clean(clean)
	}
	return filepath.Clean(filepath.Join(wd, clean))
}
