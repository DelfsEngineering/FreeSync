package odata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetJSON_retriesTransient503(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cli := &Client{
		BaseURL:          srv.URL,
		Username:         "u",
		Password:         "p",
		RetryMaxAttempts: 3,
		RetryBaseDelay:   1 * time.Millisecond,
		RetryMaxDelay:    2 * time.Millisecond,
	}
	b, code, err := cli.GetJSON(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("status: got %d", code)
	}
	if string(b) != `{"ok":true}` {
		t.Fatalf("body: %s", string(b))
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestPatchRecord_retries429(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("expected PATCH, got %s", r.Method)
		}
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cli := &Client{
		BaseURL:          srv.URL + "/db",
		Username:         "u",
		Password:         "p",
		RetryMaxAttempts: 2,
		RetryBaseDelay:   1 * time.Millisecond,
		RetryMaxDelay:    2 * time.Millisecond,
	}
	err := PatchRecord(context.Background(), cli, "People", "abc", map[string]any{"id": "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}
