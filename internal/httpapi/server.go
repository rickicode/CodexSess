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
)

type Server struct {
	svc               *service.Service
	executor          *proxyExecutor
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
	claudePolicy          claudePolicy
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
	srv := &Server{
		svc:               svc,
		bindAddr:          bindAddr,
		apiKey:            apiKey,
		adminUsername:     strings.TrimSpace(adminUsername),
		adminPasswordHash: strings.TrimSpace(adminPasswordHash),
		traffic:           traffic,
		appVersion:        normalizeVersionString(appVersion),
		codexVersion:      strings.TrimSpace(codexVersion),
	}
	srv.claudePolicy = newClaudeProtocolPolicy()
	srv.executor = newProxyExecutor(svc, srv.currentDirectAPIStrategy, srv.shouldInjectDirectAPIPrompt, srv.resolveAPIAccount, srv.findBestUsageAccount, srv.markUsageLastError, &srv.directRoundRobin)
	return srv
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.bootstrapSettingsFromStore(ctx); err != nil {
		log.Printf("settings store bootstrap failed: %v", err)
	}
	s.bootstrapCodingTemplateHome()
	mux := http.NewServeMux()
	s.registerWebRoutes(mux)
	s.registerProxyRoutes(mux)
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
