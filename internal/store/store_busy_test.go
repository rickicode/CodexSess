package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCriticalWritePaths_NoSQLiteBusyUnderContention(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "busy-retry.db")
	stA, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store A: %v", err)
	}
	defer func() { _ = stA.Close() }()

	stB, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store B: %v", err)
	}
	defer func() { _ = stB.Close() }()

	now := time.Now().UTC()
	accountID := "acc_busy"
	if err := stA.UpsertAccount(context.Background(), Account{
		ID:           accountID,
		Email:        "busy@example.com",
		TokenID:      "tok",
		TokenAccess:  "acc",
		TokenRefresh: "ref",
		CodexHome:    "/tmp/codex-home",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
		ActiveAPI:    true,
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	sessionID := "sess_busy"
	if _, err := stA.CreateCodingSession(context.Background(), CodingSession{
		ID:            sessionID,
		Title:         "Busy Session",
		Model:         "gpt-5.2-codex",
		WorkDir:       "~/",
		SandboxMode:   "full-access",
		CreatedAt:     now,
		UpdatedAt:     now,
		LastMessageAt: now,
	}); err != nil {
		t.Fatalf("seed coding session: %v", err)
	}

	const workers = 10
	const iterations = 60

	var (
		wg      sync.WaitGroup
		counter uint64
		errCh   = make(chan error, workers*iterations)
	)

	run := func(fn func(i int) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := fn(i); err != nil {
					errCh <- err
				}
			}
		}()
	}

	run(func(i int) error {
		return stA.SetSetting(context.Background(), SettingUsageCursor, fmt.Sprintf("%d", i))
	})

	run(func(i int) error {
		return stB.SaveUsage(context.Background(), UsageSnapshot{
			AccountID:       accountID,
			HourlyPct:       i % 100,
			WeeklyPct:       (i * 2) % 100,
			RawJSON:         fmt.Sprintf(`{"n":%d}`, i),
			FetchedAt:       time.Now().UTC(),
			LastError:       "",
			WindowPrimary:   "hourly",
			WindowSecondary: "weekly",
		})
	})

	run(func(i int) error {
		id := atomic.AddUint64(&counter, 1)
		_, err := stA.AppendCodingMessage(context.Background(), CodingMessage{
			ID:        fmt.Sprintf("msg_%d", id),
			SessionID: sessionID,
			Role:      "assistant",
			Content:   fmt.Sprintf("delta %d", i),
			CreatedAt: time.Now().UTC(),
		})
		return err
	})

	run(func(i int) error {
		id := atomic.AddUint64(&counter, 1)
		return stB.InsertAudit(context.Background(), AuditRecord{
			RequestID: fmt.Sprintf("req_%d", id),
			AccountID: accountID,
			Model:     "gpt-5.2-codex",
			Stream:    true,
			Status:    200,
			LatencyMS: int64(50 + i),
			CreatedAt: time.Now().UTC(),
		})
	})

	run(func(i int) error {
		_, err := stA.CreateCodingSession(context.Background(), CodingSession{
			ID:            fmt.Sprintf("sess_extra_%d", atomic.AddUint64(&counter, 1)),
			Title:         fmt.Sprintf("Extra Session %d", i),
			Model:         "gpt-5.2-codex",
			WorkDir:       "~/",
			SandboxMode:   "full-access",
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
			LastMessageAt: time.Now().UTC(),
		})
		if err == nil {
			return nil
		}
		if isSQLiteBusyError(err) {
			return err
		}
		return err
	})

	run(func(i int) error {
		return stA.UpsertAccount(context.Background(), Account{
			ID:           accountID,
			Email:        "busy@example.com",
			TokenID:      "tok",
			TokenAccess:  "acc",
			TokenRefresh: "ref",
			CodexHome:    "/tmp/codex-home",
			UpdatedAt:    time.Now().UTC(),
			LastUsedAt:   time.Now().UTC(),
			ActiveAPI:    true,
		})
	})

	wg.Wait()
	close(errCh)

	var busyErrs []string
	var otherErrs []string
	for err := range errCh {
		if err == nil {
			continue
		}
		if isSQLiteBusyError(err) || strings.Contains(strings.ToLower(err.Error()), "database is locked") {
			busyErrs = append(busyErrs, err.Error())
			continue
		}
		otherErrs = append(otherErrs, err.Error())
	}

	if len(busyErrs) > 0 {
		t.Fatalf("got SQLITE_BUSY errors (%d): %v", len(busyErrs), busyErrs[:minInt(len(busyErrs), 3)])
	}
	if len(otherErrs) > 0 {
		t.Fatalf("unexpected errors (%d): %v", len(otherErrs), otherErrs[:minInt(len(otherErrs), 3)])
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
