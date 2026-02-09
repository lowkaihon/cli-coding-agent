package agent

import "github.com/lowkaihon/cli-coding-agent/llm"

// MessageHistory provides access to the conversation history.
func (a *Agent) MessageHistory() []llm.Message {
	return a.messages
}

// MessageCount returns the number of messages in history.
func (a *Agent) MessageCount() int {
	return len(a.messages)
}
