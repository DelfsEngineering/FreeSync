// Package run orchestrates one sync pass (manifest → plan → optional apply → verify → checkpoint).
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
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

// Options controls a single run.
type Options struct {
	Apply     bool
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

	blueSrv, greenSrv, err := cfg.BlueGreen()
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	blueClient := &odata.Client{BaseURL: odata.TrimBase(blueSrv.URL), Username: blueSrv.Username, Password: blueSrv.Password, HTTPClient: httpClient}
	greenClient := &odata.Client{BaseURL: odata.TrimBase(greenSrv.URL), Username: greenSrv.Username, Password: greenSrv.Password, HTTPClient: httpClient}

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
			window = domain.SyncWindow{
				Start: now.Add(-lookback),
				End:   now,
			}
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

		plan := domain.BuildPlan(mBlue, mGreen, nil)
		logf("table %s: plan ops=%d", tbl.Name, len(plan))
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

		blueProps, err := odata.EntityProperties(ctx, blueClient, tbl.Name)
		if err != nil {
			return fmt.Errorf("%s blue metadata: %w", tbl.Name, err)
		}
		greenProps, err := odata.EntityProperties(ctx, greenClient, tbl.Name)
		if err != nil {
			return fmt.Errorf("%s green metadata: %w", tbl.Name, err)
		}
		allowed := buildSyncFieldAllowlist(blueProps, greenProps, tbl.FieldOverrides, pk, mod)
		if len(allowed) == 0 {
			return fmt.Errorf("%s: no sync fields after metadata + overrides", tbl.Name)
		}

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
				if err := applyPlan(ctx, blueClient, greenClient, tbl.Name, chunk, allowed, mod, maxWorkers, i, len(plan), logf); err != nil {
					return fmt.Errorf("%s apply batch %d: %w", tbl.Name, bi+1, err)
				}
			}
		}

		// Rows written during apply get ModificationTimestamp at insert time, often after the
		// window.End captured at run start — widen checkpoint upper bound.
		endThrough := window.End
		if opt.Apply && len(plan) > 0 {
			endThrough = time.Now().UTC()
		}

		if cfg.VerifyStrict() {
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
			plan2 := domain.BuildPlan(mBlue2, mGreen2, nil)
			if len(plan2) > 0 {
				// FileMaker bumps ModificationTimestamp on each write; pure LWW on timestamps may
				// still suggest copy ops even when replicated fields already match — confirm payload equality.
				if err := verifyReplicaFields(ctx, blueClient, greenClient, tbl.Name, plan2, allowed, mod); err != nil {
					return fmt.Errorf("%s verify failed (%d pending time-order ops): %w", tbl.Name, len(plan2), err)
				}
				logf("table %s: verify ok (%d time-order ops ignored — replicated fields match)", tbl.Name, len(plan2))
			}
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

// buildSyncFieldAllowlist keeps fields present on both sides where neither OData metadata marks
// the property as Computed, plus any name listed in overrides (for calculated fields you still
// want to push). Primary key and modification field are always included.
func buildSyncFieldAllowlist(blue, green []odata.PropertySpec, overrides []string, pk, mod string) map[string]struct{} {
	override := make(map[string]struct{}, len(overrides))
	for _, f := range overrides {
		if f != "" {
			override[f] = struct{}{}
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

func verifyReplicaFields(ctx context.Context, blue, green *odata.Client, entity string, plan []domain.Op, allowed map[string]struct{}, modField string) error {
	ids := make(map[string]struct{})
	for _, op := range plan {
		ids[op.RecordID] = struct{}{}
	}
	for id := range ids {
		bRec, err := odata.GetRecord(ctx, blue, entity, id)
		if err != nil {
			return fmt.Errorf("get blue %s: %w", id, err)
		}
		gRec, err := odata.GetRecord(ctx, green, entity, id)
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

func applyPlan(ctx context.Context, blue, green *odata.Client, entity string, plan []domain.Op, allowed map[string]struct{}, modField string, maxWorkers, progressBase, progressTotal int, logf func(string, ...any)) error {
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

	work := func(op domain.Op) error {
		switch op.Kind {
		case domain.CopyToGreen:
			rec, err := odata.GetRecord(workerCtx, blue, entity, op.RecordID)
			if err != nil {
				return fmt.Errorf("get blue %s: %w", op.RecordID, err)
			}
			odata.StripMetadata(rec)
			rec = filterRecord(rec, allowed)
			body := maps.Clone(rec)
			delete(body, modField)
			err = odata.PatchRecord(workerCtx, green, entity, op.RecordID, body)
			switch {
			case err == nil:
			case odata.IsHTTPStatus(err, http.StatusNotFound):
				if err := odata.PostRecord(workerCtx, green, entity, rec); err != nil {
					return fmt.Errorf("post green %s: %w", op.RecordID, err)
				}
			default:
				return fmt.Errorf("patch green %s: %w", op.RecordID, err)
			}
		case domain.CopyToBlue:
			rec, err := odata.GetRecord(workerCtx, green, entity, op.RecordID)
			if err != nil {
				return fmt.Errorf("get green %s: %w", op.RecordID, err)
			}
			odata.StripMetadata(rec)
			rec = filterRecord(rec, allowed)
			body := maps.Clone(rec)
			delete(body, modField)
			err = odata.PatchRecord(workerCtx, blue, entity, op.RecordID, body)
			switch {
			case err == nil:
			case odata.IsHTTPStatus(err, http.StatusNotFound):
				if err := odata.PostRecord(workerCtx, blue, entity, rec); err != nil {
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
		return err
	default:
		return nil
	}
}
