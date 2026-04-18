package store

import (
	"context"
	"strings"
)

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			alias TEXT NOT NULL DEFAULT '',
			plan_type TEXT NOT NULL DEFAULT '',
			account_id TEXT NOT NULL DEFAULT '',
			organization_id TEXT NOT NULL DEFAULT '',
			token_id TEXT NOT NULL,
			token_access TEXT NOT NULL,
			token_refresh TEXT NOT NULL,
			codex_home TEXT NOT NULL,
			active_api INTEGER NOT NULL DEFAULT 0,
			active_cli INTEGER NOT NULL DEFAULT 0,
			active INTEGER NOT NULL DEFAULT 0,
			usage_hourly_pct INTEGER NOT NULL DEFAULT 0,
			usage_weekly_pct INTEGER NOT NULL DEFAULT 0,
			usage_hourly_reset_at TEXT,
			usage_weekly_reset_at TEXT,
			usage_raw_json TEXT NOT NULL DEFAULT '{}',
			usage_fetched_at TEXT NOT NULL DEFAULT '',
			usage_last_error TEXT NOT NULL DEFAULT '',
			usage_window_primary TEXT NOT NULL DEFAULT '',
			usage_window_secondary TEXT NOT NULL DEFAULT '',
			usage_last_checked_at TEXT,
			usage_next_check_at TEXT,
			usage_fail_count INTEGER NOT NULL DEFAULT 0,
			revoked INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_used_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS request_audit (
			request_id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			model TEXT NOT NULL,
			stream INTEGER NOT NULL,
			status INTEGER NOT NULL,
			latency_ms INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS coding_sessions (
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
			last_message_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS coding_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			account_email TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS coding_message_snapshots (
			session_id TEXT NOT NULL,
			view_mode TEXT NOT NULL,
			snapshot_json TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(session_id, view_mode)
		);`,
		`CREATE TABLE IF NOT EXISTS coding_view_messages (
			session_id TEXT NOT NULL,
			view_mode TEXT NOT NULL,
			message_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(session_id, view_mode, message_id)
		);`,
		`CREATE TABLE IF NOT EXISTS coding_ws_request_dedup (
			session_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(session_id, request_id)
		);`,
		`CREATE TABLE IF NOT EXISTS system_logs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			message TEXT NOT NULL,
			meta_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS memory_items (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			scope_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			key TEXT NOT NULL,
			value_json TEXT NOT NULL DEFAULT '{}',
			source_type TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			verified INTEGER NOT NULL DEFAULT 0,
			confidence INTEGER NOT NULL DEFAULT 0,
			stale INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT NOT NULL DEFAULT '',
			UNIQUE(scope, scope_id, kind, key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_coding_messages_session_created
			ON coding_messages(session_id, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_coding_message_snapshots_updated
			ON coding_message_snapshots(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_coding_view_messages_session_seq
			ON coding_view_messages(session_id, view_mode, seq);`,
		`CREATE INDEX IF NOT EXISTS idx_coding_ws_request_dedup_created
			ON coding_ws_request_dedup(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_created
			ON system_logs(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_app_settings_updated
			ON app_settings(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_scope_updated
			ON memory_items(scope, scope_id, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_scope_kind
			ON memory_items(scope, scope_id, kind);`,
	}
	for _, stmt := range stmts {
		if _, err := s.execWithRetry(ctx, stmt); err != nil {
			return err
		}
	}
	// Add new columns with ALTER TABLE statements, ignoring "duplicate column" errors
	alterStatements := []string{
		`ALTER TABLE accounts ADD COLUMN revoked INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range alterStatements {
		if _, err := s.execWithRetry(ctx, stmt); err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
				return err
			}
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE accounts DROP COLUMN login_option`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "no such column") && !strings.Contains(msg, "syntax error") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `DROP TABLE IF EXISTS usage_snapshots`); err != nil {
		return err
	}
	if err := s.ensureChatOnlyCodingSessionsSchema(ctx); err != nil {
		return err
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_messages ADD COLUMN actor TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_messages ADD COLUMN account_email TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `UPDATE coding_sessions SET reasoning_level='medium' WHERE TRIM(COALESCE(reasoning_level,''))=''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "no such column") {
			return err
		}
	}
	alterAccountsColumns := []string{
		`ALTER TABLE accounts ADD COLUMN active_api INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE accounts ADD COLUMN active_cli INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE accounts ADD COLUMN usage_hourly_pct INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE accounts ADD COLUMN usage_weekly_pct INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE accounts ADD COLUMN usage_hourly_reset_at TEXT`,
		`ALTER TABLE accounts ADD COLUMN usage_weekly_reset_at TEXT`,
		`ALTER TABLE accounts ADD COLUMN usage_raw_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE accounts ADD COLUMN usage_fetched_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE accounts ADD COLUMN usage_last_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE accounts ADD COLUMN usage_window_primary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE accounts ADD COLUMN usage_window_secondary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE accounts ADD COLUMN usage_last_checked_at TEXT`,
		`ALTER TABLE accounts ADD COLUMN usage_next_check_at TEXT`,
		`ALTER TABLE accounts ADD COLUMN usage_fail_count INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range alterAccountsColumns {
		if _, err := s.execWithRetry(ctx, stmt); err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
				return err
			}
		}
	}
	if _, err := s.execWithRetry(ctx, `UPDATE accounts SET revoked = 1 WHERE revoked = 0 AND (usage_last_error LIKE '%token_revoked%' OR usage_last_error LIKE '%invalidated oauth token%' OR usage_last_error LIKE '%token_invalidated%' OR usage_last_error LIKE '%account_suspended%' OR usage_last_error LIKE '%account_deactivated%' OR usage_last_error LIKE '%suspended%')`); err != nil {
		return err
	}
	if _, err := s.execWithRetry(ctx, `UPDATE accounts SET active_api=active WHERE active_api=0 AND active=1`); err != nil {
		return err
	}
	accountIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_accounts_active_api
			ON accounts(active_api);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_active_cli
			ON accounts(active_cli);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_usage_next_check
			ON accounts(usage_next_check_at);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_revoked
			ON accounts(revoked);`,
	}
	for _, stmt := range accountIndexes {
		if _, err := s.execWithRetry(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) codingSessionsColumnExists(ctx context.Context, column string) (bool, error) {
	columns, err := s.codingSessionsColumns(ctx)
	if err != nil {
		return false, err
	}
	_, ok := columns[strings.TrimSpace(strings.ToLower(column))]
	return ok, nil
}

func (s *Store) codingSessionsColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(coding_sessions)`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		columns[strings.TrimSpace(strings.ToLower(name))] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func codingSessionsSchemaNeedsRebuild(columns map[string]struct{}) bool {
	return codingSessionsSchemaNeedsReset(columns)
}

func codingSessionsSchemaNeedsReset(columns map[string]struct{}) bool {
	required := map[string]struct{}{
		"id":                     {},
		"title":                  {},
		"model":                  {},
		"reasoning_level":        {},
		"work_dir":               {},
		"sandbox_mode":           {},
		"codex_thread_id":        {},
		"restart_pending":        {},
		"artifact_version":       {},
		"last_applied_event_seq": {},
		"created_at":             {},
		"updated_at":             {},
		"last_message_at":        {},
	}
	for name := range required {
		if _, ok := columns[name]; !ok {
			return true
		}
	}
	for name := range columns {
		if _, ok := required[name]; !ok {
			return true
		}
	}
	return false
}

func (s *Store) ensureChatOnlyCodingSessionsSchema(ctx context.Context) error {
	columns, err := s.codingSessionsColumns(ctx)
	if err != nil {
		return err
	}
	if !codingSessionsSchemaNeedsRebuild(columns) {
		_, err := s.execWithRetry(ctx, `UPDATE coding_sessions SET reasoning_level='medium' WHERE TRIM(COALESCE(reasoning_level,''))=''`)
		return err
	}

	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := txExecWithRetry(ctx, tx, `
		CREATE TABLE coding_sessions_chat_only (
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
			last_message_at TEXT NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `
		INSERT INTO coding_sessions_chat_only(
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			artifact_version,last_applied_event_seq,created_at,updated_at,last_message_at
		)
		SELECT
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			artifact_version,last_applied_event_seq,created_at,updated_at,last_message_at
		FROM coding_sessions
	`); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `DROP TABLE coding_sessions`); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `ALTER TABLE coding_sessions_chat_only RENAME TO coding_sessions`); err != nil {
		return err
	}
	for _, table := range []string{
		"coding_messages",
		"coding_message_snapshots",
		"coding_view_messages",
		"coding_ws_request_dedup",
		"memory_items",
	} {
		if _, err := txExecWithRetry(ctx, tx, `DELETE FROM `+table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) NormalizeCodingSessionSchema(ctx context.Context) error {
	return s.ensureChatOnlyCodingSessionsSchema(ctx)
}
