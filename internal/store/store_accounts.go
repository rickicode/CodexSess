package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const accountSelectColumns = `id,email,alias,plan_type,account_id,organization_id,token_id,token_access,token_refresh,codex_home,active_api,active_cli,active,revoked,usage_hourly_pct,usage_weekly_pct,usage_hourly_reset_at,usage_weekly_reset_at,usage_raw_json,usage_fetched_at,usage_last_error,usage_window_primary,usage_window_secondary,usage_last_checked_at,usage_next_check_at,usage_fail_count,created_at,updated_at,last_used_at`

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
	defer func() { _ = tx.Rollback() }()
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
	defer func() { _ = tx.Rollback() }()
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
	defer func() { _ = rows.Close() }()
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

func (s *Store) CountInvalidAccounts(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(id) FROM accounts
		WHERE revoked = 1
		   OR lower(usage_last_error) LIKE '%token_revoked%'
		   OR lower(usage_last_error) LIKE '%invalidated oauth token%'
		   OR lower(usage_last_error) LIKE '%token_invalidated%'
		   OR lower(usage_last_error) LIKE '%account_suspended%'
		   OR lower(usage_last_error) LIKE '%account_deactivated%'
		   OR lower(usage_last_error) LIKE '%suspended%'
		   OR (lower(usage_last_error) LIKE '%status=401%' AND lower(usage_last_error) LIKE '%oauth%')
		   OR ((lower(usage_last_error) LIKE '%"status":401%' OR lower(usage_last_error) LIKE '%"status": 401%') AND lower(usage_last_error) LIKE '%token%')
	`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountRevokedAccounts(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(id) FROM accounts WHERE revoked = 1`)
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()

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
