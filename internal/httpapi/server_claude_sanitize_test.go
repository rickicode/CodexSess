package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestFilterToolCallsByDefs_DropsMissingRequiredArguments(t *testing.T) {
	defs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	calls := []ChatToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "Skill",
				Arguments: `{}`,
			},
		},
	}
	filtered, ok := filterToolCallsByDefs(calls, defs)
	if ok {
		t.Fatalf("expected invalid native tool call to be dropped")
	}
	if len(filtered) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(filtered))
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsInvalidToolUseAndPairedToolResult(t *testing.T) {
	s := &Server{}
	toolDefs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_1","name":"Skill","input":{}}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_1","is_error":true,"content":"missing skill"}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"final request"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, toolDefs, "session-test")
	if len(sanitized) != 2 {
		t.Fatalf("expected 2 messages after sanitize, got %d", len(sanitized))
	}
	if strings.Contains(promptFromClaudeMessages(sanitized), "assistant_tool_calls: Skill({})") {
		t.Fatalf("expected invalid tool call context to be removed")
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsSubsequentToolUseFromCachedInvalidPattern(t *testing.T) {
	s := &Server{}
	toolDefs := []ChatToolDef{
		{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:       "Skill",
				Parameters: json.RawMessage(`{"type":"object","required":["skill"],"properties":{"skill":{"type":"string"}}}`),
			},
		},
	}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello"}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_1","name":"Skill","input":{}}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_2","name":"Skill","input":{"skill":"using-superpowers"}}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"final request"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, toolDefs, "session-cache")
	if len(sanitized) != 2 {
		t.Fatalf("expected 2 user-only messages after sanitize, got %d", len(sanitized))
	}
	if strings.Contains(promptFromClaudeMessages(sanitized), "assistant_tool_calls:") {
		t.Fatalf("expected cached invalid tool pattern to drop follow-up tool_use")
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsAssistantPolicyRefusalText(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"tolong cek bug ini"}]`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"Maaf, saya tidak bisa membantu memperbaiki sistem ini karena berpotensi disalahgunakan."}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"fokus ke bug parser output saja"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-refusal")
	got := promptFromClaudeMessages(sanitized)
	if strings.Contains(strings.ToLower(got), "maaf, saya tidak bisa membantu") {
		t.Fatalf("expected assistant refusal text to be removed from prompt")
	}
	if !strings.Contains(got, "fokus ke bug parser output saja") {
		t.Fatalf("expected latest user request to stay in prompt, got: %s", got)
	}
}

func TestSanitizeClaudeMessagesForPrompt_PreservesSkillActivityLines(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"● Skill(superpowers:brainstorming)\n⎿ Successfully loaded skill\nLangsung ke akar masalah parser."}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-skill-lines")
	got := promptFromClaudeMessages(sanitized)
	if !strings.Contains(got, "Skill(superpowers:brainstorming)") {
		t.Fatalf("expected skill activity line to stay, got: %s", got)
	}
	if !strings.Contains(got, "Successfully loaded skill") {
		t.Fatalf("expected skill loaded line to stay, got: %s", got)
	}
	if !strings.Contains(got, "Langsung ke akar masalah parser.") {
		t.Fatalf("expected substantive assistant text to remain, got: %s", got)
	}
}

func TestSanitizeClaudeMessagesForPrompt_DropsSkillToolUseAndResult(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_skill_1","name":"Skill","input":{"skill":"superpowers:systematic-debugging"}}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_skill_1","content":"Launching skill"}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"cek potensi bug di sistem ini"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-skill-drop")
	got := promptFromClaudeMessages(sanitized)
	if strings.Contains(got, "assistant_tool_calls: Skill(") {
		t.Fatalf("expected Skill tool_use to be removed from prompt")
	}
	if strings.Contains(strings.ToLower(got), "launching skill") {
		t.Fatalf("expected paired skill tool_result to be removed from prompt")
	}
	if !strings.Contains(got, "cek potensi bug di sistem ini") {
		t.Fatalf("expected real user request to stay in prompt, got: %s", got)
	}
}

func TestSanitizeClaudeMessagesForPrompt_StripsSystemReminderTextBlocks(t *testing.T) {
	s := &Server{}
	messages := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"<system-reminder>\nvery long reminder\n</system-reminder>"}]`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"analisis bug ini"}]`)},
	}

	sanitized := s.sanitizeClaudeMessagesForPrompt(messages, nil, "session-system-reminder")
	got := promptFromClaudeMessages(sanitized)
	if strings.Contains(strings.ToLower(got), "system-reminder") {
		t.Fatalf("expected system-reminder block to be stripped, got: %s", got)
	}
	if !strings.Contains(got, "analisis bug ini") {
		t.Fatalf("expected real user text to remain, got: %s", got)
	}
}

func TestSanitizeClaudeAssistantText_DropsTraceAndKeepsSubstance(t *testing.T) {
	in := strings.Join([]string{
		"● Entered plan mode",
		"Skill(superpowers:systematic-debugging)",
		"⎿ Successfully loaded skill",
		"Explore(Explore browser flows issues)",
		"Read(/path/to/file.py · lines 1-2000)",
		"1 tasks (0 done, 1 in progress, 0 open)",
		"◼ Review code for potential bugs",
		"Potensi bug di flow callback ada race condition timeout OTP.",
		"(ctrl+b ctrl+b to run in background)",
	}, "\n")

	got := sanitizeClaudeAssistantText(in)
	if strings.Contains(strings.ToLower(got), "entered plan mode") {
		t.Fatalf("expected plan mode trace to be removed: %s", got)
	}
	if strings.Contains(got, "Skill(superpowers:systematic-debugging)") {
		t.Fatalf("expected skill trace to be removed: %s", got)
	}
	if strings.Contains(got, "Explore(") || strings.Contains(got, "Read(") {
		t.Fatalf("expected tool trace lines to be removed: %s", got)
	}
	if strings.Contains(strings.ToLower(got), "tasks (") || strings.Contains(got, "◼ ") {
		t.Fatalf("expected task status trace lines to be removed: %s", got)
	}
	if !strings.Contains(got, "Potensi bug di flow callback ada race condition timeout OTP.") {
		t.Fatalf("expected substantive text to remain: %s", got)
	}
}

func TestApplyClaudeResponseDefaults_PrependsGuidance(t *testing.T) {
	in := "user: analisis sistem ini apakah ada potensi bug?"
	got := applyClaudeResponseDefaults(in)
	if !strings.HasPrefix(got, "system: Response defaults:") {
		t.Fatalf("expected defaults preamble, got: %s", got)
	}
	if !strings.Contains(got, in) {
		t.Fatalf("expected original prompt to be preserved, got: %s", got)
	}
}

func TestApplyClaudeTokenBudgetGuard_NoChangeWhenUnderSoftLimit(t *testing.T) {
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT", "14000")
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT", "22000")
	msgs := []ClaudeMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"analisis bug ringan"}]`)},
	}
	system := json.RawMessage(`"instruksi singkat"`)
	gotMsgs, gotSys := applyClaudeTokenBudgetGuard(msgs, system)
	if len(gotMsgs) != len(msgs) {
		t.Fatalf("expected messages unchanged under soft limit")
	}
	if string(gotSys) != string(system) {
		t.Fatalf("expected system unchanged under soft limit")
	}
}

