package provider

type ChatEvent struct {
	Type  string
	Text  string
	Actor string
}

type ChatResult struct {
	Text              string
	Messages          []string
	ThreadID          string
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	ToolCalls         []ToolCall
}

type CodexAppServer struct {
	Binary string
}

type AppServerThread struct {
	ThreadID string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ExecOptions struct {
	CodexHome       string
	WorkDir         string
	Model           string
	ReasoningEffort string
	Prompt          string
	ThreadID        string
	ResumeID        string
	Persist         bool
	SandboxMode     string
	CommandMode     string
	Actor           string
	OnProcessStart  func(pid int, forceKill func() error)
}
