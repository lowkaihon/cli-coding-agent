package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lowkaihon/cli-coding-agent/llm"
	"github.com/lowkaihon/cli-coding-agent/tools"
	"github.com/lowkaihon/cli-coding-agent/ui"
)

func newTestAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	mock := &mockLLMClient{}
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	return ag, dir
}

func TestCreateCheckpoint(t *testing.T) {
	ag, _ := newTestAgent(t)

	// Initially no checkpoints
	if len(ag.Checkpoints()) != 0 {
		t.Fatalf("expected 0 checkpoints, got %d", len(ag.Checkpoints()))
	}

	// Add a user message and create checkpoint
	ag.messages = append(ag.messages, llm.TextMessage("user", "hello"))
	ag.CreateCheckpoint("hello world")

	items := ag.Checkpoints()
	if len(items) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(items))
	}
	if items[0].Turn != 1 {
		t.Errorf("expected turn 1, got %d", items[0].Turn)
	}
	if items[0].Preview != "hello world" {
		t.Errorf("expected preview 'hello world', got %q", items[0].Preview)
	}

	// Verify msgIndex captures current message count
	if ag.checkpoints[0].MsgIndex != 2 { // system + user
		t.Errorf("expected MsgIndex 2, got %d", ag.checkpoints[0].MsgIndex)
	}
}

func TestCreateCheckpointTruncatesPreview(t *testing.T) {
	ag, _ := newTestAgent(t)

	longMsg := ""
	for i := 0; i < 20; i++ {
		longMsg += "0123456789"
	}
	ag.CreateCheckpoint(longMsg)

	items := ag.Checkpoints()
	if len(items[0].Preview) != 100 {
		t.Errorf("expected preview length 100, got %d", len(items[0].Preview))
	}
}

func TestCaptureFileBeforeModification(t *testing.T) {
	ag, dir := newTestAgent(t)

	// Create a file on disk
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("original content"), 0644)

	// First capture
	ag.captureFileBeforeModification(filePath)
	if snap, ok := ag.fileOriginals[filePath]; !ok {
		t.Fatal("expected file to be tracked")
	} else if !snap.Existed {
		t.Error("expected Existed=true")
	} else if string(snap.Content) != "original content" {
		t.Errorf("expected 'original content', got %q", string(snap.Content))
	}

	// Modify file on disk, then capture again — should be no-op
	os.WriteFile(filePath, []byte("modified content"), 0644)
	ag.captureFileBeforeModification(filePath)
	if string(ag.fileOriginals[filePath].Content) != "original content" {
		t.Error("second capture should not overwrite first")
	}
}

func TestCaptureFileBeforeModification_NewFile(t *testing.T) {
	ag, dir := newTestAgent(t)

	filePath := filepath.Join(dir, "new.txt")
	// File doesn't exist yet
	ag.captureFileBeforeModification(filePath)

	snap := ag.fileOriginals[filePath]
	if snap.Existed {
		t.Error("expected Existed=false for non-existent file")
	}
	if snap.Content != nil {
		t.Error("expected nil content for non-existent file")
	}
}

func TestRewindConversation(t *testing.T) {
	ag, _ := newTestAgent(t)

	// Simulate two turns
	ag.CreateCheckpoint("turn 1") // checkpoint 1, msgIndex=1 (system only)
	ag.messages = append(ag.messages, llm.TextMessage("user", "turn 1"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "response 1"))

	ag.CreateCheckpoint("turn 2") // checkpoint 2, msgIndex=3
	ag.messages = append(ag.messages, llm.TextMessage("user", "turn 2"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "response 2"))

	if len(ag.messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(ag.messages))
	}

	// Rewind to checkpoint 2 — should restore to state before turn 2
	ag.RewindConversation(2)

	if len(ag.messages) != 3 { // system + user1 + assistant1
		t.Errorf("expected 3 messages after rewind, got %d", len(ag.messages))
	}
	if len(ag.checkpoints) != 1 { // only checkpoint 1 remains
		t.Errorf("expected 1 checkpoint after rewind, got %d", len(ag.checkpoints))
	}
	if ag.lastTokensUsed != 0 {
		t.Errorf("expected lastTokensUsed reset to 0, got %d", ag.lastTokensUsed)
	}
}

