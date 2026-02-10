package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lowkaihon/cli-coding-agent/llm"
	"github.com/lowkaihon/cli-coding-agent/tools"
)

func testAgent(t *testing.T, workDir string) *Agent {
	t.Helper()
	client := &mockLLMClient{}
	registry := tools.NewRegistry(workDir)
	return New(client, registry, workDir, 100000)
}

func TestGenerateSessionID(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()

	if id1 == id2 {
		t.Errorf("expected unique IDs, got %s twice", id1)
	}
	// Format: 20060102-150405-<8 hex chars>
	if len(id1) < 20 {
		t.Errorf("session ID too short: %s", id1)
	}
}

func TestSaveSession_EmptyNoOp(t *testing.T) {
	dir := t.TempDir()
	ag := testAgent(t, dir)

	// Only system prompt, should be a no-op
	err := ag.SaveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Sessions dir should not exist
	sessDir, _ := globalSessionsDir(dir)
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Error("expected sessions dir to not exist for empty session")
	}
}

func TestSaveAndResumeSession(t *testing.T) {
	dir := t.TempDir()
	ag := testAgent(t, dir)

	// Add some messages
	ag.messages = append(ag.messages, llm.TextMessage("user", "Hello, help me refactor"))
	text := "Sure, I'll help you refactor."
	ag.messages = append(ag.messages, llm.Message{Role: "assistant", Content: &text})
	ag.messages = append(ag.messages, llm.TextMessage("user", "Thanks!"))

	err := ag.SaveSession()
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Verify file exists
	sessDir, _ := globalSessionsDir(dir)
	sessionPath := filepath.Join(sessDir, ag.sessionID+".json")
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("session file not found: %v", err)
	}

	var sf SessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if sf.Meta.MsgCount != 3 {
		t.Errorf("expected 3 messages, got %d", sf.Meta.MsgCount)
	}
	if sf.Meta.Preview != "Hello, help me refactor" {
		t.Errorf("unexpected preview: %s", sf.Meta.Preview)
	}

	// Now resume in a fresh agent
	ag2 := testAgent(t, dir)
	err = ag2.ResumeSession(ag.sessionID)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	// Should have system prompt + 3 saved messages
	if len(ag2.messages) != 4 {
		t.Errorf("expected 4 messages after resume, got %d", len(ag2.messages))
	}
	if ag2.messages[0].Role != "system" {
		t.Error("first message should be system prompt")
	}
	if ag2.messages[1].ContentString() != "Hello, help me refactor" {
		t.Errorf("unexpected first user message: %s", ag2.messages[1].ContentString())
	}
	if ag2.sessionID != ag.sessionID {
		t.Errorf("session ID not restored: got %s, want %s", ag2.sessionID, ag.sessionID)
	}
}

func TestListSessions_Ordering(t *testing.T) {
	dir := t.TempDir()
	sessDir, _ := globalSessionsDir(dir)
	os.MkdirAll(sessDir, 0755)

	// Create two session files with different timestamps
	now := time.Now()
	old := SessionFile{
		Meta: SessionMeta{
			ID:        "old-session",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
			Preview:   "old session",
			MsgCount:  5,
		},
		Messages: []llm.Message{llm.TextMessage("user", "old session")},
	}
	recent := SessionFile{
		Meta: SessionMeta{
			ID:        "recent-session",
			CreatedAt: now.Add(-10 * time.Minute),
			UpdatedAt: now.Add(-10 * time.Minute),
			Preview:   "recent session",
			MsgCount:  10,
		},
		Messages: []llm.Message{llm.TextMessage("user", "recent session")},
	}

	for _, sf := range []SessionFile{old, recent} {
		data, _ := json.Marshal(sf)
		os.WriteFile(filepath.Join(sessDir, sf.Meta.ID+".json"), data, 0644)
	}

	metas, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(metas))
	}
	// Most recent first
	if metas[0].ID != "recent-session" {
		t.Errorf("expected recent-session first, got %s", metas[0].ID)
	}
	if metas[1].ID != "old-session" {
		t.Errorf("expected old-session second, got %s", metas[1].ID)
	}
}

func TestListSessions_MaxLimit(t *testing.T) {
	dir := t.TempDir()
	sessDir, _ := globalSessionsDir(dir)
	os.MkdirAll(sessDir, 0755)

	now := time.Now()
	for i := 0; i < 5; i++ {
		sf := SessionFile{
			Meta: SessionMeta{
				ID:        generateSessionID(),
				CreatedAt: now,
				UpdatedAt: now.Add(time.Duration(i) * time.Minute),
				Preview:   "test",
				MsgCount:  1,
			},
			Messages: []llm.Message{llm.TextMessage("user", "test")},
		}
		data, _ := json.Marshal(sf)
		os.WriteFile(filepath.Join(sessDir, sf.Meta.ID+".json"), data, 0644)
	}

	metas, err := ListSessions(dir, 3)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(metas) != 3 {
		t.Errorf("expected 3 sessions (limited), got %d", len(metas))
	}
}

func TestListSessions_NoDir(t *testing.T) {
	dir := t.TempDir()
	metas, err := ListSessions(dir, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("expected empty list, got %d", len(metas))
	}
}

func TestResumeSession_NotFound(t *testing.T) {
	dir := t.TempDir()
	ag := testAgent(t, dir)
	err := ag.ResumeSession("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestSaveSession_NilContent(t *testing.T) {
	dir := t.TempDir()
	ag := testAgent(t, dir)

	// Add a message with nil content (assistant with tool calls)
	ag.messages = append(ag.messages, llm.TextMessage("user", "do something"))
	ag.messages = append(ag.messages, llm.Message{
		Role:    "assistant",
		Content: nil,
		ToolCalls: []llm.ToolCall{
			{ID: "tc1", Type: "function", Function: llm.FunctionCall{Name: "read", Arguments: `{"path":"test.go"}`}},
		},
	})
	ag.messages = append(ag.messages, llm.ToolResultMessage("tc1", "file contents here"))

	err := ag.SaveSession()
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Resume and verify nil content round-trips correctly
	ag2 := testAgent(t, dir)
	err = ag2.ResumeSession(ag.sessionID)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	// messages[2] is the assistant msg with nil content
	if ag2.messages[2].Content != nil {
		t.Errorf("expected nil content after round-trip, got %v", ag2.messages[2].Content)
	}
	if len(ag2.messages[2].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(ag2.messages[2].ToolCalls))
	}
}
