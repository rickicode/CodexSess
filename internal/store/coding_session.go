package store

import "time"

type CodingSession struct {
	ID                  string
	Title               string
	Model               string
	ReasoningLevel      string
	WorkDir             string
	SandboxMode         string
	CodexThreadID       string
	RestartPending      bool
	ArtifactVersion     int64
	LastAppliedEventSeq int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastMessageAt       time.Time
}
