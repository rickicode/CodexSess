package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

type Service struct {
	Cfg    config.Config
	Store  *store.Store
	Crypto *icrypto.Crypto
	Codex  *provider.CodexAppServer

	cliActiveMu       sync.RWMutex
	cliAuthStateMu    sync.Mutex
	cliActiveCachedID string
	cliActiveCachedAt time.Time
	codingRunMu       sync.Mutex
	codingRunSeq      uint64
	codingRuns        map[string]*codingRunState
}

type codingRunState struct {
	id        uint64
	startedAt time.Time
	actor     string
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

const accountsBackupVersion = "codexsess.accounts.backup"
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
		Codex:      provider.NewCodexAppServer(bin),
		codingRuns: make(map[string]*codingRunState),
	}
}
