// Package run orchestrates one sync pass (manifest → plan → optional apply → verify → checkpoint).
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DelfsEngineering/FreeSync/internal/config"
	"github.com/DelfsEngineering/FreeSync/internal/domain"
	"github.com/DelfsEngineering/FreeSync/internal/odata"
	"github.com/DelfsEngineering/FreeSync/internal/state"
	"github.com/DelfsEngineering/FreeSync/internal/timespec"
)

var (
	recordLockRetryMaxAttempts = 5
	recordLockRetryBaseDelay   = 250 * time.Millisecond
	recordLockRetryMaxDelay    = 3 * time.Second
	recordReadRetryMaxAttempts = 3
	recordReadRetryBaseDelay   = 200 * time.Millisecond
	metadataRetryMaxAttempts   = 3
	metadataRetryBaseDelay     = 300 * time.Millisecond
)

// Options controls a single run.
type Options struct {
	Apply     bool
	OneWay    string // "", "to-blue", "to-green"
	StatePath string
	Logger    *log.Logger
}

// Once executes sync for all configured tables (delete journal not wired — passes nil).
func Once(ctx context.Context, cfg *config.Config, opt Options) error {
	logf := func(format string, args ...any) {
		if opt.Logger != nil {
			opt.Logger.Printf(format, args...)
		} else {
			fmt.Printf(format+"\n", args...)
		}
	}

	mode, err := normalizeOneWayMode(opt.OneWay)
	if err != nil {
		return err
	}

	blueSrv, greenSrv, err := cfg.BlueGreen()
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	blueClient := &odata.Client{BaseURL: odata.TrimBase(blueSrv.URL), Username: blueSrv.Username, Password: blueSrv.Password, HTTPClient: httpClient, Logf: logf}
	greenClient := &odata.Client{BaseURL: odata.TrimBase(greenSrv.URL), Username: greenSrv.Username, Password: greenSrv.Password, HTTPClient: httpClient, Logf: logf}

	overlap := time.Duration(cfg.OverlapMinutes) * time.Minute
	if cfg.OverlapMinutes <= 0 {
		overlap = 10 * time.Minute
	}

	lookback, err := timespec.Parse(cfg.InitialLookback)
	if err != nil {
		return fmt.Errorf("initialLookback: %w", err)
	}
	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	maxLookback := 90 * 24 * time.Hour
	if strings.TrimSpace(cfg.MaxLookback) != "" {
		maxLookback, err = timespec.Parse(cfg.MaxLookback)
		if err != nil {
			return fmt.Errorf("maxLookback: %w", err)
		}
		if maxLookback <= 0 {
			maxLookback = 90 * 24 * time.Hour
		}
	}

	now := time.Now().UTC()
	schemaMode := strings.ToLower(strings.TrimSpace(cfg.SchemaMode))
	if schemaMode == "" {
		schemaMode = "intersection"
	}
	if schemaMode != "intersection" {
		return fmt.Errorf("unsupported schemaMode %q (only \"intersection\")", cfg.SchemaMode)
	}

	for _, tbl := range cfg.Tables {
		if tbl.Name == "" {
			continue
		}
		pk, mod := cfg.PKMod(tbl)

		var window domain.SyncWindow
		safe, ok, err := state.LoadSafeThrough(opt.StatePath, tbl.Name)
		if err != nil {
			return err
		}
		if !ok {
			start := now.Add(-lookback)
			if cfg.BootstrapBinary() {
				logf("table %s: bootstrapMode=binary (lookback=%s maxLookback=%s)", tbl.Name, lookback, maxLookback)
				probe := func(ts time.Time) (bool, error) {
					ctxProbe, cancel := context.WithTimeout(ctx, 45*time.Second)
					defer cancel()
					blueHead, err := odata.FetchManifestHead(ctxProbe, blueClient, tbl.Name, ts, now, pk, mod, 5)
					if err != nil {
						return false, fmt.Errorf("blue probe: %w", err)
					}
					greenHead, err := odata.FetchManifestHead(ctxProbe, greenClient, tbl.Name, ts, now, pk, mod, 5)
					if err != nil {
						return false, fmt.Errorf("green probe: %w", err)
					}
					if len(blueHead) != len(greenHead) {
						return true, nil
					}
					for i := range blueHead {
						if blueHead[i].ID != greenHead[i].ID || !blueHead[i].Mod.Equal(greenHead[i].Mod) {
							return true, nil
						}
					}
					return false, nil
				}
				s, err := findDivergenceBoundaryBinary(now, maxLookback, probe, logf)
				if err != nil {
					logf("table %s: binary bootstrap probe failed (%v); fallback to fixed lookback=%s", tbl.Name, err, lookback)
				} else {
					start = s
				}
			}
			window = domain.SyncWindow{Start: start, End: now}
			logf("table %s: bootstrap window [%s .. %s]", tbl.Name, window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339))
		} else {
			window = domain.ComputeSyncWindow(safe, overlap, now)
			logf("table %s: checkpoint window [%s .. %s] (safeThrough=%s)", tbl.Name, window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339), safe.Format(time.RFC3339))
		}

		var mBlue map[string]time.Time
		logf("table %s: fetching blue manifest...", tbl.Name)
		err = withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: blue manifest request", tbl.Name), func() error {
			var fetchErr error
			mBlue, fetchErr = odata.FetchManifestWithProgress(ctx, blueClient, tbl.Name, window.Start, window.End, pk, mod, func(pageNum, pageRows, totalRows int) {
				logf("table %s: blue manifest page=%d rows=%d total=%d", tbl.Name, pageNum, pageRows, totalRows)
			})
			return fetchErr
		})
		if err != nil {
			return fmt.Errorf("%s blue manifest: %w", tbl.Name, err)
		}
		var mGreen map[string]time.Time
		logf("table %s: fetching green manifest...", tbl.Name)
		err = withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: green manifest request", tbl.Name), func() error {
			var fetchErr error
			mGreen, fetchErr = odata.FetchManifestWithProgress(ctx, greenClient, tbl.Name, window.Start, window.End, pk, mod, func(pageNum, pageRows, totalRows int) {
				logf("table %s: green manifest page=%d rows=%d total=%d", tbl.Name, pageNum, pageRows, totalRows)
			})
			return fetchErr
		})
		if err != nil {
			return fmt.Errorf("%s green manifest: %w", tbl.Name, err)
		}
		logf("table %s: manifest rows blue=%d green=%d", tbl.Name, len(mBlue), len(mGreen))

		planAll := domain.BuildPlan(mBlue, mGreen, nil)
		plan := filterPlanOneWay(planAll, mode)
		if mode == "" {
			logf("table %s: plan ops=%d", tbl.Name, len(plan))
		} else {
			logf("table %s: plan ops=%d (oneWay=%s filtered=%d)", tbl.Name, len(plan), mode, len(planAll)-len(plan))
		}
		for _, op := range plan {
			logf("  %s %s", op.Kind, op.RecordID)
		}

		if len(plan) > 0 && !opt.Apply {
			logf("table %s: dry-run only — not applying, verifying, or advancing checkpoint (use -apply)", tbl.Name)
			continue
		}
		if len(plan) == 0 {
			// Nothing to apply and no writes happened, so a second manifest verify pass is unnecessary.
			if err := state.SaveSafeThrough(opt.StatePath, tbl.Name, window.End); err != nil {
				return err
			}
			logf("table %s: no changes; checkpoint advanced to %s", tbl.Name, window.End.Format(time.RFC3339))
			continue
		}

		blueProps, err := entityPropertiesWithRetry(ctx, blueClient, tbl.Name, logf)
		if err != nil {
			return fmt.Errorf("%s blue metadata: %w", tbl.Name, err)
		}
		greenProps, err := entityPropertiesWithRetry(ctx, greenClient, tbl.Name, logf)
		if err != nil {
			return fmt.Errorf("%s green metadata: %w", tbl.Name, err)
		}
		allowed := buildSyncFieldAllowlist(blueProps, greenProps, tbl.FieldOverrides, tbl.IgnoreFields, pk, mod)
		if len(allowed) == 0 {
			return fmt.Errorf("%s: no sync fields after metadata + overrides", tbl.Name)
		}

		var deferred []deferredIssue
		if opt.Apply && len(plan) > 0 {
			batchSize := cfg.ApplyBatchSize()
			maxWorkers := cfg.ApplyWorkers()
			totalBatches := (len(plan) + batchSize - 1) / batchSize
			logf("table %s: apply start ops=%d batchSize=%d maxWorkers=%d", tbl.Name, len(plan), batchSize, maxWorkers)
			for bi, i := 0, 0; i < len(plan); bi, i = bi+1, i+batchSize {
				end := i + batchSize
				if end > len(plan) {
					end = len(plan)
				}
				chunk := plan[i:end]
				logf("table %s: apply batch %d/%d ops=%d", tbl.Name, bi+1, totalBatches, len(chunk))
				batchDeferred, err := applyPlan(ctx, blueClient, greenClient, tbl.Name, chunk, allowed, pk, mod, maxWorkers, i, len(plan), logf)
				if err != nil {
					return fmt.Errorf("%s apply batch %d: %w", tbl.Name, bi+1, err)
				}
				deferred = append(deferred, batchDeferred...)
			}
			if len(deferred) > 0 {
				logf("table %s: deferred %d ops for later retry (continuing run)", tbl.Name, len(deferred))
				for i := 0; i < len(deferred) && i < 10; i++ {
					logf("table %s: deferred %s %s reason=%s", tbl.Name, deferred[i].Kind, deferred[i].RecordID, deferred[i].Reason)
				}
				if err := appendDeferredIssues(opt.StatePath, tbl.Name, deferred); err != nil {
					logf("table %s: warning: could not persist deferred issues: %v", tbl.Name, err)
				}
			}
		}

		// Rows written during apply get ModificationTimestamp at insert time, often after the
		// window.End captured at run start — widen checkpoint upper bound.
		endThrough := window.End
		if opt.Apply && len(plan) > 0 {
			endThrough = time.Now().UTC()
		}

		if cfg.VerifyStrict() && len(deferred) == 0 {
			// Verify: re-fetch manifests; expect empty plan.
			var mBlue2 map[string]time.Time
			logf("table %s: verify fetch blue manifest...", tbl.Name)
			err = withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: verify blue request", tbl.Name), func() error {
				var fetchErr error
				mBlue2, fetchErr = odata.FetchManifestWithProgress(ctx, blueClient, tbl.Name, window.Start, endThrough, pk, mod, func(pageNum, pageRows, totalRows int) {
					logf("table %s: verify blue page=%d rows=%d total=%d", tbl.Name, pageNum, pageRows, totalRows)
				})
				return fetchErr
			})
			if err != nil {
				return fmt.Errorf("%s verify blue: %w", tbl.Name, err)
			}
			var mGreen2 map[string]time.Time
			logf("table %s: verify fetch green manifest...", tbl.Name)
			err = withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: verify green request", tbl.Name), func() error {
				var fetchErr error
				mGreen2, fetchErr = odata.FetchManifestWithProgress(ctx, greenClient, tbl.Name, window.Start, endThrough, pk, mod, func(pageNum, pageRows, totalRows int) {
					logf("table %s: verify green page=%d rows=%d total=%d", tbl.Name, pageNum, pageRows, totalRows)
				})
				return fetchErr
			})
			if err != nil {
				return fmt.Errorf("%s verify green: %w", tbl.Name, err)
			}
			plan2All := domain.BuildPlan(mBlue2, mGreen2, nil)
			plan2 := filterPlanOneWay(plan2All, mode)
			if len(plan2) > 0 {
				// FileMaker bumps ModificationTimestamp on each write; pure LWW on timestamps may
				// still suggest copy ops even when replicated fields already match — confirm payload equality.
				if err := verifyReplicaFields(ctx, blueClient, greenClient, tbl.Name, plan2, allowed, pk, mod); err != nil {
					return fmt.Errorf("%s verify failed (%d pending time-order ops): %w", tbl.Name, len(plan2), err)
				}
				logf("table %s: verify ok (%d time-order ops ignored — replicated fields match)", tbl.Name, len(plan2))
			} else if mode != "" && len(plan2All) > 0 {
				logf("table %s: verify ok (oneWay=%s filtered %d opposite-direction ops)", tbl.Name, mode, len(plan2All))
			}
		} else if cfg.VerifyStrict() && len(deferred) > 0 {
			logf("table %s: verify skipped (deferredOps=%d)", tbl.Name, len(deferred))
		} else {
			logf("table %s: verify skipped (verifyMode=off)", tbl.Name)
		}

		if err := state.SaveSafeThrough(opt.StatePath, tbl.Name, endThrough); err != nil {
			return err
		}
		logf("table %s: checkpoint advanced to %s", tbl.Name, endThrough.Format(time.RFC3339))
	}
	return nil
}

