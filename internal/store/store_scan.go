package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
