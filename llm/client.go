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

// LLMClient is the interface for interacting with an LLM API.
type LLMClient interface {
	SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)
	StreamMessage(ctx context.Context, messages []Message, tools []ToolDef) (<-chan StreamEvent, error)
}

// OpenAIClient implements LLMClient for the OpenAI API.
type OpenAIClient struct {
	apiKey    string
	model     string
	maxTokens int
	baseURL   string
	http      *http.Client
}

// NewOpenAIClient creates a new OpenAI API client.
func NewOpenAIClient(apiKey, model string, maxTokens int, baseURL string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		baseURL:   baseURL,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// SendMessage sends a non-streaming request to the OpenAI API.
func (c *OpenAIClient) SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error) {
	reqBody := ChatRequest{
		Model:     c.model,
		Messages:  messages,
		MaxTokens: c.maxTokens,
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var apiResp APIResponse
	err = c.doWithRetry(ctx, bodyBytes, &apiResp)
	if err != nil {
		return nil, err
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in API response")
	}

	choice := apiResp.Choices[0]
	return &Response{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
		Usage:        apiResp.Usage,
	}, nil
}

// doWithRetry executes an HTTP request with retry logic for transient errors.
func (c *OpenAIClient) doWithRetry(ctx context.Context, body []byte, result *APIResponse) error {
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

		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

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
			if attempt < 2 {
				continue
			}
			return fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, string(respBody))

		default:
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
		}
	}

	return fmt.Errorf("exhausted retries")
}
