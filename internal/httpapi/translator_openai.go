package httpapi

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
)

type openAIOutput struct {
	ToolCalls    []ChatToolCall
	HasToolCalls bool
	OutputText   string
}

type upstreamErrorClassifier interface {
	ClassifyUpstreamError(err error) (statusCode int, errType string)
}

type openAITranslator struct{}

var defaultOpenAITranslator = openAITranslator{}

type openAIPromptSpec struct {
	Prompt     string
	DirectOpts directCodexRequestOptions
}

func (openAITranslator) NormalizeResponseFormat(format *ResponseFormat) (*structuredOutputSpec, error) {
	return normalizeResponseFormat(format)
}

func (openAITranslator) ClassifyUpstreamError(err error) (int, string) {
	return classifyDirectUpstreamError(err)
}

func (openAITranslator) ClassifySetupError(err error) (int, string, string) {
	return classifyOpenAISetupError(err)
}

func (tx openAITranslator) BuildChatPrompt(req ChatCompletionsRequest, injectPrompt bool) openAIPromptSpec {
	prompt := promptFromMessages(req.Messages)
	if injectPrompt {
		prompt = promptFromMessagesWithTools(req.Messages, req.Tools, req.ToolChoice)
	}
	return openAIPromptSpec{
		Prompt: prompt,
		DirectOpts: directCodexRequestOptions{
			Tools:      req.Tools,
			ToolChoice: req.ToolChoice,
			TextFormat: req.ResponseFormat,
		},
	}
}

func (tx openAITranslator) BuildResponsesPrompt(req ResponsesRequest, injectPrompt bool) openAIPromptSpec {
	prompt := promptFromResponsesInput(req.Input, nil, nil)
	if injectPrompt {
		prompt = promptFromResponsesInput(req.Input, req.Tools, req.ToolChoice)
	}
	directOpts := directCodexRequestOptions{
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		TextFormat: nil,
	}
	if req.Text != nil {
		directOpts.TextFormat = req.Text.Format
	}
	return openAIPromptSpec{
		Prompt:     prompt,
		DirectOpts: directOpts,
	}
}

func (openAITranslator) ResolveToolCalls(text string, defs []ChatToolDef, native []ChatToolCall) ([]ChatToolCall, bool) {
	if len(native) > 0 {
		filtered, _ := defaultOpenAITranslator.filterToolCallsByDefs(native, defs)
		if len(filtered) == 0 {
			return nil, false
		}
		return filtered, true
	}
	if len(defs) == 0 {
		return nil, false
	}
	return defaultOpenAITranslator.parseToolCallsFromText(text, defs)
}

func (tx openAITranslator) ParseToolCallsFromText(text string, defs []ChatToolDef) ([]ChatToolCall, bool) {
	return tx.parseToolCallsFromText(text, defs)
}

func (tx openAITranslator) FilterToolCallsByDefs(calls []ChatToolCall, defs []ChatToolDef) ([]ChatToolCall, bool) {
	return tx.filterToolCallsByDefs(calls, defs)
}

func (tx openAITranslator) FindToolDefByName(defs []ChatToolDef, name string) (ChatToolDef, bool) {
	return tx.findToolDefByName(defs, name)
}

func (tx openAITranslator) MissingRequiredToolFields(def ChatToolDef, argsRaw string) []string {
	return tx.missingRequiredToolFields(def, argsRaw)
}

