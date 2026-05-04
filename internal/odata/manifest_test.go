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

func TestFetchManifest_manualSkipWhenNoNextLink(t *testing.T) {
	// Simulate a server that supports $skip but does not emit @odata.nextLink.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		skip := q.Get("$skip")
		switch skip {
		case "":
			value := make([]map[string]any, 50)
			for i := 0; i < 50; i++ {
				value[i] = map[string]any{
					"id":                    fmt.Sprintf("id-%03d", i),
					"ModificationTimestamp": "2026-01-01T00:00:00Z",
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		case "50":
			value := make([]map[string]any, 30)
			for i := 0; i < 30; i++ {
				value[i] = map[string]any{
					"id":                    fmt.Sprintf("id-%03d", 50+i),
					"ModificationTimestamp": "2026-01-01T00:00:00Z",
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		default:
			t.Fatalf("unexpected $skip=%q", skip)
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
	u, err := manifestPageURL("https://example.test/base", "People", start, end, "id", "ModificationTimestamp", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "$select=%22id%22,%22ModificationTimestamp%22") {
		t.Fatalf("expected quoted select in url, got %s", u)
	}
}
