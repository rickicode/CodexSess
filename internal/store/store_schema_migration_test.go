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

	expected := map[string]struct{}{}
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
		expected[name] = struct{}{}
		if _, ok := columns[name]; !ok {
			t.Fatalf("expected survivor column %q in coding_sessions schema", name)
		}
	}
	if len(columns) != len(expected) {
		t.Fatalf("expected exact chat-only column set %v, got %v", expected, columns)
	}
	for name := range columns {
		if _, ok := expected[name]; !ok {
			t.Fatalf("expected no extra columns in coding_sessions schema, got %q", name)
		}
	}
}

func TestGetCodingSession_ExtraColumnsMigrateAndPreserveCodingSessions(t *testing.T) {
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
			artifact_version INTEGER NOT NULL DEFAULT 0,
			last_applied_event_seq INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_message_at TEXT NOT NULL,
			extra_flag INTEGER NOT NULL DEFAULT 0,
			extra_text TEXT NOT NULL DEFAULT '',
			extra_counter INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create coding_sessions table with extra columns: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := uuid.NewString()
	if _, err := rawDB.Exec(`
		INSERT INTO coding_sessions(
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			artifact_version,last_applied_event_seq,created_at,updated_at,last_message_at,extra_flag,extra_text,extra_counter
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		sessionID, "Minimal", "gpt-5.2-codex", "medium", "~/", "workspace-write", "thread_main", 0,
		4, 11, now, now, now, 1, "legacy payload", 2,
	); err != nil {
		t.Fatalf("insert row with extra columns: %v", err)
	}
	_ = rawDB.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	session, err := st.GetCodingSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("expected coding session with extra columns to survive schema migration: %v", err)
	}
	if session.CodexThreadID != "thread_main" {
		t.Fatalf("expected canonical thread id preserved, got %q", session.CodexThreadID)
	}
	sessions, err := st.ListCodingSessions(context.Background())
	if err != nil {
		t.Fatalf("list sessions after migration: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected preserved coding session after extra-column migration, got %d", len(sessions))
	}
	columns, err := st.codingSessionsColumns(context.Background())
	if err != nil {
		t.Fatalf("codingSessionsColumns after migration: %v", err)
	}
	for _, column := range []string{
		"extra_flag",
		"extra_text",
		"extra_counter",
	} {
		if _, ok := columns[column]; ok {
			t.Fatalf("expected extra column %q dropped after migration", column)
		}
	}
}

func TestGetCodingSession_ExtraLegacyColumnsMigrateAndPreserveCodingData(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "coding-runtime-columns-reset.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
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
			artifact_version INTEGER NOT NULL DEFAULT 0,
			last_applied_event_seq INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_message_at TEXT NOT NULL,
			obsolete_mode TEXT NOT NULL DEFAULT 'spawn',
			obsolete_status TEXT NOT NULL DEFAULT 'idle'
		)
	`); err != nil {
		t.Fatalf("create coding_sessions with extra runtime columns: %v", err)
	}
	if _, err := rawDB.Exec(`
		CREATE TABLE coding_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			account_email TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create coding_messages: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := uuid.NewString()
	if _, err := rawDB.Exec(`
		INSERT INTO coding_sessions(
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			artifact_version,last_applied_event_seq,created_at,updated_at,last_message_at,obsolete_mode,obsolete_status
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		sessionID, "Drop Me", "gpt-5.2-codex", "medium", "~/", "workspace-write", "thread_keep", 0,
		3, 9, now, now, now, "spawn", "idle",
	); err != nil {
		t.Fatalf("insert coding session: %v", err)
	}
	if _, err := rawDB.Exec(`
		INSERT INTO coding_messages(
			id,session_id,role,actor,account_email,content,input_tokens,output_tokens,created_at
		) VALUES(?,?,?,?,?,?,?,?,?)
	`, "msg_keep", sessionID, "assistant", "", "", "still here", 0, 0, now); err != nil {
		t.Fatalf("insert coding message: %v", err)
	}
	_ = rawDB.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	session, err := st.GetCodingSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("expected session with extra legacy columns to survive schema migration: %v", err)
	}
	if session.CodexThreadID != "thread_keep" {
		t.Fatalf("expected canonical thread id preserved, got %q", session.CodexThreadID)
	}
	messages, err := st.ListCodingMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list messages after extra-column migration: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected messages to be preserved across extra-column migration, got %#v", messages)
	}
	if messages[0].Content != "still here" {
		t.Fatalf("expected preserved message content, got %#v", messages[0])
	}
	columns, err := st.codingSessionsColumns(context.Background())
	if err != nil {
		t.Fatalf("codingSessionsColumns after migration: %v", err)
	}
	if _, ok := columns["obsolete_mode"]; ok {
		t.Fatalf("expected obsolete_mode dropped after migration")
	}
	if _, ok := columns["obsolete_status"]; ok {
		t.Fatalf("expected obsolete_status dropped after migration")
	}
}