func (tx openAITranslator) MapProviderToolCalls(calls []provider.ToolCall) []ChatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(c.ID)
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		args := tx.normalizeToolArguments(json.RawMessage(c.Arguments))
		out = append(out, ChatToolCall{
			ID:   callID,
			Type: "function",
			Function: ChatToolFunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (tx openAITranslator) parseToolCallsFromText(text string, defs []ChatToolDef) ([]ChatToolCall, bool) {
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
			name := strings.TrimSpace(tx.toolDefName(d))
			if name != "" {
				allowed[name] = struct{}{}
			}
		}
	}
	type simpleCall struct {
		ID        string
		Name      string
		Arguments json.RawMessage
	}
	anyToRaw := func(v any) json.RawMessage {
		if v == nil {
			return json.RawMessage("{}")
		}
		if s, ok := v.(string); ok {
			trimmed := strings.TrimSpace(s)
			if trimmed == "" {
				return json.RawMessage("{}")
			}
			if json.Valid([]byte(trimmed)) {
				return json.RawMessage(trimmed)
			}
			b, _ := json.Marshal(trimmed)
			return json.RawMessage(b)
		}
		b, _ := json.Marshal(v)
		if len(bytes.TrimSpace(b)) == 0 || string(bytes.TrimSpace(b)) == "null" {
			return json.RawMessage("{}")
		}
		return json.RawMessage(b)
	}
	stringFromAny := func(v any) string {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	calls := make([]simpleCall, 0, 4)
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case []any:
			for _, item := range t {
				walk(item)
			}
		case map[string]any:
			if tc, ok := t["tool_calls"]; ok {
				walk(tc)
			}
			name := stringFromAny(t["name"])
			id := firstNonEmpty(stringFromAny(t["call_id"]), stringFromAny(t["id"]))
			args, hasArgs := t["arguments"]
			if !hasArgs {
				if v, ok := t["input"]; ok {
					args = v
					hasArgs = true
				}
			}
			if fn, ok := t["function"].(map[string]any); ok {
				name = firstNonEmpty(name, stringFromAny(fn["name"]))
				if v, ok := fn["arguments"]; ok {
					args = v
					hasArgs = true
				}
				if !hasArgs {
					if v, ok := fn["input"]; ok {
						args = v
						hasArgs = true
					}
				}
			}
			looksLikeToolCall := name != "" && hasArgs
			if looksLikeToolCall {
				calls = append(calls, simpleCall{ID: id, Name: name, Arguments: anyToRaw(args)})
			}
		}
	}
	dec := json.NewDecoder(strings.NewReader(candidate))
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			break
		}
		walk(v)
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
		args := tx.normalizeToolArguments(c.Arguments)
		if def, ok := tx.findToolDefByName(defs, name); ok {
			if !tx.isToolCallArgumentsValid(def, args) {
				continue
			}
		}
		callID := strings.TrimSpace(c.ID)
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		out = append(out, ChatToolCall{
			ID:   callID,
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

func (tx openAITranslator) filterToolCallsByDefs(calls []ChatToolCall, defs []ChatToolDef) ([]ChatToolCall, bool) {
	if len(calls) == 0 {
		return nil, false
	}
	allowed := map[string]struct{}{}
	for _, d := range defs {
		if strings.EqualFold(strings.TrimSpace(d.Type), "function") {
			name := strings.TrimSpace(tx.toolDefName(d))
			if name != "" {
				allowed[name] = struct{}{}
			}
		}
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Function.Name)
		if name == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		if strings.TrimSpace(c.ID) == "" {
			c.ID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		if strings.TrimSpace(c.Type) == "" {
			c.Type = "function"
		}
		c.Function.Arguments = tx.normalizeToolArguments(json.RawMessage(c.Function.Arguments))
		if def, ok := tx.findToolDefByName(defs, name); ok {
			if !tx.isToolCallArgumentsValid(def, c.Function.Arguments) {
				continue
			}
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func (tx openAITranslator) findToolDefByName(defs []ChatToolDef, name string) (ChatToolDef, bool) {
	target := strings.TrimSpace(name)
	if target == "" {
		return ChatToolDef{}, false
	}
	for _, def := range defs {
		if strings.EqualFold(strings.TrimSpace(tx.toolDefName(def)), target) {
			return def, true
		}
	}
	return ChatToolDef{}, false
}

func (tx openAITranslator) missingRequiredToolFields(def ChatToolDef, argsRaw string) []string {
	required := tx.toolDefRequiredFields(def)
	if len(required) == 0 {
		return nil
	}
	argsText := strings.TrimSpace(argsRaw)
	if argsText == "" {
		return append([]string(nil), required...)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(argsText), &parsed); err != nil || parsed == nil {
		return append([]string(nil), required...)
	}
	missing := make([]string, 0, len(required))
	for _, key := range required {
		v, ok := parsed[key]
		if !ok || v == nil {
			missing = append(missing, key)
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func (tx openAITranslator) toolDefRequiredFields(def ChatToolDef) []string {
	raw := bytes.TrimSpace(def.Function.Parameters)
	if len(raw) == 0 {
		raw = bytes.TrimSpace(def.Parameters)
	}
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil
	}
	requiredRaw, ok := obj["required"]
	if !ok {
		return nil
	}
	switch items := requiredRaw.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			name := strings.TrimSpace(coerceAnyText(item))
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			name := strings.TrimSpace(item)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func (tx openAITranslator) toolDefName(def ChatToolDef) string {
	if n := strings.TrimSpace(def.Function.Name); n != "" {
		return n
	}
	return strings.TrimSpace(def.Name)
}

func (tx openAITranslator) toolDefPromptValue(def ChatToolDef) any {
	name := tx.toolDefName(def)
	typ := strings.TrimSpace(def.Type)
	if typ == "" {
		typ = "function"
	}
	if strings.TrimSpace(def.Function.Name) != "" {
		return def
	}
	out := map[string]any{
		"type": typ,
		"name": name,
	}
	if desc := strings.TrimSpace(def.Description); desc != "" {
		out["description"] = desc
	}
	if len(bytes.TrimSpace(def.Parameters)) > 0 {
		var params any
		if err := json.Unmarshal(def.Parameters, &params); err == nil {
			out["parameters"] = params
		}
	}
	return out
}

func (tx openAITranslator) isToolCallArgumentsValid(def ChatToolDef, argsRaw string) bool {
	return len(tx.missingRequiredToolFields(def, argsRaw)) == 0
}

func (tx openAITranslator) normalizeToolArguments(raw json.RawMessage) string {
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

func (openAITranslator) ValidateStructuredOutput(spec *structuredOutputSpec, output string) error {
	return validateStructuredOutput(spec, output)
}

func (openAITranslator) ResponseFormatPayload(format *ResponseFormat) (map[string]any, error) {
	return responseFormatPayload(format)
}

func (tx openAITranslator) NormalizeOutput(text string, defs []ChatToolDef, native []ChatToolCall) openAIOutput {
	toolCalls, hasToolCalls := tx.ResolveToolCalls(text, defs, native)
	return openAIOutput{
		ToolCalls:    toolCalls,
		HasToolCalls: hasToolCalls,
		OutputText:   text,
	}
}

func (tx openAITranslator) NormalizeBufferedOutput(streamedText string, fallbackText string, defs []ChatToolDef, native []ChatToolCall) openAIOutput {
	baseText := streamedText
	if baseText == "" {
		baseText = fallbackText
	}
	normalized := tx.NormalizeOutput(fallbackText, defs, native)
	normalized.OutputText = baseText
	return normalized
}

func (tx openAITranslator) ResolveResponseTextPayload(format *ResponseFormat) map[string]any {
	textPayload := map[string]any{"format": map[string]any{"type": "text"}}
	if format == nil {
		return textPayload
	}
	if formatPayload, err := tx.ResponseFormatPayload(format); err == nil && formatPayload != nil {
		return map[string]any{"format": formatPayload}
	}
	return textPayload
}

func (tx openAITranslator) NormalizeResponsesJSON(text string, defs []ChatToolDef, native []ChatToolCall) openAIOutput {
	return tx.NormalizeOutput(text, defs, native)
}

func (tx openAITranslator) NormalizeChatJSON(text string, defs []ChatToolDef, native []ChatToolCall) openAIOutput {
	return tx.NormalizeOutput(text, defs, native)
}

func (tx openAITranslator) NormalizeResponsesStream(streamedText string, fallbackText string, defs []ChatToolDef, native []ChatToolCall) openAIOutput {
	return tx.NormalizeBufferedOutput(streamedText, fallbackText, defs, native)
}

func (tx openAITranslator) NormalizeChatStream(streamedText string, fallbackText string, defs []ChatToolDef, native []ChatToolCall) openAIOutput {
	return tx.NormalizeBufferedOutput(streamedText, fallbackText, defs, native)
}

func (openAITranslator) CloneToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