func TestRewindCode(t *testing.T) {
	ag, dir := newTestAgent(t)

	filePath := filepath.Join(dir, "code.go")
	os.WriteFile(filePath, []byte("v1"), 0644)

	// Track the file
	ag.captureFileBeforeModification(filePath)

	// Create checkpoint 1 (captures current disk state of tracked files)
	ag.CreateCheckpoint("turn 1")

	// Modify file
	os.WriteFile(filePath, []byte("v2"), 0644)

	// Rewind code to checkpoint 1
	err := ag.RewindCode(1)
	if err != nil {
		t.Fatalf("RewindCode failed: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != "v1" {
		t.Errorf("expected file content 'v1', got %q", string(data))
	}
}

func TestRewindCode_FilesCreatedAfterCheckpoint(t *testing.T) {
	ag, dir := newTestAgent(t)

	// Create checkpoint 1 before any files modified
	ag.CreateCheckpoint("turn 1")

	// Now a file is created by the agent
	newFile := filepath.Join(dir, "new.go")
	ag.captureFileBeforeModification(newFile) // records non-existence
	os.WriteFile(newFile, []byte("new content"), 0644)

	// Rewind code to checkpoint 1 — file should be deleted
	err := ag.RewindCode(1)
	if err != nil {
		t.Fatalf("RewindCode failed: %v", err)
	}

	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Error("expected new file to be deleted after rewind")
	}
}

func TestRewindAll(t *testing.T) {
	ag, dir := newTestAgent(t)

	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("original"), 0644)
	ag.captureFileBeforeModification(filePath)

	ag.CreateCheckpoint("turn 1") // checkpoint 1

	// Add messages and modify file
	ag.messages = append(ag.messages, llm.TextMessage("user", "turn 1"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "response 1"))
	os.WriteFile(filePath, []byte("modified"), 0644)

	ag.CreateCheckpoint("turn 2") // checkpoint 2
	ag.messages = append(ag.messages, llm.TextMessage("user", "turn 2"))

	// Rewind all to checkpoint 2
	err := ag.RewindAll(2)
	if err != nil {
		t.Fatalf("RewindAll failed: %v", err)
	}

	// Check messages restored
	if len(ag.messages) != 3 { // system + user1 + assistant1
		t.Errorf("expected 3 messages, got %d", len(ag.messages))
	}

	// Check file restored
	data, _ := os.ReadFile(filePath)
	if string(data) != "modified" { // checkpoint 2 captured "modified" state
		t.Errorf("expected 'modified', got %q", string(data))
	}

	// Check checkpoints trimmed
	if len(ag.checkpoints) != 1 {
		t.Errorf("expected 1 checkpoint, got %d", len(ag.checkpoints))
	}
}

func TestSummarizeFrom(t *testing.T) {
	summaryText := "Summary of later messages."
	mock := &mockLLMClient{
		responses: []llm.Response{
			{
				Message:      llm.TextMessage("assistant", summaryText),
				FinishReason: "stop",
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	// Build conversation: system + user1 + assistant1 + user2 + assistant2
	ag.messages = append(ag.messages, llm.TextMessage("user", "first question"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "first answer"))

	ag.CreateCheckpoint("turn 2") // checkpoint at msgIndex=3

	ag.messages = append(ag.messages, llm.TextMessage("user", "second question"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "long detailed answer that should be summarized"))

	beforeCount := len(ag.messages) // 5

	err := ag.SummarizeFrom(context.Background(), 1, term)
	if err != nil {
		t.Fatalf("SummarizeFrom failed: %v", err)
	}

	// Messages before checkpoint preserved, later replaced with summary
	if len(ag.messages) >= beforeCount {
		t.Errorf("expected fewer messages after summarize, got %d (was %d)", len(ag.messages), beforeCount)
	}

	// Last message should contain the summary
	lastMsg := ag.messages[len(ag.messages)-1]
	if lastMsg.ContentString() == "" {
		t.Error("expected summary message to have content")
	}
}
