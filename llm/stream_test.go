package llm

import (
	"strings"
	"testing"
)

func TestAccumulateStreamTextOnly(t *testing.T) {
	ch := make(chan StreamEvent, 10)
	go func() {
		ch <- StreamEvent{TextDelta: "Hello "}
		ch <- StreamEvent{TextDelta: "world!"}
		ch <- StreamEvent{FinishReason: "stop"}
		ch <- StreamEvent{Done: true}
		close(ch)
	}()

	var collected strings.Builder
	resp, err := AccumulateStream(ch, func(text string) {
		collected.WriteString(text)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Message.ContentString() != "Hello world!" {
		t.Errorf("expected 'Hello world!', got %q", resp.Message.ContentString())
	}
	if collected.String() != "Hello world!" {
		t.Errorf("onText collected %q", collected.String())
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", resp.FinishReason)
	}
}

func TestAccumulateStreamToolCalls(t *testing.T) {
	ch := make(chan StreamEvent, 10)
	go func() {
		// First tool call
		ch <- StreamEvent{
			ToolCallDeltas: []ToolCallDelta{{
				Index: 0,
				ID:    "call_abc",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "glob"},
			}},
		}
		ch <- StreamEvent{
			ToolCallDeltas: []ToolCallDelta{{
				Index: 0,
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Arguments: `{"pat`},
			}},
		}
		ch <- StreamEvent{
			ToolCallDeltas: []ToolCallDelta{{
				Index: 0,
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Arguments: `tern":"*.go"}`},
			}},
		}

		// Second tool call (interleaved)
		ch <- StreamEvent{
			ToolCallDeltas: []ToolCallDelta{{
				Index: 1,
				ID:    "call_def",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "grep", Arguments: `{"pattern":"func"}`},
			}},
		}

		ch <- StreamEvent{FinishReason: "tool_calls", Done: true}
		close(ch)
	}()

	resp, err := AccumulateStream(ch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.Message.ToolCalls))
	}

	tc0 := resp.Message.ToolCalls[0]
	if tc0.ID != "call_abc" {
		t.Errorf("tc0.ID = %q", tc0.ID)
	}
	if tc0.Function.Name != "glob" {
		t.Errorf("tc0.Name = %q", tc0.Function.Name)
	}
	if tc0.Function.Arguments != `{"pattern":"*.go"}` {
		t.Errorf("tc0.Arguments = %q", tc0.Function.Arguments)
	}

	tc1 := resp.Message.ToolCalls[1]
	if tc1.ID != "call_def" {
		t.Errorf("tc1.ID = %q", tc1.ID)
	}
	if tc1.Function.Name != "grep" {
		t.Errorf("tc1.Name = %q", tc1.Function.Name)
	}
}

func TestAccumulateStreamError(t *testing.T) {
	ch := make(chan StreamEvent, 10)
	go func() {
		ch <- StreamEvent{TextDelta: "partial"}
		ch <- StreamEvent{Err: errTest("stream failed")}
		close(ch)
	}()

	_, err := AccumulateStream(ch, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "stream failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestAccumulateStreamUsage(t *testing.T) {
	ch := make(chan StreamEvent, 10)
	go func() {
		ch <- StreamEvent{TextDelta: "hi"}
		ch <- StreamEvent{
			Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}
		ch <- StreamEvent{FinishReason: "stop", Done: true}
		close(ch)
	}()

	resp, err := AccumulateStream(ch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}
