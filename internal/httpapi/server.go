package httpapi

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/trafficlog"
	"github.com/ricki/codexsess/internal/webui"
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
	usageSchedulerRunning atomic.Bool
	cliSwitchStatusMu     sync.Mutex
	cliSwitchStatus       cliSwitchStatus
}

const (
	maxTrafficRequestCaptureBytes  = 8 * 1024 * 1024
	maxTrafficResponseCaptureBytes = 8 * 1024 * 1024
	usageSchedulerParallelWorkers  = 5
	usageSchedulerLoopPollInterval = 1 * time.Minute
	activeUsageCheckInterval       = 5 * time.Minute
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
	s.bootstrapCodingTemplateHome()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts", s.handleWebAccounts)
	mux.HandleFunc("/api/accounts/types", s.handleWebAccountTypes)
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
	mux.HandleFunc("/api/coding/messages/snapshot", s.handleWebCodingMessageSnapshot)
	mux.HandleFunc("/api/coding/status", s.handleWebCodingStatus)
	mux.HandleFunc("/api/coding/runtime/debug", s.handleWebCodingRuntimeDebug)
	mux.HandleFunc("/api/coding/stop", s.handleWebCodingStop)
	mux.HandleFunc("/api/coding/ws", s.handleWebCodingWS)
	mux.HandleFunc("/api/coding/chat", s.handleWebCodingChat)
	mux.HandleFunc("/api/coding/runtime/restart", s.handleWebCodingRuntimeRestart)
	mux.HandleFunc("/api/coding/template-home", s.handleWebCodingTemplateHome)
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
	go s.runUsageSchedulerLoop(ctx)
	go s.runActiveUsageAutoSwitchLoop(ctx)
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}

func (s *Server) bootstrapCodingTemplateHome() {
	if s == nil || s.svc == nil {
		return
	}
	if _, err := s.svc.EnsureCodingTemplateHome(); err != nil {
		log.Printf("coding template home bootstrap failed: %v", err)
	}
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
