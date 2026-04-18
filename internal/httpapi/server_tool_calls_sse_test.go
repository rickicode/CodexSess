package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ricki/codexsess/internal/provider"
)

func TestParseToolCallsFromText_DropsMissingRequiredArguments(t *testing.T) {
	defs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	text := `{"tool_calls":[{"name":"Skill","arguments":{}}]}`
	calls, ok := defaultOpenAITranslator.ParseToolCallsFromText(text, defs)
	if ok {
		t.Fatalf("expected invalid tool call to be dropped")
	}
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(calls))
	}
}

func TestParseToolCallsFromText_WrappedJSON(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Function: ChatToolFunctionDef{Name: "navigate_page"}},
	}
	text := `{"tool_calls":[{"name":"navigate_page","arguments":{"page":1,"action":"url","url":"https://www.speedtest.net"}}]}`
	calls, ok := defaultOpenAITranslator.ParseToolCallsFromText(text, defs)
	if !ok {
		t.Fatalf("expected tool calls to parse")
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %s", calls[0].Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments must be valid json: %v", err)
	}
	if got, _ := args["url"].(string); !strings.Contains(got, "speedtest.net") {
		t.Fatalf("unexpected url argument: %q", got)
	}
}

func TestParseToolCallsFromText_RejectsUnknownTool(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Function: ChatToolFunctionDef{Name: "navigate_page"}},
	}
	text := `{"name":"delete_all","arguments":{"confirm":true}}`
	calls, ok := defaultOpenAITranslator.ParseToolCallsFromText(text, defs)
	if ok {
		t.Fatalf("expected parse to fail for unknown tool")
	}
	if len(calls) != 0 {
		t.Fatalf("expected no calls")
	}
}

func TestParseToolCallsFromText_AcceptsResponsesStyleToolDef(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Name: "navigate_page"},
	}
	text := `{"tool_calls":[{"name":"navigate_page","arguments":{"url":"https://example.com"}}]}`
	calls, ok := defaultOpenAITranslator.ParseToolCallsFromText(text, defs)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one parsed tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %q", calls[0].Function.Name)
	}
}

func TestParseToolCallsFromText_AcceptsToolCallsObject(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Name: "glob"},
	}
	text := `{"tool_calls":{"name":"glob","arguments":{"pattern":"./CLAUDE.md","path":"/home/ricki/.claude"}}}`
	calls, ok := defaultOpenAITranslator.ParseToolCallsFromText(text, defs)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one parsed tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "glob" {
		t.Fatalf("unexpected tool name: %q", calls[0].Function.Name)
	}
	if !strings.Contains(calls[0].Function.Arguments, "CLAUDE.md") {
		t.Fatalf("unexpected arguments: %q", calls[0].Function.Arguments)
	}
}

