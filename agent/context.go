package agent

import (
	"encoding/json"
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

// EstimateToolDefTokens estimates token count for tool definitions using the chars/4 heuristic.
func EstimateToolDefTokens(defs []llm.ToolDef) int {
	data, err := json.Marshal(defs)
	if err != nil {
		return 0
	}
	tokens := len(data) / CharsPerToken
	if tokens < 1 && len(defs) > 0 {
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
	return `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions. This summary should be thorough in capturing technical details, code patterns, and architectural decisions essential for continuing work without losing context.

Before providing your final summary, wrap your analysis in <analysis> tags to organize your thoughts. In your analysis:
1. Chronologically analyze each message, identifying: the user's explicit requests and intents, your approach, key decisions and code patterns, specific file names, code snippets, function signatures, and file edits.
2. Note errors encountered and how they were fixed, paying special attention to user feedback.
3. Double-check for technical accuracy and completeness.

Your summary should include these sections:

1. Primary Request and Intent: All of the user's explicit requests and intents in detail.
2. Key Technical Concepts: Important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Specific files examined, modified, or created, with summaries of why each is important and what changes were made. Include code snippets where applicable.
4. Errors and Fixes: All errors encountered and how they were resolved, including any user feedback.
5. Problem Solving: Problems solved and any ongoing troubleshooting.
6. Pending Tasks: Any tasks explicitly asked for that remain incomplete.
7. Current Work: Precisely what was being worked on immediately before this summary, including file names and code snippets.
8. Optional Next Step: The next step related to the most recent work, only if directly in line with the user's most recent explicit request.

Drop verbose tool outputs (full file contents, long search results) — instead note what was learned. Drop redundant back-and-forth and dead-end steps unless the dead end itself is informative.

Output the summary directly. Do not include any preamble or meta-commentary outside the analysis and summary.`
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
