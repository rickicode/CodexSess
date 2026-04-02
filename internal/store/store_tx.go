package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const sqliteBusyRetryAttempts = 6

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
