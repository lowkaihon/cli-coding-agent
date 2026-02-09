package llm

import (
	"encoding/json"
	"testing"
)

func TestConvertToResponsesInput_SystemExtracted(t *testing.T) {
	messages := []Message{
		TextMessage("system", "You are a helpful assistant."),
		TextMessage("user", "Hello"),
	}

	instructions, input := convertToResponsesInput(messages)

	if instructions != "You are a helpful assistant." {
		t.Errorf("expected system prompt as instructions, got %q", instructions)
	}

	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}

	var msg responsesMessageInput
	if err := json.Unmarshal(input[0], &msg); err != nil {
		t.Fatalf("unmarshal input[0]: %v", err)
	}
	if msg.Role != "user" || msg.Content != "Hello" {
		t.Errorf("expected user message 'Hello', got role=%q content=%q", msg.Role, msg.Content)
	}
}

func TestConvertToResponsesInput_ToolCalls(t *testing.T) {
	content := "Let me search for that."
	messages := []Message{
		TextMessage("system", "system"),
		TextMessage("user", "find files"),
		{
			Role:    "assistant",
			Content: &content,
			ToolCalls: []ToolCall{
				{
					ID:   "call_123",
					Type: "function",
					Function: FunctionCall{
						Name:      "glob",
						Arguments: `{"pattern":"*.go"}`,
					},
				},
			},
		},
		ToolResultMessage("call_123", "main.go\nutil.go"),
	}

	instructions, input := convertToResponsesInput(messages)

	if instructions != "system" {
		t.Errorf("unexpected instructions: %q", instructions)
	}

	// Should have: user msg + assistant msg + function_call + function_call_output = 4
	if len(input) != 4 {
		t.Fatalf("expected 4 input items, got %d", len(input))
	}

	// Check function_call item
	var fcInput responsesFunctionCallInput
	if err := json.Unmarshal(input[2], &fcInput); err != nil {
		t.Fatalf("unmarshal function_call: %v", err)
	}
	if fcInput.Type != "function_call" {
		t.Errorf("expected type function_call, got %q", fcInput.Type)
	}
	if fcInput.CallID != "call_123" {
		t.Errorf("expected call_id call_123, got %q", fcInput.CallID)
	}
	if fcInput.Name != "glob" {
		t.Errorf("expected name glob, got %q", fcInput.Name)
	}

	// Check function_call_output item
	var fcoInput responsesFunctionCallOutputInput
	if err := json.Unmarshal(input[3], &fcoInput); err != nil {
		t.Fatalf("unmarshal function_call_output: %v", err)
	}
	if fcoInput.Type != "function_call_output" {
		t.Errorf("expected type function_call_output, got %q", fcoInput.Type)
	}
	if fcoInput.CallID != "call_123" {
		t.Errorf("expected call_id call_123, got %q", fcoInput.CallID)
	}
	if fcoInput.Output != "main.go\nutil.go" {
		t.Errorf("expected output 'main.go\\nutil.go', got %q", fcoInput.Output)
	}
}

func TestConvertResponsesResponse_TextOnly(t *testing.T) {
	resp := responsesResponse{
		ID:     "resp_1",
		Status: "completed",
		Output: []responsesOutput{
			{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentItem{
					{Type: "output_text", Text: "Hello world!"},
				},
			},
		},
		Usage: responsesUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}

	result := convertResponsesResponse(resp)

	if result.Message.ContentString() != "Hello world!" {
		t.Errorf("expected 'Hello world!', got %q", result.Message.ContentString())
	}
	if result.FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", result.FinishReason)
	}
	if len(result.Message.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(result.Message.ToolCalls))
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestConvertResponsesResponse_ToolCalls(t *testing.T) {
	resp := responsesResponse{
		ID:     "resp_2",
		Status: "completed",
		Output: []responsesOutput{
			{
				Type:      "function_call",
				Name:      "glob",
				Arguments: `{"pattern":"*.go"}`,
				CallID:    "call_abc",
				Status:    "completed",
			},
		},
		Usage: responsesUsage{
			InputTokens:  20,
			OutputTokens: 10,
			TotalTokens:  30,
		},
	}

	result := convertResponsesResponse(resp)

	if result.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got %q", result.FinishReason)
	}
	if len(result.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.Message.ToolCalls))
	}
	tc := result.Message.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("expected ID 'call_abc', got %q", tc.ID)
	}
	if tc.Function.Name != "glob" {
		t.Errorf("expected name 'glob', got %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"pattern":"*.go"}` {
		t.Errorf("unexpected arguments: %q", tc.Function.Arguments)
	}
}

func TestConvertResponsesResponse_Incomplete(t *testing.T) {
	resp := responsesResponse{
		ID:     "resp_3",
		Status: "incomplete",
		Output: []responsesOutput{
			{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentItem{
					{Type: "output_text", Text: "Partial response..."},
				},
			},
		},
	}

	result := convertResponsesResponse(resp)

	if result.FinishReason != "length" {
		t.Errorf("expected finish_reason 'length', got %q", result.FinishReason)
	}
}

func TestConvertResponsesToolDefs(t *testing.T) {
	tools := []ToolDef{
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "glob",
				Description: "Find files",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`),
			},
		},
	}

	result := convertResponsesToolDefs(tools)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", result[0].Type)
	}
	if result[0].Name != "glob" {
		t.Errorf("expected name 'glob', got %q", result[0].Name)
	}
	if result[0].Description != "Find files" {
		t.Errorf("expected description 'Find files', got %q", result[0].Description)
	}
}

func TestConvertResponsesResponse_MixedTextAndToolCalls(t *testing.T) {
	resp := responsesResponse{
		ID:     "resp_4",
		Status: "completed",
		Output: []responsesOutput{
			{
				Type: "message",
				Role: "assistant",
				Content: []responsesContentItem{
					{Type: "output_text", Text: "Let me search for that."},
				},
			},
			{
				Type:      "function_call",
				Name:      "grep",
				Arguments: `{"pattern":"func main"}`,
				CallID:    "call_xyz",
				Status:    "completed",
			},
		},
	}

	result := convertResponsesResponse(resp)

	if result.Message.ContentString() != "Let me search for that." {
		t.Errorf("unexpected content: %q", result.Message.ContentString())
	}
	if len(result.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.Message.ToolCalls))
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got %q", result.FinishReason)
	}
}
