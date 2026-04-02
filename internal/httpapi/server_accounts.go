package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleWebAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}

	qPage := r.URL.Query().Get("page")
	qLimit := r.URL.Query().Get("limit")

	filter := store.AccountFilter{
		Query:    r.URL.Query().Get("q"),
		PlanType: r.URL.Query().Get("type"),
		Status:   r.URL.Query().Get("status"),
		Usage:    r.URL.Query().Get("usage"),
	}

	var accounts []store.Account
	var totalFiltered int
	var err error

	page, _ := strconv.Atoi(qPage)
	limit, _ := strconv.Atoi(qLimit)

	if qPage != "" || qLimit != "" {
		accounts, totalFiltered, err = s.svc.ListAccountsPaginated(r.Context(), page, limit, filter)
	} else {
		accounts, err = s.svc.ListAccounts(r.Context())
		totalFiltered = len(accounts)
	}

	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	type webAccount struct {
		ID            string               `json:"id"`
		Email         string               `json:"email"`
		Alias         string               `json:"alias"`
		PlanType      string               `json:"plan_type"`
		Active        bool                 `json:"active"`
		ActiveAPI     bool                 `json:"active_api"`
		ActiveCLI     bool                 `json:"active_cli"`
		Usage         *store.UsageSnapshot `json:"usage,omitempty"`
		Revoked       bool                 `json:"revoked"`
		RevokedReason string               `json:"revoked_reason,omitempty"`
	}
	resp := struct {
		Accounts           []webAccount `json:"accounts"`
		TotalFiltered      int          `json:"total_filtered"`
		InvalidAccounts    int          `json:"invalid_accounts_total"`
		RevokedAccounts    int          `json:"revoked_accounts_total"`
		ActiveAPIAccountID string       `json:"active_api_account_id,omitempty"`
		ActiveAPIEmail     string       `json:"active_api_account_email,omitempty"`
		ActiveAPIInvalid   bool         `json:"active_api_invalid"`
		ActiveCLIAccountID string       `json:"active_cli_account_id,omitempty"`
		ActiveCLIEmail     string       `json:"active_cli_account_email,omitempty"`
		ActiveCLIInvalid   bool         `json:"active_cli_invalid"`
	}{
		TotalFiltered: totalFiltered,
	}
	usageMap, err := s.svc.Store.ListUsageSnapshots(r.Context())
	if err != nil {
		usageMap = map[string]store.UsageSnapshot{}
	}
	cliActiveID, err := s.svc.ActiveCLIAccountID(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}

	activeAPIID := ""
	if activeAPI, activeErr := s.svc.Store.ActiveAccount(r.Context()); activeErr == nil {
		activeAPIID = strings.TrimSpace(activeAPI.ID)
		resp.ActiveAPIEmail = strings.TrimSpace(activeAPI.Email)
	}
	resp.ActiveAPIAccountID = activeAPIID
	resp.ActiveCLIAccountID = strings.TrimSpace(cliActiveID)
	if strings.TrimSpace(cliActiveID) != "" {
		if activeCLI, activeErr := s.svc.Store.FindAccountBySelector(r.Context(), cliActiveID); activeErr == nil {
			resp.ActiveCLIEmail = strings.TrimSpace(activeCLI.Email)
		}
	}
	apiRevoked := false
	cliRevoked := false
	if u, ok := usageMap[activeAPIID]; ok && usageErrorLooksRevoked(u.LastError) {
		apiRevoked = true
	}
	if u, ok := usageMap[strings.TrimSpace(cliActiveID)]; ok && usageErrorLooksRevoked(u.LastError) {
		cliRevoked = true
	}
	if apiRevoked || cliRevoked {
		if apiRevoked {
			if best, ok := s.findBestUsageAccount(r.Context(), activeAPIID); ok {
				_, _ = s.svc.UseAccountAPI(service.WithAPISwitchReason(r.Context(), "revoked"), best.ID)
			}
		}
		if cliRevoked {
			if best, ok := s.findBestUsageAccount(r.Context(), strings.TrimSpace(cliActiveID)); ok {
				_, _ = s.svc.UseAccountCLI(service.WithCLISwitchReason(r.Context(), "revoked"), best.ID)
			}
		}
		if qPage != "" || qLimit != "" {
			var refreshedTotal int
			accounts, refreshedTotal, err = s.svc.ListAccountsPaginated(r.Context(), page, limit, filter)
			_ = refreshedTotal
		} else {
			accounts, err = s.svc.ListAccounts(r.Context())
		}
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		usageMap, err = s.svc.Store.ListUsageSnapshots(r.Context())
		if err != nil {
			usageMap = map[string]store.UsageSnapshot{}
		}
		cliActiveID, err = s.svc.ActiveCLIAccountID(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		activeAPIID = ""
		if activeAPI, activeErr := s.svc.Store.ActiveAccount(r.Context()); activeErr == nil {
			activeAPIID = strings.TrimSpace(activeAPI.ID)
			resp.ActiveAPIEmail = strings.TrimSpace(activeAPI.Email)
		}
		resp.ActiveAPIAccountID = activeAPIID
		resp.ActiveCLIAccountID = strings.TrimSpace(cliActiveID)
		resp.ActiveCLIEmail = ""
		if strings.TrimSpace(cliActiveID) != "" {
			if activeCLI, activeErr := s.svc.Store.FindAccountBySelector(r.Context(), cliActiveID); activeErr == nil {
				resp.ActiveCLIEmail = strings.TrimSpace(activeCLI.Email)
			}
		}
	}

	if invalidTotal, invalidErr := s.svc.Store.CountInvalidAccounts(r.Context()); invalidErr == nil {
		resp.InvalidAccounts = invalidTotal
	}
	if revokedTotal, revokedErr := s.svc.Store.CountRevokedAccounts(r.Context()); revokedErr == nil {
		resp.RevokedAccounts = revokedTotal
	}
	if activeAPIID != "" {
		if u, ok := usageMap[activeAPIID]; ok && usageErrorLooksRevoked(u.LastError) {
			resp.ActiveAPIInvalid = true
		}
		if acc, findErr := s.svc.Store.FindAccountBySelector(r.Context(), activeAPIID); findErr == nil && acc.Revoked {
			resp.ActiveAPIInvalid = true
		}
	}
	if strings.TrimSpace(cliActiveID) != "" {
		cliID := strings.TrimSpace(cliActiveID)
		if u, ok := usageMap[cliID]; ok && usageErrorLooksRevoked(u.LastError) {
			resp.ActiveCLIInvalid = true
		}
		if acc, findErr := s.svc.Store.FindAccountBySelector(r.Context(), cliID); findErr == nil && acc.Revoked {
			resp.ActiveCLIInvalid = true
		}
	}

	for _, a := range accounts {
		isAPI := a.Active
		isCLI := cliActiveID != "" && a.ID == cliActiveID
		item := webAccount{
			ID:        a.ID,
			Email:     a.Email,
			Alias:     a.Alias,
			PlanType:  a.PlanType,
			Active:    isAPI && isCLI,
			ActiveAPI: isAPI,
			ActiveCLI: isCLI,
		}
		item.Revoked = a.Revoked
		if u, ok := usageMap[a.ID]; ok {
			ux := u
			item.Usage = &ux
			if usageErrorLooksRevoked(ux.LastError) {
				item.Revoked = true
				item.RevokedReason = strings.TrimSpace(ux.LastError)
			} else if a.Revoked {
				item.RevokedReason = "Marked as revoked in database"
			}
		} else if a.Revoked {
			item.RevokedReason = "Marked as revoked in database"
		}
		resp.Accounts = append(resp.Accounts, item)
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleWebAccountTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	accounts, err := s.svc.ListAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	counts := map[string]int{}
	for _, account := range accounts {
		planType := strings.ToLower(strings.TrimSpace(account.PlanType))
		if planType == "" {
			continue
		}
		counts[planType]++
	}
	accountTypes := make([]string, 0, len(counts))
	for planType := range counts {
		accountTypes = append(accountTypes, planType)
	}
	sort.Strings(accountTypes)
	respondJSON(w, 200, map[string]any{
		"account_types":       accountTypes,
		"account_type_counts": counts,
	})
}

func (s *Server) handleWebAccountsTotal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	total, err := s.svc.CountAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]int{"total": total})
}

