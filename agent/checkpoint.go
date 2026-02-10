package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lowkaihon/cli-coding-agent/llm"
	"github.com/lowkaihon/cli-coding-agent/ui"
)

// FileSnapshot records a file's state before it was first modified in this session.
type FileSnapshot struct {
	Existed bool   // true if file existed before first modification
	Content []byte // content before first modification (nil if didn't exist)
}

// Checkpoint captures conversation and file state at the start of a user turn.
type Checkpoint struct {
	Turn      int              // 1-based turn number
	Timestamp time.Time
	Preview   string           // user message, truncated to 100 chars
	MsgIndex  int              // len(a.messages) at checkpoint creation
	Files     map[string][]byte // filepath → content at this checkpoint (nil = didn't exist)
}

// CheckpointItem is a lightweight view of a checkpoint for UI display.
type CheckpointItem struct {
	Turn      int
	Timestamp time.Time
	Preview   string
}

// CreateCheckpoint saves a checkpoint before a user turn begins.
func (a *Agent) CreateCheckpoint(userMessage string) {
	preview := userMessage
	if len(preview) > 100 {
		preview = preview[:100]
	}

	// Snapshot current disk content of all tracked files
	files := make(map[string][]byte, len(a.fileOriginals))
	for path := range a.fileOriginals {
		data, err := os.ReadFile(path)
		if err != nil {
			files[path] = nil // file doesn't exist at this point
		} else {
			files[path] = data
		}
	}

	a.checkpoints = append(a.checkpoints, Checkpoint{
		Turn:      len(a.checkpoints) + 1,
		Timestamp: time.Now(),
		Preview:   preview,
		MsgIndex:  len(a.messages),
		Files:     files,
	})
}

// captureFileBeforeModification records a file's pre-session state the first
// time it is modified. Subsequent calls for the same path are no-ops.
func (a *Agent) captureFileBeforeModification(path string) {
	if _, ok := a.fileOriginals[path]; ok {
		return // already captured
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist yet — record that
		a.fileOriginals[path] = &FileSnapshot{Existed: false, Content: nil}
	} else {
		a.fileOriginals[path] = &FileSnapshot{Existed: true, Content: data}
	}
}

// Checkpoints returns a lightweight list of all checkpoints for UI display.
func (a *Agent) Checkpoints() []CheckpointItem {
	items := make([]CheckpointItem, len(a.checkpoints))
	for i, cp := range a.checkpoints {
		items[i] = CheckpointItem{
			Turn:      cp.Turn,
			Timestamp: cp.Timestamp,
			Preview:   cp.Preview,
		}
	}
	return items
}

// RewindConversation truncates messages and checkpoints to the given turn.
func (a *Agent) RewindConversation(turn int) {
	if turn < 1 || turn > len(a.checkpoints) {
		return
	}
	cp := a.checkpoints[turn-1]
	a.messages = a.messages[:cp.MsgIndex]
	a.checkpoints = a.checkpoints[:turn-1]
	a.lastTokensUsed = 0
}

// RewindCode restores files to their state at the given checkpoint.
func (a *Agent) RewindCode(turn int) error {
	if turn < 1 || turn > len(a.checkpoints) {
		return fmt.Errorf("invalid checkpoint turn: %d", turn)
	}
	cp := a.checkpoints[turn-1]

	// Restore files that were in the checkpoint's snapshot
	for path, content := range cp.Files {
		if content == nil {
			// File didn't exist at checkpoint time — remove it
			os.Remove(path)
		} else {
			if err := os.WriteFile(path, content, 0644); err != nil {
				return fmt.Errorf("restore %s: %w", path, err)
			}
		}
	}

	// Handle files first modified AFTER this checkpoint — restore to pre-session state
	for path, snapshot := range a.fileOriginals {
		if _, inCheckpoint := cp.Files[path]; inCheckpoint {
			continue // already handled above
		}
		// This file was first modified after this checkpoint
		if !snapshot.Existed {
			os.Remove(path)
		} else {
			if err := os.WriteFile(path, snapshot.Content, 0644); err != nil {
				return fmt.Errorf("restore original %s: %w", path, err)
			}
		}
	}

	// Trim fileOriginals: remove entries for files first modified after this checkpoint
	// (they're back to pre-session state now)
	trimmed := make(map[string]*FileSnapshot, len(cp.Files))
	for path := range cp.Files {
		if snap, ok := a.fileOriginals[path]; ok {
			trimmed[path] = snap
		}
	}
	a.fileOriginals = trimmed

	return nil
}

// RewindAll restores both code and conversation to the given checkpoint.
func (a *Agent) RewindAll(turn int) error {
	if err := a.RewindCode(turn); err != nil {
		return err
	}
	a.RewindConversation(turn)
	return nil
}

// SummarizeFrom keeps messages before the checkpoint intact and replaces
// messages from the checkpoint onward with an LLM-generated summary.
func (a *Agent) SummarizeFrom(ctx context.Context, turn int, term *ui.Terminal) error {
	if turn < 1 || turn > len(a.checkpoints) {
		return fmt.Errorf("invalid checkpoint turn: %d", turn)
	}
	cp := a.checkpoints[turn-1]

	if cp.MsgIndex >= len(a.messages) {
		term.PrintWarning("Nothing to summarize after this checkpoint.")
		return nil
	}

	// Serialize messages from checkpoint onward
	laterMessages := a.messages[cp.MsgIndex:]
	history := serializeHistory(laterMessages)

	compactMessages := []llm.Message{
		llm.TextMessage("system", compactionPrompt()),
		llm.TextMessage("user", history),
	}

	term.PrintWarning("Summarizing from checkpoint...")
	resp, err := a.client.SendMessage(ctx, compactMessages, nil)
	if err != nil {
		return fmt.Errorf("summarization failed: %w", err)
	}

	summary := ""
	if resp.Message.Content != nil {
		summary = *resp.Message.Content
	}

	// Keep messages before checkpoint, replace later ones with summary
	a.messages = a.messages[:cp.MsgIndex]
	if summary != "" {
		a.messages = append(a.messages, llm.TextMessage("user",
			"[Conversation summarized] Here is a summary of what happened:\n\n"+summary))
	}

	// Trim checkpoints to before this turn
	a.checkpoints = a.checkpoints[:turn-1]
	a.lastTokensUsed = 0
	term.PrintWarning("Summarized successfully.")
	return nil
}
