package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// AnthropicClient implements LLMClient for the Anthropic Messages API.
type AnthropicClient struct {
	apiKey    string
	model     string
	maxTokens int
	baseURL   string
	http      *http.Client
}

// NewAnthropicClient creates a new Anthropic API client.
func NewAnthropicClient(apiKey, model string, maxTokens int, baseURL string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		baseURL:   baseURL,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Anthropic-specific request/response types

type anthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Tools     []anthropicToolDef  `json:"tools,omitempty"`
	Stream    bool                `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	ToolUseID string        `json:"tool_use_id,omitempty"`
	Content   string        `json:"content,omitempty"`
}

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// convertMessages transforms our internal Message format to Anthropic format.
// Returns the system prompt (extracted from messages) and the converted messages.
func convertToAnthropicMessages(messages []Message) (string, []anthropicMessage) {
	var system string
	var result []anthropicMessage

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			system = msg.ContentString()
		case "user":
			result = append(result, anthropicMessage{
				Role:    "user",
				Content: msg.ContentString(),
			})
		case "assistant":
			blocks := buildAssistantBlocks(msg)
			result = append(result, anthropicMessage{
				Role:    "assistant",
				Content: blocks,
			})
		case "tool":
			// Anthropic tool results go in a user message with tool_result blocks
			block := anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.ContentString(),
			}
			// Merge with previous user message if it's also tool results
			if len(result) > 0 && result[len(result)-1].Role == "user" {
				if blocks, ok := result[len(result)-1].Content.([]anthropicContentBlock); ok {
					result[len(result)-1].Content = append(blocks, block)
					continue
				}
			}
			result = append(result, anthropicMessage{
				Role:    "user",
				Content: []anthropicContentBlock{block},
			})
		}
	}

	return system, result
}

func buildAssistantBlocks(msg Message) []anthropicContentBlock {
	var blocks []anthropicContentBlock
	if msg.Content != nil && *msg.Content != "" {
		blocks = append(blocks, anthropicContentBlock{
			Type: "text",
			Text: *msg.Content,
		})
	}
	for _, tc := range msg.ToolCalls {
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: ""})
	}
	return blocks
}

func convertToolDefs(tools []ToolDef) []anthropicToolDef {
	result := make([]anthropicToolDef, len(tools))
	for i, t := range tools {
		result[i] = anthropicToolDef{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		}
	}
	return result
}

// SendMessage sends a non-streaming request to the Anthropic API.
func (c *AnthropicClient) SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error) {
	system, msgs := convertToAnthropicMessages(messages)
	reqBody := anthropicRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    system,
		Messages:  msgs,
	}
	if len(tools) > 0 {
		reqBody.Tools = convertToolDefs(tools)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var apiResp anthropicResponse
	err = c.doWithRetry(ctx, bodyBytes, &apiResp)
	if err != nil {
		return nil, err
	}

	return c.convertResponse(apiResp), nil
}

func (c *AnthropicClient) convertResponse(resp anthropicResponse) *Response {
	var content string
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content += block.Text
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			if args == nil {
				args = []byte("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(args),
				},
			})
		}
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}

	finishReason := "stop"
	switch resp.StopReason {
	case "tool_use":
		finishReason = "tool_calls"
	case "max_tokens":
		finishReason = "length"
	case "end_turn":
		finishReason = "stop"
	}

	return &Response{
		Message: Message{
			Role:      "assistant",
			Content:   contentPtr,
			ToolCalls: toolCalls,
		},
		FinishReason: finishReason,
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

func (c *AnthropicClient) doWithRetry(ctx context.Context, body []byte, result *anthropicResponse) error {
	maxRetries := 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff + jitter):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/messages", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := c.http.Do(req)
		if err != nil {
			if attempt < maxRetries {
				continue
			}
			return fmt.Errorf("http request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}

		switch {
		case resp.StatusCode == 200:
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("unmarshal response: %w", err)
			}
			return nil
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return fmt.Errorf("authentication error (HTTP %d): %s", resp.StatusCode, string(respBody))
		case resp.StatusCode == 429:
			if attempt < maxRetries {
				continue
			}
			return fmt.Errorf("rate limited (HTTP 429) after %d retries: %s", maxRetries, string(respBody))
		case resp.StatusCode >= 500:
			if attempt < maxRetries {
				continue
			}
			return fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, string(respBody))
		default:
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
		}
	}

	return fmt.Errorf("exhausted retries")
}
