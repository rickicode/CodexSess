package trafficlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Timestamp         time.Time `json:"timestamp"`
	Protocol          string    `json:"protocol"`
	Method            string    `json:"method"`
	Path              string    `json:"path"`
	Status            int       `json:"status"`
	LatencyMS         int64     `json:"latency_ms"`
	RemoteAddr        string    `json:"remote_addr"`
	UserAgent         string    `json:"user_agent,omitempty"`
	AccountHint       string    `json:"account_hint,omitempty"`
	AccountID         string    `json:"account_id,omitempty"`
	AccountEmail      string    `json:"account_email,omitempty"`
	Model             string    `json:"model,omitempty"`
	Stream            bool      `json:"stream,omitempty"`
	RequestBody       string    `json:"request_body,omitempty"`
	ResponseBody      string    `json:"response_body,omitempty"`
	RequestTokens     int       `json:"request_tokens,omitempty"`
	ResponseTokens    int       `json:"response_tokens,omitempty"`
	TotalTokens       int       `json:"total_tokens,omitempty"`
	RequestTruncated  bool      `json:"request_truncated,omitempty"`
	ResponseTruncated bool      `json:"response_truncated,omitempty"`
}

type Logger struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

const maxTrafficLogLines = 50

func New(path string, maxBytes int64) (*Logger, error) {
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &Logger{path: path, maxBytes: maxBytes}, nil
}

func (l *Logger) Append(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line := append(raw, '\n')

	if info, err := os.Stat(l.path); err == nil {
		if info.Size()+int64(len(line)) > l.maxBytes {
			if err := os.WriteFile(l.path, []byte{}, 0o600); err != nil {
				return err
			}
		}
	}

	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(line); err != nil {
		return err
	}
	return l.trimToMaxLines(maxTrafficLogLines)
}

func (l *Logger) ReadTail(maxLines int) ([]string, error) {
	if maxLines <= 0 {
		maxLines = 200
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	out := make([]string, 0, maxLines)
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		v := strings.TrimSpace(lines[i])
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (l *Logger) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return os.WriteFile(l.path, []byte{}, 0o600)
}

func (l *Logger) trimToMaxLines(maxLines int) error {
	if maxLines <= 0 {
		return nil
	}
	b, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		trimmed = append(trimmed, v)
	}
	if len(trimmed) <= maxLines {
		return nil
	}
	trimmed = trimmed[len(trimmed)-maxLines:]
	out := strings.Join(trimmed, "\n") + "\n"
	return os.WriteFile(l.path, []byte(out), 0o600)
}
