package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoWithRetry_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	resp, err := doWithRetry(context.Background(), defaultRetryConfig(), func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDoWithRetry_429ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			w.Write([]byte(`rate limited`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfg := retryConfig{maxRetries: 5, baseDelay: 10 * time.Millisecond, maxDelay: 100 * time.Millisecond}
	resp, err := doWithRetry(context.Background(), cfg, func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestDoWithRetry_ExhaustedRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`rate limited`))
	}))
	defer server.Close()

	cfg := retryConfig{maxRetries: 2, baseDelay: 10 * time.Millisecond, maxDelay: 50 * time.Millisecond}
	_, err := doWithRetry(context.Background(), cfg, func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	retryErr, ok := err.(*retryableError)
	if !ok {
		t.Fatalf("expected *retryableError, got %T: %v", err, err)
	}
	if retryErr.StatusCode != 429 {
		t.Fatalf("expected status 429, got %d", retryErr.StatusCode)
	}
}

func TestDoWithRetry_AuthError_NoRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		w.Write([]byte(`unauthorized`))
	}))
	defer server.Close()

	cfg := retryConfig{maxRetries: 3, baseDelay: 10 * time.Millisecond, maxDelay: 50 * time.Millisecond}
	_, err := doWithRetry(context.Background(), cfg, func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 attempt (no retry for auth errors), got %d", calls.Load())
	}
}

func TestDoWithRetry_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`rate limited`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cfg := retryConfig{maxRetries: 5, baseDelay: time.Second, maxDelay: 10 * time.Second}
	_, err := doWithRetry(ctx, cfg, func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDoWithRetry_CancelledDuringRetryBackoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`rate limited`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cfg := retryConfig{maxRetries: 5, baseDelay: 2 * time.Second, maxDelay: 10 * time.Second}

	// Cancel after the first request completes and retry backoff begins
	var calls atomic.Int32
	_, err := doWithRetry(ctx, cfg, func() (*http.Response, error) {
		if calls.Add(1) == 1 {
			// Cancel during the backoff wait after first 429
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
		}
		return http.Get(server.URL)
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Should be a retryCancelledError with the 429 status preserved
	var retryCancel *retryCancelledError
	if !errors.As(err, &retryCancel) {
		t.Fatalf("expected *retryCancelledError, got %T: %v", err, err)
	}
	if retryCancel.LastStatusCode != 429 {
		t.Errorf("expected LastStatusCode=429, got %d", retryCancel.LastStatusCode)
	}
	if retryCancel.Attempt < 1 {
		t.Errorf("expected Attempt >= 1, got %d", retryCancel.Attempt)
	}

	// errors.Is should still match context.Canceled via Unwrap
	if !errors.Is(err, context.Canceled) {
		t.Error("expected errors.Is(err, context.Canceled) to be true")
	}
}

func TestDoWithRetry_ServerError_Retries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 1 {
			w.WriteHeader(500)
			w.Write([]byte(`internal error`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfg := retryConfig{maxRetries: 3, baseDelay: 10 * time.Millisecond, maxDelay: 50 * time.Millisecond}
	resp, err := doWithRetry(context.Background(), cfg, func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if calls.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls.Load())
	}
}

func TestDoWithRetry_RetryAfterIsOneShot(t *testing.T) {
	// Verify that a Retry-After header only affects the immediately next attempt,
	// not all subsequent attempts (i.e., exponential backoff is preserved).
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// First call: 429 with large Retry-After
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			w.Write([]byte(`rate limited`))
			return
		}
		if n == 2 {
			// Second call: 429 without Retry-After
			w.WriteHeader(429)
			w.Write([]byte(`rate limited`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`ok`))
	}))
	defer server.Close()

	cfg := retryConfig{maxRetries: 5, baseDelay: 10 * time.Millisecond, maxDelay: 5 * time.Second}

	start := time.Now()
	resp, err := doWithRetry(context.Background(), cfg, func() (*http.Response, error) {
		return http.Get(server.URL)
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// The first retry should wait ~1s (Retry-After), the second should use normal
	// exponential backoff (~20ms = 10ms * 2^1 + jitter), not ~2s.
	// Total should be well under 2s if backoff isn't permanently overridden.
	if elapsed > 2*time.Second {
		t.Errorf("total elapsed %v suggests Retry-After permanently overrode backoff", elapsed)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"not-a-number", 0},
		{"0", 0},
		{"30", 30 * time.Second},
	}
	for _, tt := range tests {
		resp := &http.Response{Header: http.Header{}}
		if tt.header != "" {
			resp.Header.Set("Retry-After", tt.header)
		}
		got := parseRetryAfter(resp)
		if got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}
