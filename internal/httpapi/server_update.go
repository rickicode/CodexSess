package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type updateInfo struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	ReleaseURL      string `json:"release_url"`
	LatestChangelog string `json:"latest_changelog"`
	UpdateAvailable bool   `json:"update_available"`
	CheckedAt       string `json:"checked_at"`
	CheckError      string `json:"check_error"`
}

func (s *Server) handleWebVersionCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	info := s.getUpdateInfo(r.Context(), true)
	respondJSON(w, 200, map[string]any{
		"ok":                 true,
		"app_version":        info.CurrentVersion,
		"latest_version":     info.LatestVersion,
		"release_url":        info.ReleaseURL,
		"latest_changelog":   info.LatestChangelog,
		"update_available":   info.UpdateAvailable,
		"update_checked_at":  info.CheckedAt,
		"update_check_error": info.CheckError,
	})
}

func (s *Server) getUpdateInfo(ctx context.Context, force bool) updateInfo {
	current := normalizeVersionString(s.appVersion)
	if current == "" {
		current = "dev"
	}
	now := time.Now().UTC()
	const ttl = 30 * time.Minute

	s.updateMu.Lock()
	stale := s.updateCheckedAt.IsZero() || now.Sub(s.updateCheckedAt) > ttl
	needCheck := force || stale
	s.updateMu.Unlock()

	var latest, releaseURL, latestChangelog, checkErrMsg string
	if needCheck {
		checkCtx, cancel := context.WithTimeout(ctx, 1800*time.Millisecond)
		latestV, releaseURLV, latestChangelogV, err := fetchLatestReleaseVersion(checkCtx)
		cancel()
		latest = normalizeVersionString(latestV)
		releaseURL = strings.TrimSpace(releaseURLV)
		latestChangelog = strings.TrimSpace(latestChangelogV)
		if err != nil {
			checkErrMsg = err.Error()
		}
	}

	s.updateMu.Lock()
	if needCheck {
		s.updateCheckedAt = now
		s.updateLatestVersion = latest
		s.updateReleaseURL = releaseURL
		s.updateCheckErrMessage = checkErrMsg
		if len(latestChangelog) > 20000 {
			latestChangelog = latestChangelog[:20000]
		}
		s.updateLatestChangelog = latestChangelog
		s.updateAvailable = compareSemver(s.updateLatestVersion, current) > 0
	}
	info := updateInfo{
		CurrentVersion:  current,
		LatestVersion:   s.updateLatestVersion,
		ReleaseURL:      s.updateReleaseURL,
		LatestChangelog: s.updateLatestChangelog,
		UpdateAvailable: s.updateAvailable,
		CheckedAt:       s.updateCheckedAt.Format(time.RFC3339),
		CheckError:      s.updateCheckErrMessage,
	}
	s.updateMu.Unlock()
	return info
}

func fetchLatestReleaseVersion(ctx context.Context) (string, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/rickicode/CodexSess/releases/latest", nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "codexsess-update-checker")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", "", fmt.Errorf("github latest release check failed: status %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", "", err
	}
	return strings.TrimSpace(payload.TagName), strings.TrimSpace(payload.HTMLURL), strings.TrimSpace(payload.Body), nil
}

func normalizeVersionString(v string) string {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(strings.ToLower(s), "v")
	if s == "" {
		return ""
	}
	return s
}

func compareSemver(a, b string) int {
	parse := func(v string) ([3]int, bool) {
		var out [3]int
		clean := normalizeVersionString(v)
		if clean == "" || clean == "dev" {
			return out, false
		}
		re := regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?`)
		m := re.FindStringSubmatch(clean)
		if len(m) < 2 {
			return out, false
		}
		out[0], _ = strconv.Atoi(m[1])
		if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
			out[1], _ = strconv.Atoi(m[2])
		}
		if len(m) > 3 && strings.TrimSpace(m[3]) != "" {
			out[2], _ = strconv.Atoi(m[3])
		}
		return out, true
	}
	aa, aok := parse(a)
	bb, bok := parse(b)
	if aok && !bok {
		return 1
	}
	if !aok && bok {
		return -1
	}
	for i := 0; i < 3; i++ {
		if aa[i] > bb[i] {
			return 1
		}
		if aa[i] < bb[i] {
			return -1
		}
	}
	return 0
}
