package llm

import "strings"

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
