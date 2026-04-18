package httpapi

import (
	"net/http"

	"github.com/ricki/codexsess/internal/webui"
)

func (s *Server) registerWebRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/accounts", s.handleWebAccounts)
	mux.HandleFunc("/api/accounts/types", s.handleWebAccountTypes)
	mux.HandleFunc("/api/accounts/total", s.handleWebAccountsTotal)
	mux.HandleFunc("/api/accounts/revoked", s.handleDeleteRevokedAccounts)
	mux.HandleFunc("/api/account/use", s.handleWebUseAccount)
	mux.HandleFunc("/api/account/use-api", s.handleWebUseAPIAccount)
	mux.HandleFunc("/api/account/use-cli", s.handleWebUseCLIAccount)
	mux.HandleFunc("/api/account/remove", s.handleWebRemoveAccount)
	mux.HandleFunc("/api/account/import", s.handleWebImportAccount)
	mux.HandleFunc("/api/accounts/backup", s.handleWebBackupAccounts)
	mux.HandleFunc("/api/accounts/export-tokens", s.handleWebExportAccountTokens)
	mux.HandleFunc("/api/accounts/restore", s.handleWebRestoreAccounts)
	mux.HandleFunc("/api/usage/refresh", s.handleWebRefreshUsage)
	mux.HandleFunc("/api/usage/automation", s.handleWebUsageAutomationStatus)
	mux.HandleFunc("/api/system/logs", s.handleWebSystemLogs)
	mux.HandleFunc("/api/settings", s.handleWebSettings)
	mux.HandleFunc("/api/settings/claude-code", s.handleWebClaudeCodeSettings)
	mux.HandleFunc("/api/settings/api-key", s.handleWebUpdateAPIKey)
	mux.HandleFunc("/api/version/check", s.handleWebVersionCheck)
	mux.HandleFunc("/api/model-mappings", s.handleWebModelMappings)
	mux.HandleFunc("/api/logs", s.handleWebLogs)
	mux.HandleFunc("/api/coding/sessions", s.handleWebCodingSessions)
	mux.HandleFunc("/api/coding/messages", s.handleWebCodingMessages)
	mux.HandleFunc("/api/coding/messages/snapshot", s.handleWebCodingMessageSnapshot)
	mux.HandleFunc("/api/coding/status", s.handleWebCodingStatus)
	mux.HandleFunc("/api/coding/runtime/debug", s.handleWebCodingRuntimeDebug)
	mux.HandleFunc("/api/coding/stop", s.handleWebCodingStop)
	mux.HandleFunc("/api/coding/ws", s.handleWebCodingWS)
	mux.HandleFunc("/api/coding/chat", s.handleWebCodingChat)
	mux.HandleFunc("/api/coding/runtime/restart", s.handleWebCodingRuntimeRestart)
	mux.HandleFunc("/api/coding/template-home", s.handleWebCodingTemplateHome)
	mux.HandleFunc("/api/coding/path-suggestions", s.handleWebCodingPathSuggestions)
	mux.HandleFunc("/api/coding/skills", s.handleWebCodingSkills)
	mux.HandleFunc("/api/auth/browser/start", s.handleWebBrowserStart)
	mux.HandleFunc("/api/auth/browser/status", s.handleWebBrowserStatus)
	mux.HandleFunc("/api/auth/browser/cancel", s.handleWebBrowserCancel)
	mux.HandleFunc("/api/auth/browser/callback", s.handleWebBrowserCallback)
	mux.HandleFunc("/api/auth/browser/complete", s.handleWebBrowserComplete)
	mux.HandleFunc("/api/auth/login", s.handleAPIAuthLogin)
	mux.HandleFunc("/auth/callback", s.handleWebBrowserCallback)
	mux.HandleFunc("/auth/login", s.handleWebAuthLogin)
	mux.HandleFunc("/auth/logout", s.handleWebAuthLogout)
	mux.HandleFunc("/api/auth/device/start", s.handleWebDeviceStart)
	mux.HandleFunc("/api/auth/device/poll", s.handleWebDevicePoll)
	mux.HandleFunc("/api/events/log", s.handleWebClientEventLog)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, 200, map[string]any{"ok": true})
	})
	mux.Handle("/", webui.Handler())
}