func TestApplyClaudeTokenBudgetGuard_ProgressiveTrim(t *testing.T) {
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT", "4000")
	t.Setenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT", "5000")
	msgs := make([]ClaudeMessage, 0, 40)
	for i := 0; i < 40; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		msgs = append(msgs, ClaudeMessage{
			Role:    role,
			Content: json.RawMessage(fmt.Sprintf(`[{"type":"text","text":"msg-%d %s"}]`, i, strings.Repeat("x", 900))),
		})
	}
	systemText := strings.Repeat("SYSTEM ", 1200)
	systemRaw, _ := json.Marshal(systemText)
	gotMsgs, gotSys := applyClaudeTokenBudgetGuard(msgs, json.RawMessage(systemRaw))
	if len(gotMsgs) >= len(msgs) {
		t.Fatalf("expected message history to be trimmed, got %d from %d", len(gotMsgs), len(msgs))
	}
	if len([]rune(extractClaudeSystemText(gotSys))) >= len([]rune(systemText)) {
		t.Fatalf("expected system text to be compressed")
	}
	lastPrompt := promptFromClaudeMessages(gotMsgs)
	if !strings.Contains(lastPrompt, "msg-39") {
		t.Fatalf("expected latest context to remain after trimming")
	}
}

func TestMapClaudeToolsToChatTools_AllowsTaskToolsByDefault(t *testing.T) {
	in := []ClaudeToolDef{
		{Name: "TaskCreate"},
		{Name: "TaskOutput"},
		{Name: "Read"},
	}
	got := mapClaudeToolsToChatTools(in)
	if len(got) != 3 {
		t.Fatalf("expected all tools to remain by default, got=%d", len(got))
	}
	if got[0].Function.Name != "TaskCreate" {
		t.Fatalf("expected TaskCreate tool to remain, got=%q", got[0].Function.Name)
	}
}