func (s *Server) handleDeleteRevokedAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	n, err := s.svc.DeleteRevokedAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"deleted": n})
}

func (s *Server) handleWebUseAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(r.Context(), "manual"), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	cliErr := ""
	if _, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(r.Context(), "manual"), req.Selector); err != nil {
		cliErr = err.Error()
	}
	respondJSON(w, 200, map[string]any{
		"ok":        true,
		"cli_ok":    cliErr == "",
		"cli_error": cliErr,
		"account":   map[string]any{"id": acc.ID, "email": acc.Email},
	})
}

func (s *Server) handleWebUseAPIAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccountAPI(service.WithAPISwitchReason(r.Context(), "manual"), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebUseCLIAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccountCLI(service.WithCLISwitchReason(r.Context(), "manual"), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebRemoveAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if err := s.svc.RemoveAccount(r.Context(), req.Selector); err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebImportAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Path  string `json:"path"`
		Alias string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.ImportTokenJSON(r.Context(), req.Path, req.Alias)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebBackupAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	payload, err := s.svc.ExportAccountsBackup(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}

	name := "codexsess-accounts-backup-" + time.Now().UTC().Format("20060102-150405") + ".json"
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	_, _ = w.Write(b)
}

func (s *Server) handleWebRestoreAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	var payload service.AccountsBackupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.RestoreAccountsBackup(r.Context(), payload)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok":       true,
		"restored": result.Restored,
		"skipped":  result.Skipped,
	})
}

