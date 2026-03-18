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

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
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
			active INTEGER NOT NULL DEFAULT 0,
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE accounts DROP COLUMN login_option`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "no such column") && !strings.Contains(msg, "syntax error") {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertAccount(ctx context.Context, a Account) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.LastUsedAt.IsZero() {
		a.LastUsedAt = time.Now().UTC()
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts(id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active,created_at,updated_at,last_used_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
			active=excluded.active,
			updated_at=excluded.updated_at,
			last_used_at=excluded.last_used_at
	`,
		a.ID, a.Email, a.Alias, a.PlanType, a.AccountID, a.OrganizationID,
		a.TokenID, a.TokenAccess, a.TokenRefresh, a.CodexHome, boolToInt(a.Active),
		a.CreatedAt.UTC().Format(time.RFC3339), now, a.LastUsedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) SetActiveAccount(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET active=0`); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE accounts SET active=1,last_used_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), id)
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, id)
	return err
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active,created_at,updated_at,last_used_at FROM accounts ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var active int
		var createdAt, updatedAt, lastUsedAt string
		if err := rows.Scan(&a.ID, &a.Email, &a.Alias, &a.PlanType, &a.AccountID, &a.OrganizationID, &a.TokenID, &a.TokenAccess, &a.TokenRefresh, &a.CodexHome, &active, &createdAt, &updatedAt, &lastUsedAt); err != nil {
			return nil, err
		}
		a.Active = active == 1
		a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		a.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsedAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) FindAccountBySelector(ctx context.Context, selector string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active,created_at,updated_at,last_used_at FROM accounts WHERE id=? OR email=? OR alias=? LIMIT 1`, selector, selector, selector)
	return scanAccount(row)
}

func (s *Store) ActiveAccount(ctx context.Context) (Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active,created_at,updated_at,last_used_at FROM accounts WHERE active=1 LIMIT 1`)
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_snapshots(account_id,hourly_pct,weekly_pct,hourly_reset_at,weekly_reset_at,raw_json,fetched_at,last_error,window_primary,window_secondary)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET
			hourly_pct=excluded.hourly_pct,
			weekly_pct=excluded.weekly_pct,
			hourly_reset_at=excluded.hourly_reset_at,
			weekly_reset_at=excluded.weekly_reset_at,
			raw_json=excluded.raw_json,
			fetched_at=excluded.fetched_at,
			last_error=excluded.last_error,
			window_primary=excluded.window_primary,
			window_secondary=excluded.window_secondary
	`, u.AccountID, u.HourlyPct, u.WeeklyPct, hr, wr, u.RawJSON, u.FetchedAt.UTC().Format(time.RFC3339), u.LastError, u.WindowPrimary, u.WindowSecondary)
	return err
}

func (s *Store) GetUsage(ctx context.Context, accountID string) (UsageSnapshot, error) {
	row := s.db.QueryRowContext(ctx, `SELECT account_id,hourly_pct,weekly_pct,hourly_reset_at,weekly_reset_at,raw_json,fetched_at,last_error,window_primary,window_secondary FROM usage_snapshots WHERE account_id=?`, accountID)
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
	rows, err := s.db.QueryContext(ctx, `SELECT account_id,hourly_pct,weekly_pct,hourly_reset_at,weekly_reset_at,raw_json,fetched_at,last_error,window_primary,window_secondary FROM usage_snapshots`)
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO request_audit(request_id,account_id,model,stream,status,latency_ms,created_at) VALUES(?,?,?,?,?,?,?)`, r.RequestID, r.AccountID, r.Model, boolToInt(r.Stream), r.Status, r.LatencyMS, r.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func scanAccount(row *sql.Row) (Account, error) {
	var a Account
	var active int
	var createdAt, updatedAt, lastUsedAt string
	if err := row.Scan(&a.ID, &a.Email, &a.Alias, &a.PlanType, &a.AccountID, &a.OrganizationID, &a.TokenID, &a.TokenAccess, &a.TokenRefresh, &a.CodexHome, &active, &createdAt, &updatedAt, &lastUsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return a, fmt.Errorf("account not found")
		}
		return a, err
	}
	a.Active = active == 1
	a.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	a.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsedAt)
	return a, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