func TestParseToolCallsFromText_AcceptsConcatenatedJSONObjects(t *testing.T) {
	defs := []ChatToolDef{
		{Type: "function", Name: "glob"},
		{Type: "function", Name: "read"},
	}
	text := strings.Join([]string{
		`{"tool_calls":{"name":"glob","arguments":{"pattern":"./CLAUDE.md","path":"/home/ricki/.claude"}}}`,
		`{"tool_calls":{"name":"read","arguments":{"filePath":"/home/ricki/.claude/CLAUDE.md"}}}`,
	}, "")
	calls, ok := defaultOpenAITranslator.ParseToolCallsFromText(text, defs)
	if !ok {
		t.Fatalf("expected parse to succeed for concatenated objects")
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Function.Name != "glob" || calls[1].Function.Name != "read" {
		t.Fatalf("unexpected call order/names: %q then %q", calls[0].Function.Name, calls[1].Function.Name)
	}
}

func TestStreamChatCompletionToolCalls_SSEShape(t *testing.T) {
	rec := httptest.NewRecorder()
	calls := []ChatToolCall{
		{
			ID:   "call_abc",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "navigate_page",
				Arguments: `{"url":"https://example.com"}`,
			},
		},
	}
	streamChatCompletionToolCalls(
		rec,
		rec,
		"chatcmpl-test",
		"gpt-5.2-codex",
		calls,
		Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		false,
	)

	frames := collectSSEDataFrames(rec.Body.Bytes())
	if len(frames) < 4 {
		t.Fatalf("expected at least 4 SSE frames, got %d", len(frames))
	}
	if strings.TrimSpace(frames[len(frames)-1]) != "[DONE]" {
		t.Fatalf("expected final [DONE] frame, got %q", frames[len(frames)-1])
	}

	var firstRaw map[string]any
	if err := json.Unmarshal([]byte(frames[0]), &firstRaw); err != nil {
		t.Fatalf("decode first raw chunk: %v", err)
	}
	if _, ok := firstRaw["usage"]; !ok {
		t.Fatalf("expected usage field to be present in stream chunk")
	}
	if firstRaw["usage"] != nil {
		t.Fatalf("expected non-final chunk usage=null, got %#v", firstRaw["usage"])
	}

	var first ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[0]), &first); err != nil {
		t.Fatalf("decode first chunk: %v", err)
	}
	if first.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("expected first delta role assistant, got %q", first.Choices[0].Delta.Role)
	}

	var nameChunk ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[1]), &nameChunk); err != nil {
		t.Fatalf("decode name chunk: %v", err)
	}
	if len(nameChunk.Choices) == 0 || len(nameChunk.Choices[0].Delta.ToolCalls) == 0 {
		t.Fatalf("expected tool_calls in name chunk")
	}
	tc := nameChunk.Choices[0].Delta.ToolCalls[0]
	if tc.Index == nil || *tc.Index != 0 {
		t.Fatalf("expected tool_call index 0, got %+v", tc.Index)
	}
	if tc.ID != "call_abc" || tc.Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool call identity: id=%q name=%q", tc.ID, tc.Function.Name)
	}

	var argChunk ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[2]), &argChunk); err != nil {
		t.Fatalf("decode arg chunk: %v", err)
	}
	if len(argChunk.Choices) == 0 || len(argChunk.Choices[0].Delta.ToolCalls) == 0 {
		t.Fatalf("expected tool_calls in argument chunk")
	}
	if !strings.Contains(argChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments, "example.com") {
		t.Fatalf("unexpected arguments delta: %q", argChunk.Choices[0].Delta.ToolCalls[0].Function.Arguments)
	}

	var final ChatCompletionsChunk
	if err := json.Unmarshal([]byte(frames[len(frames)-2]), &final); err != nil {
		t.Fatalf("decode final chunk: %v", err)
	}
	if final.Choices[0].FinishReason == nil || *final.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected finish_reason tool_calls, got %+v", final.Choices[0].FinishReason)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 3 {
		t.Fatalf("expected usage in final chunk")
	}
}

func TestResponsesFunctionCallOutputItems_Shape(t *testing.T) {
	calls := []ChatToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			},
		},
	}
	items := responsesFunctionCallOutputItems(calls)
	if len(items) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(items))
	}
	item := items[0]
	if item.Type != "function_call" {
		t.Fatalf("expected type function_call, got %q", item.Type)
	}
	if item.CallID != "call_1" || item.Name != "read_file" {
		t.Fatalf("unexpected function_call identity: call_id=%q name=%q", item.CallID, item.Name)
	}
	if !strings.Contains(item.Arguments, "README.md") {
		t.Fatalf("unexpected arguments: %q", item.Arguments)
	}
}

func TestResponsesMessageOutputItems_ContainsAnnotationsArray(t *testing.T) {
	items := responsesMessageOutputItems("ok")
	if len(items) != 1 || len(items[0].Content) != 1 {
		t.Fatalf("unexpected output shape")
	}
	if items[0].Content[0].Type != "output_text" {
		t.Fatalf("unexpected content type: %q", items[0].Content[0].Type)
	}
	if items[0].Content[0].Annotations == nil {
		t.Fatalf("annotations must be present as array for compatibility")
	}
}

