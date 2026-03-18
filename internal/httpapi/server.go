package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/trafficlog"
	"github.com/ricki/codexsess/internal/webui"
)

type Server struct {
	svc               *service.Service
	apiKey            string
	bindAddr          string
	adminUsername     string
	adminPasswordHash string
	traffic           *trafficlog.Logger
	mu                sync.RWMutex
}

func New(svc *service.Service, bindAddr string, apiKey string, adminUsername string, adminPasswordHash string, traffic *trafficlog.Logger) *Server {
	return &Server{
		svc:               svc,
		bindAddr:          bindAddr,
		apiKey:            apiKey,
		adminUsername:     strings.TrimSpace(adminUsername),
		adminPasswordHash: strings.TrimSpace(adminPasswordHash),
		traffic:           traffic,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts", s.handleWebAccounts)
	mux.HandleFunc("/api/account/use", s.handleWebUseAccount)
	mux.HandleFunc("/api/account/use-api", s.handleWebUseAPIAccount)
	mux.HandleFunc("/api/account/use-cli", s.handleWebUseCLIAccount)
	mux.HandleFunc("/api/account/remove", s.handleWebRemoveAccount)
	mux.HandleFunc("/api/account/import", s.handleWebImportAccount)
	mux.HandleFunc("/api/usage/refresh", s.handleWebRefreshUsage)
	mux.HandleFunc("/api/settings", s.handleWebSettings)
	mux.HandleFunc("/api/settings/api-key", s.handleWebUpdateAPIKey)
	mux.HandleFunc("/api/model-mappings", s.handleWebModelMappings)
	mux.HandleFunc("/api/logs", s.handleWebLogs)
	mux.HandleFunc("/api/auth/browser/start", s.handleWebBrowserStart)
	mux.HandleFunc("/api/auth/browser/cancel", s.handleWebBrowserCancel)
	mux.HandleFunc("/api/auth/browser/callback", s.handleWebBrowserCallback)
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
	mux.HandleFunc("/v1/models", s.withTrafficLog("openai", s.handleModels))
	mux.HandleFunc("/v1", s.withTrafficLog("openai", s.handleOpenAIV1Root))
	mux.HandleFunc("/v1/chat/completions", s.withTrafficLog("openai", s.handleChatCompletions))
	mux.HandleFunc("/v1/responses", s.withTrafficLog("openai", s.handleResponses))
	mux.HandleFunc("/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.HandleFunc("/claude/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.Handle("/", webui.Handler())
	handler := s.withAccessLog(withCORS(s.withManagementAuth(mux)))
	srv := &http.Server{Addr: s.bindAddr, Handler: handler}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &accessLogRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		next.ServeHTTP(rec, r)

		remote := strings.TrimSpace(r.RemoteAddr)
		if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
			remote = host
		}
		path := strings.TrimSpace(r.URL.Path)
		if path == "" {
			path = "/"
		}
		accountHint := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Codex-Account")), "-")
		apiAuth := classifyAuthSource(r)
		ua := firstNonEmpty(truncateForLog(strings.TrimSpace(r.UserAgent()), 72), "-")
		log.Printf(
			"[ACCESS] %-7s %-38s status=%3d latency=%4dms from=%s kind=%s auth=%s account=%s ua=%s",
			strings.ToUpper(strings.TrimSpace(r.Method)),
			path,
			rec.status,
			time.Since(start).Milliseconds(),
			firstNonEmpty(remote, "-"),
			requestKind(path),
			apiAuth,
			accountHint,
			ua,
		)
	})
}

func requestKind(path string) string {
	p := strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(p, "/v1"), strings.HasPrefix(p, "/claude/v1"):
		return "proxy-api"
	case strings.HasPrefix(p, "/api/"):
		return "web-api"
	case strings.HasPrefix(p, "/auth/"):
		return "auth"
	case strings.HasPrefix(p, "/assets/"), strings.HasPrefix(p, "/sounds/"), p == "/favicon.svg":
		return "asset"
	default:
		return "web-ui"
	}
}

func classifyAuthSource(r *http.Request) string {
	bearer := strings.TrimSpace(BearerToken(r.Header.Get("Authorization")))
	xAPIKey := strings.TrimSpace(r.Header.Get("x-api-key"))
	switch {
	case bearer != "":
		return "bearer:" + maskSecret(bearer)
	case xAPIKey != "":
		return "x-api-key:" + maskSecret(xAPIKey)
	default:
		return "none"
	}
}

func maskSecret(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return "-"
	}
	if len(s) <= 6 {
		return s[:1] + "***"
	}
	return s[:3] + "..." + s[len(s)-2:]
}

func ptrString(v string) *string {
	return &v
}

type accessLogRecorder struct {
	http.ResponseWriter
	status int
}

