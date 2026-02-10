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

// StreamMessage sends a streaming request to the Responses API.
func (c *OpenAIResponsesClient) StreamMessage(ctx context.Context, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {
	instructions, input := convertToResponsesInput(messages)
	reqBody := responsesRequest{
		Model:           c.model,
		Input:           input,
		Instructions:    instructions,
		MaxOutputTokens: c.maxTokens,
		Stream:          true,
	}
	if len(tools) > 0 {
		reqBody.Tools = convertResponsesToolDefs(tools)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

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

	ch := make(chan StreamEvent, 32)
	go c.parseResponsesStream(ctx, resp.Body, ch)
	return ch, nil
}

// Responses API SSE event types

type responsesStreamEvent struct {
	Type string `json:"type"`
}

type responsesOutputItemAdded struct {
	Type       string          `json:"type"`
	OutputIndex int            `json:"output_index"`
	Item       responsesOutput `json:"item"`
}

type responsesTextDelta struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	ContentIndex int   `json:"content_index"`
	Delta       string `json:"delta"`
}

type responsesFuncArgsDelta struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	Delta       string `json:"delta"`
}

type responsesCompleted struct {
	Type     string            `json:"type"`
	Response responsesResponse `json:"response"`
}

func (c *OpenAIResponsesClient) parseResponsesStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	// Track function_call output items by output_index for tool call delta indexing
	type funcCallState struct {
		outputIndex int
		callID      string
		name        string
	}
	funcCalls := make(map[int]*funcCallState)
	toolCallIdx := 0

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

		// Responses API uses "event: <type>" + "data: <json>" format
		if strings.HasPrefix(line, "event: ") {
			// Read next line for data
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var baseEvent responsesStreamEvent
		if err := json.Unmarshal([]byte(data), &baseEvent); err != nil {
			continue
		}

		switch baseEvent.Type {
		case "response.output_item.added":
			var ev responsesOutputItemAdded
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if ev.Item.Type == "function_call" {
				fc := &funcCallState{
					outputIndex: ev.OutputIndex,
					callID:      ev.Item.CallID,
					name:        ev.Item.Name,
				}
				funcCalls[ev.OutputIndex] = fc
				// Emit initial tool call delta with ID and name
				ch <- StreamEvent{
					ToolCallDeltas: []ToolCallDelta{{
						Index: toolCallIdx,
						ID:    ev.Item.CallID,
						Type:  "function",
						Function: struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						}{
							Name: ev.Item.Name,
						},
					}},
				}
				toolCallIdx++
			}

		case "response.output_text.delta":
			var ev responsesTextDelta
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			ch <- StreamEvent{TextDelta: ev.Delta}

		case "response.function_call_arguments.delta":
			var ev responsesFuncArgsDelta
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			// Find the tool call index for this output item
			tcIdx := 0
			for i := 0; i < ev.OutputIndex; i++ {
				if _, ok := funcCalls[i]; ok {
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
						Arguments: ev.Delta,
					},
				}},
			}

		case "response.completed":
			var ev responsesCompleted
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				// Still send Done even if we can't parse
				ch <- StreamEvent{Done: true}
				return
			}
			// Extract finish reason and usage from the completed response
			event := StreamEvent{}
			if len(funcCalls) > 0 {
				event.FinishReason = "tool_calls"
			} else {
				switch ev.Response.Status {
				case "completed":
					event.FinishReason = "stop"
				case "incomplete":
					event.FinishReason = "length"
				default:
					event.FinishReason = "stop"
				}
			}
			event.Usage = &Usage{
				PromptTokens:     ev.Response.Usage.InputTokens,
				CompletionTokens: ev.Response.Usage.OutputTokens,
				TotalTokens:      ev.Response.Usage.TotalTokens,
			}
			ch <- event
			ch <- StreamEvent{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Err: fmt.Errorf("read SSE stream: %w", err)}
	}
}
