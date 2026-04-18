package store

import "time"

const (
	SettingAPIKey                   = "codexsess_api_key"
	SettingDirectAPIStrategy        = "direct_api_strategy"
	SettingModelMappings            = "model_mappings"
	SettingAdminPasswordHash        = "admin_password_hash"
	SettingCodexHome                = "codex_home"
	SettingUsageAlertThreshold      = "usage_alert_threshold"
	SettingUsageAutoSwitchThreshold = "usage_auto_switch_threshold"
	SettingUsageSchedulerEnabled    = "usage_scheduler_enabled"
	SettingUsageSchedulerInterval   = "usage_scheduler_interval_minutes"
	SettingUsageRefreshTimeoutSec   = "usage_scheduler_refresh_timeout_seconds"
	SettingUsageSwitchTimeoutSec    = "usage_scheduler_switch_timeout_seconds"
	SettingUsageCursor              = "usage_scheduler_cursor"
)

type Account struct {
	ID                   string
	Email                string
	Alias                string
	PlanType             string
	AccountID            string
	OrganizationID       string
	TokenID              string
	TokenAccess          string
	TokenRefresh         string
	CodexHome            string
	ActiveAPI            bool
	ActiveCLI            bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LastUsedAt           time.Time
	Active               bool
	Revoked              bool
	UsageHourlyPct       int
	UsageWeeklyPct       int
	UsageHourlyResetAt   *time.Time
	UsageWeeklyResetAt   *time.Time
	UsageRawJSON         string
	UsageFetchedAt       time.Time
	UsageLastError       string
	UsageWindowPrimary   string
	UsageWindowSecondary string
	UsageLastCheckedAt   *time.Time
	UsageNextCheckAt     *time.Time
	UsageFailCount       int
}

type AccountFilter struct {
	Query    string
	PlanType string
	Status   string
	Usage    string
}

type UsageSnapshot struct {
	AccountID       string
	HourlyPct       int
	WeeklyPct       int
	HourlyResetAt   *time.Time
	WeeklyResetAt   *time.Time
	RawJSON         string
	FetchedAt       time.Time
	LastError       string
	ResolvedAt      *time.Time
	WindowPrimary   string
	WindowSecondary string
}

type AuditRecord struct {
	RequestID string
	AccountID string
	Model     string
	Stream    bool
	Status    int
	LatencyMS int64
	CreatedAt time.Time
}

type CodingMessage struct {
	ID           string
	SessionID    string
	Role         string
	Actor        string
	AccountEmail string
	Content      string
	InputTokens  int
	OutputTokens int
	CreatedAt    time.Time
	Sequence     int64
}

type MemoryItem struct {
	ID         string
	Scope      string
	ScopeID    string
	Kind       string
	Key        string
	ValueJSON  string
	SourceType string
	SourceRef  string
	Verified   bool
	Confidence int
	Stale      bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ExpiresAt  *time.Time
}

type MemoryQuery struct {
	Scope        string
	ScopeID      string
	Kinds        []string
	VerifiedOnly bool
	IncludeStale bool
	Limit        int
}