func normalizeOneWayMode(mode string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "":
		return "", nil
	case "to-blue", "to-green":
		return strings.TrimSpace(strings.ToLower(mode)), nil
	default:
		return "", fmt.Errorf("invalid oneWay %q (expected: to-blue, to-green)", mode)
	}
}

func filterPlanOneWay(plan []domain.Op, mode string) []domain.Op {
	if mode == "" {
		return plan
	}
	out := make([]domain.Op, 0, len(plan))
	for _, op := range plan {
		switch mode {
		case "to-blue":
			if op.Kind == domain.CopyToBlue || op.Kind == domain.DeleteFromBlue {
				out = append(out, op)
			}
		case "to-green":
			if op.Kind == domain.CopyToGreen || op.Kind == domain.DeleteFromGreen {
				out = append(out, op)
			}
		}
	}
	return out
}

// buildSyncFieldAllowlist keeps fields present on both sides where neither OData metadata marks
// the property as Computed, plus any name listed in overrides (for calculated fields you still
// want to push). Primary key and modification field are always included.
func buildSyncFieldAllowlist(blue, green []odata.PropertySpec, overrides, ignored []string, pk, mod string) map[string]struct{} {
	override := make(map[string]struct{}, len(overrides))
	for _, f := range overrides {
		if f != "" {
			override[f] = struct{}{}
		}
	}
	ignore := make(map[string]struct{}, len(ignored))
	for _, f := range ignored {
		if f != "" {
			ignore[f] = struct{}{}
		}
	}
	bm := make(map[string]odata.PropertySpec, len(blue))
	for _, p := range blue {
		bm[p.Name] = p
	}
	gm := make(map[string]odata.PropertySpec, len(green))
	for _, p := range green {
		gm[p.Name] = p
	}
	out := make(map[string]struct{})
	for name, bp := range bm {
		if _, skip := ignore[name]; skip {
			continue
		}
		gp, ok := gm[name]
		if !ok {
			continue
		}
		if _, force := override[name]; force {
			out[name] = struct{}{}
			continue
		}
		if !bp.Computed && !gp.Computed {
			out[name] = struct{}{}
		}
	}
	if pk != "" {
		out[pk] = struct{}{}
	}
	if mod != "" {
		out[mod] = struct{}{}
	}
	return out
}

