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
	"time"

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

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "People", plan, allowed, "id", "ModificationTimestamp", 1, 0, len(plan), nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&greenGetCount) != 0 {
		t.Fatalf("expected no destination GET, got %d", greenGetCount)
	}
	if atomic.LoadInt32(&greenPatchCount) != 1 {
		t.Fatalf("expected 1 PATCH, got %d", greenPatchCount)
	}
}

func TestApplyPlan_IgnoreFieldExcludedFromPatch(t *testing.T) {
	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("blue expected GET, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                    "r1",
			"ModificationTimestamp": "2026-05-01T00:00:00Z",
			"name":                  "A",
			"thumbURL":              "source-generated-value",
		})
	}))
	defer blue.Close()

	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("green expected PATCH, got %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode patch body: %v", err)
		}
		if _, ok := body["thumbURL"]; ok {
			t.Fatalf("ignored field should not be patched: %#v", body)
		}
		if body["name"] != "A" {
			t.Fatalf("expected name to be patched, body=%#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer green.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: "r1", Kind: domain.CopyToGreen}}
	allowed := map[string]struct{}{"id": {}, "ModificationTimestamp": {}, "name": {}}

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "People", plan, allowed, "id", "ModificationTimestamp", 1, 0, len(plan), nil); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReplicaFields_IgnoresExcludedField(t *testing.T) {
	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                    "r1",
			"ModificationTimestamp": "2026-05-01T00:00:00Z",
			"name":                  "A",
			"thumbURL":              "blue-local-generated",
		})
	}))
	defer blue.Close()

	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                    "r1",
			"ModificationTimestamp": "2026-05-01T00:00:00Z",
			"name":                  "A",
			"thumbURL":              "green-local-generated",
		})
	}))
	defer green.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: "r1", Kind: domain.CopyToBlue}}
	allowed := map[string]struct{}{"id": {}, "ModificationTimestamp": {}, "name": {}}

	if err := verifyReplicaFields(context.Background(), blueCli, greenCli, "People", plan, allowed, "id", "ModificationTimestamp"); err != nil {
		t.Fatalf("ignored local-generated field should not fail verification: %v", err)
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

func TestApplyPlan_SourceNotFound_SkipsOp(t *testing.T) {
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodPost {
			t.Fatalf("unexpected write call when source GET is not found")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer green.Close()

	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should not be called for this path.
		if r.Method == http.MethodPatch || r.Method == http.MethodPost || r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer blue.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: "gone", Kind: domain.CopyToBlue}}
	allowed := map[string]struct{}{"id": {}, "modificationTS": {}}

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "Inbox", plan, allowed, "id", "modificationTS", 1, 0, len(plan), nil); err != nil {
		t.Fatalf("expected skip on source not found, got error: %v", err)
	}
}

