package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
	_ "modernc.org/sqlite"
)

func TestAppStartup_ResetsLegacyCodingSessionsAndPreservesFreshChatLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script codex smoke runner is unix-only")
	}

	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.MkdirAll(cfg.AuthStoreDir, 0o700); err != nil {
		t.Fatalf("mkdir auth store dir: %v", err)
	}
	if err := os.MkdirAll(cfg.CodexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(cfg.MasterKeyPath, key, 0o600); err != nil {
		t.Fatalf("write master key: %v", err)
	}
	cry, err := icrypto.New(key)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "data.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	now := time.Now().UTC()
	tokenID, err := cry.Encrypt([]byte("id-token-smoke"))
	if err != nil {
		t.Fatalf("encrypt id token: %v", err)
	}
	tokenAccess, err := cry.Encrypt([]byte("access-token-smoke"))
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	tokenRefresh, err := cry.Encrypt([]byte("refresh-token-smoke"))
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	if err := st.UpsertAccount(ctx, store.Account{
		ID:           "acc_smoke",
		Email:        "smoke@example.com",
		Alias:        "smoke@example.com",
		AccountID:    "acct-smoke",
		TokenID:      tokenID,
		TokenAccess:  tokenAccess,
		TokenRefresh: tokenRefresh,
		CodexHome:    cfg.CodexHome,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastUsedAt:   now,
	}); err != nil {
		t.Fatalf("upsert account: %v", err)
	}
	if err := st.SetActiveCLIAccount(ctx, "acc_smoke"); err != nil {
		t.Fatalf("set active cli account: %v", err)
	}
	if err := util.WriteAuthJSON(filepath.Join(cfg.AuthStoreDir, "acc_smoke"), "id-token-smoke", "access-token-smoke", "refresh-token-smoke", "acct-smoke"); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite db: %v", err)
	}
	defer func() { _ = legacyDB.Close() }()
	if _, err := legacyDB.Exec(`DROP TABLE IF EXISTS coding_sessions`); err != nil {
		t.Fatalf("drop coding_sessions: %v", err)
	}
	if _, err := legacyDB.Exec(`
		CREATE TABLE coding_sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			reasoning_level TEXT NOT NULL DEFAULT 'medium',
			work_dir TEXT NOT NULL DEFAULT '~/',
			sandbox_mode TEXT NOT NULL DEFAULT 'full-access',
			codex_thread_id TEXT NOT NULL DEFAULT '',
			restart_pending INTEGER NOT NULL DEFAULT 0,
			legacy_enabled INTEGER NOT NULL DEFAULT 0,
			chat_codex_thread_id TEXT NOT NULL DEFAULT '',
			legacy_supervisor_thread_id TEXT NOT NULL DEFAULT '',
			legacy_executor_thread_id TEXT NOT NULL DEFAULT '',
			chat_needs_hydration INTEGER NOT NULL DEFAULT 0,
			chat_context_version INTEGER NOT NULL DEFAULT 0,
			last_hydrated_chat_context_version INTEGER NOT NULL DEFAULT 0,
			last_mode_transition_summary TEXT NOT NULL DEFAULT '',
			artifact_version INTEGER NOT NULL DEFAULT 0,
			last_applied_event_seq INTEGER NOT NULL DEFAULT 0,
			legacy_plan_artifact_path TEXT NOT NULL DEFAULT '',
			legacy_plan_updated_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_message_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy coding_sessions: %v", err)
	}
	legacyTime := now.Add(-5 * time.Minute).Format(time.RFC3339)
	if _, err := legacyDB.Exec(`
		INSERT INTO coding_sessions(
			id,title,model,reasoning_level,work_dir,sandbox_mode,codex_thread_id,restart_pending,
			legacy_enabled,chat_codex_thread_id,legacy_supervisor_thread_id,legacy_executor_thread_id,
			chat_needs_hydration,chat_context_version,last_hydrated_chat_context_version,last_mode_transition_summary,
			artifact_version,last_applied_event_seq,legacy_plan_artifact_path,legacy_plan_updated_at,
			created_at,updated_at,last_message_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		"sess_legacy_chat_session", "Legacy Chat Session", "gpt-5.2-codex", "medium", "~/legacy", "workspace-write", "thread_legacy_runtime", 0,
		1, "thread_legacy_chat", "thread_legacy_aux_a", "thread_legacy_aux_b",
		1, 4, 3, "resume chat from legacy runtime", 7, 19, ".omx/plans/legacy-smoke.md", legacyTime,
		legacyTime, legacyTime, legacyTime,
	); err != nil {
		t.Fatalf("insert legacy coding session: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db seed: %v", err)
	}

	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	superpowersRoot := writeSmokeSuperpowersFixture(t)
	codexBin := writeSmokeCodexScript(t)
	appBin := buildSmokeBinary(t)
	port := reserveLocalPort(t)
	baseURL := "http://127.0.0.1:" + port

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()
	var appLogs bytes.Buffer
	cmd := exec.CommandContext(appCtx, appBin)
	cmd.Dir = "/home/ricki/workspaces/codexsess"
	cmd.Stdout = &appLogs
	cmd.Stderr = &appLogs
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PORT="+port,
		"CODEXSESS_NO_OPEN_BROWSER=1",
		"CODEXSESS_CODEX_BIN="+codexBin,
		"CODEXSESS_SUPERPOWERS_REPO_PATH="+superpowersRoot,
		"CODEXSESS_SUPERPOWERS_REPO_URL=https://example.invalid/superpowers.git",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(func() {
		cancelApp()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil && appCtx.Err() == nil {
				t.Fatalf("app exited unexpectedly: %v\nlogs:\n%s", err, appLogs.String())
			}
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	waitForSmokeApp(t, baseURL, &appLogs)

	client := newSmokeHTTPClient(t)
	smokeLogin(t, client, baseURL)

	initialList := smokeGetJSON(t, client, baseURL+"/api/coding/sessions")
	initialSessions := mustSessions(t, initialList)
	if len(initialSessions) != 1 {
		t.Fatalf("expected preserved legacy coding session row on startup, got %d body=%v", len(initialSessions), initialList)
	}
	assertNoLegacySessionFields(t, initialSessions[0])
	if got := stringFromMap(initialSessions[0], "thread_id"); got != "thread_legacy_runtime" {
		t.Fatalf("expected preserved canonical thread id for legacy session, got %q", got)
	}

	createResp := smokePostJSON(t, client, baseURL+"/api/coding/sessions", map[string]any{
		"title":           "Smoke Session",
		"model":           "gpt-5.2-codex",
		"reasoning_level": "medium",
		"work_dir":        workspace,
		"sandbox_mode":    "workspace-write",
	})
	if ok, _ := createResp["ok"].(bool); !ok {
		t.Fatalf("expected create session ok=true, got %#v", createResp)
	}
	createdSession, _ := createResp["session"].(map[string]any)
	if createdSession == nil {
		t.Fatalf("expected create session payload, got %#v", createResp)
	}
	assertNoLegacySessionFields(t, createdSession)
	createdSessionID := stringFromMap(createdSession, "id")
	if strings.TrimSpace(createdSessionID) == "" {
		t.Fatalf("expected created session id, got %#v", createdSession)
	}
	if got := stringFromMap(createdSession, "thread_id"); got != "thread_smoke_session" {
		t.Fatalf("expected created session thread id, got %q", got)
	}

	afterCreateList := smokeGetJSON(t, client, baseURL+"/api/coding/sessions")
	afterCreateSessions := mustSessions(t, afterCreateList)
	if len(afterCreateSessions) != 2 {
		t.Fatalf("expected preserved legacy session plus one fresh session after create, got %d body=%v", len(afterCreateSessions), afterCreateList)
	}
	foundFresh := false
	foundLegacy := false
	for _, session := range afterCreateSessions {
		assertNoLegacySessionFields(t, session)
		switch stringFromMap(session, "id") {
		case createdSessionID:
			foundFresh = true
		case "sess_legacy_chat_session":
			foundLegacy = true
		}
	}
	if !foundFresh || !foundLegacy {
		t.Fatalf("expected both fresh and legacy session rows after create, got %#v", afterCreateSessions)
	}

	wsConn := openSmokeWebSocket(t, client, baseURL, "/api/coding/ws")
	defer func() { _ = wsConn.Close() }()
	if err := wsConn.WriteJSON(map[string]any{
		"type":       "session.send",
		"request_id": "req_smoke_long_run",
		"session_id": createdSessionID,
		"content":    "long running stop verification",
	}); err != nil {
		t.Fatalf("write websocket send: %v", err)
	}
	startedEvt := readSmokeWSEvent(t, wsConn)
	if got := stringFromMap(startedEvt, "event"); got != "session.started" {
		t.Fatalf("expected session.started, got %#v", startedEvt)
	}

	waitForSessionFlightState(t, client, baseURL, createdSessionID, true)

	stopResp := smokePostJSON(t, client, baseURL+"/api/coding/stop", map[string]any{
		"session_id": createdSessionID,
		"force":      false,
	})
	if ok, _ := stopResp["ok"].(bool); !ok {
		t.Fatalf("expected stop ok=true, got %#v", stopResp)
	}
	if stopped, _ := stopResp["stopped"].(bool); !stopped {
		t.Fatalf("expected stop to report stopped=true, got %#v", stopResp)
	}
	waitForSessionFlightState(t, client, baseURL, createdSessionID, false)

	restartResp := smokePostJSON(t, client, baseURL+"/api/coding/runtime/restart", map[string]any{
		"session_id": createdSessionID,
		"force":      false,
	})
	if accepted, _ := restartResp["accepted"].(bool); !accepted {
		t.Fatalf("expected restart accepted=true, got %#v", restartResp)
	}
	if deferred, _ := restartResp["deferred"].(bool); deferred {
		t.Fatalf("expected restart deferred=false after stopped run, got %#v", restartResp)
	}
	if inFlight, _ := restartResp["in_flight"].(bool); inFlight {
		t.Fatalf("expected restart response to report in_flight=false, got %#v", restartResp)
	}

	debugResp := smokeGetJSON(t, client, baseURL+"/api/coding/runtime/debug?session_id="+url.QueryEscape(createdSessionID))
	sessionDebug, _ := debugResp["session"].(map[string]any)
	if sessionDebug == nil {
		t.Fatalf("expected runtime debug session payload, got %#v", debugResp)
	}
	if got := stringFromMap(sessionDebug, "thread_id"); got != "thread_smoke_session" {
		t.Fatalf("expected runtime debug thread_id to stay chat-only, got %q", got)
	}

	cancelApp()
	waitForProcessExit(t, cmd, &appLogs)

	verifyMigratedSchemaState(t, dbPath)
}

func writeSmokeSuperpowersFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	skillsRoot := filepath.Join(root, "skills")
	for _, name := range []string{
		"using-superpowers",
		"brainstorming",
		"writing-plans",
		"executing-plans",
		"subagent-driven-development",
		"systematic-debugging",
		"verification-before-completion",
		"using-git-worktrees",
	} {
		skillRoot := filepath.Join(skillsRoot, name)
		if err := os.MkdirAll(skillRoot, 0o700); err != nil {
			t.Fatalf("mkdir skill %s: %v", name, err)
		}
		body := fmt.Sprintf("---\nname: %s\n---\n\n# %s\n", name, name)
		if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("write skill %s: %v", name, err)
		}
	}
	return root
}

