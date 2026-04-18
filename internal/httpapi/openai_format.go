package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var openAIWireTranslator = defaultOpenAITranslator

func writeOpenAIExecutionSetupError(w http.ResponseWriter, err error) {
	code, errType, msg := openAIWireTranslator.ClassifySetupError(err)
	respondErr(w, code, errType, msg)
}

func (s *Server) writeChatCompletionsStreamResponse(w http.ResponseWriter, r *http.Request, reqID string, state *openAIChatRequestState, exec *proxyExecutionSession, status *int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		*status = 500
		respondErr(w, 500, "internal_error", "streaming not supported")
		return
	}
	bufferedToolsMode := len(state.request.Tools) > 0 || state.structuredSpec != nil
	includeUsageChunk := state.request.StreamOpts != nil && state.request.StreamOpts.IncludeUsage
	var streamedText strings.Builder
	stopKeepAlive := func() {}
	if bufferedToolsMode {
		stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
	}
	res, err := s.executor.executeStream(r.Context(), exec, state.prompt, state.directOpts, func(delta string) error {
		if bufferedToolsMode {
			streamedText.WriteString(delta)
			return nil
		}
		chunk := ChatCompletionsChunk{
			ID:      "chatcmpl-" + reqID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   state.request.Model,
			Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: delta}}},
		}
		writeChatCompletionsChunk(w, flusher, chunk)
		return nil
	}, !bufferedToolsMode)
	stopKeepAlive()
	if err != nil {
		code, errType := openAIWireTranslator.ClassifyUpstreamError(err)
		*status = code
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"`+escape(errType)+`"}}`)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	usage := Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens}
	if bufferedToolsMode {
		normalized := openAIWireTranslator.NormalizeChatStream(strings.TrimSpace(streamedText.String()), res.Text, state.request.Tools, res.ToolCalls)
		if state.structuredSpec != nil {
			if normalized.HasToolCalls {
				*status = 400
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"tool_calls not allowed when response_format is set","type":"invalid_response_format"}}`)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			if err := openAIWireTranslator.ValidateStructuredOutput(state.structuredSpec, normalized.OutputText); err != nil {
				*status = 400
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"invalid_response_format"}}`)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, state.request.Model, normalized.OutputText, usage, includeUsageChunk)
			return
		}
		if normalized.HasToolCalls {
			streamChatCompletionToolCalls(w, flusher, "chatcmpl-"+reqID, state.request.Model, normalized.ToolCalls, usage, includeUsageChunk)
			return
		}
		streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, state.request.Model, normalized.OutputText, usage, includeUsageChunk)
		return
	}
	streamChatCompletionFinalStop(w, flusher, "chatcmpl-"+reqID, state.request.Model, usage, includeUsageChunk)
}

func writeChatCompletionsJSONResponse(w http.ResponseWriter, reqID string, state *openAIChatRequestState, result proxyBackendResult, status *int) {
	normalized := openAIWireTranslator.NormalizeChatJSON(result.Text, state.request.Tools, result.ToolCalls)
	if state.structuredSpec != nil {
		if normalized.HasToolCalls {
			*status = 400
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when response_format is set")
			return
		}
		if err := openAIWireTranslator.ValidateStructuredOutput(state.structuredSpec, normalized.OutputText); err != nil {
			*status = 400
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	choice := ChatChoice{
		Index:        0,
		Message:      ChatMessage{Role: "assistant", Content: normalized.OutputText},
		FinishReason: "stop",
	}
	if normalized.HasToolCalls {
		choice.Message = ChatMessage{Role: "assistant", Content: "", ToolCalls: normalized.ToolCalls}
		choice.FinishReason = "tool_calls"
	}
	respondJSON(w, 200, ChatCompletionsResponse{
		ID:      "chatcmpl-" + reqID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   state.request.Model,
		Choices: []ChatChoice{choice},
		Usage:   Usage{PromptTokens: result.InputTokens, CompletionTokens: result.OutputTokens, TotalTokens: result.InputTokens + result.OutputTokens},
	})
}

func (s *Server) writeResponsesStreamResponse(w http.ResponseWriter, r *http.Request, reqID string, state *openAIResponsesRequestState, exec *proxyExecutionSession, status *int) {
	responseCreatedAt := time.Now().Unix()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		*status = 500
		respondErr(w, 500, "internal_error", "streaming not supported")
		return
	}
	seq := 0
	emit := func(event string, payload map[string]any) {
		if payload == nil {
			return
		}
		if _, exists := payload["sequence_number"]; !exists {
			seq++
			payload["sequence_number"] = seq
		}
		_ = event
		writeOpenAISSE(w, payload)
		flusher.Flush()
	}
	emit("response.created", map[string]any{
		"type":     "response.created",
		"response": buildResponseObject(reqID, state.request.Model, "in_progress", []any{}, nil, responseCreatedAt),
	})

	bufferedToolsMode := len(state.request.Tools) > 0 || state.structuredSpec != nil
	var streamedText strings.Builder
	textItemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	stopKeepAlive := func() {}
	if bufferedToolsMode {
		stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
	}
	if !bufferedToolsMode {
		emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": 0,
			"item": map[string]any{
				"type":    "message",
				"id":      textItemID,
				"status":  "in_progress",
				"role":    "assistant",
				"content": []any{},
			},
		})
	}
	result, err := s.executor.executeStream(r.Context(), exec, state.prompt, state.directOpts, func(delta string) error {
		if bufferedToolsMode {
			streamedText.WriteString(delta)
			return nil
		}
		streamedText.WriteString(delta)
		emit("response.output_text.delta", map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"delta":         delta,
			"logprobs":      []any{},
		})
		return nil
	}, !bufferedToolsMode)
	stopKeepAlive()
	if err != nil {
		code, errType := openAIWireTranslator.ClassifyUpstreamError(err)
		*status = code
		emit("error", map[string]any{"type": "error", "code": errType, "message": err.Error(), "param": nil})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	usage := ResponsesUsage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, TotalTokens: result.InputTokens + result.OutputTokens}
	if bufferedToolsMode {
		normalized := openAIWireTranslator.NormalizeResponsesStream(strings.TrimSpace(streamedText.String()), strings.TrimSpace(result.Text), state.request.Tools, result.ToolCalls)
		if state.structuredSpec != nil {
			if normalized.HasToolCalls {
				*status = 400
				emit("error", map[string]any{"type": "error", "code": "invalid_response_format", "message": "tool_calls not allowed when text.format is set", "param": nil})
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			if err := openAIWireTranslator.ValidateStructuredOutput(state.structuredSpec, normalized.OutputText); err != nil {
				*status = 400
				emit("error", map[string]any{"type": "error", "code": "invalid_response_format", "message": err.Error(), "param": nil})
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			streamResponsesText(emit, reqID, state.request.Model, normalized.OutputText, usage, responseCreatedAt)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		if normalized.HasToolCalls {
			streamResponsesFunctionCalls(emit, reqID, state.request.Model, normalized.ToolCalls, usage, responseCreatedAt)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		streamResponsesText(emit, reqID, state.request.Model, normalized.OutputText, usage, responseCreatedAt)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	finalText := strings.TrimSpace(result.Text)
	if finalText == "" {
		finalText = strings.TrimSpace(streamedText.String())
	}
	emit("response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       textItemID,
		"output_index":  0,
		"content_index": 0,
		"text":          finalText,
		"logprobs":      []any{},
	})
	outputItem := map[string]any{
		"type":   "message",
		"id":     textItemID,
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]any{
			{"type": "output_text", "text": finalText, "annotations": []any{}},
		},
	}
	emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": outputItem})
	emit("response.completed", map[string]any{
		"type": "response.completed",
		"response": buildResponseObject(reqID, state.request.Model, "completed", []any{outputItem}, map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.TotalTokens,
		}, responseCreatedAt),
	})
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeResponsesJSONResponse(w http.ResponseWriter, reqID string, state *openAIResponsesRequestState, result proxyBackendResult, status *int) {
	normalized := openAIWireTranslator.NormalizeResponsesJSON(result.Text, state.request.Tools, result.ToolCalls)
	if state.structuredSpec != nil {
		if normalized.HasToolCalls {
			*status = 400
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when text.format is set")
			return
		}
		if err := openAIWireTranslator.ValidateStructuredOutput(state.structuredSpec, normalized.OutputText); err != nil {
			*status = 400
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	output := responsesMessageOutputItems(normalized.OutputText)
	outputText := strings.TrimSpace(normalized.OutputText)
	if normalized.HasToolCalls {
		output = responsesFunctionCallOutputItems(normalized.ToolCalls)
		outputText = ""
	}
	completedAt := time.Now().Unix()
	textPayload := openAIWireTranslator.ResolveResponseTextPayload(nil)
	if state.request.Text != nil {
		textPayload = openAIWireTranslator.ResolveResponseTextPayload(state.request.Text.Format)
	}
	respondJSON(w, 200, ResponsesResponse{
		ID:                 reqID,
		Object:             "response",
		CreatedAt:          completedAt,
		OutputText:         outputText,
		Status:             "completed",
		CompletedAt:        &completedAt,
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       nil,
		MaxOutputTokens:    nil,
		Model:              state.request.Model,
		Output:             output,
		ParallelToolCalls:  true,
		PreviousResponseID: nil,
		Reasoning:          map[string]any{"effort": nil, "summary": nil},
		Store:              true,
		Temperature:        1.0,
		Text:               textPayload,
		ToolChoice:         "auto",
		Tools:              []any{},
		TopP:               1.0,
		Truncation:         "disabled",
		Usage: ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		},
		User:     nil,
		Metadata: map[string]any{},
	})
}