func TestApplyPlan_RecordLockRetriesContinueOtherRecords(t *testing.T) {
	oldAttempts := recordLockRetryMaxAttempts
	oldBase := recordLockRetryBaseDelay
	oldMax := recordLockRetryMaxDelay
	recordLockRetryMaxAttempts = 2
	recordLockRetryBaseDelay = time.Millisecond
	recordLockRetryMaxDelay = 2 * time.Millisecond
	defer func() {
		recordLockRetryMaxAttempts = oldAttempts
		recordLockRetryBaseDelay = oldBase
		recordLockRetryMaxDelay = oldMax
	}()

	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("blue expected GET, got %s", r.Method)
		}
		id := "r1"
		if strings.Contains(r.URL.Path, "r2") {
			id = "r2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             id,
			"modificationTS": "2026-05-01T00:00:00Z",
			"name":           "x",
		})
	}))
	defer blue.Close()

	var patchR1 int32
	var patchR2 int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("green expected PATCH, got %s", r.Method)
		}
		if strings.Contains(r.URL.Path, "r1") {
			atomic.AddInt32(&patchR1, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"301","message":"(301): Record is locked by another user"}}`))
			return
		}
		if strings.Contains(r.URL.Path, "r2") {
			atomic.AddInt32(&patchR2, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Fatalf("unexpected path: %s", r.URL.Path)
	}))
	defer green.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{
		{RecordID: "r1", Kind: domain.CopyToGreen},
		{RecordID: "r2", Kind: domain.CopyToGreen},
	}
	allowed := map[string]struct{}{"id": {}, "modificationTS": {}, "name": {}}

	deferred, err := applyPlan(context.Background(), blueCli, greenCli, "Inbox", plan, allowed, "id", "modificationTS", 1, 0, len(plan), nil)
	if err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	if len(deferred) != 1 || deferred[0].RecordID != "r1" || deferred[0].Reason != "record_lock" {
		t.Fatalf("unexpected deferred issues: %+v", deferred)
	}
	if atomic.LoadInt32(&patchR1) < 2 {
		t.Fatalf("expected lock retries for r1, got patch count=%d", patchR1)
	}
	if atomic.LoadInt32(&patchR2) != 1 {
		t.Fatalf("expected r2 to continue and patch once, got %d", patchR2)
	}
}

func TestApplyPlan_RecordLockRetryRecovers(t *testing.T) {
	oldAttempts := recordLockRetryMaxAttempts
	oldBase := recordLockRetryBaseDelay
	oldMax := recordLockRetryMaxDelay
	recordLockRetryMaxAttempts = 3
	recordLockRetryBaseDelay = time.Millisecond
	recordLockRetryMaxDelay = 2 * time.Millisecond
	defer func() {
		recordLockRetryMaxAttempts = oldAttempts
		recordLockRetryBaseDelay = oldBase
		recordLockRetryMaxDelay = oldMax
	}()

	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "r1",
			"modificationTS": "2026-05-01T00:00:00Z",
		})
	}))
	defer blue.Close()

	var patchCount int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("green expected PATCH, got %s", r.Method)
		}
		if atomic.AddInt32(&patchCount, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"301","message":"(301): Record is locked by another user"}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer green.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: "r1", Kind: domain.CopyToGreen}}
	allowed := map[string]struct{}{"id": {}, "modificationTS": {}}

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "Inbox", plan, allowed, "id", "modificationTS", 1, 0, len(plan), nil); err != nil {
		t.Fatalf("expected lock retry to recover, got error: %v", err)
	}
	if atomic.LoadInt32(&patchCount) < 2 {
		t.Fatalf("expected at least one retry, got patch count=%d", patchCount)
	}
}

func TestApplyPlan_SourceReadTransientDecodeRetry(t *testing.T) {
	oldAttempts := recordReadRetryMaxAttempts
	oldBase := recordReadRetryBaseDelay
	recordReadRetryMaxAttempts = 2
	recordReadRetryBaseDelay = time.Millisecond
	defer func() {
		recordReadRetryMaxAttempts = oldAttempts
		recordReadRetryBaseDelay = oldBase
	}()

	var greenGetCount int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			n := atomic.AddInt32(&greenGetCount, 1)
			if n == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("° transient junk"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "r1",
				"modificationTS": "2026-05-01T00:00:00Z",
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer green.Close()

	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer blue.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: "r1", Kind: domain.CopyToBlue}}
	allowed := map[string]struct{}{"id": {}, "modificationTS": {}}

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "Inbox", plan, allowed, "id", "modificationTS", 1, 0, len(plan), nil); err != nil {
		t.Fatalf("expected transient read decode retry to recover, got %v", err)
	}
	if atomic.LoadInt32(&greenGetCount) < 2 {
		t.Fatalf("expected at least two source GET attempts, got %d", greenGetCount)
	}
}

func TestApplyPlan_SourceReadExhausted_ContinuesOtherRecords(t *testing.T) {
	oldAttempts := recordReadRetryMaxAttempts
	oldBase := recordReadRetryBaseDelay
	recordReadRetryMaxAttempts = 2
	recordReadRetryBaseDelay = time.Millisecond
	defer func() {
		recordReadRetryMaxAttempts = oldAttempts
		recordReadRetryBaseDelay = oldBase
	}()

	var greenGetBad int32
	var bluePatchGood int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("green expected GET, got %s", r.Method)
		}
		if strings.Contains(r.URL.Path, "bad") {
			atomic.AddInt32(&greenGetBad, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("° broken"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "good",
			"modificationTS": "2026-05-01T00:00:00Z",
		})
	}))
	defer green.Close()

	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "good") {
			atomic.AddInt32(&bluePatchGood, 1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer blue.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{
		{RecordID: "bad", Kind: domain.CopyToBlue},
		{RecordID: "good", Kind: domain.CopyToBlue},
	}
	allowed := map[string]struct{}{"id": {}, "modificationTS": {}}

	deferred, err := applyPlan(context.Background(), blueCli, greenCli, "Forms", plan, allowed, "id", "modificationTS", 1, 0, len(plan), nil)
	if err != nil {
		t.Fatalf("expected no fatal error, got %v", err)
	}
	if len(deferred) != 1 || deferred[0].RecordID != "bad" || deferred[0].Reason != "source_read" {
		t.Fatalf("unexpected deferred issues: %+v", deferred)
	}
	if atomic.LoadInt32(&greenGetBad) < 2 {
		t.Fatalf("expected bad record read retries, got %d", greenGetBad)
	}
	if atomic.LoadInt32(&bluePatchGood) != 1 {
		t.Fatalf("expected good record to continue and patch once, got %d", bluePatchGood)
	}
}

func TestApplyPlan_KeyMismatch_ResolvesSourceAndDestinationRecordPaths(t *testing.T) {
	const logicalID = "ST_UUID_1"

	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("blue expected GET, got %s", r.Method)
		}
		if strings.Contains(r.URL.RawQuery, "$filter=") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{
						"id":              logicalID,
						"modificationTS":  "2026-05-01T00:00:00Z",
						"name":            "site from blue",
						"@odata.editLink": "Sites(40)",
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"8309","message":"An expression contains incompatible data types."}}`))
	}))
	defer blue.Close()

	var patchedDefaultPath int32
	var patchedResolvedPath int32
	var posts int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if strings.Contains(r.URL.RawQuery, "$filter=") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"value": []map[string]any{
						{
							"id":              logicalID,
							"@odata.editLink": "Sites(92)",
						},
					},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		case http.MethodPatch:
			if strings.Contains(r.URL.Path, "Sites('"+logicalID+"')") {
				atomic.AddInt32(&patchedDefaultPath, 1)
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"8309","message":"An expression contains incompatible data types."}}`))
				return
			}
			if strings.Contains(r.URL.Path, "Sites(92)") {
				atomic.AddInt32(&patchedResolvedPath, 1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			t.Fatalf("unexpected patch path: %s", r.URL.Path)
		case http.MethodPost:
			atomic.AddInt32(&posts, 1)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer green.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: logicalID, Kind: domain.CopyToGreen}}
	allowed := map[string]struct{}{"id": {}, "modificationTS": {}, "name": {}}

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "Sites", plan, allowed, "id", "modificationTS", 1, 0, len(plan), nil); err != nil {
		t.Fatalf("expected key resolution flow to succeed, got %v", err)
	}
	if atomic.LoadInt32(&patchedDefaultPath) != 1 {
		t.Fatalf("expected exactly one failed default-path patch, got %d", patchedDefaultPath)
	}
	if atomic.LoadInt32(&patchedResolvedPath) != 1 {
		t.Fatalf("expected one resolved-path patch, got %d", patchedResolvedPath)
	}
	if atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("expected no POST create, got %d", posts)
	}
}

func TestApplyPlan_KeyMismatch_UsesSelectedPKLookups(t *testing.T) {
	const logicalID = "ST_UUID_2"

	var sourcePKLookup int32
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("green expected GET, got %s", r.Method)
		}
		if strings.Contains(r.URL.RawQuery, "$filter=") {
			atomic.AddInt32(&sourcePKLookup, 1)
			if !strings.Contains(r.URL.RawQuery, "$select=%22id%22,%22name%22") {
				t.Fatalf("source PK lookup should use selected fields, query=%q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{
						"id":              logicalID,
						"name":            "site from green",
						"heavyCalc":       strings.Repeat("x", 1000),
						"@odata.editLink": "Sites(40)",
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"8309","message":"An expression contains incompatible data types."}}`))
	}))
	defer green.Close()

	var destPKLookup int32
	var patchedResolvedPath int32
	blue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if strings.Contains(r.URL.RawQuery, "$filter=") {
				atomic.AddInt32(&destPKLookup, 1)
				if !strings.Contains(r.URL.RawQuery, "$select=%22id%22") {
					t.Fatalf("destination key lookup should select only id, query=%q", r.URL.RawQuery)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"value": []map[string]any{
						{
							"id":              logicalID,
							"@odata.editLink": "Sites(92)",
						},
					},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPatch:
			if strings.Contains(r.URL.Path, "Sites('"+logicalID+"')") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"8309","message":"An expression contains incompatible data types."}}`))
				return
			}
			if strings.Contains(r.URL.Path, "Sites(92)") {
				atomic.AddInt32(&patchedResolvedPath, 1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode patch body: %v", err)
				}
				if _, ok := body["heavyCalc"]; ok {
					t.Fatalf("patch should not include fields outside allowlist: %#v", body)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			t.Fatalf("unexpected patch path: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer blue.Close()

	blueCli := &odata.Client{BaseURL: blue.URL + "/db", Username: "u", Password: "p"}
	greenCli := &odata.Client{BaseURL: green.URL + "/db", Username: "u", Password: "p"}
	plan := []domain.Op{{RecordID: logicalID, Kind: domain.CopyToBlue}}
	allowed := map[string]struct{}{"id": {}, "name": {}}

	if _, err := applyPlan(context.Background(), blueCli, greenCli, "Sites", plan, allowed, "id", "ModificationTimestamp", 1, 0, len(plan), nil); err != nil {
		t.Fatalf("expected selected key resolution flow to succeed, got %v", err)
	}
	if atomic.LoadInt32(&sourcePKLookup) != 1 {
		t.Fatalf("expected one selected source PK lookup, got %d", sourcePKLookup)
	}
	if atomic.LoadInt32(&destPKLookup) != 1 {
		t.Fatalf("expected one selected destination PK lookup, got %d", destPKLookup)
	}
	if atomic.LoadInt32(&patchedResolvedPath) != 1 {
		t.Fatalf("expected resolved path patch, got %d", patchedResolvedPath)
	}
}
