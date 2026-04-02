package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const usageURL = "https://chatgpt.com/backend-api/wham/usage"

var usageHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

type UsageResult struct {
	HourlyPct       int
	WeeklyPct       int
	HourlyResetAt   *time.Time
	WeeklyResetAt   *time.Time
	RawJSON         string
	WindowPrimary   string
	WindowSecondary string
}

func FetchUsage(ctx context.Context, accessToken string, accountID string) (UsageResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return UsageResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}
	resp, err := usageHTTPClient.Do(req)
	if err != nil {
		return UsageResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return UsageResult{}, err
	}
	raw, _ := json.Marshal(payload)
	if resp.StatusCode >= 400 {
		return UsageResult{}, fmt.Errorf("usage API error: %d %s", resp.StatusCode, string(raw))
	}
	hourly, hrReset, hrWin := parseWindow(payload, "primary_window")
	weekly, wkReset, wkWin := parseWindow(payload, "secondary_window")
	return UsageResult{
		HourlyPct:       hourly,
		WeeklyPct:       weekly,
		HourlyResetAt:   hrReset,
		WeeklyResetAt:   wkReset,
		RawJSON:         string(raw),
		WindowPrimary:   hrWin,
		WindowSecondary: wkWin,
	}, nil
}

func parseWindow(payload map[string]any, key string) (int, *time.Time, string) {
	rateLimit, _ := payload["rate_limit"].(map[string]any)
	window, _ := rateLimit[key].(map[string]any)
	if window == nil {
		return 100, nil, ""
	}
	used := int(num(window["used_percent"]))
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	remaining := 100 - used
	var reset *time.Time
	if v := int64(num(window["reset_at"])); v > 0 {
		t := time.Unix(v, 0).UTC()
		reset = &t
	} else if s := int64(num(window["reset_after_seconds"])); s > 0 {
		t := time.Now().UTC().Add(time.Duration(s) * time.Second)
		reset = &t
	}
	var label string
	if secs := int64(num(window["limit_window_seconds"])); secs > 0 {
		label = fmt.Sprintf("%dm", (secs+59)/60)
	}
	return remaining, reset, label
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}