func (s *Server) handleWebRefreshUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
		All      bool   `json:"all"`
		Source   string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	source := strings.TrimSpace(strings.ToLower(req.Source))
	switch source {
	case "auto", "manual":
	default:
		source = "manual"
	}
	if req.All {
		respondErr(w, 400, "bad_request", "bulk usage refresh is disabled; refresh per account only")
		return
	}
	if strings.TrimSpace(req.Selector) == "" {
		respondErr(w, 400, "bad_request", "selector required")
		return
	}
	u, err := s.svc.RefreshUsage(r.Context(), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	msg := "Manual usage refresh"
	if source == "auto" {
		msg = "Automatic usage refresh"
	}
	s.svc.AddSystemLog(r.Context(), "usage_refresh", msg, map[string]any{
		"all":      false,
		"selector": strings.TrimSpace(req.Selector),
		"hourly":   u.HourlyPct,
		"weekly":   u.WeeklyPct,
		"source":   source,
	})
	respondJSON(w, 200, map[string]any{"ok": true, "usage": u})
}

func (s *Server) handleWebUsageAutomationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	enabled, threshold, intervalMinutes := s.currentUsageSchedulerState()
	refreshTimeout, switchTimeout := s.currentUsageSchedulerTimeouts()
	status := s.getCLISwitchStatus()
	respondJSON(w, http.StatusOK, map[string]any{
		"usage_scheduler_enabled":          enabled,
		"usage_auto_switch_threshold":      threshold,
		"usage_scheduler_interval_minutes": intervalMinutes,
		"usage_refresh_timeout_seconds":    int(refreshTimeout.Seconds()),
		"usage_switch_timeout_seconds":     int(switchTimeout.Seconds()),
		"retry_cooldown_seconds":           0,
		"active_check_interval_seconds":    int(activeUsageCheckInterval.Seconds()),
		"all_check_interval_seconds":       intervalMinutes * 60,
		"last_all_attempt_at":              s.lastAllAttemptAt.Load(),
		"last_all_failure_at":              s.lastAllFailureAt.Load(),
		"last_active_check_at":             s.lastActiveCheckAt.Load(),
		"last_all_check_at":                s.lastAllCheckAt.Load(),
		"last_cli_switch_at":               status.At,
		"last_cli_switch_from":             status.From,
		"last_cli_switch_to":               status.To,
		"last_cli_switch_reason":           status.Reason,
		"last_cli_switch_strategy":         status.Strategy,
		"last_cli_switch_error":            status.Error,
		"last_cli_switch_candidates":       status.Candidates,
	})
}

func (s *Server) handleWebSystemLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				if v > 2000 {
					v = 2000
				}
				limit = v
			}
		}
		entries, err := s.svc.Store.ListSystemLogs(r.Context(), limit)
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		total, _ := s.svc.Store.CountSystemLogs(r.Context())
		items := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			items = append(items, map[string]any{
				"id":         e.ID,
				"kind":       e.Kind,
				"message":    e.Message,
				"meta_json":  e.MetaJSON,
				"created_at": e.CreatedAt.Format(time.RFC3339),
			})
		}
		respondJSON(w, 200, map[string]any{
			"logs":  items,
			"total": total,
		})
		return
	case http.MethodDelete:
		if err := s.svc.Store.ClearSystemLogs(r.Context()); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}
