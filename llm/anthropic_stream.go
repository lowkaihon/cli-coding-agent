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

// StreamMessage sends a streaming request to the Anthropic API.
func (c *AnthropicClient) StreamMessage(ctx context.Context, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {
	system, msgs := convertToAnthropicMessages(messages)
	reqBody := anthropicRequest{
		Model:     c.model,
		MaxTokens: c.maxTokens,
		System:    system,
		Messages:  msgs,
		Stream:    true,
	}
	if len(tools) > 0 {
		reqBody.Tools = convertToolDefs(tools)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := doWithRetry(ctx, defaultRetryConfig(), func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/messages", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		return c.http.Do(req)
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 32)
	go c.parseAnthropicStream(ctx, resp.Body, ch)
	return ch, nil
}

// Anthropic SSE event types
type anthropicStreamEvent struct {
	Type string `json:"type"`
}

type anthropicContentBlockStart struct {
	Type         string                `json:"type"`
	Index        int                   `json:"index"`
	ContentBlock anthropicContentBlock `json:"content_block"`
}

type anthropicContentBlockDelta struct {
	Type  string                      `json:"type"`
	Index int                         `json:"index"`
	Delta anthropicDelta              `json:"delta"`
}

type anthropicDelta struct {
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	PartialJSON string          `json:"partial_json,omitempty"`
	StopReason  string          `json:"stop_reason,omitempty"`
}

type anthropicMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

func (c *AnthropicClient) parseAnthropicStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	// Track active content blocks for tool_use
	type blockState struct {
		index int
		id    string
		name  string
		btype string // "text" or "tool_use"
	}
	blocks := make(map[int]*blockState)
	toolCallIndex := 0

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var baseEvent anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &baseEvent); err != nil {
			continue
		}

		switch baseEvent.Type {
		case "content_block_start":
			var ev anthropicContentBlockStart
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			bs := &blockState{
				index: ev.Index,
				btype: ev.ContentBlock.Type,
			}
			if ev.ContentBlock.Type == "tool_use" {
				bs.id = ev.ContentBlock.ID
				bs.name = ev.ContentBlock.Name
				// Emit initial tool call delta with ID and name
				ch <- StreamEvent{
					ToolCallDeltas: []ToolCallDelta{{
						Index: toolCallIndex,
						ID:    ev.ContentBlock.ID,
						Type:  "function",
						Function: struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						}{
							Name: ev.ContentBlock.Name,
						},
					}},
				}
				toolCallIndex++
			}
			blocks[ev.Index] = bs

		case "content_block_delta":
			var ev anthropicContentBlockDelta
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}

			bs := blocks[ev.Index]
			if bs == nil {
				continue
			}

			switch ev.Delta.Type {
			case "text_delta":
				ch <- StreamEvent{TextDelta: ev.Delta.Text}
			case "input_json_delta":
				// Find the tool call index for this block
				tcIdx := 0
				for i := 0; i < ev.Index; i++ {
					if b, ok := blocks[i]; ok && b.btype == "tool_use" {
						tcIdx++
					}
				}
				ch <- StreamEvent{
					ToolCallDeltas: []ToolCallDelta{{
						Index: tcIdx,
						Function: struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						}{
							Arguments: ev.Delta.PartialJSON,
						},
					}},
				}
			}

		case "message_delta":
			var ev anthropicMessageDelta
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			event := StreamEvent{}
			switch ev.Delta.StopReason {
			case "tool_use":
				event.FinishReason = "tool_calls"
			case "max_tokens":
				event.FinishReason = "length"
			case "end_turn":
				event.FinishReason = "stop"
			}
			if ev.Usage != nil {
				event.Usage = &Usage{
					PromptTokens:     ev.Usage.InputTokens,
					CompletionTokens: ev.Usage.OutputTokens,
					TotalTokens:      ev.Usage.InputTokens + ev.Usage.OutputTokens,
				}
			}
			ch <- event

		case "message_stop":
			ch <- StreamEvent{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Err: fmt.Errorf("read SSE stream: %w", err)}
	}
}
