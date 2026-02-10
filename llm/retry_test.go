package llm

import (
	"context"
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
