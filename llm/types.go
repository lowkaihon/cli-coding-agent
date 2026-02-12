// Package llm provides an abstract LLM client interface with OpenAI and Anthropic
// implementations, streaming support, and automatic retry with exponential backoff.
package llm

import (
	"context"
	"encoding/json"
)

// LLMClient is the interface for interacting with an LLM API.
type LLMClient interface {
	SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)
	StreamMessage(ctx context.Context, messages []Message, tools []ToolDef) (<-chan StreamEvent, error)
}

// Message represents a chat message.
// Content is a pointer to distinguish empty string (valid for tool results) from absent.
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// TextMessage creates a message with text content.
func TextMessage(role, content string) Message {
	return Message{Role: role, Content: &content}
}

// ToolResultMessage creates a tool result message.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{Role: "tool", Content: &content, ToolCallID: toolCallID}
}

// AssistantMessage creates an assistant message with optional tool calls.
func AssistantMessage(content *string, toolCalls []ToolCall) Message {
	return Message{Role: "assistant", Content: content, ToolCalls: toolCalls}
}

// ContentString returns the content as a string, or empty string if nil.
func (m Message) ContentString() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

// ToolCall represents a tool call requested by the assistant.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments as a JSON string.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef defines a tool available to the LLM.
type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef describes a tool's function signature.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage tracks token consumption.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response is the higher-level response returned by the LLM client.
type Response struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// StreamEvent represents a chunk from a streaming response.
type StreamEvent struct {
	// TextDelta contains a text chunk (empty if this is a tool call delta).
	TextDelta string
	// ToolCallDeltas contains incremental tool call data.
	ToolCallDeltas []ToolCallDelta
	// Done signals the stream is complete.
	Done bool
	// Err signals an error occurred during streaming.
	Err error
	// Usage is populated in the final event if the API provides it.
	Usage *Usage
	// FinishReason from the final chunk.
	FinishReason string
}

// ToolCallDelta represents an incremental update to a tool call during streaming.
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}
