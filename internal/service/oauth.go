package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

var oauthHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
}

const (
	oauthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	authEndpoint  = "https://auth.openai.com/oauth/authorize"
	tokenEndpoint = "https://auth.openai.com/oauth/token"
	oauthCBPort   = 1455
)

type OAuthPending struct {
	LoginID      string    `json:"login_id"`
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	RedirectURI  string    `json:"redirect_uri"`
	Alias        string    `json:"alias,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type BrowserLogin struct {
	LoginID  string
	AuthURL  string
	Port     int
	Callback <-chan string
	stopFn   func()
}

type BrowserWebLogin struct {
	LoginID string `json:"login_id"`
	AuthURL string `json:"auth_url"`
}

type DeviceLogin struct {
	LoginID                 string    `json:"login_id"`
	DeviceCode              string    `json:"device_code"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete,omitempty"`
	IntervalSeconds         int       `json:"interval_seconds"`
	ExpiresAt               time.Time `json:"expires_at"`
	Alias                   string    `json:"alias,omitempty"`
	CodexHome               string    `json:"codex_home,omitempty"`
	LogPath                 string    `json:"log_path,omitempty"`
	ExitCodePath            string    `json:"exit_code_path,omitempty"`
}

type DevicePollResult struct {
	Status  string         `json:"status"`
	Account *store.Account `json:"account,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type activeBrowserWebSession struct {
	LoginID string
	AuthURL string
	stopFn  func()
}

var (
	activeBrowserWebMu    sync.Mutex
	activeBrowserWebState *activeBrowserWebSession
)

func (s *Service) StartBrowserLogin(ctx context.Context) (BrowserLogin, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", oauthCBPort))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			return BrowserLogin{}, fmt.Errorf("browser login port in use: %d", oauthCBPort)
		}
		return BrowserLogin{}, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	loginID, err := randomBase64URL(16)
	if err != nil {
		return BrowserLogin{}, err
	}
	state, err := randomBase64URL(24)
	if err != nil {
		return BrowserLogin{}, err
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		return BrowserLogin{}, err
	}
	challenge := codeChallenge(verifier)
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", oauthClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid profile email offline_access")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("state", state)
	q.Set("originator", "codex_vscode")
	authURL := authEndpoint + "?" + q.Encode()

	pending := OAuthPending{
		LoginID:      loginID,
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.savePending(pending); err != nil {
		return BrowserLogin{}, err
	}

	callbackCh := make(chan string, 1)
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		gotState := r.URL.Query().Get("state")
		if gotState != state || strings.TrimSpace(code) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid callback"))
			return
		}
		_, _ = w.Write([]byte("Login success, you can close this tab."))
		select {
		case callbackCh <- r.URL.String():
		default:
		}
		go func() { _ = srv.Shutdown(context.Background()) }()
	})
	srv.Handler = mux

	go func() {
		_ = srv.Serve(listener)
	}()

	return BrowserLogin{
		LoginID:  loginID,
		AuthURL:  authURL,
		Port:     port,
		Callback: callbackCh,
		stopFn: func() {
			_ = srv.Close()
		},
	}, nil
}

func (s *Service) CompleteBrowserLogin(ctx context.Context, loginID string, callbackURL string, alias string) (storeAccount store.Account, err error) {
	pending, err := s.loadPending(loginID)
	if err != nil {
		return storeAccount, err
	}
	parsed, err := parseCallback(callbackURL, pending)
	if err != nil {
		return storeAccount, err
	}
	tokenSet, err := exchangeCode(ctx, parsed, pending)
	if err != nil {
		return storeAccount, err
	}
	acc, err := s.SaveAccountFromTokens(ctx, tokenSet, alias)
	if err != nil {
		return storeAccount, err
	}
	_ = s.deletePending(loginID)
	return acc, nil
}

func (s *Service) CompleteFromManualCallback(ctx context.Context, loginID string, callbackURL string, alias string) (store.Account, error) {
	return s.CompleteBrowserLogin(ctx, loginID, callbackURL, alias)
}

func (s *Service) StartBrowserLoginWeb(ctx context.Context, externalBaseURL string, alias string) (BrowserWebLogin, error) {
	activeBrowserWebMu.Lock()
	if activeBrowserWebState != nil {
		reuse := *activeBrowserWebState
		activeBrowserWebMu.Unlock()
		return BrowserWebLogin{LoginID: reuse.LoginID, AuthURL: reuse.AuthURL}, nil
	}
	activeBrowserWebMu.Unlock()

	_ = externalBaseURL
	login, err := s.StartBrowserLogin(ctx)
	if err != nil {
		return BrowserWebLogin{}, err
	}
	trimmedAlias := strings.TrimSpace(alias)
	if trimmedAlias != "" {
		pending, err := s.loadPending(login.LoginID)
		if err == nil {
			pending.Alias = trimmedAlias
			_ = s.savePending(pending)
		}
	}

	go func(login BrowserLogin, alias string) {
		defer func() {
			activeBrowserWebMu.Lock()
			if activeBrowserWebState != nil && activeBrowserWebState.LoginID == login.LoginID {
				activeBrowserWebState = nil
			}
			activeBrowserWebMu.Unlock()
		}()
		select {
		case cb := <-login.Callback:
			_, _ = s.CompleteBrowserLogin(context.Background(), login.LoginID, cb, alias)
		case <-time.After(5 * time.Minute):
			_ = s.deletePending(login.LoginID)
			if login.stopFn != nil {
				login.stopFn()
			}
		}
	}(login, trimmedAlias)

	activeBrowserWebMu.Lock()
	activeBrowserWebState = &activeBrowserWebSession{
		LoginID: login.LoginID,
		AuthURL: login.AuthURL,
		stopFn:  login.stopFn,
	}
	activeBrowserWebMu.Unlock()

	return BrowserWebLogin{
		LoginID: login.LoginID,
		AuthURL: login.AuthURL,
	}, nil
}

func (s *Service) CancelBrowserLoginWeb(loginID string) {
	activeBrowserWebMu.Lock()
	current := activeBrowserWebState
	if current == nil {
		activeBrowserWebMu.Unlock()
		return
	}
	if strings.TrimSpace(loginID) != "" && strings.TrimSpace(loginID) != current.LoginID {
		activeBrowserWebMu.Unlock()
		return
	}
	activeBrowserWebState = nil
	activeBrowserWebMu.Unlock()

	_ = s.deletePending(current.LoginID)
	if current.stopFn != nil {
		current.stopFn()
	}
}

func (s *Service) CompleteBrowserLoginCode(ctx context.Context, loginID, code, state string) (store.Account, error) {
	pending, err := s.loadPending(loginID)
	if err != nil {
		return store.Account{}, err
	}
	if strings.TrimSpace(code) == "" {
		return store.Account{}, fmt.Errorf("missing code")
	}
	if strings.TrimSpace(state) != pending.State {
		return store.Account{}, fmt.Errorf("state mismatch")
	}
	values := url.Values{}
	values.Set("code", code)
	values.Set("state", state)
	tokenSet, err := exchangeCode(ctx, values, pending)
	if err != nil {
		return store.Account{}, err
	}
	acc, err := s.SaveAccountFromTokens(ctx, tokenSet, pending.Alias)
	if err != nil {
		return store.Account{}, err
	}
	_ = s.deletePending(loginID)
	return acc, nil
}

func (s *Service) CompleteBrowserLoginCodeByState(ctx context.Context, code, state string) (store.Account, error) {
	if strings.TrimSpace(code) == "" {
		return store.Account{}, fmt.Errorf("missing code")
	}
	if strings.TrimSpace(state) == "" {
		return store.Account{}, fmt.Errorf("missing state")
	}
	entries, err := os.ReadDir(s.pendingDir())
	if err != nil {
		return store.Account{}, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "device_") {
			continue
		}
		loginID := strings.TrimSuffix(name, ".json")
		pending, err := s.loadPending(loginID)
		if err != nil {
			continue
		}
		if pending.State != strings.TrimSpace(state) {
			continue
		}
		values := url.Values{}
		values.Set("code", strings.TrimSpace(code))
		values.Set("state", strings.TrimSpace(state))
		tokenSet, err := exchangeCode(ctx, values, pending)
		if err != nil {
			return store.Account{}, err
		}
		acc, err := s.SaveAccountFromTokens(ctx, tokenSet, pending.Alias)
		if err != nil {
			return store.Account{}, err
		}
		_ = s.deletePending(loginID)
		return acc, nil
	}
	return store.Account{}, fmt.Errorf("no matching browser login session for state")
}

func (s *Service) StartDeviceLogin(ctx context.Context, alias string) (DeviceLogin, error) {
	loginID, err := randomBase64URL(16)
	if err != nil {
		return DeviceLogin{}, err
	}
	if err := os.MkdirAll(s.pendingDir(), 0o700); err != nil {
		return DeviceLogin{}, err
	}
	codexHome := filepath.Join(s.pendingDir(), "device_home_"+loginID)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return DeviceLogin{}, err
	}
	logPath := filepath.Join(s.pendingDir(), "device_"+loginID+".log")
	exitPath := filepath.Join(s.pendingDir(), "device_"+loginID+".exit")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return DeviceLogin{}, err
	}
	cmd := exec.CommandContext(context.Background(), s.Codex.Binary, "login", "--device-auth")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return DeviceLogin{}, err
	}
	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			exitCode = 1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exitCode = ee.ExitCode()
			}
		}
		_ = os.WriteFile(exitPath, []byte(strconv.Itoa(exitCode)), 0o600)
		_ = logFile.Close()
	}()

	deadline := time.Now().Add(15 * time.Second)
	var verifyURL, userCode string
	for {
		raw, _ := os.ReadFile(logPath)
		verifyURL, userCode = parseDevicePrompt(string(raw))
		if verifyURL != "" && userCode != "" {
			break
		}
		if time.Now().After(deadline) {
			return DeviceLogin{}, fmt.Errorf("device login start timeout: unable to parse device code from codex output")
		}
		select {
		case <-ctx.Done():
			return DeviceLogin{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}

	d := DeviceLogin{
		LoginID:                 loginID,
		DeviceCode:              "",
		UserCode:                userCode,
		VerificationURI:         verifyURL,
		VerificationURIComplete: verifyURL,
		IntervalSeconds:         3,
		ExpiresAt:               time.Now().UTC().Add(15 * time.Minute),
		Alias:                   strings.TrimSpace(alias),
		CodexHome:               codexHome,
		LogPath:                 logPath,
		ExitCodePath:            exitPath,
	}
	if d.UserCode == "" || d.VerificationURI == "" {
		return DeviceLogin{}, fmt.Errorf("device response missing fields")
	}
	if err := s.savePendingDevice(d); err != nil {
		return DeviceLogin{}, err
	}
	return d, nil
}

func (s *Service) PollDeviceLogin(ctx context.Context, loginID string) (DevicePollResult, error) {
	p, err := s.loadPendingDevice(loginID)
	if err != nil {
		return DevicePollResult{}, err
	}
	if time.Now().UTC().After(p.ExpiresAt) {
		s.cleanupPendingDevice(p)
		return DevicePollResult{Status: "expired", Error: "device login expired"}, nil
	}
	exitData, err := os.ReadFile(p.ExitCodePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DevicePollResult{Status: "pending"}, nil
		}
		return DevicePollResult{}, err
	}
	exitCode, _ := strconv.Atoi(strings.TrimSpace(string(exitData)))
	if exitCode != 0 {
		logTail := ""
		if b, err := os.ReadFile(p.LogPath); err == nil {
			logTail = trimTail(stripANSI(string(b)), 500)
		}
		s.cleanupPendingDevice(p)
		if strings.TrimSpace(logTail) == "" {
			return DevicePollResult{Status: "error", Error: fmt.Sprintf("device auth process exited with code %d", exitCode)}, nil
		}
		return DevicePollResult{Status: "error", Error: logTail}, nil
	}
	authPath := filepath.Join(p.CodexHome, "auth.json")
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return DevicePollResult{Status: "error", Error: "device auth succeeded but auth.json not found"}, nil
	}
	var af util.AuthFile
	if err := json.Unmarshal(raw, &af); err != nil {
		return DevicePollResult{Status: "error", Error: "invalid auth.json from codex login"}, nil
	}
	t := TokenSet{
		IDToken:      strings.TrimSpace(af.Tokens.IDToken),
		AccessToken:  strings.TrimSpace(af.Tokens.AccessToken),
		RefreshToken: strings.TrimSpace(af.Tokens.RefreshToken),
		AccountID:    strings.TrimSpace(af.Tokens.AccountID),
	}
	if t.IDToken == "" || t.AccessToken == "" {
		return DevicePollResult{Status: "error", Error: "auth.json missing token fields"}, nil
	}
	acc, err := s.SaveAccountFromTokens(ctx, t, p.Alias)
	if err != nil {
		return DevicePollResult{}, err
	}
	s.cleanupPendingDevice(p)
	return DevicePollResult{Status: "success", Account: &acc}, nil
}

func parseCallback(raw string, pending OAuthPending) (url.Values, error) {
	u, err := url.Parse(raw)
	if err != nil {
		if strings.HasPrefix(raw, "/auth/callback") {
			u, err = url.Parse("http://localhost" + raw)
		}
		if err != nil {
			return nil, err
		}
	}
	q := u.Query()
	if q.Get("state") != pending.State {
		return nil, fmt.Errorf("state mismatch")
	}
	if strings.TrimSpace(q.Get("code")) == "" {
		return nil, fmt.Errorf("missing code")
	}
	return q, nil
}

func exchangeCode(ctx context.Context, q url.Values, pending OAuthPending) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", q.Get("code"))
	form.Set("redirect_uri", pending.RedirectURI)
	form.Set("client_id", oauthClientID)
	form.Set("code_verifier", pending.CodeVerifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return TokenSet{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return TokenSet{}, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(b))
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return TokenSet{}, err
	}
	t := TokenSet{
		IDToken:      asString(m["id_token"]),
		AccessToken:  asString(m["access_token"]),
		RefreshToken: asString(m["refresh_token"]),
	}
	if strings.TrimSpace(t.IDToken) == "" || strings.TrimSpace(t.AccessToken) == "" {
		return TokenSet{}, fmt.Errorf("token response missing fields")
	}
	return t, nil
}

func refreshAccessToken(ctx context.Context, refreshToken string) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", oauthClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return TokenSet{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return TokenSet{}, fmt.Errorf("refresh token failed (%d): %s", resp.StatusCode, string(b))
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return TokenSet{}, err
	}
	t := TokenSet{
		IDToken:      asString(m["id_token"]),
		AccessToken:  asString(m["access_token"]),
		RefreshToken: asString(m["refresh_token"]),
	}
	if t.RefreshToken == "" {
		t.RefreshToken = refreshToken
	}
	if strings.TrimSpace(t.IDToken) == "" || strings.TrimSpace(t.AccessToken) == "" {
		return TokenSet{}, fmt.Errorf("refresh response missing fields")
	}
	return t, nil
}

func (s *Service) pendingDir() string {
	return filepath.Join(s.Cfg.DataDir, "pending")
}

func (s *Service) savePending(p OAuthPending) error {
	if err := os.MkdirAll(s.pendingDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.pendingDir(), p.LoginID+".json"), b, 0o600)
}

func (s *Service) loadPending(loginID string) (OAuthPending, error) {
	var p OAuthPending
	b, err := os.ReadFile(filepath.Join(s.pendingDir(), loginID+".json"))
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	return p, nil
}

func (s *Service) deletePending(loginID string) error {
	return os.Remove(filepath.Join(s.pendingDir(), loginID+".json"))
}

func (s *Service) savePendingDevice(d DeviceLogin) error {
	if err := os.MkdirAll(s.pendingDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.pendingDir(), "device_"+d.LoginID+".json"), b, 0o600)
}

func (s *Service) loadPendingDevice(loginID string) (DeviceLogin, error) {
	var d DeviceLogin
	b, err := os.ReadFile(filepath.Join(s.pendingDir(), "device_"+loginID+".json"))
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.LoginID == "" {
		return d, errors.New("invalid device pending")
	}
	return d, nil
}

func (s *Service) deletePendingDevice(loginID string) error {
	return os.Remove(filepath.Join(s.pendingDir(), "device_"+loginID+".json"))
}

func randomBase64URL(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func asString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}



func (s *Service) cleanupPendingDevice(d DeviceLogin) {
	_ = s.deletePendingDevice(d.LoginID)
	if strings.TrimSpace(d.LogPath) != "" {
		_ = os.Remove(d.LogPath)
	}
	if strings.TrimSpace(d.ExitCodePath) != "" {
		_ = os.Remove(d.ExitCodePath)
	}
	if strings.TrimSpace(d.CodexHome) != "" {
		_ = os.RemoveAll(d.CodexHome)
	}
}

var (
	ansiPattern       = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	deviceURLPattern  = regexp.MustCompile(`https://auth\.openai\.com/codex/device`)
	deviceCodePattern = regexp.MustCompile(`\b[A-Z0-9]{4,5}-[A-Z0-9]{4,6}\b`)
)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func parseDevicePrompt(raw string) (string, string) {
	clean := stripANSI(raw)
	verifyURL := deviceURLPattern.FindString(clean)
	userCode := deviceCodePattern.FindString(clean)
	return strings.TrimSpace(verifyURL), strings.TrimSpace(userCode)
}

func trimTail(s string, max int) string {
	if len(s) <= max {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[len(s)-max:])
}
