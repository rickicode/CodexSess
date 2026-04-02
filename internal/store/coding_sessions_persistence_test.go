package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCodingSession_PublicShapeIsChatOnly(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(CodingSession{})
	for _, field := range []string{
		"AutopilotEnabled",
		"AutopilotOrchestratorThreadID",
		"AutopilotExecutorThreadID",
		"ChatNeedsHydration",
		"ChatContextVersion",
		"LastHydratedChatContextVer",
		"LastModeTransitionSummary",
		"AutopilotPlanArtifactPath",
		"AutopilotPlanUpdatedAt",
		"ChatCodexThreadID",
	} {
		if _, ok := typ.FieldByName(field); ok {
			t.Fatalf("expected CodingSession to drop legacy field %q", field)
		}
	}
}

func TestCodingSession_CreatePersistsCanonicalChatThreadAndVersionState(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-legacy-session.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	session, err := st.CreateCodingSession(context.Background(), CodingSession{
		ID:                  "sess_legacy_session",
		Title:               "Legacy Session",
		Model:               "gpt-5.2-codex",
		ReasoningLevel:      "medium",
		WorkDir:             "~/",
		SandboxMode:         "write",
		CodexThreadID:       "thread_chat_123",
		ArtifactVersion:     4,
		LastAppliedEventSeq: 11,
		CreatedAt:           now,
		UpdatedAt:           now,
		LastMessageAt:       now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := st.GetCodingSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.CodexThreadID != "thread_chat_123" {
		t.Fatalf("expected canonical thread id to be preserved, got %q", got.CodexThreadID)
	}
	if got.ArtifactVersion != 4 {
		t.Fatalf("expected artifact_version to survive chat-only persistence, got %d", got.ArtifactVersion)
	}
	if got.LastAppliedEventSeq != 11 {
		t.Fatalf("expected last_applied_event_seq to survive chat-only persistence, got %d", got.LastAppliedEventSeq)
	}
}

func TestCodingSession_UpdateIfArtifactVersion(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-artifact-version.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	session, err := st.CreateCodingSession(context.Background(), CodingSession{
		ID:              "sess_artifact_cas",
		Title:           "Artifact CAS",
		Model:           "gpt-5.2-codex",
		ReasoningLevel:  "medium",
		WorkDir:         "~/",
		SandboxMode:     "write",
		ArtifactVersion: 2,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastMessageAt:   now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	stale := session
	stale.Title = "Artifact CAS stale"
	stale.ArtifactVersion = 3
	stale.UpdatedAt = now.Add(1 * time.Minute)
	ok, err := st.UpdateCodingSessionIfArtifactVersion(context.Background(), stale, 1)
	if err != nil {
		t.Fatalf("UpdateCodingSessionIfArtifactVersion stale: %v", err)
	}
	if ok {
		t.Fatalf("expected stale artifact version write to be rejected")
	}

	current, err := st.GetCodingSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if current.ArtifactVersion != 2 {
		t.Fatalf("expected artifact version to remain 2 after stale write, got %d", current.ArtifactVersion)
	}

	fresh := current
	fresh.Title = "Artifact CAS fresh"
	fresh.ArtifactVersion = 3
	fresh.UpdatedAt = now.Add(2 * time.Minute)
	ok, err = st.UpdateCodingSessionIfArtifactVersion(context.Background(), fresh, 2)
	if err != nil {
		t.Fatalf("UpdateCodingSessionIfArtifactVersion fresh: %v", err)
	}
	if !ok {
		t.Fatalf("expected fresh artifact version write to succeed")
	}
	updated, err := st.GetCodingSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get updated session: %v", err)
	}
	if updated.ArtifactVersion != 3 {
		t.Fatalf("expected artifact version to advance to 3, got %d", updated.ArtifactVersion)
	}
	if updated.Title != "Artifact CAS fresh" {
		t.Fatalf("expected fresh write to update title, got %q", updated.Title)
	}
}

func TestCodingSession_ClaimArtifactVersionReturnsDeterministicVersion(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-artifact-claim.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	session, err := st.CreateCodingSession(context.Background(), CodingSession{
		ID:              "sess_artifact_claim",
		Title:           "Artifact Claim",
		Model:           "gpt-5.2-codex",
		ReasoningLevel:  "medium",
		WorkDir:         "~/",
		SandboxMode:     "write",
		ArtifactVersion: 4,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastMessageAt:   now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	claimedVersion, claimed, err := st.ClaimCodingSessionArtifactVersion(context.Background(), session.ID, 4)
	if err != nil {
		t.Fatalf("ClaimCodingSessionArtifactVersion: %v", err)
	}
	if !claimed {
		t.Fatalf("expected claim to succeed")
	}
	if claimedVersion != 5 {
		t.Fatalf("expected deterministic claimed version 5, got %d", claimedVersion)
	}
	current, err := st.GetCodingSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session after claim: %v", err)
	}
	if current.ArtifactVersion != 5 {
		t.Fatalf("expected stored artifact version 5, got %d", current.ArtifactVersion)
	}

	_, claimed, err = st.ClaimCodingSessionArtifactVersion(context.Background(), session.ID, 4)
	if err != nil {
		t.Fatalf("ClaimCodingSessionArtifactVersion stale: %v", err)
	}
	if claimed {
		t.Fatalf("expected stale claim to be rejected")
	}
}