func writeSmokeCodexScript(t *testing.T) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "fake-codex-smoke.sh")
	content := `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo "codex smoke 1.0"
  exit 0
fi
if [ "${1:-}" != "app-server" ]; then
  exit 1
fi
while IFS= read -r line; do
  if printf '%s' "$line" | grep -q '"method":"initialize"'; then
    echo '{"id":"1","result":{"userAgent":"codexsess/test","codexHome":"/tmp/codex-home","platformFamily":"unix","platformOs":"linux"}}'
    continue
  fi
  if printf '%s' "$line" | grep -q '"method":"initialized"'; then
    continue
  fi
  if printf '%s' "$line" | grep -q '"method":"thread/start"'; then
    echo '{"id":"2","result":{"thread":{"id":"thread_smoke_session"}}}'
    echo '{"method":"thread/started","params":{"thread":{"id":"thread_smoke_session"}}}'
    continue
  fi
  if printf '%s' "$line" | grep -q '"method":"turn/start"'; then
    if printf '%s' "$line" | grep -q 'long running stop verification'; then
      echo '{"id":"3","result":{"turn":{"id":"turn_smoke_long","status":"inProgress"}}}'
      echo '{"method":"turn/started","params":{"threadId":"thread_smoke_session","turn":{"id":"turn_smoke_long"}}}'
      sleep 30
      exit 0
    fi
    echo '{"id":"3","result":{"turn":{"id":"turn_smoke_quick","status":"inProgress"}}}'
    echo '{"method":"turn/started","params":{"threadId":"thread_smoke_session","turn":{"id":"turn_smoke_quick"}}}'
    echo '{"method":"item/completed","params":{"threadId":"thread_smoke_session","turnId":"turn_smoke_quick","item":{"type":"agentMessage","id":"item_smoke_reply","text":"smoke reply after restart"}}}'
    echo '{"method":"turn/completed","params":{"threadId":"thread_smoke_session","turn":{"id":"turn_smoke_quick","status":"completed"}}}'
    exit 0
  fi
done
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake codex smoke script: %v", err)
	}
	return scriptPath
}

func buildSmokeBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "codexsess-smoke")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "/home/ricki/workspaces/codexsess"
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build smoke binary: %v\n%s", err, string(output))
	}
	return binPath
}

func reserveLocalPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return port
}

func waitForSmokeApp(t *testing.T, baseURL string, logs *bytes.Buffer) {
	t.Helper()
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("app did not become healthy\nlogs:\n%s", logs.String())
}

func newSmokeHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 20 * time.Second,
	}
}

func smokeLogin(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	resp := smokePostJSON(t, client, baseURL+"/api/auth/login", map[string]any{
		"username": "admin",
		"password": "hijilabs",
	})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected login ok=true, got %#v", resp)
	}
}

func smokeGetJSON(t *testing.T, client *http.Client, rawURL string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeSmokeJSON(t, resp, rawURL)
}

func smokePostJSON(t *testing.T, client *http.Client, rawURL string, body map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal POST body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeSmokeJSON(t, resp, rawURL)
}

func decodeSmokeJSON(t *testing.T, resp *http.Response, rawURL string) map[string]any {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response body: %v", rawURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s returned %d: %s", rawURL, resp.StatusCode, string(body))
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s response: %v body=%s", rawURL, err, string(body))
	}
	return decoded
}

func mustSessions(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	items, _ := payload["sessions"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("expected session object, got %T", item)
		}
		out = append(out, row)
	}
	return out
}

func assertNoLegacySessionFields(t *testing.T, session map[string]any) {
	t.Helper()
	expectedKeys := map[string]struct{}{
		"id":                     {},
		"thread_id":              {},
		"title":                  {},
		"model":                  {},
		"reasoning_level":        {},
		"work_dir":               {},
		"sandbox_mode":           {},
		"last_applied_event_seq": {},
		"created_at":             {},
		"updated_at":             {},
		"last_message_at":        {},
	}
	for key := range session {
		if _, ok := expectedKeys[key]; !ok {
			t.Fatalf("expected compact session payload, found unexpected key %q in %#v", key, session)
		}
	}
}

func openSmokeWebSocket(t *testing.T, client *http.Client, baseURL, path string) *websocket.Conn {
	t.Helper()
	httpURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	wsURL := "ws://" + httpURL.Host + path
	reqHeader := http.Header{}
	cookies := client.Jar.Cookies(httpURL)
	if len(cookies) > 0 {
		parts := make([]string, 0, len(cookies))
		for _, cookie := range cookies {
			parts = append(parts, cookie.Name+"="+cookie.Value)
		}
		reqHeader.Set("Cookie", strings.Join(parts, "; "))
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, reqHeader)
	if err != nil {
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("dial websocket: %v body=%s", err, string(body))
		}
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readSmokeWSEvent(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set websocket read deadline: %v", err)
	}
	var payload map[string]any
	if err := conn.ReadJSON(&payload); err != nil {
		t.Fatalf("read websocket event: %v", err)
	}
	return payload
}

func waitForSessionFlightState(t *testing.T, client *http.Client, baseURL, sessionID string, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		payload := smokeGetJSON(t, client, baseURL+"/api/coding/status?session_id="+url.QueryEscape(sessionID))
		got, _ := payload["in_flight"].(bool)
		if got == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("session %s did not reach in_flight=%v", sessionID, want)
}

func waitForProcessExit(t *testing.T, cmd *exec.Cmd, logs *bytes.Buffer) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "killed") {
			return
		}
		if err != nil {
			t.Fatalf("app exit after cancel: %v\nlogs:\n%s", err, logs.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("timed out waiting for app exit\nlogs:\n%s", logs.String())
	}
}

func verifyMigratedSchemaState(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open migrated sqlite db: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`PRAGMA table_info(coding_sessions)`)
	if err != nil {
		t.Fatalf("query coding_sessions schema: %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns := map[string]struct{}{}
	for rows.Next() {
		var (
			cid        int
			name       string
			typeName   string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		columns[strings.TrimSpace(name)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma rows: %v", err)
	}
	for _, column := range []string{
		"legacy_enabled",
		"legacy_supervisor_thread_id",
		"legacy_executor_thread_id",
		"chat_needs_hydration",
		"chat_context_version",
		"last_hydrated_chat_context_version",
		"last_mode_transition_summary",
		"legacy_plan_artifact_path",
		"legacy_plan_updated_at",
	} {
		if _, ok := columns[column]; ok {
			t.Fatalf("expected migrated schema to drop legacy column %q", column)
		}
	}

	var codingSessionCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coding_sessions WHERE id=?`, "sess_legacy_chat_session").Scan(&codingSessionCount); err != nil {
		t.Fatalf("count legacy row after rebuild: %v", err)
	}
	if codingSessionCount != 1 {
		t.Fatalf("expected legacy coding session row to survive canonical-column rebuild, got count=%d", codingSessionCount)
	}

	var codingMessageCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coding_messages WHERE session_id=?`, "sess_legacy_chat_session").Scan(&codingMessageCount); err != nil {
		t.Fatalf("count legacy messages after child reset: %v", err)
	}
	if codingMessageCount != 0 {
		t.Fatalf("expected legacy coding messages to be cleared after child-table reset, got count=%d", codingMessageCount)
	}
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", m[key]))
}
