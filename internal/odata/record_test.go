package odata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatchRecord_returnsHTTPStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("expected PATCH, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	err := PatchRecord(context.Background(), cli, "People", "abc", map[string]any{"id": "abc"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("expected 404 status error, got %v", err)
	}
}
