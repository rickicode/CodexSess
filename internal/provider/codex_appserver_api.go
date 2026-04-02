package provider

import (
	"context"
	"os"
	"strings"
)

func NewCodexAppServer(binary string) *CodexAppServer {
	if binary == "" {
		binary = "codex"
	}
	return &CodexAppServer{Binary: binary}
}

func (c *CodexAppServer) Chat(ctx context.Context, codexHome string, model string, prompt string) (ChatResult, error) {
	return c.ChatWithOptions(ctx, ExecOptions{
		CodexHome: codexHome,
		WorkDir:   defaultExecWorkDir(codexHome),
		Model:     model,
		Prompt:    prompt,
	})
}

func (c *CodexAppServer) ChatWithOptions(ctx context.Context, opts ExecOptions) (ChatResult, error) {
	return c.AppServerChatWithOptions(ctx, opts)
}

func (c *CodexAppServer) StreamChat(ctx context.Context, codexHome string, model string, prompt string, onEvent func(ChatEvent) error) (ChatResult, error) {
	return c.StreamChatWithOptions(ctx, ExecOptions{
		CodexHome: codexHome,
		WorkDir:   defaultExecWorkDir(codexHome),
		Model:     model,
		Prompt:    prompt,
	}, onEvent)
}

func (c *CodexAppServer) StreamChatWithOptions(ctx context.Context, opts ExecOptions, onEvent func(ChatEvent) error) (ChatResult, error) {
	return c.AppServerStreamChatWithOptions(ctx, opts, onEvent)
}

func defaultExecWorkDir(codexHome string) string {
	if v := strings.TrimSpace(os.Getenv("CODEXSESS_CODEX_WORKDIR")); v != "" {
		return v
	}
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		return strings.TrimSpace(wd)
	}
	return strings.TrimSpace(codexHome)
}

func normalizeReasoningEffort(v string) string {
	effort := strings.TrimSpace(strings.ToLower(v))
	switch effort {
	case "low", "high":
		return effort
	default:
		return "medium"
	}
}
