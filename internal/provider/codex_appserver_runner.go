package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type appServerRuntimeClient struct {
	binary    string
	codexHome string
	client    *appServerClient
}

type appServerRuntimeCache struct {
	mu      sync.Mutex
	clients map[string]*appServerRuntimeClient
}

var persistentAppServerRuntimeCache = &appServerRuntimeCache{
	clients: map[string]*appServerRuntimeClient{},
}

var appServerTurnCompletionIdleTimeout = 12 * time.Second

type appServerClient struct {
	cmd        *exec.Cmd
	stdin      *bufio.Writer
	sessionMu  sync.Mutex
	writeMu    sync.Mutex
	nextID     int64
	pendingMu  sync.Mutex
	pending    map[string]chan rpcResponse
	subMu      sync.Mutex
	subSeq     int64
	subs       map[int64]func(rpcEnvelope) error
	stderrMu   sync.Mutex
	stderrSeq  int64
	stderrSubs map[int64]func(string) error
	closed     atomic.Bool
}

type rpcResponse struct {
	Result json.RawMessage
	Err    error
}

type rpcEnvelope struct {
	ID     any             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type appServerThreadResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type appServerTurnResponse struct {
	Turn struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

type appServerTurnError struct {
	Message        string `json:"message"`
	CodexErrorInfo *struct {
		Code string `json:"code"`
	} `json:"codexErrorInfo,omitempty"`
	AdditionalDetails string `json:"additionalDetails,omitempty"`
}

type appServerEventMeta struct {
	SourceEventType string
	SourceThreadID  string
	SourceTurnID    string
	SourceItemID    string
	SourceItemType  string
	EventSeq        int64
	CreatedAt       string
}

func runAppServerChat(ctx context.Context, binary string, opts ExecOptions, onEvent func(ChatEvent) error) (ChatResult, error) {
	var out ChatResult
	err := withPersistentAppServerClient(ctx, binary, opts.CodexHome, firstNonEmpty(opts.WorkDir, defaultExecWorkDir(opts.CodexHome)), opts.OnProcessStart, func(client *appServerClient) error {
		threadID := strings.TrimSpace(opts.ResumeID)
		existingThreadID := strings.TrimSpace(opts.ThreadID)
		if threadID == "" && existingThreadID != "" {
			threadID = existingThreadID
		}
		var stderrSubID int64
		if onEvent != nil {
			stderrSubID = client.subscribeStderr(func(line string) error {
				text := strings.TrimSpace(sanitizeSensitiveText(line))
				if text == "" {
					return nil
				}
				return onEvent(newAppServerChatEvent("stderr", text, firstNonEmpty(strings.TrimSpace(opts.Actor), "executor"), appServerEventMeta{
					SourceEventType: "stderr",
				}))
			})
			defer client.unsubscribeStderr(stderrSubID)
		}
		var runErr error
		if strings.TrimSpace(opts.ResumeID) != "" {
			runErr = client.resumeThread(ctx, opts)
		} else if existingThreadID != "" {
			runErr = nil
		} else {
			threadID, runErr = client.startThread(ctx, opts)
		}
		if runErr != nil {
			return runErr
		}

		out.ThreadID = threadID
		actor := strings.TrimSpace(opts.Actor)
		if actor == "" {
			actor = "executor"
		}
		turnID, runErr := client.startTurn(ctx, threadID, opts, func(evt rpcEnvelope) error {
			params := map[string]any{}
			if len(evt.Params) > 0 {
				_ = json.Unmarshal(evt.Params, &params)
			}
			meta := extractAppServerEventMeta(evt.Method, params)
			if onEvent != nil {
				raw, _ := json.Marshal(evt)
				if err := onEvent(newAppServerChatEvent("raw_event", sanitizeSensitiveText(string(raw)), actor, meta)); err != nil {
					return err
				}
			}
			return mapAppServerEvent(evt, &out, actor, onEvent)
		})
		if runErr != nil {
			return runErr
		}
		if strings.TrimSpace(turnID) == "" {
			return errors.New("codex app-server did not return a turn id")
		}
		return nil
	})
	if err != nil {
		return ChatResult{}, err
	}
	return out, nil
}

func (c *CodexAppServer) AppServerStartThread(ctx context.Context, opts ExecOptions) (AppServerThread, error) {
	var threadID string
	err := withPersistentAppServerClient(ctx, c.Binary, opts.CodexHome, firstNonEmpty(opts.WorkDir, defaultExecWorkDir(opts.CodexHome)), opts.OnProcessStart, func(client *appServerClient) error {
		var runErr error
		threadID, runErr = client.startThread(ctx, opts)
		return runErr
	})
	if err != nil {
		return AppServerThread{}, err
	}
	return AppServerThread{ThreadID: threadID}, nil
}

func (c *CodexAppServer) AppServerResumeThread(ctx context.Context, opts ExecOptions) (AppServerThread, error) {
	err := withPersistentAppServerClient(ctx, c.Binary, opts.CodexHome, firstNonEmpty(opts.WorkDir, defaultExecWorkDir(opts.CodexHome)), opts.OnProcessStart, func(client *appServerClient) error {
		return client.resumeThread(ctx, opts)
	})
	if err != nil {
		return AppServerThread{}, err
	}
	return AppServerThread{ThreadID: strings.TrimSpace(opts.ResumeID)}, nil
}

func (c *CodexAppServer) AppServerChatWithOptions(ctx context.Context, opts ExecOptions) (ChatResult, error) {
	return runAppServerChat(ctx, c.Binary, opts, nil)
}

func (c *CodexAppServer) AppServerStreamChatWithOptions(ctx context.Context, opts ExecOptions, onEvent func(ChatEvent) error) (ChatResult, error) {
	return runAppServerChat(ctx, c.Binary, opts, onEvent)
}

func startAppServerClient(ctx context.Context, binary, codexHome, workDir string) (*appServerClient, error) {
	if strings.TrimSpace(binary) == "" {
		binary = "codex"
	}
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	if strings.TrimSpace(workDir) != "" {
		cmd.Dir = workDir
	}
	cmd.Env = buildAppServerEnv(cmd.Environ(), codexHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client := &appServerClient{
		cmd:        cmd,
		stdin:      bufio.NewWriter(stdin),
		pending:    map[string]chan rpcResponse{},
		subs:       map[int64]func(rpcEnvelope) error{},
		stderrSubs: map[int64]func(string) error{},
	}
	go client.readLoop(stdout)
	go client.drainAppServerStderr(stderr)
	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func withPersistentAppServerClient(
	ctx context.Context,
	binary, codexHome, workDir string,
	onProcessStart func(pid int, forceKill func() error),
	fn func(*appServerClient) error,
) error {
	client, created, err := persistentAppServerRuntimeCache.acquire(ctx, binary, codexHome, workDir)
	if err != nil {
		return err
	}
	client.sessionMu.Lock()
	defer client.sessionMu.Unlock()
	if onProcessStart != nil && client != nil && client.cmd != nil && client.cmd.Process != nil {
		onProcessStart(client.cmd.Process.Pid, client.Close)
	}
	err = fn(client)
	if err != nil {
		if created || persistentAppServerRuntimeCache.shouldResetOnError(err) {
			persistentAppServerRuntimeCache.discard(binary, codexHome, client)
		}
	}
	return err
}

func (c *appServerRuntimeCache) key(binary, codexHome string) string {
	return strings.TrimSpace(binary) + "|" + strings.TrimSpace(codexHome)
}

func (c *appServerRuntimeCache) acquire(ctx context.Context, binary, codexHome, workDir string) (*appServerClient, bool, error) {
	key := c.key(binary, codexHome)
	c.mu.Lock()
	entry := c.clients[key]
	if entry != nil && (entry.client == nil || entry.client.closed.Load()) {
		delete(c.clients, key)
		entry = nil
	}
	if entry == nil {
		entry = &appServerRuntimeClient{binary: strings.TrimSpace(binary), codexHome: strings.TrimSpace(codexHome)}
		c.clients[key] = entry
	}
	c.mu.Unlock()

	if entry.client == nil {
		client, err := startAppServerClient(ctx, binary, codexHome, workDir)
		if err != nil {
			c.discard(binary, codexHome, nil)
			return nil, true, err
		}
		entry.client = client
		return client, true, nil
	}
	return entry.client, false, nil
}

func (c *appServerRuntimeCache) discard(binary, codexHome string, client *appServerClient) {
	key := c.key(binary, codexHome)
	c.mu.Lock()
	entry := c.clients[key]
	if entry != nil && (client == nil || entry.client == client) {
		delete(c.clients, key)
	}
	c.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

func (c *appServerRuntimeCache) shouldResetOnError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "closed") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "eof") ||
		isTransientAppServerReconnectMessage(text)
}

func CloseAppServerClientsUnder(root string) {
	root = strings.TrimSpace(root)
	if root == "" {
		return
	}
	persistentAppServerRuntimeCache.mu.Lock()
	entries := make([]*appServerRuntimeClient, 0)
	keys := make([]string, 0)
	for key, entry := range persistentAppServerRuntimeCache.clients {
		if entry == nil || entry.client == nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(entry.codexHome), root) {
			keys = append(keys, key)
			entries = append(entries, entry)
		}
	}
	for _, key := range keys {
		delete(persistentAppServerRuntimeCache.clients, key)
	}
	persistentAppServerRuntimeCache.mu.Unlock()
	for _, entry := range entries {
		_ = entry.client.Close()
	}
}

func (c *appServerClient) Close() error {
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	return nil
}

func (c *appServerClient) initialize(ctx context.Context) error {
	var result struct {
		CodexHome string `json:"codexHome"`
	}
	if err := c.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "codexsess",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}, &result); err != nil {
		return err
	}
	return c.notify("initialized", nil)
}

