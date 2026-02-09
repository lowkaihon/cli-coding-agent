package agent

import (
	"fmt"
	"strings"

	"github.com/lowkaihon/cli-coding-agent/llm"
)

const (
	// CharsPerToken is the heuristic ratio for estimating token count.
	CharsPerToken = 4
	// ContextBuffer is the fraction of context to keep free (20%).
	ContextBuffer = 0.2
)

// EstimateTokens estimates the token count for a message using the char heuristic.
func EstimateTokens(msg llm.Message) int {
	tokens := len(msg.Role) / CharsPerToken
	if msg.Content != nil {
		tokens += len(*msg.Content) / CharsPerToken
	}
	for _, tc := range msg.ToolCalls {
		tokens += len(tc.Function.Name) / CharsPerToken
		tokens += len(tc.Function.Arguments) / CharsPerToken
	}
	// Minimum 1 token per message for overhead
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// EstimateTotalTokens estimates total tokens across all messages.
func EstimateTotalTokens(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg)
	}
	return total
}

// TruncateHistory truncates message history to stay within context limits.
// Strategy:
//  1. Always keep the system prompt (index 0)
//  2. Always keep the first user message (index 1)
//  3. Always keep the last N messages
//  4. Summarize tool results in the middle
//  5. Drop old tool call/result pairs first
func TruncateHistory(messages []llm.Message, maxTokens int) []llm.Message {
	currentTokens := EstimateTotalTokens(messages)
	limit := int(float64(maxTokens) * (1 - ContextBuffer))

	if currentTokens <= limit {
		return messages
	}

	// Keep first 2 messages (system + first user) and last 10 messages
	keepEnd := 10
	if keepEnd >= len(messages) {
		return messages
	}
	keepStart := 2
	if keepStart >= len(messages) {
		return messages
	}

	// Work on the middle section
	middle := make([]llm.Message, len(messages[keepStart:len(messages)-keepEnd]))
	copy(middle, messages[keepStart:len(messages)-keepEnd])

	// First pass: summarize tool results (they're usually the biggest)
	for i := range middle {
		if middle[i].Role == "tool" && middle[i].Content != nil {
			content := *middle[i].Content
			if len(content) > 200 {
				lines := strings.Count(content, "\n") + 1
				summary := fmt.Sprintf("[Tool result truncated: %d lines, ~%d chars]", lines, len(content))
				middle[i] = llm.ToolResultMessage(middle[i].ToolCallID, summary)
			}
		}
	}

	result := make([]llm.Message, 0, keepStart+len(middle)+keepEnd)
	result = append(result, messages[:keepStart]...)
	result = append(result, middle...)
	result = append(result, messages[len(messages)-keepEnd:]...)

	// If still over limit, drop middle messages in pairs (tool_call + tool_result)
	for EstimateTotalTokens(result) > limit && len(result) > keepStart+keepEnd {
		// Remove from just after keepStart
		result = append(result[:keepStart], result[keepStart+1:]...)
	}

	return result
}
