package httpapi

import "encoding/json"

type ChatCompletionsRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Stream         bool            `json:"stream"`
	StreamOpts     *ChatStreamOpts `json:"stream_options,omitempty"`
	Tools          []ChatToolDef   `json:"tools,omitempty"`
	ToolChoice     json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type ChatMessage struct {
	Role       string         `json:"role,omitempty"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type ChatCompletionsResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatCompletionsChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []ChatChunkChoice `json:"choices"`
	Usage   *Usage            `json:"usage"`
}

type ChatChunkChoice struct {
	Index        int         `json:"index"`
	Delta        ChatMessage `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type ChatStreamOpts struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type ChatToolDef struct {
	Type        string              `json:"type"`
	Function    ChatToolFunctionDef `json:"function"`
	Name        string              `json:"name,omitempty"`
	Description string              `json:"description,omitempty"`
	Parameters  json.RawMessage     `json:"parameters,omitempty"`
}

type ChatToolFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ChatToolCall struct {
	Index    *int                 `json:"index,omitempty"`
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function ChatToolFunctionCall `json:"function"`
}

type ChatToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ResponsesRequest struct {
	Model      string              `json:"model"`
	Input      json.RawMessage     `json:"input"`
	Stream     bool                `json:"stream"`
	Tools      []ChatToolDef       `json:"tools,omitempty"`
	ToolChoice json.RawMessage     `json:"tool_choice,omitempty"`
	Text       *ResponsesTextParam `json:"text,omitempty"`
}

type ResponsesTextParam struct {
	Format *ResponseFormat `json:"format,omitempty"`
}

type ResponseFormat struct {
	Type       string                    `json:"type"`
	JSONSchema *ResponseFormatJSONSchema `json:"json_schema,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Schema     json.RawMessage           `json:"schema,omitempty"`
	Strict     *bool                     `json:"strict,omitempty"`
}

type ResponseFormatJSONSchema struct {
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
}

type ResponsesResponse struct {
	ID                 string          `json:"id"`
	Object             string          `json:"object"`
	CreatedAt          int64           `json:"created_at"`
	OutputText         string          `json:"output_text"`
	Status             string          `json:"status"`
	CompletedAt        *int64          `json:"completed_at,omitempty"`
	Error              any             `json:"error"`
	IncompleteDetails  any             `json:"incomplete_details"`
	Instructions       any             `json:"instructions"`
	MaxOutputTokens    any             `json:"max_output_tokens"`
	Model              string          `json:"model"`
	Output             []ResponsesItem `json:"output"`
	ParallelToolCalls  bool            `json:"parallel_tool_calls"`
	PreviousResponseID any             `json:"previous_response_id"`
	Reasoning          map[string]any  `json:"reasoning"`
	Store              bool            `json:"store"`
	Temperature        float64         `json:"temperature"`
	Text               map[string]any  `json:"text"`
	ToolChoice         any             `json:"tool_choice"`
	Tools              []any           `json:"tools"`
	TopP               float64         `json:"top_p"`
	Truncation         string          `json:"truncation"`
	Usage              ResponsesUsage  `json:"usage"`
	User               any             `json:"user"`
	Metadata           map[string]any  `json:"metadata"`
}

type ResponsesItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Role      string          `json:"role,omitempty"`
	Content   []ResponsesText `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
}

type ResponsesText struct {
	Type        string         `json:"type"`
	Text        string         `json:"text"`
	Logprobs    any            `json:"logprobs,omitempty"`
	Annotations []ResponsesRef `json:"annotations"`
}

type ResponsesRef struct {
	Type string `json:"type"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ClaudeMessagesRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Messages      []ClaudeMessage `json:"messages"`
	System        json.RawMessage `json:"system,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	Tools         []ClaudeToolDef `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream"`
}

type ClaudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ClaudeMessagesResponse struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Role         string               `json:"role"`
	Model        string               `json:"model"`
	Content      []ClaudeContentBlock `json:"content"`
	StopReason   string               `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence"`
	Usage        ClaudeMessagesUsage  `json:"usage"`
}

type ClaudeContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type ClaudeToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ClaudeMessagesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