func TestExtractResponsesInput_HandlesFunctionCallItems(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"Use tool now"}]},
		{"type":"function_call","call_id":"call_abc","name":"navigate","arguments":{"url":"https://example.com"}},
		{"type":"function_call_output","call_id":"call_abc","output":{"ok":true}}
	]`)
	text := extractResponsesInput(raw)
	if !strings.Contains(text, "user: Use tool now") {
		t.Fatalf("expected user text in prompt, got %q", text)
	}
	if !strings.Contains(text, "assistant_tool_calls: navigate") {
		t.Fatalf("expected function call summary in prompt, got %q", text)
	}
	if !strings.Contains(text, "tool(call_abc):") {
		t.Fatalf("expected function output summary in prompt, got %q", text)
	}
}

func TestExtractOpenAIContentText_ArrayParts(t *testing.T) {
	raw := []any{
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
		map[string]any{"type": "text", "text": "world"},
	}
	got := extractOpenAIContentText(raw)
	if got != "hello\nworld" {
		t.Fatalf("unexpected extracted text: %q", got)
	}
}

func TestPromptFromMessages_AcceptsOpenAIContentParts(t *testing.T) {
	msgs := []ChatMessage{
		{
			Role: "system",
			Content: []any{
				map[string]any{"type": "text", "text": "system rules"},
			},
		},
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "please analyze file"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/b.png"}},
			},
		},
	}
	got := promptFromMessages(msgs)
	if !strings.Contains(got, "system: system rules") {
		t.Fatalf("missing system text in prompt: %q", got)
	}
	if !strings.Contains(got, "user: please analyze file") {
		t.Fatalf("missing user text in prompt: %q", got)
	}
	if strings.Contains(got, "image_url") {
		t.Fatalf("non-text part should not be injected verbatim into prompt: %q", got)
	}
}

func TestPromptFromClaudeMessages_EncodesToolSignals(t *testing.T) {
	msgs := []ClaudeMessage{
		{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"text","text":"I'll use a tool now"},
				{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"README.md"}}
			]`),
		},
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"file content"}]}
			]`),
		},
	}
	got := promptFromClaudeMessages(msgs)
	if !strings.Contains(got, "assistant_tool_calls: read_file(") {
		t.Fatalf("expected assistant tool calls in prompt, got %q", got)
	}
	if !strings.Contains(got, "tool(toolu_1): file content") {
		t.Fatalf("expected tool result line in prompt, got %q", got)
	}
}

func TestBuildClaudeResponseContent_ToolUse(t *testing.T) {
	content, stopReason := buildClaudeResponseContent(
		"",
		[]ChatToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: ChatToolFunctionCall{
					Name:      "navigate_page",
					Arguments: `{"url":"https://example.com"}`,
				},
			},
		},
	)
	if stopReason != "tool_use" {
		t.Fatalf("expected stop reason tool_use, got %q", stopReason)
	}
	if len(content) != 1 {
		t.Fatalf("expected single tool block, got %d", len(content))
	}
	if content[0].Type != "tool_use" || content[0].Name != "navigate_page" {
		t.Fatalf("unexpected content block: %+v", content[0])
	}
	input, ok := content[0].Input.(map[string]any)
	if !ok {
		t.Fatalf("expected tool input object, got %T", content[0].Input)
	}
	if got := strings.TrimSpace(coerceAnyText(input["url"])); got != "https://example.com" {
		t.Fatalf("unexpected tool input url: %q", got)
	}
}

func TestParseDirectResponseSSE_NativeFunctionCallWithoutText(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7},"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	res, err := parseDirectResponseSSE(strings.NewReader(sse), nil)
	if err != nil {
		t.Fatalf("parseDirectResponseSSE returned error: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 native tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool name: %q", res.ToolCalls[0].Function.Name)
	}
	if !strings.Contains(res.ToolCalls[0].Function.Arguments, "README.md") {
		t.Fatalf("unexpected tool args: %q", res.ToolCalls[0].Function.Arguments)
	}
	if res.InputTokens != 11 || res.OutputTokens != 7 {
		t.Fatalf("unexpected usage: in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
}

func TestParseDirectResponseSSE_NativeFunctionCallFromResponseDone(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.done","response":{"usage":{"input_tokens":5,"output_tokens":3},"output":[{"type":"function_call","id":"fc_2","call_id":"call_2","name":"navigate_page","arguments":"{\"url\":\"https://example.com\"}"}]}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	res, err := parseDirectResponseSSE(strings.NewReader(sse), nil)
	if err != nil {
		t.Fatalf("parseDirectResponseSSE returned error: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 native tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected tool name: %q", res.ToolCalls[0].Function.Name)
	}
	if res.InputTokens != 5 || res.OutputTokens != 3 {
		t.Fatalf("unexpected usage: in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
}

func TestResolveToolCalls_PrefersNative(t *testing.T) {
	defs := []ChatToolDef{{Type: "function", Name: "navigate_page"}}
	native := []ChatToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "navigate_page",
				Arguments: `{"url":"https://example.com"}`,
			},
		},
	}
	calls, ok := defaultOpenAITranslator.ResolveToolCalls(`plain text`, defs, native)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected native tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "navigate_page" {
		t.Fatalf("unexpected resolved tool name: %q", calls[0].Function.Name)
	}
}

func TestResolveToolCalls_PrefersProviderNative(t *testing.T) {
	defs := []ChatToolDef{{Type: "function", Name: "read_file"}}
	native := defaultOpenAITranslator.MapProviderToolCalls([]provider.ToolCall{
		{
			ID:        "call_1",
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	})
	calls, ok := defaultOpenAITranslator.ResolveToolCalls(`not json`, defs, native)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected provider-native tool call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool name: %q", calls[0].Function.Name)
	}
	if !strings.Contains(calls[0].Function.Arguments, "README.md") {
		t.Fatalf("unexpected tool args: %q", calls[0].Function.Arguments)
	}
}

func TestStreamResponsesText_EmitsOpenCodeRequiredFields(t *testing.T) {
	rec := httptest.NewRecorder()
	emit := func(event string, payload map[string]any) {
		writeSSE(rec, event, payload)
	}
	streamResponsesText(
		emit,
		"resp_test",
		"gpt-5.2-codex",
		"hello",
		ResponsesUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		1710000000,
	)

	frames := collectSSEDataFrames(rec.Body.Bytes())
	if len(frames) < 5 {
		t.Fatalf("expected at least 5 SSE frames, got %d", len(frames))
	}

	parsed := make([]map[string]any, 0, len(frames))
	for i := 0; i < len(frames); i++ {
		var evt map[string]any
		if err := json.Unmarshal([]byte(frames[i]), &evt); err != nil {
			t.Fatalf("decode frame[%d]: %v", i, err)
		}
		if _, exists := evt["response_id"]; exists {
			t.Fatalf("unexpected non-standard response_id field in frame[%d]: %+v", i, evt)
		}
		parsed = append(parsed, evt)
	}

	find := func(typ string) map[string]any {
		for _, evt := range parsed {
			if got, _ := evt["type"].(string); got == typ {
				return evt
			}
		}
		return nil
	}

	added := find("response.output_item.added")
	if added == nil {
		t.Fatalf("missing response.output_item.added event")
	}
	item, _ := added["item"].(map[string]any)
	itemID, _ := item["id"].(string)
	if strings.TrimSpace(itemID) == "" {
		t.Fatalf("expected item.id in output_item.added")
	}

	delta := find("response.output_text.delta")
	if delta == nil {
		t.Fatalf("missing response.output_text.delta event")
	}
	deltaItemID, _ := delta["item_id"].(string)
	if deltaItemID != itemID {
		t.Fatalf("expected response.output_text.delta.item_id=%q, got %q", itemID, deltaItemID)
	}

	textDone := find("response.output_text.done")
	if textDone == nil {
		t.Fatalf("missing response.output_text.done event")
	}
	if doneItemID, _ := textDone["item_id"].(string); doneItemID != itemID {
		t.Fatalf("expected response.output_text.done.item_id=%q, got %q", itemID, doneItemID)
	}

	itemDone := find("response.output_item.done")
	if itemDone == nil {
		t.Fatalf("missing response.output_item.done event")
	}

	completed := find("response.completed")
	if completed == nil {
		t.Fatalf("missing response.completed event")
	}
	if done := find("response.done"); done != nil {
		t.Fatalf("unexpected non-standard response.done event still emitted")
	}
	resp, _ := completed["response"].(map[string]any)
	if got, _ := resp["created_at"].(float64); int64(got) != 1710000000 {
		t.Fatalf("unexpected created_at in completed frame: %v", resp["created_at"])
	}
	usage, _ := resp["usage"].(map[string]any)
	if int(usage["input_tokens"].(float64)) != 1 || int(usage["output_tokens"].(float64)) != 2 {
		t.Fatalf("unexpected usage in completed frame: %+v", usage)
	}
	if got, _ := resp["output_text"].(string); got != "hello" {
		t.Fatalf("unexpected output_text in completed frame: %q", got)
	}
}

func TestWriteOpenAISSE_DataOnlyFrame(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOpenAISSE(rec, map[string]any{"type": "response.created"})

	raw := rec.Body.String()
	if strings.Contains(raw, "\nevent:") || strings.HasPrefix(raw, "event:") {
		t.Fatalf("expected data-only sse frame without event header, got %q", raw)
	}
	if !strings.Contains(raw, "data:") {
		t.Fatalf("expected data frame, got %q", raw)
	}
}

func collectSSEDataFrames(raw []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	frames := make([]string, 0, 8)
	dataLines := make([]string, 0, 4)
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		frames = append(frames, strings.TrimSpace(strings.Join(dataLines, "\n")))
		dataLines = dataLines[:0]
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	return frames
}

func TestOAuthBaseURLFromRequest_UsesRequestHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/auth/browser/start", nil)
	req.Host = "app.example.com:8443"

	base := oauthBaseURLFromRequest(req)
	if base != "http://app.example.com:8443" {
		t.Fatalf("expected base url from request host, got %q", base)
	}
}

func TestOAuthBaseURLFromRequest_UsesForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3061/api/auth/browser/start", nil)
	req.Host = "127.0.0.1:3061"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "codexsess.example.com")

	base := oauthBaseURLFromRequest(req)
	if base != "https://codexsess.example.com" {
		t.Fatalf("expected forwarded base url, got %q", base)
	}
}

func TestResolveSSEKeepAliveInterval_DefaultAndClamp(t *testing.T) {
	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "")
	if got := resolveSSEKeepAliveInterval(); got != 8*time.Second {
		t.Fatalf("expected default keepalive 8s, got %s", got)
	}

	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "1")
	if got := resolveSSEKeepAliveInterval(); got != 2*time.Second {
		t.Fatalf("expected min-clamped keepalive 2s, got %s", got)
	}

	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "99")
	if got := resolveSSEKeepAliveInterval(); got != 30*time.Second {
		t.Fatalf("expected max-clamped keepalive 30s, got %s", got)
	}
}

func TestResolveSSEKeepAliveInterval_ParsesValidValue(t *testing.T) {
	t.Setenv("CODEXSESS_SSE_KEEPALIVE_SECONDS", "12")
	if got := resolveSSEKeepAliveInterval(); got != 12*time.Second {
		t.Fatalf("expected keepalive 12s, got %s", got)
	}
}
