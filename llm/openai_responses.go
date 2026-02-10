package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIResponsesClient implements LLMClient for OpenAI's /v1/responses endpoint.
// Used for GPT-5.x models that require the newer Responses API.
type OpenAIResponsesClient struct {
	apiKey    string
	model     string
	maxTokens int
	baseURL   string
	http      *http.Client
}

// NewOpenAIResponsesClient creates a new OpenAI Responses API client.
func NewOpenAIResponsesClient(apiKey, model string, maxTokens int, baseURL string) *OpenAIResponsesClient {
	return &OpenAIResponsesClient{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		baseURL:   baseURL,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Responses API request types

type responsesRequest struct {
	Model           string              `json:"model"`
	Input           []json.RawMessage   `json:"input"`
	Instructions    string              `json:"instructions,omitempty"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Stream          bool                `json:"stream,omitempty"`
}

type responsesMessageInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responsesFunctionCallInput struct {
	Type      string `json:"type"` // "function_call"
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
}

type responsesFunctionCallOutputInput struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responsesTool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Responses API response types

type responsesResponse struct {
	ID     string            `json:"id"`
	Status string            `json:"status"` // "completed", "incomplete", "failed"
	Output []responsesOutput `json:"output"`
	Usage  responsesUsage    `json:"usage"`
	Error  *responsesError   `json:"error,omitempty"`
}

type responsesOutput struct {
	Type    string `json:"type"` // "message", "function_call"
	// For type "message":
	Role    string                 `json:"role,omitempty"`
	Content []responsesContentItem `json:"content,omitempty"`
	// For type "function_call":
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type responsesContentItem struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// convertToResponsesInput converts internal messages to Responses API input format.
// Returns the system instructions and the input items.
func convertToResponsesInput(messages []Message) (string, []json.RawMessage) {
	var instructions string
	var input []json.RawMessage

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			instructions = msg.ContentString()

		case "user", "developer":
			data, _ := json.Marshal(responsesMessageInput{
				Role:    msg.Role,
				Content: msg.ContentString(),
			})
			input = append(input, data)

		case "assistant":
			// First emit any text content as an assistant message
			if msg.Content != nil && *msg.Content != "" {
				data, _ := json.Marshal(responsesMessageInput{
					Role:    "assistant",
					Content: *msg.Content,
				})
				input = append(input, data)
			}
			// Then emit each tool call as a function_call input item
			for _, tc := range msg.ToolCalls {
				data, _ := json.Marshal(responsesFunctionCallInput{
					Type:      "function_call",
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
					CallID:    tc.ID,
				})
				input = append(input, data)
			}

		case "tool":
			data, _ := json.Marshal(responsesFunctionCallOutputInput{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.ContentString(),
			})
			input = append(input, data)
		}
	}

	return instructions, input
}

// convertResponsesToolDefs converts internal ToolDef to Responses API flat format.
func convertResponsesToolDefs(tools []ToolDef) []responsesTool {
	result := make([]responsesTool, len(tools))
	for i, t := range tools {
		result[i] = responsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		}
	}
	return result
}

// convertResponsesResponse converts the API response to internal Response format.
func convertResponsesResponse(resp responsesResponse) *Response {
	var content string
	var toolCalls []ToolCall

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					content += c.Text
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else {
		switch resp.Status {
		case "completed":
			finishReason = "stop"
		case "incomplete":
			finishReason = "length"
		case "failed":
			finishReason = "stop"
		}
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
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
}

// SendMessage sends a non-streaming request to the Responses API.
func (c *OpenAIResponsesClient) SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error) {
	instructions, input := convertToResponsesInput(messages)
	reqBody := responsesRequest{
		Model:           c.model,
		Input:           input,
		Instructions:    instructions,
		MaxOutputTokens: c.maxTokens,
	}
	if len(tools) > 0 {
		reqBody.Tools = convertResponsesToolDefs(tools)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var apiResp responsesResponse
	resp, err := doWithRetry(ctx, defaultRetryConfig(), func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/responses", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		return c.http.Do(req)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API error: %s: %s", apiResp.Error.Code, apiResp.Error.Message)
	}

	return convertResponsesResponse(apiResp), nil
}

