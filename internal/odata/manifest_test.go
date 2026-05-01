package odata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchManifest_singlePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "filter") {
			t.Errorf("expected filter in query: %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{"id": "1", "ModificationTimestamp": "2026-05-01T12:00:00Z"},
			},
		})
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	m, err := FetchManifest(context.Background(), cli, "People", start, end, "id", "ModificationTimestamp")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || !m["1"].Equal(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("manifest: %+v err=%v", m, err)
	}
}

func TestFetchManifest_nextLink(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "page2") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": "b", "ModificationTimestamp": "2026-01-02T00:00:00Z"},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{"id": "a", "ModificationTimestamp": "2026-01-01T00:00:00Z"},
			},
			"@odata.nextLink": srv.URL + "/db/page2",
		})
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	m, err := FetchManifest(context.Background(), cli, "People", start, end, "id", "ModificationTimestamp")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 rows, got %d %+v", len(m), m)
	}
}