func (c *appServerClient) startThread(ctx context.Context, opts ExecOptions) (string, error) {
	var result appServerThreadResponse
	if err := c.call(ctx, "thread/start", map[string]any{
		"cwd":                    firstNonEmpty(opts.WorkDir, defaultExecWorkDir(opts.CodexHome)),
		"model":                  strings.TrimSpace(opts.Model),
		"approvalPolicy":         "never",
		"sandbox":                "danger-full-access",
		"experimentalRawEvents":  true,
		"persistExtendedHistory": true,
	}, &result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Thread.ID), nil
}

func (c *appServerClient) resumeThread(ctx context.Context, opts ExecOptions) error {
	var result appServerThreadResponse
	return c.call(ctx, "thread/resume", map[string]any{
		"threadId":               strings.TrimSpace(opts.ResumeID),
		"cwd":                    firstNonEmpty(opts.WorkDir, defaultExecWorkDir(opts.CodexHome)),
		"model":                  strings.TrimSpace(opts.Model),
		"approvalPolicy":         "never",
		"sandbox":                "danger-full-access",
		"persistExtendedHistory": true,
	}, &result)
}

func (c *appServerClient) startTurn(ctx context.Context, threadID string, opts ExecOptions, onNotification func(rpcEnvelope) error) (string, error) {
	done := make(chan appServerTurnResponse, 1)
	errs := make(chan error, 1)
	turnID := atomic.Value{}
	turnID.Store("")
	var (
		idleMu             sync.Mutex
		idleTimer          *time.Timer
		assistantStarted   bool
		assistantCompleted bool
		currentTurnID      string
	)
	stopIdleTimer := func() {
		idleMu.Lock()
		defer idleMu.Unlock()
		if idleTimer != nil {
			idleTimer.Stop()
			idleTimer = nil
		}
	}
	scheduleIdleFallback := func() {
		timeout := appServerTurnCompletionIdleTimeout
		if timeout <= 0 {
			return
		}
		idleMu.Lock()
		defer idleMu.Unlock()
		if idleTimer != nil {
			idleTimer.Stop()
		}
		idleTimer = time.AfterFunc(timeout, func() {
			if strings.TrimSpace(currentTurnID) == "" {
				if id, ok := turnID.Load().(string); ok {
					currentTurnID = strings.TrimSpace(id)
				}
			}
			select {
			case done <- appServerTurnResponse{Turn: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{ID: strings.TrimSpace(currentTurnID), Status: "completed"}}:
			default:
			}
		})
	}
	subID := c.subscribe(func(evt rpcEnvelope) error {
		if onNotification != nil {
			if err := onNotification(evt); err != nil {
				select {
				case errs <- err:
				default:
				}
				return err
			}
		}
		switch strings.TrimSpace(evt.Method) {
		case "turn/started":
			var payload struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			if err := json.Unmarshal(evt.Params, &payload); err == nil && strings.TrimSpace(payload.ThreadID) == strings.TrimSpace(threadID) {
				currentTurnID = strings.TrimSpace(payload.Turn.ID)
				turnID.Store(currentTurnID)
			}
		case "turn/completed":
			stopIdleTimer()
			var payload struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"turn"`
			}
			if err := json.Unmarshal(evt.Params, &payload); err == nil && strings.TrimSpace(payload.ThreadID) == strings.TrimSpace(threadID) {
				currentTurnID = strings.TrimSpace(payload.Turn.ID)
				select {
				case done <- appServerTurnResponse{Turn: struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}{ID: strings.TrimSpace(payload.Turn.ID), Status: strings.TrimSpace(payload.Turn.Status)}}:
				default:
				}
			}
		case "error":
			stopIdleTimer()
			var payload struct {
				ThreadID string             `json:"threadId"`
				Error    appServerTurnError `json:"error"`
			}
			if err := json.Unmarshal(evt.Params, &payload); err == nil && strings.TrimSpace(payload.ThreadID) == strings.TrimSpace(threadID) {
				select {
				case errs <- fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(payload.Error.Message), "codex app-server turn failed")):
				default:
				}
			}
		case "item/agentMessage/delta":
			var payload struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			if err := json.Unmarshal(evt.Params, &payload); err == nil && strings.TrimSpace(payload.ThreadID) == strings.TrimSpace(threadID) {
				assistantStarted = true
				if currentTurnID == "" {
					currentTurnID = strings.TrimSpace(payload.TurnID)
				}
				scheduleIdleFallback()
			}
		case "item/completed", "rawResponseItem/completed":
			var payload struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Item     struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if err := json.Unmarshal(evt.Params, &payload); err == nil && strings.TrimSpace(payload.ThreadID) == strings.TrimSpace(threadID) {
				if currentTurnID == "" {
					currentTurnID = strings.TrimSpace(payload.TurnID)
				}
				switch normalizeAppServerItemType(payload.Item.Type) {
				case "agentmessage", "agent_message":
					assistantStarted = true
					assistantCompleted = true
					scheduleIdleFallback()
				default:
					if assistantStarted || assistantCompleted {
						scheduleIdleFallback()
					}
				}
			}
		case "thread/tokenUsage/updated":
			var payload struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			if err := json.Unmarshal(evt.Params, &payload); err == nil && strings.TrimSpace(payload.ThreadID) == strings.TrimSpace(threadID) && (assistantStarted || assistantCompleted) {
				if currentTurnID == "" {
					currentTurnID = strings.TrimSpace(payload.TurnID)
				}
				scheduleIdleFallback()
			}
		}
		return nil
	})
	defer c.unsubscribe(subID)
	defer stopIdleTimer()

	var result appServerTurnResponse
	err := c.call(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{
				"type":          "text",
				"text":          strings.TrimSpace(opts.Prompt),
				"text_elements": []map[string]any{},
			},
		},
		"model":  strings.TrimSpace(opts.Model),
		"effort": normalizeReasoningEffort(opts.ReasoningEffort),
	}, &result)
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(result.Turn.ID); id != "" {
		turnID.Store(id)
	}
	select {
	case completed := <-done:
		if id := strings.TrimSpace(completed.Turn.ID); id != "" {
			return id, nil
		}
		return strings.TrimSpace(turnID.Load().(string)), nil
	case err := <-errs:
		return strings.TrimSpace(turnID.Load().(string)), err
	case <-ctx.Done():
		currentTurnID, _ := turnID.Load().(string)
		if strings.TrimSpace(currentTurnID) != "" {
			_ = c.interruptTurn(context.Background(), threadID, currentTurnID)
		}
		return strings.TrimSpace(currentTurnID), ctx.Err()
	}
}

func (c *appServerClient) interruptTurn(ctx context.Context, threadID, turnID string) error {
	var result map[string]any
	return c.call(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, &result)
}

func (c *appServerClient) call(ctx context.Context, method string, params any, out any) error {
	id := fmt.Sprintf("%d", atomic.AddInt64(&c.nextID, 1))
	respCh := make(chan rpcResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()
	if err := c.send(rpcEnvelope{ID: id, Method: method, Params: mustJSON(params)}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return err
	}
	select {
	case resp := <-respCh:
		if resp.Err != nil {
			return resp.Err
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return err
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *appServerClient) notify(method string, params any) error {
	return c.send(rpcEnvelope{Method: method, Params: mustJSON(params)})
}

func (c *appServerClient) send(msg rpcEnvelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed.Load() {
		return errors.New("app-server client closed")
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(payload); err != nil {
		return err
	}
	if err := c.stdin.WriteByte('\n'); err != nil {
		return err
	}
	return c.stdin.Flush()
}

func (c *appServerClient) readLoop(stdout io.ReadCloser) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var env rpcEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		if env.ID != nil && env.Method == "" {
			key := fmt.Sprintf("%v", env.ID)
			c.pendingMu.Lock()
			ch := c.pending[key]
			delete(c.pending, key)
			c.pendingMu.Unlock()
			if ch != nil {
				if env.Error != nil {
					ch <- rpcResponse{Err: fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(env.Error.Message), "codex app-server request failed"))}
				} else {
					ch <- rpcResponse{Result: env.Result}
				}
			}
			continue
		}
		if strings.TrimSpace(env.Method) != "" {
			for _, handler := range c.subscribers() {
				_ = handler(env)
			}
		}
	}
	c.closed.Store(true)
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for key, ch := range c.pending {
		delete(c.pending, key)
		ch <- rpcResponse{Err: errors.New("codex app-server closed")}
	}
}

func (c *appServerClient) subscribe(fn func(rpcEnvelope) error) int64 {
	if fn == nil {
		return 0
	}
	id := atomic.AddInt64(&c.subSeq, 1)
	c.subMu.Lock()
	c.subs[id] = fn
	c.subMu.Unlock()
	return id
}

func (c *appServerClient) unsubscribe(id int64) {
	if id == 0 {
		return
	}
	c.subMu.Lock()
	delete(c.subs, id)
	c.subMu.Unlock()
}

func (c *appServerClient) subscribeStderr(fn func(string) error) int64 {
	if fn == nil {
		return 0
	}
	id := atomic.AddInt64(&c.stderrSeq, 1)
	c.stderrMu.Lock()
	c.stderrSubs[id] = fn
	c.stderrMu.Unlock()
	return id
}

func (c *appServerClient) unsubscribeStderr(id int64) {
	if id == 0 {
		return
	}
	c.stderrMu.Lock()
	delete(c.stderrSubs, id)
	c.stderrMu.Unlock()
}

func (c *appServerClient) stderrSubscribers() []func(string) error {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	out := make([]func(string) error, 0, len(c.stderrSubs))
	for _, fn := range c.stderrSubs {
		out = append(out, fn)
	}
	return out
}

func (c *appServerClient) subscribers() []func(rpcEnvelope) error {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	out := make([]func(rpcEnvelope) error, 0, len(c.subs))
	for _, fn := range c.subs {
		out = append(out, fn)
	}
	return out
}

func mapAppServerEvent(evt rpcEnvelope, out *ChatResult, actor string, onEvent func(ChatEvent) error) error {
	method := strings.TrimSpace(evt.Method)
	if method == "" {
		return nil
	}
	eventActor := strings.TrimSpace(actor)
	if eventActor == "" {
		eventActor = "executor"
	}
	emitted := false
	params := map[string]any{}
	if len(evt.Params) > 0 {
		_ = json.Unmarshal(evt.Params, &params)
	}
	meta := extractAppServerEventMeta(method, params)
	mergedEvent := appServerEventMap(method, params)
	switch method {
	case "thread/started":
		thread, _ := params["thread"].(map[string]any)
		if tid, _ := thread["id"].(string); strings.TrimSpace(tid) != "" {
			out.ThreadID = strings.TrimSpace(tid)
		}
	case "item/agentMessage/delta":
		if delta, _ := params["delta"].(string); strings.TrimSpace(delta) != "" {
			out.Text += delta
			if onEvent != nil {
				emitted = true
				return onEvent(newAppServerChatEvent("delta", delta, eventActor, meta))
			}
		}
	case "item/completed", "rawResponseItem/completed":
		text := extractAppServerAssistantText(params)
		if text != "" {
			if len(out.Messages) > 0 && strings.TrimSpace(out.Messages[len(out.Messages)-1]) == strings.TrimSpace(text) {
				out.Text = text
				return nil
			}
			out.Messages = append(out.Messages, text)
			out.Text = text
			if onEvent != nil {
				emitted = true
				return onEvent(newAppServerChatEvent("assistant_message", text, eventActor, meta))
			}
		}
	case "thread/tokenUsage/updated":
		usage, _ := params["tokenUsage"].(map[string]any)
		last, _ := usage["last"].(map[string]any)
		out.InputTokens = int(number(last["inputTokens"]))
		out.CachedInputTokens = int(number(last["cachedInputTokens"]))
		out.OutputTokens = int(number(last["outputTokens"]))
	case "error":
		errText := strings.TrimSpace(extractAppServerError(params))
		if isTransientAppServerReconnectMessage(errText) {
			if onEvent != nil {
				return onEvent(newAppServerChatEvent("activity", firstNonEmpty(errText, "codex app-server reconnecting"), eventActor, meta))
			}
			return nil
		}
		return fmt.Errorf("codex app-server error: %s", errText)
	}
	if delta, ok := codexEventDeltaText(mergedEvent); ok {
		out.Text += delta
		if onEvent != nil {
			emitted = true
			return onEvent(newAppServerChatEvent("delta", delta, eventActor, meta))
		}
	}
	if onEvent != nil {
		if text, ok := codexEventActivityText(mergedEvent); ok {
			emitted = true
			return onEvent(newAppServerChatEvent("activity", text, eventActor, meta))
		}
	}
	if shouldSuppressAppServerSummary(method) {
		return nil
	}
	if onEvent != nil && !emitted {
		switch method {
		case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta", "item/plan/delta", "item/commandExecution/outputDelta", "item/fileChange/outputDelta", "item/mcpToolCall/progress", "thread/status/changed", "turn/started", "turn/completed", "model/rerouted", "configWarning", "deprecationNotice", "mcpServer/startupStatus/updated", "serverRequest/resolved":
			summary := summarizeAppServerEvent(method, params)
			if strings.TrimSpace(summary) == "" {
				return nil
			}
			emitted = true
			return onEvent(newAppServerChatEvent("activity", summary, eventActor, meta))
		}
	}
	if onEvent != nil && !emitted {
		summary := truncateActivityText(sanitizeSensitiveText(summarizeAppServerEvent(method, params)))
		if strings.TrimSpace(summary) != "" {
			return onEvent(newAppServerChatEvent("activity", summary, eventActor, meta))
		}
	}
	return nil
}

func newAppServerChatEvent(eventType, text, actor string, meta appServerEventMeta) ChatEvent {
	return ChatEvent{
		Type:            eventType,
		Text:            text,
		Actor:           actor,
		SourceEventType: meta.SourceEventType,
		SourceThreadID:  meta.SourceThreadID,
		SourceTurnID:    meta.SourceTurnID,
		SourceItemID:    meta.SourceItemID,
		SourceItemType:  meta.SourceItemType,
		EventSeq:        meta.EventSeq,
		CreatedAt:       meta.CreatedAt,
	}
}

func extractAppServerEventMeta(method string, params map[string]any) appServerEventMeta {
	thread, _ := params["thread"].(map[string]any)
	turn, _ := params["turn"].(map[string]any)
	item, _ := params["item"].(map[string]any)
	return appServerEventMeta{
		SourceEventType: strings.TrimSpace(method),
		SourceThreadID:  firstNonEmpty(firstStringFromMap(params, "threadId"), firstStringFromMap(thread, "id")),
		SourceTurnID:    firstNonEmpty(firstStringFromMap(params, "turnId"), firstStringFromMap(turn, "id")),
		SourceItemID:    firstNonEmpty(firstStringFromMap(params, "itemId"), firstStringFromMap(item, "id")),
		SourceItemType:  firstNonEmpty(firstStringFromMap(params, "itemType"), firstStringFromMap(item, "type")),
		EventSeq: firstNonZeroInt64(
			firstInt64FromMap(params, "sequence", "seq", "eventSeq", "event_seq"),
			firstInt64FromMap(item, "sequence", "seq", "eventSeq", "event_seq"),
			firstInt64FromMap(turn, "sequence", "seq", "eventSeq", "event_seq"),
			firstInt64FromMap(thread, "sequence", "seq", "eventSeq", "event_seq"),
		),
		CreatedAt: firstNonEmpty(
			firstStringFromMap(params, "createdAt", "created_at"),
			firstStringFromMap(item, "createdAt", "created_at"),
			firstStringFromMap(turn, "createdAt", "created_at"),
			firstStringFromMap(thread, "createdAt", "created_at"),
		),
	}
}

func firstInt64FromMap(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		value, ok := m[key]
		if !ok {
			continue
		}
		switch t := value.(type) {
		case int:
			return int64(t)
		case int64:
			return t
		case float64:
			return int64(t)
		case json.Number:
			if n, err := t.Int64(); err == nil {
				return n
			}
		case string:
			if n, err := json.Number(strings.TrimSpace(t)).Int64(); err == nil {
				return n
			}
		}
	}
	return 0
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func isTransientAppServerReconnectMessage(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "reconnecting...") ||
		strings.Contains(normalized, "reconnecting ") ||
		(strings.Contains(normalized, "reconnect") && strings.Contains(normalized, "/5")) ||
		(strings.Contains(normalized, "reconnect") && strings.Contains(normalized, "retry"))
}

func shouldSuppressAppServerSummary(method string) bool {
	switch strings.TrimSpace(method) {
	case "item/started", "item/completed", "rawResponseItem/completed", "thread/started", "thread/tokenUsage/updated", "item/fileChange/outputDelta", "turn/diff/updated":
		return true
	default:
		return false
	}
}

func appServerEventMap(method string, params map[string]any) map[string]any {
	out := make(map[string]any, len(params)+1)
	out["type"] = method
	for key, value := range params {
		out[key] = value
	}
	return out
}

func summarizeAppServerEvent(method string, params map[string]any) string {
	item, _ := params["item"].(map[string]any)
	itemType := strings.ToLower(strings.TrimSpace(firstStringFromMap(item, "type")))
	switch method {
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		return ""
	case "item/plan/delta":
		return ""
	case "item/commandExecution/outputDelta":
		delta := strings.TrimSpace(fmt.Sprintf("%v", params["delta"]))
		switch strings.ToLower(delta) {
		case "", "<nil>", "null", "{}", "[]":
			return ""
		default:
			return strings.TrimSpace("Command output: " + delta)
		}
	case "item/fileChange/outputDelta":
		return ""
	case "item/mcpToolCall/progress":
		return strings.TrimSpace(fmt.Sprintf("MCP progress: %v", mustJSONString(params)))
	case "thread/status/changed":
		return ""
	case "turn/started":
		return ""
	case "turn/completed":
		return ""
	case "model/rerouted":
		return strings.TrimSpace(fmt.Sprintf("Model rerouted: %v", mustJSONString(params)))
	case "configWarning":
		return strings.TrimSpace(fmt.Sprintf("Config warning: %v", mustJSONString(params)))
	case "deprecationNotice":
		return strings.TrimSpace(fmt.Sprintf("Deprecation notice: %v", mustJSONString(params)))
	case "mcpServer/startupStatus/updated":
		status := strings.ToLower(strings.TrimSpace(firstStringFromMap(params, "status")))
		if status == "ready" || status == "starting" {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("MCP server status: %v", mustJSONString(params)))
	case "account/rateLimits/updated":
		return ""
	case "item/started", "item/completed", "item/updated", "rawResponseItem/completed":
		switch itemType {
		case "usermessage", "message", "reasoning", "agentmessage", "file_change", "file_read":
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("%s: %s", method, mustJSONString(params)))
	case "turn/diff/updated":
		return ""
	case "serverRequest/resolved":
		return strings.TrimSpace(fmt.Sprintf("Server request resolved: %v", mustJSONString(params)))
	default:
		return strings.TrimSpace(fmt.Sprintf("%s: %s", method, mustJSONString(params)))
	}
}

func extractAppServerAssistantText(params map[string]any) string {
	item, _ := params["item"].(map[string]any)
	itemType := normalizeAppServerItemType(firstStringFromMap(item, "type"))
	itemRole := strings.ToLower(strings.TrimSpace(firstStringFromMap(item, "role")))
	acceptText := false
	switch itemType {
	case "agentmessage":
		acceptText = true
	case "message":
		acceptText = itemRole == "assistant"
	}
	if acceptText {
		if text, _ := item["text"].(string); strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	if content, ok := item["content"].([]any); ok {
		parts := make([]string, 0, len(content))
		for _, entry := range content {
			row, _ := entry.(map[string]any)
			if row == nil {
				continue
			}
			rowType, _ := row["type"].(string)
			rowType = strings.TrimSpace(strings.ToLower(rowType))
			switch rowType {
			case "output_text":
				if text, _ := row["text"].(string); strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			case "text":
				if acceptText {
					if text, _ := row["text"].(string); strings.TrimSpace(text) != "" {
						parts = append(parts, strings.TrimSpace(text))
					}
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}
	return ""
}

func extractAppServerError(params map[string]any) string {
	errInfo, _ := params["error"].(map[string]any)
	if message, _ := errInfo["message"].(string); strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	return strings.TrimSpace(mustJSONString(params))
}

func buildAppServerEnv(base []string, codexHome string) []string {
	env := make([]string, 0, len(base)+2)
	hasCodexHome := false
	for _, item := range base {
		if strings.HasPrefix(item, "CODEX_HOME=") {
			hasCodexHome = true
			if strings.TrimSpace(codexHome) == "" {
				continue
			}
			env = append(env, "CODEX_HOME="+strings.TrimSpace(codexHome))
			continue
		}
		env = append(env, item)
	}
	if !hasCodexHome && strings.TrimSpace(codexHome) != "" {
		env = append(env, "CODEX_HOME="+strings.TrimSpace(codexHome))
	}
	return env
}

func (c *appServerClient) drainAppServerStderr(rc io.ReadCloser) {
	defer func() { _ = rc.Close() }()
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 16*1024), 2*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sanitizeSensitiveText(sc.Text()))
		if line == "" {
			continue
		}
		for _, fn := range c.stderrSubscribers() {
			if fn == nil {
				continue
			}
			_ = fn(line)
		}
	}
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func mustJSONString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