func filterRecord(rec map[string]any, allowed map[string]struct{}) map[string]any {
	out := make(map[string]any)
	for k, v := range rec {
		if _, ok := allowed[k]; ok {
			out[k] = v
		}
	}
	return out
}

func fieldsFromAllowlist(allowed map[string]struct{}) []string {
	fields := make([]string, 0, len(allowed))
	for f := range allowed {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields
}

func verifyReplicaFields(ctx context.Context, blue, green *odata.Client, entity string, plan []domain.Op, allowed map[string]struct{}, pkField, modField string) error {
	selectedFields := fieldsFromAllowlist(allowed)
	ids := make(map[string]struct{})
	for _, op := range plan {
		ids[op.RecordID] = struct{}{}
	}
	for id := range ids {
		bRec, _, err := getRecordForID(ctx, blue, entity, pkField, id, selectedFields, nil)
		if err != nil {
			return fmt.Errorf("get blue %s: %w", id, err)
		}
		gRec, _, err := getRecordForID(ctx, green, entity, pkField, id, selectedFields, nil)
		if err != nil {
			return fmt.Errorf("get green %s: %w", id, err)
		}
		odata.StripMetadata(bRec)
		odata.StripMetadata(gRec)
		bRec = filterRecord(bRec, allowed)
		gRec = filterRecord(gRec, allowed)
		delete(bRec, modField)
		delete(gRec, modField)
		// Computed JSON text fields often embed keys present on only one FileMaker schema (e.g. notes).
		for _, k := range []string{"asJSON", "asJSON_full", "asJSON_summary"} {
			delete(bRec, k)
			delete(gRec, k)
		}
		normalizeJSONishMaps(bRec)
		normalizeJSONishMaps(gRec)
		bRec, gRec = unionAbsentAsNil(bRec, gRec)
		bj, err := json.Marshal(bRec)
		if err != nil {
			return fmt.Errorf("record %q: %w", id, err)
		}
		gj, err := json.Marshal(gRec)
		if err != nil {
			return fmt.Errorf("record %q: %w", id, err)
		}
		if !bytes.Equal(bj, gj) {
			return fmt.Errorf("record %q: common fields still differ (blue=%s green=%s)", id, truncate(string(bj), 200), truncate(string(gj), 200))
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// normalizeJSONishMaps walks a record and replaces string values that contain JSON
// objects/arrays with their parsed form so two semantically equal payloads compare equal.
func normalizeJSONishMaps(m map[string]any) {
	for k, v := range m {
		m[k] = normalizeJSONishValue(v)
	}
}

func unionAbsentAsNil(a, b map[string]any) (map[string]any, map[string]any) {
	a = maps.Clone(a)
	b = maps.Clone(b)
	for k := range a {
		if _, ok := b[k]; !ok {
			b[k] = nil
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			a[k] = nil
		}
	}
	return a, b
}

func normalizeJSONishValue(v any) any {
	switch t := v.(type) {
	case string:
		if len(t) == 0 || (t[0] != '{' && t[0] != '[') {
			return t
		}
		var x any
		if json.Unmarshal([]byte(t), &x) != nil {
			return t
		}
		return normalizeJSONishValue(x)
	case map[string]any:
		normalizeJSONishMaps(t)
		return t
	case []any:
		for i, e := range t {
			t[i] = normalizeJSONishValue(e)
		}
		return t
	default:
		return v
	}
}

func withHeartbeat(ctx context.Context, every time.Duration, logf func(format string, args ...any), label string, fn func() error) error {
	if logf == nil || every <= 0 {
		return fn()
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		elapsed := time.Duration(0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				elapsed += every
				logf("%s: still waiting (%s)", label, elapsed)
			}
		}
	}()
	err := fn()
	close(done)
	return err
}

type deferredIssue struct {
	RecordID string `json:"recordId"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason"`
}

func applyPlan(ctx context.Context, blue, green *odata.Client, entity string, plan []domain.Op, allowed map[string]struct{}, pkField, modField string, maxWorkers, progressBase, progressTotal int, logf func(string, ...any)) ([]deferredIssue, error) {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	type job struct {
		op domain.Op
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan job)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var doneCount int64
	var deferred []deferredIssue
	var deferredMu sync.Mutex
	selectedFields := fieldsFromAllowlist(allowed)

	work := func(op domain.Op) error {
		switch op.Kind {
		case domain.CopyToGreen:
			rec, _, err := getRecordForID(workerCtx, blue, entity, pkField, op.RecordID, selectedFields, logf)
			if err != nil {
				if errors.Is(err, odata.ErrNotFound) {
					if logf != nil {
						logf("table %s: source record disappeared on blue (%s), skipping op", entity, op.RecordID)
					}
					return nil
				}
				return fmt.Errorf("get blue %s: %w", op.RecordID, err)
			}
			odata.StripMetadata(rec)
			rec = filterRecord(rec, allowed)
			body := maps.Clone(rec)
			delete(body, modField)
			err = patchRecordForID(workerCtx, green, entity, pkField, op, "patch green", body, logf)
			switch {
			case err == nil:
			case isRecordLockExhausted(err):
				return err
			case errors.Is(err, odata.ErrNotFound):
				if err := retryRecordLock(workerCtx, entity, op, "post green", logf, func() error {
					return odata.PostRecord(workerCtx, green, entity, rec)
				}); err != nil {
					return fmt.Errorf("post green %s: %w", op.RecordID, err)
				}
			default:
				return fmt.Errorf("patch green %s: %w", op.RecordID, err)
			}
		case domain.CopyToBlue:
			rec, _, err := getRecordForID(workerCtx, green, entity, pkField, op.RecordID, selectedFields, logf)
			if err != nil {
				if errors.Is(err, odata.ErrNotFound) {
					if logf != nil {
						logf("table %s: source record disappeared on green (%s), skipping op", entity, op.RecordID)
					}
					return nil
				}
				return fmt.Errorf("get green %s: %w", op.RecordID, err)
			}
			odata.StripMetadata(rec)
			rec = filterRecord(rec, allowed)
			body := maps.Clone(rec)
			delete(body, modField)
			err = patchRecordForID(workerCtx, blue, entity, pkField, op, "patch blue", body, logf)
			switch {
			case err == nil:
			case isRecordLockExhausted(err):
				return err
			case errors.Is(err, odata.ErrNotFound):
				if err := retryRecordLock(workerCtx, entity, op, "post blue", logf, func() error {
					return odata.PostRecord(workerCtx, blue, entity, rec)
				}); err != nil {
					return fmt.Errorf("post blue %s: %w", op.RecordID, err)
				}
			default:
				return fmt.Errorf("patch blue %s: %w", op.RecordID, err)
			}
		case domain.DeleteFromBlue, domain.DeleteFromGreen:
			return fmt.Errorf("delete ops not implemented yet (%s)", op.Kind)
		}
		return nil
	}

	for w := 0; w < maxWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := work(j.op); err != nil {
					if isRecordLockExhausted(err) {
						deferredMu.Lock()
						deferred = append(deferred, deferredIssue{RecordID: j.op.RecordID, Kind: j.op.Kind.String(), Reason: "record_lock"})
						deferredMu.Unlock()
						if logf != nil {
							logf("table %s: unresolved record lock after retries (%s %s)", entity, j.op.Kind, j.op.RecordID)
						}
						cur := int(atomic.AddInt64(&doneCount, 1))
						if logf != nil {
							totalDone := progressBase + cur
							if totalDone%100 == 0 || cur == len(plan) {
								logf("table %s: apply progress %d/%d", entity, totalDone, progressTotal)
							}
						}
						continue
					}
					if isRecordReadExhausted(err) {
						deferredMu.Lock()
						deferred = append(deferred, deferredIssue{RecordID: j.op.RecordID, Kind: j.op.Kind.String(), Reason: "source_read"})
						deferredMu.Unlock()
						if logf != nil {
							logf("table %s: unresolved source read after retries (%s %s)", entity, j.op.Kind, j.op.RecordID)
						}
						cur := int(atomic.AddInt64(&doneCount, 1))
						if logf != nil {
							totalDone := progressBase + cur
							if totalDone%100 == 0 || cur == len(plan) {
								logf("table %s: apply progress %d/%d", entity, totalDone, progressTotal)
							}
						}
						continue
					}
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				cur := int(atomic.AddInt64(&doneCount, 1))
				if logf != nil {
					totalDone := progressBase + cur
					if totalDone%100 == 0 || cur == len(plan) {
						logf("table %s: apply progress %d/%d", entity, totalDone, progressTotal)
					}
				}
			}
		}()
	}

sendLoop:
	for _, op := range plan {
		select {
		case <-workerCtx.Done():
			break sendLoop
		case jobs <- job{op: op}:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
		return deferred, nil
	}
}

func appendDeferredIssues(statePath, table string, issues []deferredIssue) error {
	if len(issues) == 0 {
		return nil
	}
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := statePath + ".deferred.jsonl"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, iss := range issues {
		row := map[string]any{
			"ts":       time.Now().UTC().Format(time.RFC3339Nano),
			"table":    table,
			"recordId": iss.RecordID,
			"kind":     iss.Kind,
			"reason":   iss.Reason,
		}
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

type recordLockExhaustedError struct {
	Entity    string
	Op        domain.Op
	Attempt   int
	LastErr   error
	Operation string
}

func (e *recordLockExhaustedError) Error() string {
	return fmt.Sprintf("%s %s %s: lock persisted after %d attempts: %v", e.Entity, e.Op.Kind, e.Op.RecordID, e.Attempt, e.LastErr)
}

func isRecordLockExhausted(err error) bool {
	var re *recordLockExhaustedError
	return errors.As(err, &re)
}

func retryRecordLock(ctx context.Context, entity string, op domain.Op, operation string, logf func(string, ...any), fn func() error) error {
	maxAttempts := recordLockRetryMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	base := recordLockRetryBaseDelay
	if base <= 0 {
		base = 250 * time.Millisecond
	}
	maxDelay := recordLockRetryMaxDelay
	if maxDelay <= 0 {
		maxDelay = 3 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if !isRecordLockedError(err) {
			return err
		}
		lastErr = err
		if attempt == maxAttempts {
			return &recordLockExhaustedError{
				Entity:    entity,
				Op:        op,
				Attempt:   attempt,
				LastErr:   err,
				Operation: operation,
			}
		}
		wait := backoffWithJitter(base, maxDelay, attempt)
		if logf != nil {
			logf("table %s: record lock on %s %s (%s), retry %d/%d in %s", entity, op.Kind, op.RecordID, operation, attempt, maxAttempts, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}

func backoffWithJitter(base, max time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= max {
			d = max
			break
		}
	}
	// jitter in [0.8x, 1.2x]
	jitter := 0.8 + (rand.Float64() * 0.4)
	out := time.Duration(float64(d) * jitter)
	if out > max {
		return max
	}
	if out < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	return out
}

func isRecordLockedError(err error) bool {
	var hs *odata.HTTPStatusError
	if !errors.As(err, &hs) {
		return false
	}
	body := strings.ToLower(hs.Body)
	if strings.Contains(body, `"code": "301"`) || strings.Contains(body, "record is locked") {
		return true
	}
	if strings.Contains(body, "(301): record is locked") {
		return true
	}
	return false
}

type recordReadExhaustedError struct {
	Entity  string
	Record  string
	Attempt int
	LastErr error
}

func (e *recordReadExhaustedError) Error() string {
	return fmt.Sprintf("%s %s: source read failed after %d attempts: %v", e.Entity, e.Record, e.Attempt, e.LastErr)
}

func isRecordReadExhausted(err error) bool {
	var re *recordReadExhaustedError
	return errors.As(err, &re)
}

func getRecordWithRetry(ctx context.Context, cli *odata.Client, entity, id string, selectedFields []string, logf func(string, ...any)) (map[string]any, error) {
	maxAttempts := recordReadRetryMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	base := recordReadRetryBaseDelay
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		rec, err := odata.GetRecordPathSelected(ctx, cli, odata.RecordPath(entity, id), selectedFields)
		if err == nil {
			return rec, nil
		}
		if errors.Is(err, odata.ErrNotFound) {
			return nil, err
		}
		lastErr = err
		if !isTransientRecordReadError(err) {
			return nil, err
		}
		if attempt == maxAttempts {
			return nil, &recordReadExhaustedError{
				Entity:  entity,
				Record:  id,
				Attempt: attempt,
				LastErr: err,
			}
		}
		wait := time.Duration(attempt) * base
		if logf != nil {
			logf("table %s: transient source read error for %s, retry %d/%d in %s (%v)", entity, id, attempt, maxAttempts, wait, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func isTransientRecordReadError(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "invalid character") || strings.Contains(msg, "decode") {
		return true
	}
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "client.timeout") || strings.Contains(msg, "timeout") {
		return true
	}
	if strings.Contains(msg, "stream error") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "eof") || strings.Contains(msg, "received from peer") {
		return true
	}
	return false
}

func getRecordForID(ctx context.Context, cli *odata.Client, entity, pkField, id string, selectedFields []string, logf func(string, ...any)) (map[string]any, string, error) {
	rec, err := getRecordWithRetry(ctx, cli, entity, id, selectedFields, logf)
	if err == nil {
		return rec, odata.RecordPath(entity, id), nil
	}
	if !(errors.Is(err, odata.ErrNotFound) || isRecordKeyMismatchError(err)) {
		return nil, "", err
	}
	rec, recordPath, err := getRecordByPKWithRetry(ctx, cli, entity, pkField, id, selectedFields, logf)
	if err != nil {
		return nil, "", err
	}
	if recordPath == "" {
		recordPath = odata.RecordPath(entity, id)
	}
	return rec, recordPath, nil
}

func getRecordByPKWithRetry(ctx context.Context, cli *odata.Client, entity, pkField, id string, selectedFields []string, logf func(string, ...any)) (map[string]any, string, error) {
	maxAttempts := recordReadRetryMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	base := recordReadRetryBaseDelay
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		rec, recordPath, err := odata.GetRecordByPKSelected(ctx, cli, entity, pkField, id, selectedFields)
		if err == nil {
			return rec, recordPath, nil
		}
		if errors.Is(err, odata.ErrNotFound) {
			return nil, "", err
		}
		lastErr = err
		if !isTransientRecordReadError(err) {
			return nil, "", err
		}
		if attempt == maxAttempts {
			return nil, "", &recordReadExhaustedError{
				Entity:  entity,
				Record:  id,
				Attempt: attempt,
				LastErr: err,
			}
		}
		wait := time.Duration(attempt) * base
		if logf != nil {
			logf("table %s: transient source read error for %s (pk lookup), retry %d/%d in %s (%v)", entity, id, attempt, maxAttempts, wait, err)
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, "", lastErr
}

func patchRecordForID(ctx context.Context, dst *odata.Client, entity, pkField string, op domain.Op, operation string, body map[string]any, logf func(string, ...any)) error {
	defaultPath := odata.RecordPath(entity, op.RecordID)
	err := retryRecordLock(ctx, entity, op, operation, logf, func() error {
		return odata.PatchRecordPath(ctx, dst, defaultPath, body)
	})
	switch {
	case err == nil:
		return nil
	case isRecordLockExhausted(err):
		return err
	case odata.IsHTTPStatus(err, http.StatusNotFound):
		return odata.ErrNotFound
	case !isRecordKeyMismatchError(err):
		return err
	}

	_, resolvedPath, lookupErr := getRecordByPKWithRetry(ctx, dst, entity, pkField, op.RecordID, []string{pkField}, logf)
	if lookupErr != nil {
		if errors.Is(lookupErr, odata.ErrNotFound) {
			return odata.ErrNotFound
		}
		return lookupErr
	}
	if resolvedPath == "" || resolvedPath == defaultPath {
		return err
	}
	err = retryRecordLock(ctx, entity, op, operation+" (resolved key)", logf, func() error {
		return odata.PatchRecordPath(ctx, dst, resolvedPath, body)
	})
	if odata.IsHTTPStatus(err, http.StatusNotFound) {
		return odata.ErrNotFound
	}
	return err
}

func isRecordKeyMismatchError(err error) bool {
	var hs *odata.HTTPStatusError
	if !errors.As(err, &hs) {
		return false
	}
	if hs.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(hs.Body)
	return strings.Contains(body, "incompatible data types") || strings.Contains(body, `"code":"8309"`) || strings.Contains(body, `"code": "8309"`)
}

func entityPropertiesWithRetry(ctx context.Context, cli *odata.Client, entity string, logf func(string, ...any)) ([]odata.PropertySpec, error) {
	maxAttempts := metadataRetryMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	base := metadataRetryBaseDelay
	if base <= 0 {
		base = 300 * time.Millisecond
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		props, err := odata.EntityProperties(ctx, cli, entity)
		if err == nil {
			return props, nil
		}
		lastErr = err
		if !isTransientMetadataError(err) || attempt == maxAttempts {
			return nil, err
		}
		wait := time.Duration(attempt) * base
		if logf != nil {
			logf("table %s: metadata transient error, retry %d/%d in %s (%v)", entity, attempt, maxAttempts, wait, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func isTransientMetadataError(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "client.timeout") || strings.Contains(msg, "timeout") {
		return true
	}
	return false
}
