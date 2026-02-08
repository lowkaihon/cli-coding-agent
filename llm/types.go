package llm

import "encoding/json"

// Message represents an OpenAI chat message.
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

// APIResponse is the raw response from the OpenAI chat completions API.
type APIResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a single completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
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

// ChatRequest is the request body for the OpenAI chat completions API.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
	Stream   bool      `json:"stream,omitempty"`
	// MaxTokens limits the response length.
	MaxTokens int `json:"max_tokens,omitempty"`
	// StreamOptions is used to get usage in stream mode.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

// StreamOptions configures streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// StreamChunk is a single SSE chunk from the streaming API.
type StreamChunk struct {
	ID      string         `json:"id"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// StreamChoice represents a single choice in a streaming chunk.
type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        StreamDelta  `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

// StreamDelta contains the incremental data in a streaming chunk.
type StreamDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   *string         `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
}
