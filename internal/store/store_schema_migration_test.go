package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCodingSessionsSchema_UsesChatOnlyColumns(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "coding-chat-only-schema.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rows, err := st.db.Query(`PRAGMA table_info(coding_sessions)`)
	if err != nil {
		t.Fatalf("query coding_sessions schema: %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			typeName   string
			notNull    int
			defaultV   any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &primaryKey); err != nil {
			t.Fatalf("scan schema row: %v", err)
		}
		columns[strings.TrimSpace(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema rows: %v", err)
	}

	for _, name := range []string{
		"id",
		"title",
		"model",
		"reasoning_level",
		"work_dir",
		"sandbox_mode",
		"codex_thread_id",
		"restart_pending",
		"artifact_version",
		"last_applied_event_seq",
		"created_at",
		"updated_at",
		"last_message_at",
	} {
		if _, ok := columns[name]; !ok {
			t.Fatalf("expected survivor column %q in coding_sessions schema", name)
		}
	}
	for _, name := range []string{
		"chat_codex_thread_id",
		"legacy_enabled",
		"legacy_supervisor_thread_id",
		"legacy_executor_thread_id",
		"chat_needs_hydration",
		"chat_context_version",
		"last_hydrated_chat_context_version",
		"last_mode_transition_summary",
		"legacy_plan_artifact_path",
		"legacy_plan_updated_at",
	} {
		if _, ok := columns[name]; ok {
			t.Fatalf("expected legacy column %q to be removed from coding_sessions schema", name)
		}
	}
}

func TestGetCodingSession_LegacySchemaResetDropsOldCodingSessions(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "coding-legacy-reset.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if _, err := rawDB.Exec(`
		CREATE TABLE coding_sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			reasoning_level TEXT NOT NULL DEFAULT 'medium',
			work_dir TEXT NOT NULL DEFAULT '~/',
			sandbox_mode TEXT NOT NULL DEFAULT 'full-access',
			codex_thread_id TEXT NOT NULL DEFAULT '',
			restart_pending INTEGER NOT NULL DEFAULT 0,
			legacy_enabled INTEGER NOT NULL DEFAULT 0,
			chat_codex_thread_id TEXT NOT NULL DEFAULT '',
			legacy_supervisor_thread_id TEXT NOT NULL DEFAULT '',
			legacy_executor_thread_id TEXT NOT NULL DEFAULT '',
			chat_needs_hydration INTEGER NOT NULL DEFAULT 0,
			chat_context_version INTEGER NOT NULL DEFAULT 0,
			last_hydrated_chat_context_version INTEGER NOT NULL DEFAULT 0,
			last_mode_transition_summary TEXT NOT NULL DEFAULT '',
			artifact_version INTEGER NOT NULL DEFAULT 0,
			last_applied_event_seq INTEGER NOT NULL DEFAULT 0,
			legacy_plan_artifact_path TEXT NOT NULL DEFAULT '',
			legacy_plan_updated_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_message_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy coding_sessions table: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := uuid.NewString()
	if _, err := rawDB.Exec(`
		INSERT INTO coding_sessions(
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			legacy_enabled,chat_codex_thread_id,legacy_supervisor_thread_id,legacy_executor_thread_id,
			chat_needs_hydration,chat_context_version,last_hydrated_chat_context_version,last_mode_transition_summary,artifact_version,last_applied_event_seq,
			legacy_plan_artifact_path,legacy_plan_updated_at,created_at,updated_at,last_message_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		sessionID, "Minimal", "gpt-5.2-codex", "medium", "~/", "workspace-write", "thread_main", 0,
		1, "thread_chat", "thread_orch", "thread_exec",
		1, 3, 2, "resume chat from legacy artifacts", 4, 11,
		"docs/superpowers/plans/minimal.md", now, now, now, now,
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	_ = rawDB.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.GetCodingSession(context.Background(), sessionID); err == nil {
		t.Fatalf("expected legacy coding session to be dropped during schema reset")
	}
	sessions, err := st.ListCodingSessions(context.Background())
	if err != nil {
		t.Fatalf("list sessions after reset: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no coding sessions after legacy schema reset, got %d", len(sessions))
	}
}