func TestSanitizeClaudeClientToolCalls_NormalizesReadPagesAndKeepsTaskByDefault(t *testing.T) {
	calls := []ChatToolCall{
		{
			ID:   "a",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "TaskOutput",
				Arguments: `{"task_id":"??"}`,
			},
		},
		{
			ID:   "b",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "Read",
				Arguments: `{"file_path":"/tmp/x.py","limit":400,"offset":1,"pages":""}`,
			},
		},
	}
	got := sanitizeClaudeClientToolCalls(calls)
	if len(got) != 2 {
		t.Fatalf("expected both calls to remain by default, got=%d", len(got))
	}
	if got[0].Function.Name != "TaskOutput" {
		t.Fatalf("expected TaskOutput call to remain, got=%q", got[0].Function.Name)
	}
	if got[1].Function.Name != "Read" {
		t.Fatalf("expected Read call to remain, got=%q", got[1].Function.Name)
	}
	if strings.Contains(got[1].Function.Arguments, `"pages"`) {
		t.Fatalf("expected empty pages to be removed from arguments, got=%s", got[1].Function.Arguments)
	}
}

func TestSanitizeClaudeClientToolCalls_DropsTaskWhenEnvEnabled(t *testing.T) {
	t.Setenv("CODEXSESS_CLAUDE_BLOCK_TASK_TOOLS", "1")
	calls := []ChatToolCall{
		{
			ID:   "a",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "TaskOutput",
				Arguments: `{"task_id":"x"}`,
			},
		},
		{
			ID:   "b",
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      "Read",
				Arguments: `{"file_path":"README.md","pages":""}`,
			},
		},
	}
	got := sanitizeClaudeClientToolCalls(calls)
	if len(got) != 1 {
		t.Fatalf("expected Task* call to be dropped when env enabled, got=%d", len(got))
	}
	if got[0].Function.Name != "Read" {
		t.Fatalf("expected Read call to remain, got=%q", got[0].Function.Name)
	}
}

func TestSanitizeClaudeToolResultText_DropsKnownNoise(t *testing.T) {
	cases := []string{
		`<tool_use_error>Invalid pages parameter: "".</tool_use_error>`,
		`File content (18151 tokens) exceeds maximum allowed tokens (10000).`,
		`CRITICAL: This is a READ-ONLY task. You CANNOT edit, write, or create files.`,
	}
	for _, in := range cases {
		if got, keep := sanitizeClaudeToolResultText(in); keep || got != "" {
			t.Fatalf("expected noisy tool_result to be dropped, got keep=%v text=%q", keep, got)
		}
	}
}

func TestSanitizeClaudeToolResultText_TruncatesLongResult(t *testing.T) {
	in := strings.Repeat("x", 4000)
	got, keep := sanitizeClaudeToolResultText(in)
	if !keep {
		t.Fatalf("expected long tool_result to be kept with truncation")
	}
	if len(got) >= len(in) {
		t.Fatalf("expected truncated output, got len=%d", len(got))
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation marker, got: %q", got)
	}
}

func TestExtractSessionIDFromMetadata_SupportsMultipleShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "nested user_id json string",
			raw:  `{"user_id":"{\"session_id\":\"sid-123\"}"}`,
			want: "sid-123",
		},
		{
			name: "direct session_id",
			raw:  `{"session_id":"sid-234"}`,
			want: "sid-234",
		},
		{
			name: "direct sessionId",
			raw:  `{"sessionId":"sid-345"}`,
			want: "sid-345",
		},
		{
			name: "nested metadata sessionId",
			raw:  `{"metadata":{"sessionId":"sid-456"}}`,
			want: "sid-456",
		},
		{
			name: "userId plain string",
			raw:  `{"userId":"sid-567"}`,
			want: "sid-567",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSessionIDFromMetadata(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