func (r *accessLogRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *accessLogRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withManagementAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		target := "/auth/login"
		path := strings.TrimSpace(r.URL.Path)
		if path != "" && path != "/" {
			target += "?next=" + url.QueryEscape(path)
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
}

func (s *Server) isPublicPath(path string) bool {
	p := strings.TrimSpace(path)
	switch {
	case p == "/healthz":
		return true
	case strings.HasPrefix(p, "/v1"):
		return true
	case strings.HasPrefix(p, "/claude/v1"):
		return true
	case p == "/auth/callback":
		return true
	case p == "/api/auth/browser/callback":
		return true
	case p == "/auth/login":
		return true
	case p == "/api/auth/login":
		return true
	}
	return false
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	ck, err := r.Cookie("codexsess_auth")
	if err != nil || strings.TrimSpace(ck.Value) == "" {
		return false
	}
	return s.validateAuthCookie(ck.Value)
}

func (s *Server) validateAuthCookie(raw string) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	parts := strings.Split(string(b), "|")
	if len(parts) != 3 {
		return false
	}
	username := strings.TrimSpace(parts[0])
	expRaw := strings.TrimSpace(parts[1])
	sig := strings.TrimSpace(parts[2])
	if username == "" || expRaw == "" || sig == "" {
		return false
	}
	expUnix, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expUnix {
		return false
	}
	if username != s.adminUsername {
		return false
	}
	expect := s.cookieSignature(username, expRaw)
	return sig == expect
}

func (s *Server) cookieSignature(username, expRaw string) string {
	sum := sha256.Sum256([]byte(username + "|" + expRaw + "|" + s.adminPasswordHash))
	return hex.EncodeToString(sum[:])
}

func (s *Server) issueAuthCookieValue() string {
	expRaw := strconv.FormatInt(time.Now().Add(30*24*time.Hour).Unix(), 10)
	sig := s.cookieSignature(s.adminUsername, expRaw)
	payload := s.adminUsername + "|" + expRaw + "|" + sig
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func (s *Server) setAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "codexsess_auth",
		Value:    s.issueAuthCookieValue(),
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "codexsess_auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) verifyAdminCredentials(username, password string) bool {
	user := strings.TrimSpace(username)
	pass := strings.TrimSpace(password)
	if user == "" || pass == "" {
		return false
	}
	if user != s.adminUsername {
		return false
	}
	return config.VerifyPassword(pass, s.adminPasswordHash)
}

