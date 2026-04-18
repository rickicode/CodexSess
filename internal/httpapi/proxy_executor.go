package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/util"
)

type proxyExecutor struct {
	svc                      *service.Service
	currentDirectAPIStrategy func() string
	shouldInjectDirectPrompt func() bool
	resolveAPIAccount        func(context.Context, string) (store.Account, error)
	findBestUsageAccount     func(context.Context, string) (store.Account, bool)
	markUsageLastError       func(context.Context, string, string)
	directRoundRobin         *atomic.Uint64
}

func newProxyExecutor(
	svc *service.Service,
	currentDirectAPIStrategy func() string,
	shouldInjectDirectPrompt func() bool,
	resolveAPIAccount func(context.Context, string) (store.Account, error),
	findBestUsageAccount func(context.Context, string) (store.Account, bool),
	markUsageLastError func(context.Context, string, string),
	directRoundRobin *atomic.Uint64,
) *proxyExecutor {
	return &proxyExecutor{
		svc:                      svc,
		currentDirectAPIStrategy: currentDirectAPIStrategy,
		shouldInjectDirectPrompt: shouldInjectDirectPrompt,
		resolveAPIAccount:        resolveAPIAccount,
		findBestUsageAccount:     findBestUsageAccount,
		markUsageLastError:       markUsageLastError,
		directRoundRobin:         directRoundRobin,
	}
}

func (e *proxyExecutor) resolveAPIAccountWithTokens(ctx context.Context, selector string) (store.Account, service.TokenSet, error) {
	var (
		account store.Account
		err     error
	)
	if strings.TrimSpace(selector) == "" {
		account, err = e.selectDirectAPIAccount(ctx)
	} else if e != nil && e.resolveAPIAccount != nil {
		account, err = e.resolveAPIAccount(ctx, selector)
	} else {
		err = fmt.Errorf("account resolver unavailable")
	}
	if err != nil {
		return store.Account{}, service.TokenSet{}, err
	}
	resolved, tk, err := e.svc.ResolveForRequest(ctx, account.ID)
	if err != nil {
		return store.Account{}, service.TokenSet{}, err
	}
	return resolved, tk, nil
}

func (e *proxyExecutor) selectDirectAPIAccount(ctx context.Context) (store.Account, error) {
	if e != nil && e.currentDirectAPIStrategy != nil && e.currentDirectAPIStrategy() == "load_balance" {
		return e.selectDirectAPIAccountByUsage(ctx)
	}
	return e.selectDirectAPIAccountRoundRobin(ctx)
}

func (e *proxyExecutor) selectDirectAPIAccountRoundRobin(ctx context.Context) (store.Account, error) {
	accounts, err := e.svc.ListAccounts(ctx)
	if err != nil {
		return store.Account{}, err
	}
	if len(accounts) == 0 {
		return store.Account{}, fmt.Errorf("account not found")
	}

	var next uint64 = 1
	if e != nil && e.directRoundRobin != nil {
		next = e.directRoundRobin.Add(1)
	}
	start := int((next - 1) % uint64(len(accounts)))
	var lastErr error
	for i := 0; i < len(accounts); i++ {
		idx := (start + i) % len(accounts)
		candidate := strings.TrimSpace(accounts[idx].ID)
		if candidate == "" {
			continue
		}
		account, resolveErr := e.resolveAPIAccount(ctx, candidate)
		if resolveErr == nil {
			return account, nil
		}
		lastErr = resolveErr
	}
	account, fallbackErr := e.resolveAPIAccount(ctx, "")
	if fallbackErr == nil {
		return account, nil
	}
	if lastErr != nil {
		return store.Account{}, lastErr
	}
	return store.Account{}, fallbackErr
}

func (e *proxyExecutor) selectDirectAPIAccountByUsage(ctx context.Context) (store.Account, error) {
	accounts, err := e.svc.ListAccounts(ctx)
	if err != nil {
		return store.Account{}, err
	}
	if len(accounts) == 0 {
		return store.Account{}, fmt.Errorf("account not found")
	}

	usageMap, _ := e.svc.Store.ListUsageSnapshots(ctx)
	type usageCandidate struct {
		id    string
		score int
	}
	candidates := make([]usageCandidate, 0, len(accounts))
	now := time.Now()
	for _, account := range accounts {
		id := strings.TrimSpace(account.ID)
		if id == "" {
			continue
		}
		usage, ok := usageMap[id]
		if !ok || strings.TrimSpace(usage.LastError) != "" || usage.FetchedAt.IsZero() {
			continue
		}
		if now.Sub(usage.FetchedAt) > autoSwitchUsageFreshness {
			continue
		}
		if !usageAvailable(usage) {
			continue
		}
		candidates = append(candidates, usageCandidate{id: id, score: usageScore(usage)})
	}
	if len(candidates) == 0 {
		return e.selectDirectAPIAccountRoundRobin(ctx)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].score > candidates[j].score
	})
	var lastErr error
	for _, candidate := range candidates {
		account, resolveErr := e.resolveAPIAccount(ctx, candidate.id)
		if resolveErr == nil {
			return account, nil
		}
		lastErr = resolveErr
	}
	account, fallbackErr := e.selectDirectAPIAccountRoundRobin(ctx)
	if fallbackErr == nil {
		return account, nil
	}
	if lastErr != nil {
		return store.Account{}, lastErr
	}
	return store.Account{}, fallbackErr
}

