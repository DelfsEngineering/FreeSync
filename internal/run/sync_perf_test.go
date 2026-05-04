package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DelfsEngineering/FreeSync/internal/config"
	"github.com/DelfsEngineering/FreeSync/internal/domain"
	"github.com/DelfsEngineering/FreeSync/internal/odata"
)

func TestApplyPlan_PatchFirst_NoDestinationGet(t *testing.T) {
	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("blue expected GET, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                    "r1",
			"ModificationTimestamp": "2026-05-01T00:00:00Z",
			"name":                  "A",
		})
	}))
	defer blue.Close()

	var greenGetCount int32
	var greenPatchCount int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			atomic.AddInt32(&greenGetCount, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case http.MethodPatch:
			atomic.AddInt32(&greenPatchCount, 1)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			t.Fatalf("unexpected POST")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer green.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: "r1", Kind: domain.CopyToGreen}}
	allowed := map[string]struct{}{"id": {}, "ModificationTimestamp": {}, "name": {}}

	if err := applyPlan(context.Background(), blueCli, greenCli, "People", plan, allowed, "ModificationTimestamp", 1, 0, len(plan), nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&greenGetCount) != 0 {
		t.Fatalf("expected no destination GET, got %d", greenGetCount)
	}
	if atomic.LoadInt32(&greenPatchCount) != 1 {
		t.Fatalf("expected 1 PATCH, got %d", greenPatchCount)
	}
}

func TestOnce_NoPlan_SkipsMetadataAndVerify(t *testing.T) {
	var metadataHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/$metadata") {
			atomic.AddInt32(&metadataHits, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("metadata should not be requested when plan is empty"))
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "$filter=") {
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Servers: []config.Server{
			{ID: "blue", URL: srv.URL + "/blue", Username: "u", Password: "p"},
			{ID: "green", URL: srv.URL + "/green", Username: "u", Password: "p"},
		},
		Tables: []config.TableSpec{
			{Name: "People", PrimaryKey: "id", ModifiedField: "ModificationTimestamp"},
		},
		InitialLookback: "1d",
		OverlapMinutes:  10,
		SchemaMode:      "intersection",
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	err := Once(context.Background(), cfg, Options{Apply: true, StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&metadataHits) != 0 {
		t.Fatalf("expected 0 metadata requests on no-plan run, got %d", metadataHits)
	}
}
