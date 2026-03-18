package store

import "time"

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
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastUsedAt     time.Time
	Active         bool
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