func (e *proxyExecutor) executeRequest(ctx context.Context, exec *proxyExecutionSession, prompt string, opts directCodexRequestOptions) (proxyBackendResult, error) {
	if e == nil || exec == nil {
		return proxyBackendResult{}, nil
	}
	res, err := e.executeDirect(ctx, "", exec, prompt, opts, nil, false)
	if err != nil {
		return proxyBackendResult{}, err
	}
	return proxyBackendResult{
		Text:         res.Text,
		InputTokens:  res.InputTokens,
		OutputTokens: res.OutputTokens,
		ToolCalls:    res.ToolCalls,
	}, nil
}

func (e *proxyExecutor) executeStream(ctx context.Context, exec *proxyExecutionSession, prompt string, opts directCodexRequestOptions, onDelta func(string) error, deltaVisible bool) (proxyBackendResult, error) {
	if e == nil || exec == nil {
		return proxyBackendResult{}, nil
	}
	res, err := e.executeDirect(ctx, "", exec, prompt, opts, onDelta, deltaVisible)
	if err != nil {
		return proxyBackendResult{}, err
	}
	return proxyBackendResult{
		Text:         res.Text,
		InputTokens:  res.InputTokens,
		OutputTokens: res.OutputTokens,
		ToolCalls:    res.ToolCalls,
	}, nil
}

func (e *proxyExecutor) executeDirect(ctx context.Context, selector string, exec *proxyExecutionSession, prompt string, opts directCodexRequestOptions, onDelta func(string) error, deltaVisible bool) (directAPIResult, error) {
	if e == nil || exec == nil {
		return directAPIResult{}, nil
	}
	return e.callDirectCodexResponsesAutoSwitch429(ctx, selector, &exec.Account, &exec.Tokens, exec.Model, prompt, opts, onDelta, deltaVisible)
}

func (e *proxyExecutor) callDirectCodexResponsesAutoSwitch429(
	ctx context.Context,
	selector string,
	account *store.Account,
	tk *service.TokenSet,
	model string,
	prompt string,
	opts directCodexRequestOptions,
	onDelta func(string) error,
	deltaVisible bool,
) (directAPIResult, error) {
	if account == nil || tk == nil {
		return directAPIResult{}, fmt.Errorf("direct_api account context is required")
	}
	streamEmitted := false
	trackedOnDelta := onDelta
	if onDelta != nil {
		trackedOnDelta = func(delta string) error {
			if deltaVisible && strings.TrimSpace(delta) != "" {
				streamEmitted = true
			}
			return onDelta(delta)
		}
	}
	res, err := e.callDirectCodexResponses(ctx, *account, *tk, model, prompt, opts, trackedOnDelta)
	if err == nil {
		return res, nil
	}
	if streamEmitted {
		return directAPIResult{}, err
	}
	if shouldRetryDirectAPIError(err) {
		log.Printf("[autoswitch] direct_api transient error, retrying once on same account %s: %v", strings.TrimSpace(account.ID), err)
		retryRes, retryErr := e.callDirectCodexResponses(ctx, *account, *tk, model, prompt, opts, trackedOnDelta)
		if retryErr == nil {
			return retryRes, nil
		}
		err = retryErr
	}
	if streamEmitted {
		return directAPIResult{}, err
	}
	if isDirectAPIRevokedError(err) {
		if e.markUsageLastError != nil {
			e.markUsageLastError(ctx, account.ID, err.Error())
		}
		if strings.TrimSpace(selector) != "" {
			return directAPIResult{}, err
		}
		best, ok := e.findBestUsageAccount(ctx, account.ID)
		if !ok {
			return directAPIResult{}, err
		}
		prevID := strings.TrimSpace(account.ID)
		switched, switchErr := e.svc.UseAccountAPI(service.WithAPISwitchReason(ctx, "autoswitch"), best.ID)
		if switchErr != nil {
			return directAPIResult{}, err
		}
		resolved, nextTokens, resolveErr := e.svc.ResolveForRequest(ctx, switched.ID)
		if resolveErr != nil {
			return directAPIResult{}, err
		}
		*account = resolved
		*tk = nextTokens
		log.Printf("[autoswitch] direct_api received revoked token error, switched API account from %s to %s and retrying once", prevID, resolved.ID)
		return e.callDirectCodexResponses(ctx, *account, *tk, model, prompt, opts, onDelta)
	}
	if strings.TrimSpace(selector) != "" || !isDirectAPIStatus(err, http.StatusTooManyRequests) {
		return directAPIResult{}, err
	}

	best, ok := e.findBestUsageAccount(ctx, account.ID)
	if !ok {
		return directAPIResult{}, err
	}
	prevID := strings.TrimSpace(account.ID)
	switched, switchErr := e.svc.UseAccountAPI(service.WithAPISwitchReason(ctx, "autoswitch"), best.ID)
	if switchErr != nil {
		return directAPIResult{}, err
	}
	resolved, nextTokens, resolveErr := e.svc.ResolveForRequest(ctx, switched.ID)
	if resolveErr != nil {
		return directAPIResult{}, err
	}
	*account = resolved
	*tk = nextTokens

	log.Printf("[autoswitch] direct_api received 429, switched API account from %s to %s and retrying once", prevID, resolved.ID)
	return e.callDirectCodexResponses(ctx, *account, *tk, model, prompt, opts, onDelta)
}

