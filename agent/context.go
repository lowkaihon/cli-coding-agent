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

// compactionPrompt returns the system prompt used when asking the LLM to summarize the conversation.
func compactionPrompt() string {
	return `You are a conversation summarizer. Your job is to produce a concise summary of the conversation provided.

Your summary MUST preserve:
- The user's current task and goal
- All file paths, function names, and code identifiers discussed
- Architectural decisions made and their rationale
- Any errors encountered and how they were resolved
- Key findings from file reads, searches, and tool outputs

Your summary MUST drop:
- Verbose tool outputs (full file contents, long grep results) — instead note what was learned
- Redundant back-and-forth that doesn't affect the current state
- Intermediate steps that led to dead ends (unless the dead end itself is informative)

Output a structured, concise summary. Do not include any preamble or meta-commentary.`
}

// serializeHistory formats conversation messages into readable text for the LLM to summarize.
func serializeHistory(messages []llm.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			sb.WriteString("[System]\n")
			if msg.Content != nil {
				// Truncate system prompt to avoid overwhelming the summary
				content := *msg.Content
				if len(content) > 500 {
					content = content[:500] + "...[truncated]"
				}
				sb.WriteString(content)
			}
		case "user":
			sb.WriteString("[User]\n")
			if msg.Content != nil {
				sb.WriteString(*msg.Content)
			}
		case "assistant":
			sb.WriteString("[Assistant]\n")
			if msg.Content != nil {
				sb.WriteString(*msg.Content)
			}
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&sb, "\n[Tool Call: %s(%s)]", tc.Function.Name, tc.Function.Arguments)
			}
		case "tool":
			sb.WriteString("[Tool Result]\n")
			if msg.Content != nil {
				content := *msg.Content
				// Truncate long tool results
				if len(content) > 1000 {
					content = content[:1000] + "...[truncated]"
				}
				sb.WriteString(content)
			}
		default:
			fmt.Fprintf(&sb, "[%s]\n", msg.Role)
			if msg.Content != nil {
				sb.WriteString(*msg.Content)
			}
		}
		sb.WriteString("\n\n")
	}
	return sb.String()
}
