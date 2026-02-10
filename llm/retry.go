package llm

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// retryConfig holds retry parameters for HTTP requests.
type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// defaultRetryConfig returns standard retry settings.
func defaultRetryConfig() retryConfig {
	return retryConfig{
		maxRetries: 5,
		baseDelay:  2 * time.Second,
		maxDelay:   60 * time.Second,
	}
}

// retryableError is returned when retries are exhausted, containing the last status and body.
type retryableError struct {
	StatusCode int
	Body       string
	Retries    int
}

func (e *retryableError) Error() string {
	if e.StatusCode == 429 {
		return fmt.Sprintf("rate limited (HTTP 429) after %d retries: %s", e.Retries, e.Body)
	}
	return fmt.Sprintf("server error (HTTP %d) after %d retries: %s", e.StatusCode, e.Retries, e.Body)
}

// doWithRetry executes an HTTP request function with exponential backoff retry
// for 429 and 5xx errors. It respects the Retry-After header when present.
// The doReq function receives the attempt number (0-based) and should return
// the HTTP response. On success (2xx), it returns the response for the caller
// to process. On non-retryable errors (4xx except 429), it returns immediately.
func doWithRetry(ctx context.Context, cfg retryConfig, doReq func() (*http.Response, error)) (*http.Response, error) {
	for attempt := 0; attempt <= cfg.maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt-1, cfg.baseDelay, cfg.maxDelay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		resp, err := doReq()
		if err != nil {
			if attempt < cfg.maxRetries {
				continue
			}
			return nil, fmt.Errorf("http request: %w", err)
		}

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return resp, nil

		case resp.StatusCode == 401 || resp.StatusCode == 403:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("authentication error (HTTP %d): %s", resp.StatusCode, string(body))

		case resp.StatusCode == 429, resp.StatusCode >= 500:
			// Check Retry-After header and use it as minimum delay for next attempt
			if retryAfter := parseRetryAfter(resp); retryAfter > 0 && retryAfter < cfg.maxDelay {
				nextBackoff := backoffDelay(attempt, cfg.baseDelay, cfg.maxDelay)
				if retryAfter > nextBackoff {
					// Override base delay temporarily for next iteration
					cfg.baseDelay = retryAfter
				}
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if attempt < cfg.maxRetries {
				continue
			}
			return nil, &retryableError{
				StatusCode: resp.StatusCode,
				Body:       string(body),
				Retries:    cfg.maxRetries,
			}

		default:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(body))
		}
	}

	return nil, fmt.Errorf("exhausted retries")
}

// backoffDelay calculates the delay for a given attempt using exponential backoff with jitter.
func backoffDelay(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt)))
	jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
	delay += jitter
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

// parseRetryAfter extracts the Retry-After header value as a duration.
// Supports integer seconds format. Returns 0 if not present or unparseable.
func parseRetryAfter(resp *http.Response) time.Duration {
	val := resp.Header.Get("Retry-After")
	if val == "" {
		return 0
	}
	seconds, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
