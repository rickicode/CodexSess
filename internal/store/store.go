package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const accountSelectColumns = `id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active_api,active_cli,active,revoked,usage_hourly_pct,usage_weekly_pct,usage_hourly_reset_at,usage_weekly_reset_at,usage_raw_json,usage_fetched_at,usage_last_error,usage_window_primary,usage_window_secondary,usage_last_checked_at,usage_next_check_at,usage_fail_count,created_at,updated_at,last_used_at`
const sqliteBusyRetryAttempts = 6

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite busy")
}

func (s *Store) execWithRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var lastErr error
	delay := 20 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetryAttempts; attempt++ {
		res, err := s.db.ExecContext(ctx, query, args...)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt == sqliteBusyRetryAttempts-1 {
			break
		}
		time.Sleep(delay)
		if delay < 240*time.Millisecond {
			delay *= 2
		}
	}
	return nil, lastErr
}

func (s *Store) beginTxWithRetry(ctx context.Context) (*sql.Tx, error) {
	var lastErr error
	delay := 20 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetryAttempts; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err == nil {
			return tx, nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt == sqliteBusyRetryAttempts-1 {
			break
		}
		time.Sleep(delay)
		if delay < 240*time.Millisecond {
			delay *= 2
		}
	}
	return nil, lastErr
}

