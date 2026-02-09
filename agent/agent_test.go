package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lowkaihon/cli-coding-agent/llm"
	"github.com/lowkaihon/cli-coding-agent/tools"
	"github.com/lowkaihon/cli-coding-agent/ui"
)

// mockLLMClient implements llm.LLMClient for testing.
type mockLLMClient struct {
	responses []llm.Response
	callCount int32
}

func (m *mockLLMClient) SendMessage(ctx context.Context, messages []llm.Message, toolDefs []llm.ToolDef) (*llm.Response, error) {
	idx := int(atomic.AddInt32(&m.callCount, 1)) - 1
	if idx >= len(m.responses) {
		text := "done"
		return &llm.Response{
			Message:      llm.TextMessage("assistant", text),
			FinishReason: "stop",
		}, nil
	}
	return &m.responses[idx], nil
}

func (m *mockLLMClient) StreamMessage(ctx context.Context, messages []llm.Message, toolDefs []llm.ToolDef) (<-chan llm.StreamEvent, error) {
	idx := int(atomic.AddInt32(&m.callCount, 1)) - 1
	ch := make(chan llm.StreamEvent, 10)
	go func() {
		defer close(ch)
		if idx >= len(m.responses) {
			text := "done"
			ch <- llm.StreamEvent{TextDelta: text}
			ch <- llm.StreamEvent{FinishReason: "stop", Done: true}
			return
		}

		resp := m.responses[idx]
		if resp.Message.Content != nil {
			ch <- llm.StreamEvent{TextDelta: *resp.Message.Content}
		}

		for i, tc := range resp.Message.ToolCalls {
			ch <- llm.StreamEvent{
				ToolCallDeltas: []llm.ToolCallDelta{{
					Index: i,
					ID:    tc.ID,
					Type:  "function",
					Function: struct {
						Name      string `json:"name,omitempty"`
						Arguments string `json:"arguments,omitempty"`
					}{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}},
			}
		}

		ch <- llm.StreamEvent{FinishReason: resp.FinishReason, Done: true}
	}()
	return ch, nil
}

func TestAgentSingleTurn(t *testing.T) {
	text := "Hello! I can help you with your code."
	mock := &mockLLMClient{
		responses: []llm.Response{
			{
				Message:      llm.TextMessage("assistant", text),
				FinishReason: "stop",
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	err := ag.Run(context.Background(), "hello", term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have: system + user + assistant = 3 messages
	if ag.MessageCount() != 3 {
		t.Errorf("expected 3 messages, got %d", ag.MessageCount())
	}
}

func TestAgentToolUseLoop(t *testing.T) {
	// First response: LLM calls glob tool
	globArgs, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	mock := &mockLLMClient{
		responses: []llm.Response{
			{
				Message: llm.AssistantMessage(nil, []llm.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "glob",
							Arguments: string(globArgs),
						},
					},
				}),
				FinishReason: "tool_calls",
			},
			// Second response: final text
			{
				Message:      llm.TextMessage("assistant", "I found some Go files."),
				FinishReason: "stop",
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	err := ag.Run(context.Background(), "find go files", term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// system + user + assistant(tool_call) + tool_result + assistant(final) = 5
	if ag.MessageCount() != 5 {
		t.Errorf("expected 5 messages, got %d", ag.MessageCount())
	}
}

func TestAgentMaxIterations(t *testing.T) {
	// Create a mock that always returns tool calls (infinite loop)
	globArgs, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	resp := llm.Response{
		Message: llm.AssistantMessage(nil, []llm.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "glob",
					Arguments: string(globArgs),
				},
			},
		}),
		FinishReason: "tool_calls",
	}

	responses := make([]llm.Response, MaxIterationsPerTurn+5)
	for i := range responses {
		responses[i] = resp
		responses[i].Message.ToolCalls[0].ID = "call_" + string(rune('a'+i%26))
	}

	mock := &mockLLMClient{responses: responses}
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	err := ag.Run(context.Background(), "infinite loop", term)
	if err == nil {
		t.Fatal("expected max iterations error")
	}
	if got := err.Error(); got != "agent loop exceeded maximum iterations (50)" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestAgentConcurrentToolExecution(t *testing.T) {
	// LLM returns two read-only tool calls
	globArgs, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	grepArgs, _ := json.Marshal(map[string]string{"pattern": "func"})

	mock := &mockLLMClient{
		responses: []llm.Response{
			{
				Message: llm.AssistantMessage(nil, []llm.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "glob",
							Arguments: string(globArgs),
						},
					},
					{
						ID:   "call_2",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "grep",
							Arguments: string(grepArgs),
						},
					},
				}),
				FinishReason: "tool_calls",
			},
			{
				Message:      llm.TextMessage("assistant", "Found results."),
				FinishReason: "stop",
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	err := ag.Run(context.Background(), "search code", term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// system + user + assistant(2 tool calls) + 2 tool results + assistant(final) = 6
	if ag.MessageCount() != 6 {
		t.Errorf("expected 6 messages, got %d", ag.MessageCount())
	}
}

func TestCompaction(t *testing.T) {
	// Use a very small context window so compaction triggers easily
	summaryText := "Summary: user asked to find Go files."
	mock := &mockLLMClient{
		responses: []llm.Response{
			// First call: SendMessage for compaction — returns summary
			{
				Message:      llm.TextMessage("assistant", summaryText),
				FinishReason: "stop",
			},
			// Second call: StreamMessage for the actual response after compaction
			{
				Message:      llm.TextMessage("assistant", "Here is my response."),
				FinishReason: "stop",
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	// contextWindow=500 tokens, system prompt alone is large enough to exceed 80% of 500
	ag := New(mock, registry, dir, 500)
	term := ui.NewTerminal()

	// Add enough messages to exceed the threshold
	longContent := strings.Repeat("This is a long message to fill tokens. ", 100)
	ag.messages = append(ag.messages, llm.TextMessage("user", "find go files"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", longContent))
	ag.messages = append(ag.messages, llm.TextMessage("user", "now what?"))

	err := ag.Run(context.Background(), "continue", term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After compaction, messages should be much shorter than before.
	// The history should contain: system + compacted summary + last user msg + assistant response
	// Exact count depends on implementation but should be small.
	if ag.MessageCount() > 6 {
		t.Errorf("expected compacted message count <= 6, got %d", ag.MessageCount())
	}
}

func TestNoCompactionUnderLimit(t *testing.T) {
	text := "Hello!"
	mock := &mockLLMClient{
		responses: []llm.Response{
			{
				Message:      llm.TextMessage("assistant", text),
				FinishReason: "stop",
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	// Large context window — compaction should not trigger
	ag := New(mock, registry, dir, 1000000)
	term := ui.NewTerminal()

	err := ag.Run(context.Background(), "hello", term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// system + user + assistant = 3, no compaction should have occurred
	if ag.MessageCount() != 3 {
		t.Errorf("expected 3 messages (no compaction), got %d", ag.MessageCount())
	}

	// Only 1 LLM call should have been made (StreamMessage), not 2 (no SendMessage for compaction)
	if mock.callCount != 1 {
		t.Errorf("expected 1 LLM call (no compaction), got %d", mock.callCount)
	}
}

func TestCompactCommand(t *testing.T) {
	summaryText := "Summary of conversation."
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

	// Add some conversation history
	ag.messages = append(ag.messages, llm.TextMessage("user", "hello"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "Hi there! How can I help?"))
	ag.messages = append(ag.messages, llm.TextMessage("user", "find bugs"))

	before := ag.MessageCount()
	err := ag.Compact(context.Background(), term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After compaction, should be shorter: system + summary + last user msg
	if ag.MessageCount() >= before {
		t.Errorf("expected fewer messages after compaction, got %d (was %d)", ag.MessageCount(), before)
	}

	// Should have made exactly 1 LLM call (SendMessage for compaction)
	if mock.callCount != 1 {
		t.Errorf("expected 1 LLM call for compaction, got %d", mock.callCount)
	}
}

func TestCompactEmptyConversation(t *testing.T) {
	mock := &mockLLMClient{}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	// Only system prompt, no conversation
	err := ag.Compact(context.Background(), term)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No LLM call should have been made
	if mock.callCount != 0 {
		t.Errorf("expected 0 LLM calls for empty conversation, got %d", mock.callCount)
	}

	// Still just the system prompt
	if ag.MessageCount() != 1 {
		t.Errorf("expected 1 message (system only), got %d", ag.MessageCount())
	}
}

func TestClear(t *testing.T) {
	mock := &mockLLMClient{}

	dir := t.TempDir()
	registry := tools.NewRegistry(dir)
	ag := New(mock, registry, dir, 128000)
	term := ui.NewTerminal()

	// Add conversation history
	ag.messages = append(ag.messages, llm.TextMessage("user", "hello"))
	ag.messages = append(ag.messages, llm.TextMessage("assistant", "Hi!"))
	ag.messages = append(ag.messages, llm.TextMessage("user", "do stuff"))

	if ag.MessageCount() != 4 {
		t.Fatalf("expected 4 messages before clear, got %d", ag.MessageCount())
	}

	ag.Clear(term)

	// Should be back to just system prompt
	if ag.MessageCount() != 1 {
		t.Errorf("expected 1 message after clear, got %d", ag.MessageCount())
	}

	// No LLM calls should have been made
	if mock.callCount != 0 {
		t.Errorf("expected 0 LLM calls for clear, got %d", mock.callCount)
	}
}
