package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleAPIAuthJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}

	account, err := s.resolveAPIAccount(r.Context(), "")
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondErr(w, http.StatusNotFound, "account_not_found", err.Error())
		case strings.Contains(msg, "exhausted"):
			respondErr(w, http.StatusTooManyRequests, "quota_exhausted", "target account quota exhausted")
		default:
			respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	authPath := filepath.Join(s.svc.APICodexHome(account.ID), "auth.json")
	content, err := os.ReadFile(authPath)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "internal_error", "failed to load auth.json for active API account")
		return
	}
	if !json.Valid(content) {
		respondErr(w, http.StatusInternalServerError, "internal_error", "invalid auth.json content for active API account")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleAPIUsageStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
		return
	}

	type activeUsageAccount struct {
		ID        string               `json:"id"`
		Email     string               `json:"email"`
		Alias     string               `json:"alias"`
		PlanType  string               `json:"plan_type"`
		ActiveAPI bool                 `json:"active_api"`
		ActiveCLI bool                 `json:"active_cli"`
		Usage     *store.UsageSnapshot `json:"usage,omitempty"`
		Available bool                 `json:"available"`
		Score     int                  `json:"score"`
	}
	type usageStatus struct {
		Object    string              `json:"object"`
		Generated string              `json:"generated_at"`
		APIActive *activeUsageAccount `json:"api_active,omitempty"`
		CLIActive *activeUsageAccount `json:"cli_active,omitempty"`
	}

	accounts, err := s.svc.ListAccounts(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	usageMap, err := s.svc.Store.ListUsageSnapshots(r.Context())
	if err != nil {
		usageMap = map[string]store.UsageSnapshot{}
	}
	cliActiveID, err := s.svc.ActiveCLIAccountID(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	findByID := func(id string) (store.Account, bool) {
		needle := strings.TrimSpace(id)
		if needle == "" {
			return store.Account{}, false
		}
		for _, account := range accounts {
			if strings.TrimSpace(account.ID) == needle {
				return account, true
			}
		}
		return store.Account{}, false
	}
	build := func(account store.Account, isAPI bool, isCLI bool) *activeUsageAccount {
		if strings.TrimSpace(account.ID) == "" {
			return nil
		}
		item := &activeUsageAccount{
			ID:        account.ID,
			Email:     account.Email,
			Alias:     account.Alias,
			PlanType:  account.PlanType,
			ActiveAPI: isAPI,
			ActiveCLI: isCLI,
		}
		if usage, ok := usageMap[account.ID]; ok {
			ux := usage
			item.Usage = &ux
			item.Available = usageAvailable(usage)
			item.Score = usageScore(usage)
		}
		return item
	}

	var apiActive *activeUsageAccount
	for _, account := range accounts {
		if account.Active {
			apiActive = build(account, true, strings.TrimSpace(cliActiveID) != "" && strings.TrimSpace(cliActiveID) == strings.TrimSpace(account.ID))
			break
		}
	}

	var cliActive *activeUsageAccount
	if account, ok := findByID(cliActiveID); ok {
		cliActive = build(account, account.Active, true)
	}

	respondJSON(w, http.StatusOK, usageStatus{
		Object:    "usage_status",
		Generated: time.Now().UTC().Format(time.RFC3339),
		APIActive: apiActive,
		CLIActive: cliActive,
	})
}