func txExecWithRetry(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	var lastErr error
	delay := 20 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetryAttempts; attempt++ {
		res, err := tx.ExecContext(ctx, query, args...)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt == sqliteBusyRetryAttempts-1 {
			break
		}
		time.Sleep(delay)
		if delay < 240*time.Millisecond {
			delay *= 2
		}
	}
	return nil, lastErr
}

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
		`CREATE TABLE IF NOT EXISTS usage_snapshots (
			account_id TEXT PRIMARY KEY,
			hourly_pct INTEGER NOT NULL,
			weekly_pct INTEGER NOT NULL,
			hourly_reset_at TEXT,
			weekly_reset_at TEXT,
			raw_json TEXT NOT NULL,
			fetched_at TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			window_primary TEXT NOT NULL DEFAULT '',
			window_secondary TEXT NOT NULL DEFAULT ''
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
				runtime_mode TEXT NOT NULL DEFAULT 'spawn',
				runtime_status TEXT NOT NULL DEFAULT 'idle',
				restart_pending INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_message_at TEXT NOT NULL
			);`,
		`CREATE TABLE IF NOT EXISTS coding_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS system_logs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			message TEXT NOT NULL,
			meta_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS zo_api_keys (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			key_secret TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			conversation_id TEXT NOT NULL DEFAULT '',
			conversation_updated_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_used_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS zo_api_key_usage (
			key_id TEXT PRIMARY KEY,
			usage_count INTEGER NOT NULL,
			last_request_at TEXT NOT NULL,
			last_reset_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_coding_messages_session_created
			ON coding_messages(session_id, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_created
			ON system_logs(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_zo_api_keys_updated
			ON zo_api_keys(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_zo_api_key_usage_updated
			ON zo_api_key_usage(updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_app_settings_updated
			ON app_settings(updated_at);`,
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
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN work_dir TEXT NOT NULL DEFAULT '~/'`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}

	// Backfill revoked status from cached legacy usage snapshots
	if _, err := s.execWithRetry(ctx, `UPDATE accounts SET revoked = 1 WHERE revoked = 0 AND (usage_last_error LIKE '%token_revoked%' OR usage_last_error LIKE '%invalidated oauth token%' OR usage_last_error LIKE '%token_invalidated%' OR usage_last_error LIKE '%account_suspended%' OR usage_last_error LIKE '%account_deactivated%' OR usage_last_error LIKE '%suspended%')`); err != nil {
		return err
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN codex_thread_id TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN sandbox_mode TEXT NOT NULL DEFAULT 'full-access'`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN runtime_mode TEXT NOT NULL DEFAULT 'spawn'`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN runtime_status TEXT NOT NULL DEFAULT 'idle'`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN restart_pending INTEGER NOT NULL DEFAULT 0`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE coding_sessions ADD COLUMN reasoning_level TEXT NOT NULL DEFAULT 'medium'`); err != nil {
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
	if _, err := s.execWithRetry(ctx, `ALTER TABLE zo_api_key_usage ADD COLUMN last_request_at TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") && !strings.Contains(msg, "no such table") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE zo_api_keys ADD COLUMN conversation_id TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") && !strings.Contains(msg, "no such table") {
			return err
		}
	}
	if _, err := s.execWithRetry(ctx, `ALTER TABLE zo_api_keys ADD COLUMN conversation_updated_at TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") && !strings.Contains(msg, "no such table") {
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
	if _, err := s.execWithRetry(ctx, `UPDATE accounts SET active_api=active WHERE active_api=0 AND active=1`); err != nil {
		return err
	}
	if _, err := s.execWithRetry(ctx, `
		UPDATE accounts
		SET
			usage_hourly_pct=COALESCE((SELECT u.hourly_pct FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_hourly_pct),
			usage_weekly_pct=COALESCE((SELECT u.weekly_pct FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_weekly_pct),
			usage_hourly_reset_at=COALESCE((SELECT u.hourly_reset_at FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_hourly_reset_at),
			usage_weekly_reset_at=COALESCE((SELECT u.weekly_reset_at FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_weekly_reset_at),
			usage_raw_json=CASE
				WHEN TRIM(COALESCE(usage_raw_json,''))='' THEN COALESCE((SELECT u.raw_json FROM usage_snapshots u WHERE u.account_id=accounts.id), '{}')
				WHEN usage_raw_json='{}' THEN COALESCE((SELECT u.raw_json FROM usage_snapshots u WHERE u.account_id=accounts.id), '{}')
				ELSE usage_raw_json
			END,
			usage_fetched_at=CASE
				WHEN TRIM(COALESCE(usage_fetched_at,''))='' THEN COALESCE((SELECT u.fetched_at FROM usage_snapshots u WHERE u.account_id=accounts.id), '')
				ELSE usage_fetched_at
			END,
			usage_last_error=COALESCE((SELECT u.last_error FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_last_error),
			usage_window_primary=COALESCE((SELECT u.window_primary FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_window_primary),
			usage_window_secondary=COALESCE((SELECT u.window_secondary FROM usage_snapshots u WHERE u.account_id=accounts.id), usage_window_secondary)
	`); err != nil {
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

func (s *Store) UpsertAccount(ctx context.Context, a Account) error {
	now := time.Now().UTC().Format(time.RFC3339)
	activeAPI := a.ActiveAPI
	if !activeAPI && a.Active {
		activeAPI = true
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.LastUsedAt.IsZero() {
		a.LastUsedAt = time.Now().UTC()
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	_, err := s.execWithRetry(ctx, `
		INSERT INTO accounts(id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active_api,active_cli,active,revoked,created_at,updated_at,last_used_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			email=excluded.email,
			alias=excluded.alias,
			plan_type=excluded.plan_type,
			account_id=excluded.account_id,
			organization_id=excluded.organization_id,
			token_id=excluded.token_id,
			token_access=excluded.token_access,
			token_refresh=excluded.token_refresh,
			codex_home=excluded.codex_home,
			active_api=excluded.active_api,
			active_cli=excluded.active_cli,
			active=excluded.active,
			revoked=excluded.revoked,
			updated_at=excluded.updated_at,
			last_used_at=excluded.last_used_at
	`,
		a.ID, a.Email, a.Alias, a.PlanType, a.AccountID, a.OrganizationID,
		a.TokenID, a.TokenAccess, a.TokenRefresh, a.CodexHome, boolToInt(activeAPI), boolToInt(a.ActiveCLI), boolToInt(a.Active), boolToInt(a.Revoked),
		a.CreatedAt.UTC().Format(time.RFC3339), now, a.LastUsedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) SetActiveAccount(ctx context.Context, id string) error {
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := txExecWithRetry(ctx, tx, `UPDATE accounts SET active=0,active_api=0`); err != nil {
		return err
	}
	res, err := txExecWithRetry(ctx, tx, `UPDATE accounts SET active=1,active_api=1,last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("account not found: %s", id)
	}
	return tx.Commit()
}

func (s *Store) SetActiveCLIAccount(ctx context.Context, id string) error {
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := txExecWithRetry(ctx, tx, `UPDATE accounts SET active_cli=0`); err != nil {
		return err
	}
	res, err := txExecWithRetry(ctx, tx, `UPDATE accounts SET active_cli=1,last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("account not found: %s", id)
	}
	return tx.Commit()
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	_, err := s.execWithRetry(ctx, `DELETE FROM accounts WHERE id=?`, id)
	return err
}

func (s *Store) DeleteRevokedAccounts(ctx context.Context) (int, error) {
	res, err := s.execWithRetry(ctx, `DELETE FROM accounts WHERE revoked=1`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+accountSelectColumns+` FROM accounts ORDER BY revoked ASC, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccountRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(id) FROM accounts`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListAccountsPaginated(ctx context.Context, page, limit int, filter AccountFilter) ([]Account, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	offset := (page - 1) * limit

	whereClause := "1=1"
	var args []any

	if filter.Query != "" {
		whereClause += " AND (email LIKE ? OR alias LIKE ? OR id LIKE ? OR plan_type LIKE ?)"
		q := "%" + filter.Query + "%"
		args = append(args, q, q, q, q)
	}
	if filter.PlanType != "" && filter.PlanType != "all" {
		whereClause += " AND plan_type = ?"
		args = append(args, filter.PlanType)
	}
	switch filter.Status {
	case "revoked":
		whereClause += " AND revoked = 1"
	case "not_revoked":
		whereClause += " AND revoked = 0"
	}

	switch filter.Usage {
	case "exhausted":
		whereClause += " AND usage_fetched_at != '' AND (usage_hourly_pct <= 0 OR usage_weekly_pct <= 0) AND revoked = 0"
	case "available":
		whereClause += " AND usage_fetched_at != '' AND usage_hourly_pct > 0 AND usage_weekly_pct > 0 AND revoked = 0"
	}

	countQuery := `SELECT COUNT(id) FROM accounts WHERE ` + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT ` + accountSelectColumns + ` FROM accounts WHERE ` + whereClause + ` ORDER BY 
		revoked ASC,
		CASE WHEN usage_fetched_at != '' AND (CASE WHEN usage_hourly_pct < usage_weekly_pct THEN usage_hourly_pct ELSE usage_weekly_pct END) < 50 THEN 1 ELSE 0 END ASC,
		active_cli DESC, 
		active_api DESC, 
		CASE WHEN usage_fetched_at = '' THEN 0 ELSE 1 END DESC,
		(CASE WHEN usage_hourly_pct < usage_weekly_pct THEN usage_hourly_pct ELSE usage_weekly_pct END) DESC,
		updated_at DESC 
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccountRows(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (s *Store) FindAccountBySelector(ctx context.Context, selector string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+accountSelectColumns+` FROM accounts WHERE id=? OR email=? OR alias=? LIMIT 1`, selector, selector, selector)
	return scanAccount(row)
}

func (s *Store) ActiveAccount(ctx context.Context) (Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+accountSelectColumns+` FROM accounts WHERE active_api=1 LIMIT 1`)
	return scanAccount(row)
}

func (s *Store) ActiveCLIAccount(ctx context.Context) (Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+accountSelectColumns+` FROM accounts WHERE active_cli=1 LIMIT 1`)
	return scanAccount(row)
}

func (s *Store) SaveUsage(ctx context.Context, u UsageSnapshot) error {
	var hr, wr any
	if u.HourlyResetAt != nil {
		hr = u.HourlyResetAt.UTC().Format(time.RFC3339)
	}
	if u.WeeklyResetAt != nil {
		wr = u.WeeklyResetAt.UTC().Format(time.RFC3339)
	}
	_, err := s.execWithRetry(ctx, `
		UPDATE accounts SET
			usage_hourly_pct=?,
			usage_weekly_pct=?,
			usage_hourly_reset_at=?,
			usage_weekly_reset_at=?,
			usage_raw_json=?,
			usage_fetched_at=?,
			usage_last_error=?,
			usage_window_primary=?,
			usage_window_secondary=?,
			usage_last_checked_at=?,
			usage_fail_count=?,
			usage_next_check_at=?
		WHERE id=?
	`,
		u.HourlyPct, u.WeeklyPct, hr, wr, u.RawJSON, u.FetchedAt.UTC().Format(time.RFC3339), u.LastError, u.WindowPrimary, u.WindowSecondary,
		u.FetchedAt.UTC().Format(time.RFC3339),
		intBool(strings.TrimSpace(u.LastError) != ""),
		nextUsageCheckAt(u).UTC().Format(time.RFC3339),
		u.AccountID,
	)
	return err
}

func (s *Store) SetAccountRevoked(ctx context.Context, id string, revoked bool) error {
	_, err := s.execWithRetry(ctx, `UPDATE accounts SET revoked=? WHERE id=?`, boolToInt(revoked), id)
	return err
}

func (s *Store) GetUsage(ctx context.Context, accountID string) (UsageSnapshot, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,usage_hourly_pct,usage_weekly_pct,usage_hourly_reset_at,usage_weekly_reset_at,usage_raw_json,usage_fetched_at,usage_last_error,usage_window_primary,usage_window_secondary FROM accounts WHERE id=?`, accountID)
	var u UsageSnapshot
	var hr, wr sql.NullString
	var fetched string
	if err := row.Scan(&u.AccountID, &u.HourlyPct, &u.WeeklyPct, &hr, &wr, &u.RawJSON, &fetched, &u.LastError, &u.WindowPrimary, &u.WindowSecondary); err != nil {
		return u, err
	}
	u.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
	if hr.Valid {
		t, _ := time.Parse(time.RFC3339, hr.String)
		u.HourlyResetAt = &t
	}
	if wr.Valid {
		t, _ := time.Parse(time.RFC3339, wr.String)
		u.WeeklyResetAt = &t
	}
	return u, nil
}

func (s *Store) ListUsageSnapshots(ctx context.Context) (map[string]UsageSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,usage_hourly_pct,usage_weekly_pct,usage_hourly_reset_at,usage_weekly_reset_at,usage_raw_json,usage_fetched_at,usage_last_error,usage_window_primary,usage_window_secondary FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]UsageSnapshot{}
	for rows.Next() {
		var u UsageSnapshot
		var hr, wr sql.NullString
		var fetched string
		if err := rows.Scan(&u.AccountID, &u.HourlyPct, &u.WeeklyPct, &hr, &wr, &u.RawJSON, &fetched, &u.LastError, &u.WindowPrimary, &u.WindowSecondary); err != nil {
			return nil, err
		}
		u.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
		if hr.Valid {
			t, _ := time.Parse(time.RFC3339, hr.String)
			u.HourlyResetAt = &t
		}
		if wr.Valid {
			t, _ := time.Parse(time.RFC3339, wr.String)
			u.WeeklyResetAt = &t
		}
		out[u.AccountID] = u
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) InsertAudit(ctx context.Context, r AuditRecord) error {
	_, err := s.execWithRetry(ctx, `INSERT INTO request_audit(request_id,account_id,model,stream,status,latency_ms,created_at) VALUES(?,?,?,?,?,?,?)`, r.RequestID, r.AccountID, r.Model, boolToInt(r.Stream), r.Status, r.LatencyMS, r.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *Store) CreateCodingSession(ctx context.Context, session CodingSession) (CodingSession, error) {
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = now
	}
	if session.LastMessageAt.IsZero() {
		session.LastMessageAt = now
	}
	if strings.TrimSpace(session.Title) == "" {
		session.Title = "New Session"
	}
	if strings.TrimSpace(session.Model) == "" {
		session.Model = "gpt-5.2-codex"
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	if strings.TrimSpace(session.WorkDir) == "" {
		session.WorkDir = "~/"
	}
	if strings.TrimSpace(session.RuntimeMode) == "" {
		session.RuntimeMode = "spawn"
	}
	if strings.TrimSpace(session.RuntimeStatus) == "" {
		session.RuntimeStatus = "idle"
	}
	_, err := s.execWithRetry(ctx, `
		INSERT INTO coding_sessions(id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,runtime_mode,runtime_status,restart_pending,created_at,updated_at,last_message_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, session.ID, session.Title, session.Model, session.ReasoningLevel, session.WorkDir, session.SandboxMode, session.CodexThreadID, session.RuntimeMode, session.RuntimeStatus, boolToInt(session.RestartPending), session.CreatedAt.UTC().Format(time.RFC3339), session.UpdatedAt.UTC().Format(time.RFC3339), session.LastMessageAt.UTC().Format(time.RFC3339))
	if err != nil {
		return CodingSession{}, err
	}
	return s.GetCodingSession(ctx, session.ID)
}

func (s *Store) ListCodingSessions(ctx context.Context) ([]CodingSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,runtime_mode,runtime_status,restart_pending,created_at,updated_at,last_message_at
		FROM coding_sessions
		ORDER BY last_message_at DESC, updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodingSession
	for rows.Next() {
		var session CodingSession
		var createdAt, updatedAt, lastMessageAt string
		var restartPending int
		if err := rows.Scan(&session.ID, &session.Title, &session.Model, &session.ReasoningLevel, &session.WorkDir, &session.SandboxMode, &session.CodexThreadID, &session.RuntimeMode, &session.RuntimeStatus, &restartPending, &createdAt, &updatedAt, &lastMessageAt); err != nil {
			return nil, err
		}
		session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
		session.RestartPending = restartPending != 0
		session.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		session.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		session.LastMessageAt, _ = time.Parse(time.RFC3339, lastMessageAt)
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetCodingSession(ctx context.Context, id string) (CodingSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,runtime_mode,runtime_status,restart_pending,created_at,updated_at,last_message_at
		FROM coding_sessions
		WHERE id=?
		LIMIT 1
	`, strings.TrimSpace(id))
	var session CodingSession
	var restartPending int
	var createdAt, updatedAt, lastMessageAt string
	if err := row.Scan(&session.ID, &session.Title, &session.Model, &session.ReasoningLevel, &session.WorkDir, &session.SandboxMode, &session.CodexThreadID, &session.RuntimeMode, &session.RuntimeStatus, &restartPending, &createdAt, &updatedAt, &lastMessageAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session, fmt.Errorf("coding session not found")
		}
		return session, err
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	session.RestartPending = restartPending != 0
	session.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	session.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	session.LastMessageAt, _ = time.Parse(time.RFC3339, lastMessageAt)
	return session, nil
}

func (s *Store) UpdateCodingSession(ctx context.Context, session CodingSession) error {
	if strings.TrimSpace(session.ID) == "" {
		return fmt.Errorf("coding session id is required")
	}
	if strings.TrimSpace(session.Title) == "" {
		session.Title = "New Session"
	}
	if strings.TrimSpace(session.Model) == "" {
		session.Model = "gpt-5.2-codex"
	}
	session.ReasoningLevel = normalizeCodingReasoningLevelStore(session.ReasoningLevel)
	if strings.TrimSpace(session.WorkDir) == "" {
		session.WorkDir = "~/"
	}
	if strings.TrimSpace(session.SandboxMode) == "" {
		session.SandboxMode = "full-access"
	}
	if strings.TrimSpace(session.RuntimeMode) == "" {
		session.RuntimeMode = "spawn"
	}
	if strings.TrimSpace(session.RuntimeStatus) == "" {
		session.RuntimeStatus = "idle"
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now().UTC()
	}
	if session.LastMessageAt.IsZero() {
		session.LastMessageAt = session.UpdatedAt
	}
	_, err := s.execWithRetry(ctx, `
		UPDATE coding_sessions
		SET title=?, model=?, reasoning_level=?, work_dir=?, sandbox_mode=?, codex_thread_id=?, runtime_mode=?, runtime_status=?, restart_pending=?, updated_at=?, last_message_at=?
		WHERE id=?
	`, session.Title, session.Model, session.ReasoningLevel, session.WorkDir, session.SandboxMode, session.CodexThreadID, session.RuntimeMode, session.RuntimeStatus, boolToInt(session.RestartPending), session.UpdatedAt.UTC().Format(time.RFC3339), session.LastMessageAt.UTC().Format(time.RFC3339), session.ID)
	return err
}

func (s *Store) DeleteCodingSession(ctx context.Context, id string) error {
	sessionID := strings.TrimSpace(id)
	if sessionID == "" {
		return fmt.Errorf("coding session id is required")
	}
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_messages WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM coding_sessions WHERE id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendCodingMessage(ctx context.Context, msg CodingMessage) (CodingMessage, error) {
	if strings.TrimSpace(msg.ID) == "" {
		return CodingMessage{}, fmt.Errorf("coding message id is required")
	}
	if strings.TrimSpace(msg.SessionID) == "" {
		return CodingMessage{}, fmt.Errorf("coding message session_id is required")
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	res, err := s.execWithRetry(ctx, `
		INSERT INTO coding_messages(id,session_id,role,content,input_tokens,output_tokens,created_at)
		VALUES(?,?,?,?,?,?,?)
	`, msg.ID, msg.SessionID, msg.Role, msg.Content, msg.InputTokens, msg.OutputTokens, msg.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return CodingMessage{}, err
	}
	if seq, seqErr := res.LastInsertId(); seqErr == nil {
		msg.Sequence = seq
	}
	return msg, nil
}

func (s *Store) ListCodingMessages(ctx context.Context, sessionID string) ([]CodingMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rowid,id,session_id,role,content,input_tokens,output_tokens,created_at
		FROM coding_messages
		WHERE session_id=?
		ORDER BY created_at ASC, rowid ASC
	`, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodingMessage
	for rows.Next() {
		var msg CodingMessage
		var sequence int64
		var createdAt string
		if err := rows.Scan(&sequence, &msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.InputTokens, &msg.OutputTokens, &createdAt); err != nil {
			return nil, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		msg.Sequence = sequence
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListCodingMessagesPage(ctx context.Context, sessionID string, limit int, beforeID string) ([]CodingMessage, bool, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, false, fmt.Errorf("coding session id is required")
	}
	if limit <= 0 {
		return nil, false, fmt.Errorf("limit must be positive")
	}
	cursorID := strings.TrimSpace(beforeID)
	var cursorCreatedAt string
	var cursorRowID int64
	if cursorID != "" {
		row := s.db.QueryRowContext(ctx, `
			SELECT created_at, rowid
			FROM coding_messages
			WHERE session_id=? AND id=?
			LIMIT 1
		`, sid, cursorID)
		if err := row.Scan(&cursorCreatedAt, &cursorRowID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Cursor can become stale when history is refreshed/deleted while user scrolls.
				// Treat as end-of-history instead of failing the whole request.
				return []CodingMessage{}, false, nil
			}
			return nil, false, err
		}
	}
	var (
		rows *sql.Rows
		err  error
	)
	if cursorID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT rowid,id,session_id,role,content,input_tokens,output_tokens,created_at
			FROM coding_messages
			WHERE session_id=?
				AND (created_at < ? OR (created_at = ? AND rowid < ?))
			ORDER BY created_at DESC, rowid DESC
			LIMIT ?
		`, sid, cursorCreatedAt, cursorCreatedAt, cursorRowID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT rowid,id,session_id,role,content,input_tokens,output_tokens,created_at
			FROM coding_messages
			WHERE session_id=?
			ORDER BY created_at DESC, rowid DESC
			LIMIT ?
		`, sid, limit)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]CodingMessage, 0, limit)
	rowIDs := make([]int64, 0, limit)
	for rows.Next() {
		var msg CodingMessage
		var rowID int64
		var createdAt string
		if err := rows.Scan(&rowID, &msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.InputTokens, &msg.OutputTokens, &createdAt); err != nil {
			return nil, false, err
		}
		msg.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		msg.Sequence = rowID
		out = append(out, msg)
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
		rowIDs[i], rowIDs[j] = rowIDs[j], rowIDs[i]
	}
	hasMore := false
	if len(out) == limit && len(out) > 0 {
		oldest := out[0]
		oldestCreatedAt := oldest.CreatedAt.UTC().Format(time.RFC3339)
		oldestRowID := rowIDs[0]
		row := s.db.QueryRowContext(ctx, `
			SELECT 1
			FROM coding_messages
			WHERE session_id=?
				AND (created_at < ? OR (created_at = ? AND rowid < ?))
			LIMIT 1
		`, sid, oldestCreatedAt, oldestCreatedAt, oldestRowID)
		var exists int
		if err := row.Scan(&exists); err == nil {
			hasMore = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	return out, hasMore, nil
}

func scanAccount(row *sql.Row) (Account, error) {
	var a Account
	var active, activeAPI, activeCLI, revoked int // Added 'revoked'
	var createdAt, updatedAt, lastUsedAt string
	var usageFetched string
	var usageHourlyResetAt, usageWeeklyResetAt sql.NullString
	var usageLastCheckedAt, usageNextCheckAt sql.NullString
	if err := row.Scan(
		&a.ID, &a.Email, &a.Alias, &a.PlanType, &a.AccountID, &a.OrganizationID, &a.TokenID, &a.TokenAccess, &a.TokenRefresh, &a.CodexHome,
		&activeAPI, &activeCLI, &active, &revoked, // Added &revoked
		&a.UsageHourlyPct, &a.UsageWeeklyPct, &usageHourlyResetAt, &usageWeeklyResetAt, &a.UsageRawJSON, &usageFetched, &a.UsageLastError, &a.UsageWindowPrimary, &a.UsageWindowSecondary,
		&usageLastCheckedAt, &usageNextCheckAt, &a.UsageFailCount,
		&createdAt, &updatedAt, &lastUsedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a, fmt.Errorf("account not found")
		}
		return a, err
	}
	a.Active = active == 1
	a.ActiveAPI = activeAPI == 1
	a.ActiveCLI = activeCLI == 1
	a.Revoked = revoked == 1 // Set Revoked field
	if a.ActiveAPI {
		a.Active = true
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	a.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsedAt)
	if usageHourlyResetAt.Valid {
		t, _ := time.Parse(time.RFC3339, usageHourlyResetAt.String)
		a.UsageHourlyResetAt = &t
	}
	if usageWeeklyResetAt.Valid {
		t, _ := time.Parse(time.RFC3339, usageWeeklyResetAt.String)
		a.UsageWeeklyResetAt = &t
	}
	a.UsageFetchedAt, _ = time.Parse(time.RFC3339, usageFetched)
	if usageLastCheckedAt.Valid {
		t, _ := time.Parse(time.RFC3339, usageLastCheckedAt.String)
		a.UsageLastCheckedAt = &t
	}
	if usageNextCheckAt.Valid {
		t, _ := time.Parse(time.RFC3339, usageNextCheckAt.String)
		a.UsageNextCheckAt = &t
	}
	return a, nil
}

func scanAccountRows(rows *sql.Rows) (Account, error) {
	var a Account
	var activeAPI, activeCLI, active, revoked, failCount int
	var hp, wp int
	var created, updated, lastUsed string
	var hr, wr, ujson, ufetched, ulerr, uwinpri, uwinsec, ulastcheck, unextcheck sql.NullString
	if err := rows.Scan(
		&a.ID, &a.Email, &a.Alias, &a.PlanType, &a.AccountID, &a.OrganizationID, &a.TokenID, &a.TokenAccess, &a.TokenRefresh, &a.CodexHome,
		&activeAPI, &activeCLI, &active, &revoked,
		&hp, &wp, &hr, &wr, &ujson, &ufetched, &ulerr, &uwinpri, &uwinsec, &ulastcheck, &unextcheck, &failCount,
		&created, &updated, &lastUsed,
	); err != nil {
		return a, err
	}
	a.Active = active == 1
	a.ActiveAPI = activeAPI == 1
	a.ActiveCLI = activeCLI == 1
	a.Revoked = revoked == 1
	if a.ActiveAPI {
		a.Active = true
	}
	a.UsageHourlyPct = hp
	a.UsageWeeklyPct = wp
	a.UsageRawJSON = ujson.String
	a.UsageLastError = ulerr.String
	a.UsageWindowPrimary = uwinpri.String
	a.UsageWindowSecondary = uwinsec.String
	a.UsageFailCount = failCount
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	a.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsed)
	if hr.Valid {
		t, _ := time.Parse(time.RFC3339, hr.String)
		a.UsageHourlyResetAt = &t
	}
	if wr.Valid {
		t, _ := time.Parse(time.RFC3339, wr.String)
		a.UsageWeeklyResetAt = &t
	}
	a.UsageFetchedAt, _ = time.Parse(time.RFC3339, ufetched.String)
	if ulastcheck.Valid {
		t, _ := time.Parse(time.RFC3339, ulastcheck.String)
		a.UsageLastCheckedAt = &t
	}
	if unextcheck.Valid {
		t, _ := time.Parse(time.RFC3339, unextcheck.String)
		a.UsageNextCheckAt = &t
	}
	return a, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func intBool(v bool) int {
	if v {
		return 1
	}
	return 0
}

func normalizeCodingReasoningLevelStore(v string) string {
	level := strings.TrimSpace(strings.ToLower(v))
	switch level {
	case "low", "high":
		return level
	default:
		return "medium"
	}
}

func nextUsageCheckAt(u UsageSnapshot) time.Time {
	now := time.Now().UTC()
	if strings.TrimSpace(u.LastError) != "" {
		return now.Add(30 * time.Minute)
	}
	return now.Add(15 * time.Minute)
}