func (e *proxyExecutor) callDirectCodexResponses(
	ctx context.Context,
	account store.Account,
	tk service.TokenSet,
	model string,
	prompt string,
	opts directCodexRequestOptions,
	onDelta func(string) error,
) (directAPIResult, error) {
	claims, err := util.ParseClaims(tk.IDToken, tk.AccessToken)
	if err != nil {
		return directAPIResult{}, fmt.Errorf("parse oauth claims: %w", err)
	}
	accountID := strings.TrimSpace(claims.AccountID)
	if accountID == "" {
		accountID = strings.TrimSpace(account.AccountID)
	}

	payload := map[string]any{
		"model":  strings.TrimSpace(model),
		"store":  false,
		"stream": true,
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"text": map[string]any{
			"verbosity": "medium",
		},
		"include": []string{"reasoning.encrypted_content"},
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": strings.TrimSpace(prompt),
					},
				},
			},
		},
	}
	if e != nil && e.shouldInjectDirectPrompt != nil && e.shouldInjectDirectPrompt() {
		if instructions := strings.TrimSpace(resolveDirectAPIInstructions()); instructions != "" {
			payload["instructions"] = instructions
		}
	}
	if len(opts.StopSequences) > 0 {
		payload["stop"] = opts.StopSequences
	}
	if opts.MaxOutputTokens > 0 && !opts.ClaudeProtocol {
		payload["max_output_tokens"] = opts.MaxOutputTokens
	}
	if len(opts.Tools) > 0 {
		payload["tools"] = mapDirectCodexTools(opts.Tools)
	}
	if len(bytes.TrimSpace(opts.ToolChoice)) > 0 {
		var toolChoice any
		if json.Unmarshal(opts.ToolChoice, &toolChoice) == nil {
			payload["tool_choice"] = toolChoice
		}
	}
	if opts.TextFormat != nil {
		if formatPayload, err := openAIWireTranslator.ResponseFormatPayload(opts.TextFormat); err == nil && formatPayload != nil {
			payload["text"] = map[string]any{"format": formatPayload}
		}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return directAPIResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, directCodexBaseURL+"/codex/responses", bytes.NewReader(b))
	if err != nil {
		return directAPIResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(tk.AccessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if beta := strings.TrimSpace(resolveDirectAPIBetaHeader()); beta != "" {
		req.Header.Set("OpenAI-Beta", beta)
	}
	if opts.ClaudeProtocol {
		req.Header.Set("anthropic-version", normalizeAnthropicVersion(opts.AnthropicVer))
		if beta := strings.TrimSpace(resolveDirectAPIAnthropicBetaHeader()); beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
	}
	req.Header.Set("originator", "codex_cli_rs")
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}

	client := &http.Client{Timeout: resolveDirectAPITimeout()}
	resp, err := client.Do(req)
	if err != nil {
		return directAPIResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		return directAPIResult{}, &directAPIHTTPError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
	}
	return parseDirectResponseSSE(resp.Body, onDelta)
}

