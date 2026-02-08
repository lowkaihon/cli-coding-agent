package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamMessage sends a streaming request and returns a channel of events.
func (c *OpenAIClient) StreamMessage(ctx context.Context, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {
	reqBody := ChatRequest{
		Model:     c.model,
		Messages:  messages,
		Stream:    true,
		MaxTokens: c.maxTokens,
		StreamOptions: &StreamOptions{
			IncludeUsage: true,
		},
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamEvent, 32)
	go c.parseSSEStream(ctx, resp.Body, ch)
	return ch, nil
}

func (c *OpenAIClient) parseSSEStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// Increase buffer for large SSE lines
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			ch <- StreamEvent{Done: true}
			return
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- StreamEvent{Err: fmt.Errorf("parse SSE chunk: %w", err)}
			return
		}

		event := StreamEvent{}

		// Extract usage if present (final chunk)
		if chunk.Usage != nil {
			event.Usage = chunk.Usage
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.FinishReason != nil {
				event.FinishReason = *choice.FinishReason
			}
			if choice.Delta.Content != nil {
				event.TextDelta = *choice.Delta.Content
			}
			if len(choice.Delta.ToolCalls) > 0 {
				event.ToolCallDeltas = choice.Delta.ToolCalls
			}
		}

		ch <- event
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Err: fmt.Errorf("read SSE stream: %w", err)}
	}
}

// AccumulateStream collects streaming events into a complete Response.
// It also calls onText for each text delta for real-time display.
func AccumulateStream(events <-chan StreamEvent, onText func(string)) (*Response, error) {
	var content strings.Builder
	toolCalls := make(map[int]*ToolCall) // accumulate by index
	var usage Usage
	var finishReason string

	for event := range events {
		if event.Err != nil {
			return nil, event.Err
		}
		if event.Done {
			break
		}

		if event.TextDelta != "" {
			content.WriteString(event.TextDelta)
			if onText != nil {
				onText(event.TextDelta)
			}
		}

		for _, delta := range event.ToolCallDeltas {
			tc, ok := toolCalls[delta.Index]
			if !ok {
				tc = &ToolCall{
					Type: "function",
				}
				toolCalls[delta.Index] = tc
			}
			if delta.ID != "" {
				tc.ID = delta.ID
			}
			if delta.Function.Name != "" {
				tc.Function.Name = delta.Function.Name
			}
			tc.Function.Arguments += delta.Function.Arguments
		}

		if event.Usage != nil {
			usage = *event.Usage
		}
		if event.FinishReason != "" {
			finishReason = event.FinishReason
		}
	}

	// Build the final message
	var contentPtr *string
	if content.Len() > 0 {
		s := content.String()
		contentPtr = &s
	}

	var calls []ToolCall
	for i := 0; i < len(toolCalls); i++ {
		if tc, ok := toolCalls[i]; ok {
			calls = append(calls, *tc)
		}
	}

	msg := Message{
		Role:      "assistant",
		Content:   contentPtr,
		ToolCalls: calls,
	}

	return &Response{
		Message:      msg,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}