func (s *Server) handleAPIAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if !s.verifyAdminCredentials(req.Username, req.Password) {
		respondErr(w, http.StatusUnauthorized, "unauthorized", "invalid username or password")
		return
	}
	s.setAuthCookie(w)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		nextPath := strings.TrimSpace(r.URL.Query().Get("next"))
		if nextPath == "" {
			nextPath = "/"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>CodexSess Login</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@500;600&family=IBM+Plex+Sans:wght@400;500;600;700&display=swap" rel="stylesheet" />
<style>
:root{
  --bg:#0f1115;
  --panel:#171717;
  --border:rgba(148,163,184,.23);
  --text:#f8fafc;
  --muted:#94a3b8;
  --primary:#00d4aa;
}
*{box-sizing:border-box}
body{
  margin:0;
  min-height:100vh;
  display:grid;
  place-items:center;
  background:radial-gradient(1200px 500px at 10% -20%,rgba(0,212,170,.10),transparent),var(--bg);
  color:var(--text);
  font-family:"IBM Plex Sans",sans-serif;
}
.login-shell{
  width:min(420px,92vw);
  background:var(--panel);
  border:1px solid var(--border);
  border-radius:14px;
  padding:18px;
  display:grid;
  gap:14px;
}
.brand{
  display:grid;
  gap:2px;
  justify-items:center;
  text-align:center;
}
.brand strong{
  font-family:"IBM Plex Mono",monospace;
  letter-spacing:.02em;
  font-size:20px;
}
.brand span{
  color:var(--muted);
  font-size:12px;
}
form{
  display:grid;
  gap:10px;
}
label{
  color:var(--muted);
  font-size:12px;
}
input{
  width:100%;
  border:1px solid var(--border);
  border-radius:9px;
  padding:10px 11px;
  background:#131313;
  color:var(--text);
  outline:none;
}
input:focus{
  border-color:rgba(0,212,170,.7);
  box-shadow:0 0 0 2px rgba(0,212,170,.16);
}
button{
  margin-top:4px;
  border:none;
  border-radius:10px;
  padding:10px 12px;
  background:var(--primary);
  color:#02251f;
  font-weight:700;
  cursor:pointer;
  display:inline-flex;
  align-items:center;
  justify-content:center;
  gap:8px;
}
button[disabled]{
  opacity:.8;
  cursor:wait;
}
.spin{
  width:14px;
  height:14px;
  border-radius:50%;
  border:2px solid rgba(2,37,31,.25);
  border-top-color:#02251f;
  animation:spin .8s linear infinite;
}
@keyframes spin{
  to{transform:rotate(360deg)}
}
.foot{
  color:var(--muted);
  font-size:12px;
  text-align:center;
}
.err{
  margin:0;
  min-height:20px;
  border:1px solid rgba(239,68,68,.35);
  background:rgba(239,68,68,.12);
  color:#fecaca;
  border-radius:9px;
  padding:8px 10px;
  font-size:12px;
}
.err[hidden]{
  display:none;
}
.foot a{
  color:#7fead6;
  text-decoration:none;
}
.foot a:hover{
  text-decoration:underline;
}
</style>
</head>
<body>
<section class="login-shell">
  <div class="brand">
    <strong>CodexSess</strong>
    <span>Codex Account Management</span>
  </div>
  <form id="loginForm" method="post" action="/auth/login">
    <input type="hidden" name="next" value="` + templateEscape(nextPath) + `" />
    <label for="username">Username</label>
    <input id="username" name="username" autocomplete="username" placeholder="admin" />
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password" placeholder="Enter password" />
    <p id="loginError" class="err" role="alert" aria-live="polite" hidden></p>
    <button id="loginButton" type="submit">Sign In</button>
  </form>
  <div class="foot">
    Session will be remembered for 30 days.<br/>
    <a href="https://hijinetwork.net" target="_blank" rel="noopener noreferrer">Powered by HIJINETWORK</a>
  </div>
</section>
<script>
(() => {
  const form = document.getElementById("loginForm");
  const btn = document.getElementById("loginButton");
  const err = document.getElementById("loginError");
  if (!form || !btn) return;
  const defaultButtonHTML = 'Sign In';
  const loadingButtonHTML = '<span class="spin" aria-hidden="true"></span><span>Signing in...</span>';
  const setError = (message) => {
    if (!err) return;
    if (!message) {
      err.hidden = true;
      err.textContent = "";
      return;
    }
    err.hidden = false;
    err.textContent = message;
  };
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (btn.disabled) return;
    setError("");
    btn.disabled = true;
    btn.innerHTML = loadingButtonHTML;
    const fd = new FormData(form);
    const username = String(fd.get("username") || "");
    const password = String(fd.get("password") || "");
    const next = String(fd.get("next") || "/");
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Accept": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ username, password }),
      });
      if (res.ok) {
        window.location.assign(next || "/");
        return;
      }
      let msg = "Invalid username or password";
      try {
        const body = await res.json();
        if (body && body.error && typeof body.error.message === "string" && body.error.message.trim()) {
          msg = body.error.message;
        }
      } catch (_) {}
      setError(msg);
    } catch (_) {
      setError("Unable to sign in. Please try again.");
    } finally {
      btn.disabled = false;
      btn.innerHTML = defaultButtonHTML;
    }
  });
})();
</script>
</body>
</html>`))
		return
	}
	if r.Method != http.MethodPost {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if err := r.ParseForm(); err != nil {
		respondErr(w, http.StatusBadRequest, "bad_request", "invalid form")
		return
	}
	username := r.Form.Get("username")
	password := r.Form.Get("password")
	nextPath := strings.TrimSpace(r.Form.Get("next"))
	if nextPath == "" {
		nextPath = "/"
	}
	if !s.verifyAdminCredentials(username, password) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid username or password"))
		return
	}
	s.setAuthCookie(w)
	http.Redirect(w, r, nextPath, http.StatusFound)
}

func (s *Server) handleWebAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		respondErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	s.clearAuthCookie(w)
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

func templateEscape(v string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(v)
}

func (s *Server) handleWebAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	accounts, err := s.svc.ListAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	type webAccount struct {
		ID        string               `json:"id"`
		Email     string               `json:"email"`
		Alias     string               `json:"alias"`
		PlanType  string               `json:"plan_type"`
		Active    bool                 `json:"active"`
		ActiveAPI bool                 `json:"active_api"`
		ActiveCLI bool                 `json:"active_cli"`
		Usage     *store.UsageSnapshot `json:"usage,omitempty"`
	}
	resp := struct {
		Accounts []webAccount `json:"accounts"`
	}{}
	usageMap, err := s.svc.Store.ListUsageSnapshots(r.Context())
	if err != nil {
		usageMap = map[string]store.UsageSnapshot{}
	}
	cliActiveID, err := s.svc.ActiveCLIAccountID(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
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
		if u, ok := usageMap[a.ID]; ok {
			ux := u
			item.Usage = &ux
		}
		resp.Accounts = append(resp.Accounts, item)
	}
	respondJSON(w, 200, resp)
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
	acc, err := s.svc.UseAccount(r.Context(), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
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
	acc, err := s.svc.UseAccountAPI(r.Context(), req.Selector)
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
	acc, err := s.svc.UseAccountCLI(r.Context(), req.Selector)
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

func (s *Server) handleWebRefreshUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
		All      bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if req.All {
		accounts, err := s.svc.ListAccounts(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		if len(accounts) == 0 {
			respondJSON(w, 200, map[string]any{"ok": true, "refreshed": 0, "total": 0})
			return
		}
		workerCount := 4
		if len(accounts) < workerCount {
			workerCount = len(accounts)
		}
		jobs := make(chan string, len(accounts))
		results := make(chan bool, len(accounts))
		var wg sync.WaitGroup
		for i := 0; i < workerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range jobs {
					_, err := s.svc.RefreshUsage(r.Context(), id)
					results <- err == nil
				}
			}()
		}
		for _, a := range accounts {
			jobs <- a.ID
		}
		close(jobs)
		wg.Wait()
		close(results)
		ok := 0
		for v := range results {
			if v {
				ok++
			}
		}
		respondJSON(w, 200, map[string]any{"ok": true, "refreshed": ok, "total": len(accounts)})
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
	respondJSON(w, 200, map[string]any{"ok": true, "usage": u})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	now := time.Now().Unix()
	available := codexAvailableModels()
	data := make([]ModelInfo, 0, len(available))
	for _, id := range available {
		data = append(data, ModelInfo{ID: id, Object: "model", Created: now, OwnedBy: "codexsess"})
	}
	resp := ModelsResponse{
		Object: "list",
		Data:   data,
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleOpenAIV1Root(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleModels(w, r)
		return
	case http.MethodPost:
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON body")
			return
		}
		if _, ok := anyBody["messages"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleChatCompletions(w, r)
			return
		}
		if _, ok := anyBody["input"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleResponses(w, r)
			return
		}
		respondErr(w, 400, "bad_request", "unsupported /v1 payload, use /v1/chat/completions or /v1/responses")
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	prompt := promptFromMessagesWithTools(req.Messages, req.Tools, req.ToolChoice)
	account, _, err := s.svc.ResolveForRequest(r.Context(), selector)
	if err != nil {
		respondErr(w, 404, "account_not_found", err.Error())
		return
	}
	setResolvedAccountHeaders(w, account)
	usage, usageErr := s.svc.Store.GetUsage(r.Context(), account.ID)
	if usageErr != nil {
		if snap, err := s.svc.RefreshUsage(r.Context(), account.ID); err == nil {
			usage = snap
			usageErr = nil
		}
	}
	if usageErr == nil {
		if usage.HourlyPct <= 0 || usage.WeeklyPct <= 0 {
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
			return
		}
	}
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    req.Stream,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		res, err := s.svc.Codex.StreamChat(r.Context(), account.CodexHome, req.Model, prompt, func(evt provider.ChatEvent) error {
			chunk := ChatCompletionsChunk{
				ID:      "chatcmpl-" + reqID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   req.Model,
				Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: evt.Text}}},
			}
			b, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			return nil
		})
		if err != nil {
			status = 500
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`"}}`)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		final := ChatCompletionsChunk{
			ID:      "chatcmpl-" + reqID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{}, FinishReason: ptrString("stop")}},
			Usage:   &Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
		}
		b, _ := json.Marshal(final)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), account.CodexHome, req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	toolCalls, hasToolCalls := parseToolCallsFromText(res.Text, req.Tools)
	choice := ChatChoice{
		Index:        0,
		Message:      ChatMessage{Role: "assistant", Content: res.Text},
		FinishReason: "stop",
	}
	if hasToolCalls {
		choice.Message = ChatMessage{
			Role:      "assistant",
			Content:   "",
			ToolCalls: toolCalls,
		}
		choice.FinishReason = "tool_calls"
	}
	resp := ChatCompletionsResponse{
		ID:      "chatcmpl-" + reqID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{choice},
		Usage:   Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	inputText := extractResponsesInput(req.Input)
	if strings.TrimSpace(inputText) == "" {
		respondErr(w, 400, "bad_request", "input is required")
		return
	}
	account, _, err := s.svc.ResolveForRequest(r.Context(), selector)
	if err != nil {
		respondErr(w, 404, "account_not_found", err.Error())
		return
	}
	setResolvedAccountHeaders(w, account)
	usage, usageErr := s.svc.Store.GetUsage(r.Context(), account.ID)
	if usageErr != nil {
		if snap, err := s.svc.RefreshUsage(r.Context(), account.ID); err == nil {
			usage = snap
			usageErr = nil
		}
	}
	if usageErr == nil {
		if usage.HourlyPct <= 0 || usage.WeeklyPct <= 0 {
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
			return
		}
	}
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    false,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		createdEvent := map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     reqID,
				"object": "response",
				"model":  req.Model,
				"status": "in_progress",
			},
		}
		writeSSE(w, "response.created", createdEvent)
		flusher.Flush()

		result, err := s.svc.Codex.StreamChat(r.Context(), account.CodexHome, req.Model, inputText, func(evt provider.ChatEvent) error {
			deltaEvent := map[string]any{
				"type":        "response.output_text.delta",
				"response_id": reqID,
				"delta":       evt.Text,
			}
			writeSSE(w, "response.output_text.delta", deltaEvent)
			flusher.Flush()
			return nil
		})
		if err != nil {
			status = 500
			writeSSE(w, "error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "upstream_error",
					"message": err.Error(),
				},
			})
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		completedEvent := map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     reqID,
				"object": "response",
				"model":  req.Model,
				"status": "completed",
				"output": []map[string]any{
					{
						"type":   "message",
						"id":     "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
						"status": "completed",
						"role":   "assistant",
						"content": []map[string]any{
							{"type": "output_text", "text": result.Text},
						},
					},
				},
				"usage": map[string]any{
					"input_tokens":  result.InputTokens,
					"output_tokens": result.OutputTokens,
					"total_tokens":  result.InputTokens + result.OutputTokens,
				},
			},
		}
		writeSSE(w, "response.completed", completedEvent)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	result, err := s.svc.Codex.Chat(r.Context(), account.CodexHome, req.Model, inputText)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	resp := ResponsesResponse{
		ID:     reqID,
		Object: "response",
		Model:  req.Model,
		Output: []ResponsesItem{
			{
				Type:   "message",
				ID:     "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				Status: "completed",
				Role:   "assistant",
				Content: []ResponsesText{
					{Type: "output_text", Text: result.Text},
				},
			},
		},
		Usage: ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		},
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ClaudeMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	prompt := promptFromClaudeMessages(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		respondErr(w, 400, "bad_request", "messages are required")
		return
	}
	account, _, err := s.svc.ResolveForRequest(r.Context(), selector)
	if err != nil {
		respondErr(w, 404, "account_not_found", err.Error())
		return
	}
	setResolvedAccountHeaders(w, account)
	usage, usageErr := s.svc.Store.GetUsage(r.Context(), account.ID)
	if usageErr != nil {
		if snap, err := s.svc.RefreshUsage(r.Context(), account.ID); err == nil {
			usage = snap
			usageErr = nil
		}
	}
	if usageErr == nil {
		if usage.HourlyPct <= 0 || usage.WeeklyPct <= 0 {
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
			return
		}
	}
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    req.Stream,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    reqID,
				"type":  "message",
				"role":  "assistant",
				"model": req.Model,
			},
		})
		flusher.Flush()
		_, err := s.svc.Codex.StreamChat(r.Context(), account.CodexHome, req.Model, prompt, func(evt provider.ChatEvent) error {
			writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": evt.Text},
			})
			flusher.Flush()
			return nil
		})
		if err != nil {
			status = 500
			writeSSE(w, "error", map[string]any{"type": "error", "error": map[string]any{"message": err.Error()}})
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), account.CodexHome, req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	resp := ClaudeMessagesResponse{
		ID:         reqID,
		Type:       "message",
		Role:       "assistant",
		Model:      req.Model,
		Content:    []ClaudeContentBlock{{Type: "text", Text: res.Text}},
		StopReason: "end_turn",
		Usage: ClaudeMessagesUsage{
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		},
	}
	respondJSON(w, 200, resp)
}

