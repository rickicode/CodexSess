package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/config"
)

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
	case p == "/favicon.png":
		return true
	case strings.HasPrefix(p, "/v1"):
		return true
	case strings.HasPrefix(p, "/claude/v1"):
		return true
	case strings.HasPrefix(p, "/zo/v1"):
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
	sum := sha256.Sum256([]byte(username + "|" + expRaw + "|" + s.currentAdminPasswordHash()))
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
	return config.VerifyPassword(pass, s.currentAdminPasswordHash())
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
<link rel="icon" type="image/png" href="/favicon.png" />
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