func parseDirectResponseSSE(r io.Reader, onDelta func(string) error) (directAPIResult, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 128*1024), 8*1024*1024)

	var res directAPIResult
	var dataLines []string

	flushFrame := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			return nil
		}
		eventType, _ := evt["type"].(string)
		eventType = strings.TrimSpace(strings.ToLower(eventType))

		switch eventType {
		case "response.output_text.delta":
			delta, _ := evt["delta"].(string)
			if strings.TrimSpace(delta) != "" {
				res.Text += delta
				if onDelta != nil {
					if err := onDelta(delta); err != nil {
						return err
					}
				}
			}
		case "response.completed", "response.done":
			response, _ := evt["response"].(map[string]any)
			if usage, _ := response["usage"].(map[string]any); usage != nil {
				res.InputTokens = int(anyNumber(usage["input_tokens"]))
				res.OutputTokens = int(anyNumber(usage["output_tokens"]))
			}
			if calls := extractFunctionCallsFromCompleted(response); len(calls) > 0 {
				res.ToolCalls = calls
			}
			if strings.TrimSpace(res.Text) == "" {
				res.Text = strings.TrimSpace(extractOutputTextFromCompleted(response))
			}
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if err := flushFrame(); err != nil {
				return directAPIResult{}, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := sc.Err(); err != nil {
		return directAPIResult{}, err
	}
	if err := flushFrame(); err != nil {
		return directAPIResult{}, err
	}
	if strings.TrimSpace(res.Text) == "" && len(res.ToolCalls) == 0 {
		return directAPIResult{}, fmt.Errorf("empty response from direct_api")
	}
	return res, nil
}

func extractOutputTextFromCompleted(response map[string]any) string {
	if response == nil {
		return ""
	}
	output, _ := response["output"].([]any)
	if len(output) == 0 {
		return ""
	}
	var parts []string
	for _, itemRaw := range output {
		item, _ := itemRaw.(map[string]any)
		if item == nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(asString(item["type"]))) != "message" {
			continue
		}
		content, _ := item["content"].([]any)
		for _, partRaw := range content {
			part, _ := partRaw.(map[string]any)
			if part == nil {
				continue
			}
			if strings.TrimSpace(strings.ToLower(asString(part["type"]))) != "output_text" {
				continue
			}
			text := strings.TrimSpace(asString(part["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractFunctionCallsFromCompleted(response map[string]any) []ChatToolCall {
	if response == nil {
		return nil
	}
	output, _ := response["output"].([]any)
	if len(output) == 0 {
		return nil
	}
	out := make([]ChatToolCall, 0, len(output))
	for _, itemRaw := range output {
		item, _ := itemRaw.(map[string]any)
		if item == nil {
			continue
		}
		if strings.TrimSpace(strings.ToLower(asString(item["type"]))) != "function_call" {
			continue
		}
		name := strings.TrimSpace(asString(item["name"]))
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(asString(item["call_id"]))
		if callID == "" {
			callID = strings.TrimSpace(asString(item["id"]))
		}
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(name, " ", "_")
		}
		args := coerceAnyJSON(item["arguments"])
		out = append(out, ChatToolCall{
			ID:   callID,
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func anyNumber(v any) float64 {
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

func resolveDirectAPITimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_DIRECT_API_TIMEOUT_SECONDS"))
	if raw == "" {
		return 180 * time.Second
	}
	sec, err := strconv.Atoi(raw)
	if err != nil {
		return 180 * time.Second
	}
	if sec < 30 {
		sec = 30
	}
	if sec > 600 {
		sec = 600
	}
	return time.Duration(sec) * time.Second
}

func resolveDirectAPIBetaHeader() string {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_DIRECT_API_BETA"))
	if raw == "" {
		return "responses=experimental"
	}
	if strings.EqualFold(raw, "off") || raw == "-" {
		return ""
	}
	return raw
}

func resolveDirectAPIAnthropicBetaHeader() string {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_DIRECT_API_ANTHROPIC_BETA"))
	if raw == "" {
		return "claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
	}
	if strings.EqualFold(raw, "off") || raw == "-" {
		return ""
	}
	return raw
}

func resolveDirectAPIInstructions() string {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_DIRECT_API_INSTRUCTIONS"))
	if raw == "" {
		return "You are Codex. Be concise, accurate, and focus on coding tasks. Use available context and respond directly."
	}
	if strings.EqualFold(raw, "off") || raw == "-" {
		return ""
	}
	return raw
}

func shouldRetryDirectAPIError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode >= 500 {
			return true
		}
		return httpErr.StatusCode == http.StatusRequestTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "eof")
}

func mapDirectCodexTools(defs []ChatToolDef) []map[string]any {
	if len(defs) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Function.Name)
		if name == "" {
			name = strings.TrimSpace(def.Name)
		}
		if name == "" {
			continue
		}
		tool := map[string]any{
			"type": "function",
			"name": name,
		}
		desc := strings.TrimSpace(def.Function.Description)
		if desc == "" {
			desc = strings.TrimSpace(def.Description)
		}
		if desc != "" {
			tool["description"] = desc
		}
		params := bytes.TrimSpace(def.Function.Parameters)
		if len(params) == 0 {
			params = bytes.TrimSpace(def.Parameters)
		}
		if len(params) > 0 {
			var parsed any
			if json.Unmarshal(params, &parsed) == nil {
				tool["parameters"] = parsed
			}
		}
		out = append(out, tool)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
