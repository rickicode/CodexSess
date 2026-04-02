package trafficlog

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestLogger_RotatesByLineCount(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "traffic.log")
	logger, err := New(logPath, 2*1024*1024)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	for i := 1; i <= 60; i++ {
		err := logger.Append(Entry{Path: fmt.Sprintf("/req/%02d", i), Method: "POST"})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	lines, err := logger.ReadTail(200)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(lines) != 50 {
		t.Fatalf("expected 50 lines, got %d", len(lines))
	}

	var first Entry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first line: %v", err)
	}
	if first.Path != "/req/11" {
		t.Fatalf("expected first path /req/11, got %q", first.Path)
	}

	var last Entry
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("decode last line: %v", err)
	}
	if last.Path != "/req/60" {
		t.Fatalf("expected last path /req/60, got %q", last.Path)
	}
}
