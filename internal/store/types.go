package store

import "time"

const (
	SettingLegacyAPIKey      = "api_key"
	SettingAPIKey            = "codexsess_api_key"
	SettingAPIMode           = "api_mode"
	SettingDirectAPIStrategy = "direct_api_strategy"
	SettingCodingCLIStrategy = "coding_cli_strategy"
	SettingZoAPIStrategy     = "zo_api_strategy"
	SettingModelMappings     = "model_mappings"
	SettingAdminPasswordHash = "admin_password_hash"
	SettingCodexHome         = "codex_home"
	SettingUsageCursor       = "usage_scheduler_cursor"
)

type Account struct {
	ID             string
	Email          string
	Alias          string
	PlanType       string
	AccountID      string
	OrganizationID string
	TokenID        string
	TokenAccess    string
	TokenRefresh   string
	CodexHome      string
	ActiveAPI      bool
	ActiveCLI      bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastUsedAt     time.Time
	Active         bool
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

type CodingSession struct {
	ID            string
	Title         string
	Model         string
	WorkDir       string
	SandboxMode   string
	CodexThreadID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastMessageAt time.Time
}

type CodingMessage struct {
	ID           string
	SessionID    string
	Role         string
	Content      string
	InputTokens  int
	OutputTokens int
	CreatedAt    time.Time
}

type ZoAPIKey struct {
	ID                    string
	Name                  string
	Token                 string
	Active                bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastUsedAt            time.Time
	ConversationID        string
	ConversationUpdatedAt *time.Time
}

type ZoAPIKeyUsage struct {
	KeyID         string
	TotalRequests int
	LastRequestAt *time.Time
	LastResetAt   *time.Time
}

type ZoAPIKeyWithUsage struct {
	Key   ZoAPIKey
	Usage ZoAPIKeyUsage
}
