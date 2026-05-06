package odata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPatchRecord_returnsHTTPStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("expected PATCH, got %s", r.Method)
		}
		if got := r.Header.Get("Prefer"); got != "return=minimal" {
			t.Fatalf("expected Prefer return=minimal, got %q", got)
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

func TestGetRecordByPK_returnsConcreteRecordPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "$filter=%22id%22%20eq%20%27site-1%27") {
			t.Fatalf("expected quoted/encoded filter, got query=%q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"id":              "site-1",
					"@odata.editLink": "Sites(40)",
				},
			},
		})
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	rec, recordPath, err := GetRecordByPK(context.Background(), cli, "Sites", "id", "site-1")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if rec["id"] != "site-1" {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if recordPath != "Sites(40)" {
		t.Fatalf("expected record path Sites(40), got %q", recordPath)
	}
}

func TestGetRecordByPKSelected_includesQuotedSelect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "$filter=%22id%22%20eq%20%27site-1%27") {
			t.Fatalf("expected quoted/encoded filter, got query=%q", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.RawQuery, "$select=%22id%22,%22name%22") {
			t.Fatalf("expected quoted select fields, got query=%q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{
				{
					"id":              "site-1",
					"name":            "Site",
					"heavyCalc":       "should not matter",
					"@odata.editLink": "Sites(40)",
				},
			},
		})
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	rec, recordPath, err := GetRecordByPKSelected(context.Background(), cli, "Sites", "id", "site-1", []string{"id", "name"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if rec["id"] != "site-1" || rec["name"] != "Site" {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if recordPath != "Sites(40)" {
		t.Fatalf("expected record path Sites(40), got %q", recordPath)
	}
}

func TestGetRecordPathSelected_includesQuotedSelect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/db/People('r1')" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "$select=%22id%22,%22name%22") {
			t.Fatalf("expected quoted select fields, got query=%q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "r1",
			"name": "A",
		})
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/db", Username: "u", Password: "p"}
	rec, err := GetRecordPathSelected(context.Background(), cli, "People('r1')", []string{"id", "name"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if rec["id"] != "r1" || rec["name"] != "A" {
		t.Fatalf("unexpected record: %#v", rec)
	}
}

func TestRecordPathFromMetadata_supportsAbsoluteODataID(t *testing.T) {
	rec := map[string]any{
		"@odata.id": "https://example.com/fmi/odata/v4/BetterForms_Prod/Sites(92)",
	}
	got := RecordPathFromMetadata("Sites", rec)
	if got != "Sites(92)" {
		t.Fatalf("expected Sites(92), got %q", got)
	}
}
