package odata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestFetchManifestWithProgress_reportsPages(t *testing.T) {
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
	var pages []int
	m, err := FetchManifestWithProgress(context.Background(), cli, "People", start, end, "id", "ModificationTimestamp", func(pageNum, pageRows, totalRows int) {
		pages = append(pages, pageNum)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 rows, got %d %+v", len(m), m)
	}
	if len(pages) != 2 || pages[0] != 1 || pages[1] != 2 {
		t.Fatalf("unexpected page callbacks: %+v", pages)
	}
}

func TestFetchManifest_manualKeysetWhenNoNextLink(t *testing.T) {
	// Simulate a server that does not emit @odata.nextLink.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		filter := q.Get("$filter")
		switch {
		case strings.Contains(filter, "\"ModificationTimestamp\" gt 2026-01-01T00:00:00Z"):
			value := make([]map[string]any, 30)
			for i := 0; i < 30; i++ {
				value[i] = map[string]any{
					"id":                    fmt.Sprintf("id-%03d", 50+i),
					"ModificationTimestamp": "2026-01-01T00:00:00Z",
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		case strings.Contains(filter, "and (\"ModificationTimestamp\" gt"):
			t.Fatalf("unexpected keyset cursor in filter: %q", filter)
		default:
			value := make([]map[string]any, 50)
			for i := 0; i < 50; i++ {
				value[i] = map[string]any{
					"id":                    fmt.Sprintf("id-%03d", i),
					"ModificationTimestamp": "2026-01-01T00:00:00Z",
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		}
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	m, err := FetchManifest(context.Background(), cli, "People", start, end, "id", "ModificationTimestamp")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 80 {
		t.Fatalf("want 80 rows, got %d", len(m))
	}
}

func TestManifestPageURL_includesQuotedSelect(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	u, err := manifestPageURL("https://example.test/base", "People", start, end, "id", "ModificationTimestamp", "", 50, "", time.Time{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "$select=%22id%22,%22ModificationTimestamp%22") {
		t.Fatalf("expected quoted select in url, got %s", u)
	}
	if !strings.Contains(u, "$orderby=%22ModificationTimestamp%22%20asc,%22id%22%20asc") {
		t.Fatalf("expected stable quoted orderby in url, got %s", u)
	}
}

func TestManifestPageURL_includesKeysetCursor(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	cursorMod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	u, err := manifestPageURL("https://example.test/base", "People", start, end, "id", "ModificationTimestamp", "", 50, "abc-123", cursorMod, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "%22ModificationTimestamp%22%20gt%202026-01-02T03:04:05Z") {
		t.Fatalf("expected keyset cursor in filter, got %s", u)
	}
	if !strings.Contains(u, "%22id%22%20gt%20%27abc-123%27") {
		t.Fatalf("expected keyset id clause in filter, got %s", u)
	}
}

func TestFetchManifestHead_returnsOrderedRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "$top=3") {
			t.Fatalf("expected top=3, got query %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{"id": "a", "ModificationTimestamp": "2026-01-01T00:00:00Z"},
				{"id": "b", "ModificationTimestamp": "2026-01-01T00:05:00Z"},
			},
		})
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := FetchManifestHead(context.Background(), cli, "People", start, end, "id", "ModificationTimestamp", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d", len(rows))
	}
	if rows[0].ID != "a" || rows[1].ID != "b" {
		t.Fatalf("unexpected row order: %+v", rows)
	}
}