func promptFromMessages(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		if role == "tool" && strings.TrimSpace(m.ToolCallID) != "" {
			sb.WriteString("(")
			sb.WriteString(strings.TrimSpace(m.ToolCallID))
			sb.WriteString(")")
		}
		sb.WriteString(": ")
		sb.WriteString(strings.TrimSpace(m.Content))
		if len(m.ToolCalls) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			for i, tc := range m.ToolCalls {
				if i > 0 {
					sb.WriteString(" | ")
				}
				sb.WriteString(strings.TrimSpace(tc.Function.Name))
				sb.WriteString("(")
				sb.WriteString(strings.TrimSpace(tc.Function.Arguments))
				sb.WriteString(")")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func promptFromMessagesWithTools(msgs []ChatMessage, tools []ChatToolDef, toolChoice json.RawMessage) string {
	base := promptFromMessages(msgs)
	if len(tools) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\nAVAILABLE_TOOLS_JSON:\n")
	sb.WriteString("[\n")
	for i, t := range tools {
		if i > 0 {
			sb.WriteString(",\n")
		}
		b, _ := json.Marshal(t)
		sb.WriteString(string(b))
	}
	sb.WriteString("\n]\n")
	if len(bytes.TrimSpace(toolChoice)) > 0 {
		sb.WriteString("TOOL_CHOICE_JSON:\n")
		sb.WriteString(strings.TrimSpace(string(toolChoice)))
		sb.WriteString("\n")
	}
	sb.WriteString("TOOL_OUTPUT_RULES:\n")
	sb.WriteString("- If a tool is required, respond with JSON only.\n")
	sb.WriteString("- JSON format must be exactly: {\"tool_calls\":[{\"name\":\"<tool_name>\",\"arguments\":{...}}]}.\n")
	sb.WriteString("- Do not wrap JSON in markdown fences.\n")
	sb.WriteString("- If no tool is needed, respond normally with plain assistant text.\n")
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessages(msgs []ClaudeMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(extractClaudeContentText(m.Content))
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func extractClaudeContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err == nil {
		var parts []string
		for _, it := range asItems {
			if t, _ := it["type"].(string); t != "" && t != "text" {
				continue
			}
			if text, _ := it["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func respondErr(w http.ResponseWriter, code int, errType, msg string) {
	respondJSON(w, code, map[string]any{"error": map[string]any{"type": errType, "message": msg}})
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		x := strings.TrimSpace(v)
		if x != "" {
			return x
		}
	}
	return ""
}

func extractResponsesInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err == nil {
		var parts []string
		for _, it := range asItems {
			if role, _ := it["role"].(string); role != "" {
				if content, ok := it["content"].(string); ok {
					parts = append(parts, role+": "+content)
					continue
				}
				if arr, ok := it["content"].([]any); ok {
					for _, c := range arr {
						obj, _ := c.(map[string]any)
						if text, _ := obj["text"].(string); text != "" {
							parts = append(parts, role+": "+text)
						}
					}
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func parseToolCallsFromText(text string, defs []ChatToolDef) ([]ChatToolCall, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil, false
	}
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start := strings.IndexAny(raw, "{[")
	end := strings.LastIndexAny(raw, "}]")
	if start < 0 || end < start {
		return nil, false
	}
	candidate := strings.TrimSpace(raw[start : end+1])
	allowed := map[string]struct{}{}
	for _, d := range defs {
		if strings.EqualFold(strings.TrimSpace(d.Type), "function") {
			name := strings.TrimSpace(d.Function.Name)
			if name != "" {
				allowed[name] = struct{}{}
			}
		}
	}
	type simpleCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	type wrapped struct {
		ToolCalls []simpleCall `json:"tool_calls"`
	}
	var calls []simpleCall
	var w wrapped
	if err := json.Unmarshal([]byte(candidate), &w); err == nil && len(w.ToolCalls) > 0 {
		calls = w.ToolCalls
	} else {
		var one simpleCall
		if err := json.Unmarshal([]byte(candidate), &one); err == nil && strings.TrimSpace(one.Name) != "" {
			calls = []simpleCall{one}
		} else {
			var arr []simpleCall
			if err := json.Unmarshal([]byte(candidate), &arr); err == nil && len(arr) > 0 {
				calls = arr
			}
		}
	}
	if len(calls) == 0 {
		return nil, false
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		args := normalizeToolArguments(c.Arguments)
		out = append(out, ChatToolCall{
			ID:   "call_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func normalizeToolArguments(raw json.RawMessage) string {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || string(b) == "null" {
		return "{}"
	}
	if json.Valid(b) {
		return string(b)
	}
	enc, _ := json.Marshal(string(b))
	return string(enc)
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	b, _ := json.Marshal(payload)
	if strings.TrimSpace(event) != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func setResolvedAccountHeaders(w http.ResponseWriter, account store.Account) {
	if w == nil {
		return
	}
	if rec, ok := w.(*trafficRecorder); ok {
		rec.accountID = strings.TrimSpace(account.ID)
		rec.accountEmail = strings.TrimSpace(account.Email)
		return
	}
}

func (s *Server) currentAPIKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apiKey
}

func (s *Server) isValidAPIKey(r *http.Request) bool {
	key := s.currentAPIKey()
	if BearerToken(r.Header.Get("Authorization")) == key {
		return true
	}
	return strings.TrimSpace(r.Header.Get("x-api-key")) == key
}

func (s *Server) setAPIKey(v string) {
	s.mu.Lock()
	s.apiKey = v
	s.mu.Unlock()
}

func (s *Server) currentModelMappings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for k, v := range s.svc.Cfg.ModelMappings {
		out[k] = v
	}
	return out
}

func (s *Server) resolveMappedModel(requested string) string {
	model := strings.TrimSpace(requested)
	if model == "" {
		return model
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if target, ok := s.svc.Cfg.ModelMappings[model]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	return model
}

func (s *Server) upsertModelMapping(alias, model string) error {
	cfg, err := config.LoadOrInit()
	if err != nil {
		return err
	}
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	cfg.ModelMappings[strings.TrimSpace(alias)] = strings.TrimSpace(model)
	if err := config.Save(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	return nil
}

func (s *Server) deleteModelMapping(alias string) error {
	cfg, err := config.LoadOrInit()
	if err != nil {
		return err
	}
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	delete(cfg.ModelMappings, strings.TrimSpace(alias))
	if err := config.Save(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	return nil
}

func codexAvailableModels() []string {
	return []string{
		"gpt-5.1-codex-max",
		"gpt-5.2",
		"gpt-5.2-codex",
		"gpt-5.3-codex",
		"gpt-5.4-mini",
		"gpt-5.4",
	}
}

func isValidCodexModel(model string) bool {
	m := strings.TrimSpace(model)
	if m == "" {
		return false
	}
	for _, v := range codexAvailableModels() {
		if v == m {
			return true
		}
	}
	return false
}

func (s *Server) handleWebSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		base := externalBaseURLFromRequest(r, s.bindAddr)
		modelMappings := s.currentModelMappings()
		s.mu.RLock()
		usageAlertThreshold := s.svc.Cfg.UsageAlertThreshold
		usageAutoSwitchThreshold := s.svc.Cfg.UsageAutoSwitchThreshold
		s.mu.RUnlock()
		respondJSON(w, 200, map[string]any{
			"api_key":                     s.currentAPIKey(),
			"openai_endpoint":             strings.TrimRight(base, "/") + "/v1/chat/completions",
			"claude_endpoint":             strings.TrimRight(base, "/") + "/v1/messages",
			"openai_models_url":           strings.TrimRight(base, "/") + "/v1/models",
			"openai_chat_url":             strings.TrimRight(base, "/") + "/v1/chat/completions",
			"openai_responses_url":        strings.TrimRight(base, "/") + "/v1/responses",
			"available_models":            codexAvailableModels(),
			"model_mappings":              modelMappings,
			"usage_alert_threshold":       usageAlertThreshold,
			"usage_auto_switch_threshold": usageAutoSwitchThreshold,
		})
		return
	case http.MethodPost:
		var req struct {
			UsageAlertThreshold      *int `json:"usage_alert_threshold"`
			UsageAutoSwitchThreshold *int `json:"usage_auto_switch_threshold"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		s.mu.Lock()
		cfg := s.svc.Cfg
		if req.UsageAlertThreshold != nil {
			v := *req.UsageAlertThreshold
			if v < 0 || v > 100 {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "usage_alert_threshold must be in range 0..100")
				return
			}
			cfg.UsageAlertThreshold = v
		}
		if req.UsageAutoSwitchThreshold != nil {
			v := *req.UsageAutoSwitchThreshold
			if v < 0 || v > 100 {
				s.mu.Unlock()
				respondErr(w, 400, "bad_request", "usage_auto_switch_threshold must be in range 0..100")
				return
			}
			cfg.UsageAutoSwitchThreshold = v
		}
		if err := config.Save(cfg); err != nil {
			s.mu.Unlock()
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		s.svc.Cfg = cfg
		s.mu.Unlock()
		respondJSON(w, 200, map[string]any{
			"ok":                          true,
			"usage_alert_threshold":       cfg.UsageAlertThreshold,
			"usage_auto_switch_threshold": cfg.UsageAutoSwitchThreshold,
		})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleWebLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > 1000 {
				v = 1000
			}
			limit = v
		}
	}
	if s.traffic == nil {
		respondJSON(w, 200, map[string]any{"ok": true, "lines": []string{}})
		return
	}
	lines, err := s.traffic.ReadTail(limit)
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "lines": lines})
}

func (s *Server) handleWebClientEventLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Type    string         `json:"type"`
		Source  string         `json:"source"`
		Level   string         `json:"level"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	eventType := firstNonEmpty(strings.TrimSpace(req.Type), "event")
	eventSource := firstNonEmpty(strings.TrimSpace(req.Source), "web-console")
	eventLevel := firstNonEmpty(strings.TrimSpace(req.Level), "info")
	eventMessage := firstNonEmpty(strings.TrimSpace(req.Message), "-")
	meta := "-"
	if len(req.Data) > 0 {
		if b, err := json.Marshal(req.Data); err == nil {
			meta = string(b)
		}
	}
	log.Printf("[EVENT] source=%s type=%s level=%s message=%s meta=%s", eventSource, eventType, eventLevel, eventMessage, meta)
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebModelMappings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, 200, map[string]any{
			"ok":               true,
			"available_models": codexAvailableModels(),
			"mappings":         s.currentModelMappings(),
		})
		return
	case http.MethodPost:
		var req struct {
			Alias string `json:"alias"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		alias := strings.TrimSpace(req.Alias)
		model := strings.TrimSpace(req.Model)
		if alias == "" || model == "" {
			respondErr(w, 400, "bad_request", "alias and model are required")
			return
		}
		if !isValidCodexModel(model) {
			respondErr(w, 400, "bad_request", "invalid target model")
			return
		}
		if err := s.upsertModelMapping(alias, model); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	case http.MethodDelete:
		alias := strings.TrimSpace(r.URL.Query().Get("alias"))
		if alias == "" {
			respondErr(w, 400, "bad_request", "alias is required")
			return
		}
		if err := s.deleteModelMapping(alias); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) withTrafficLog(protocol string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.traffic == nil {
			next(w, r)
			return
		}
		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		start := time.Now()
		rec := &trafficRecorder{
			ResponseWriter:    w,
			status:            http.StatusOK,
			responseBodyLimit: -1,
		}
		next(rec, r)

		model, stream := detectTrafficModelAndStream(r.URL.Path, bodyBytes)
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			remote = host
		}
		responseBody := strings.TrimSpace(string(rec.responseBody))
		_ = s.traffic.Append(trafficlog.Entry{
			Timestamp:    time.Now().UTC(),
			Protocol:     protocol,
			Method:       r.Method,
			Path:         r.URL.Path,
			Status:       rec.status,
			LatencyMS:    time.Since(start).Milliseconds(),
			RemoteAddr:   strings.TrimSpace(remote),
			UserAgent:    strings.TrimSpace(r.UserAgent()),
			AccountHint:  strings.TrimSpace(r.Header.Get("X-Codex-Account")),
			AccountID:    strings.TrimSpace(rec.accountID),
			AccountEmail: strings.TrimSpace(rec.accountEmail),
			Model:        model,
			Stream:       stream,
			RequestBody:  strings.TrimSpace(string(bodyBytes)),
			ResponseBody: responseBody,
		})
	}
}

type trafficRecorder struct {
	http.ResponseWriter
	status            int
	responseBody      []byte
	responseBodyLimit int
	bodyTruncated     bool
	accountID         string
	accountEmail      string
}

func (r *trafficRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *trafficRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.responseBodyLimit <= 0 {
		r.responseBody = append(r.responseBody, p...)
	} else if !r.bodyTruncated {
		remaining := r.responseBodyLimit - len(r.responseBody)
		if remaining > 0 {
			if len(p) <= remaining {
				r.responseBody = append(r.responseBody, p...)
			} else {
				r.responseBody = append(r.responseBody, p[:remaining]...)
				r.bodyTruncated = true
			}
		} else {
			r.bodyTruncated = true
		}
	}
	return r.ResponseWriter.Write(p)
}

func (r *trafficRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func detectTrafficModelAndStream(path string, body []byte) (string, bool) {
	switch strings.TrimSpace(path) {
	case "/v1/chat/completions":
		var req ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/responses":
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/messages", "/claude/v1/messages":
		var req ClaudeMessagesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	}
	return "", false
}

func truncateForLog(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func (s *Server) handleWebUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		APIKey     string `json:"api_key"`
		Regenerate bool   `json:"regenerate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	newKey := strings.TrimSpace(req.APIKey)
	if req.Regenerate {
		k, err := randomProxyKey()
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		newKey = k
	}
	if newKey == "" {
		respondErr(w, 400, "bad_request", "api_key required")
		return
	}
	cfg, err := config.LoadOrInit()
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	cfg.ProxyAPIKey = newKey
	if err := config.Save(cfg); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	s.svc.Cfg.ProxyAPIKey = newKey
	s.setAPIKey(newKey)
	respondJSON(w, 200, map[string]any{"ok": true, "api_key": newKey})
}

func (s *Server) handleWebBrowserStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	base := oauthBaseURLFromRequest(r)
	login, err := s.svc.StartBrowserLoginWeb(r.Context(), base, "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "login_id": login.LoginID, "auth_url": login.AuthURL})
}

func (s *Server) handleWebBrowserCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.svc.CancelBrowserLoginWeb(strings.TrimSpace(req.LoginID))
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebBrowserCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	loginID := strings.TrimSpace(r.URL.Query().Get("login_id"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))

	var err error
	if loginID != "" {
		_, err = s.svc.CompleteBrowserLoginCode(r.Context(), loginID, code, state)
	} else {
		_, err = s.svc.CompleteBrowserLoginCodeByState(r.Context(), code, state)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("authentication failed: " + err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><body style='font-family: sans-serif;background:#0f172a;color:#f8fafc;padding:20px'>Login success. You can close this tab and return to codexsess.</body></html>"))
}

func (s *Server) handleWebDeviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	login, err := s.svc.StartDeviceLogin(r.Context(), "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok": true,
		"login": map[string]any{
			"login_id":                  login.LoginID,
			"user_code":                 login.UserCode,
			"verification_uri":          login.VerificationURI,
			"verification_uri_complete": login.VerificationURIComplete,
			"interval_seconds":          login.IntervalSeconds,
			"expires_at":                login.ExpiresAt,
		},
	})
}

func (s *Server) handleWebDevicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.PollDeviceLogin(r.Context(), strings.TrimSpace(req.LoginID))
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func randomProxyKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(buf), nil
}

func oauthBaseURLFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return scheme + "://localhost:3061"
	}
	port := ""
	if _, p, err := net.SplitHostPort(host); err == nil {
		port = p
	} else if i := strings.LastIndex(host, ":"); i > -1 {
		port = host[i+1:]
	}
	if strings.TrimSpace(port) == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://localhost:" + port
}

func externalBaseURLFromRequest(r *http.Request, bindAddr string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host != "" {
		return scheme + "://" + host
	}
	return scheme + "://" + bindAddr
}
