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
	"strconv"
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
	Verbose   bool
	Logger    *log.Logger
}

// runSummary aggregates per-pass stats for the closing log lines.
type runSummary struct {
	skippedEmptyName int
	tablesTotal      int
	unchanged        int // len(plan)==0 after one-way filter
	pendingTables    int // len(plan)>0, dry-run
	appliedTables    int // len(plan)>0, apply mode
	pendingOps       int // row-level ops in dry-run tables
	appliedOps       int // row-level ops in applied tables (excludes deferred)
	deferredOps      int
	byKind           [4]int // domain OpKind indices 0..3
	// Manifest sizes summed across tables (each len = distinct record ids in that table’s sync window).
	manifestEntriesBlue  int
	manifestEntriesGreen int
}

func (s *runSummary) addKinds(plan []domain.Op) {
	for _, op := range plan {
		i := int(op.Kind)
		if i >= 0 && i < len(s.byKind) {
			s.byKind[i]++
		}
	}
}

func (s *runSummary) merge(other runSummary) {
	s.skippedEmptyName += other.skippedEmptyName
	s.tablesTotal += other.tablesTotal
	s.unchanged += other.unchanged
	s.pendingTables += other.pendingTables
	s.appliedTables += other.appliedTables
	s.pendingOps += other.pendingOps
	s.appliedOps += other.appliedOps
	s.deferredOps += other.deferredOps
	s.manifestEntriesBlue += other.manifestEntriesBlue
	s.manifestEntriesGreen += other.manifestEntriesGreen
	for i := range s.byKind {
		s.byKind[i] += other.byKind[i]
	}
}

func formatKindCounts(byKind [4]int) string {
	var b strings.Builder
	for k := 0; k < len(byKind); k++ {
		if byKind[k] == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(",")
		}
		b.WriteString(domain.OpKind(k).String())
		b.WriteString(":")
		b.WriteString(strconv.Itoa(byKind[k]))
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

func prefixLogf(fileID string, logf func(string, ...any)) func(string, ...any) {
	return func(format string, args ...any) {
		allArgs := make([]any, 0, len(args)+1)
		allArgs = append(allArgs, fileID)
		allArgs = append(allArgs, args...)
		logf("file %s: "+format, allArgs...)
	}
}

func logScopeSummary(header string, logf func(string, ...any), sum runSummary, d time.Duration, apply bool, oneWay string) {
	kinds := formatKindCounts(sum.byKind)
	workOps := sum.pendingOps + sum.appliedOps
	oneWayLabel := oneWay
	if oneWayLabel == "" {
		oneWayLabel = "bidirectional"
	}
	kindsPart := ""
	if kinds != "" && workOps > 0 {
		kindsPart = fmt.Sprintf(" opKinds=%s", kinds)
	}

	logf("--- %s ---", header)
	logf("summary: duration=%s oneWay=%s apply=%v tablesProcessed=%d manifestRowsBlue=%d manifestRowsGreen=%d (per-table id counts summed)",
		d.Round(time.Millisecond), oneWayLabel, apply, sum.tablesTotal, sum.manifestEntriesBlue, sum.manifestEntriesGreen)

	if apply {
		logf("summary: unchangedTables=%d tablesWithRowOps=%d rowOpsPlanned=%d rowOpsDeferred=%d%s",
			sum.unchanged, sum.appliedTables, sum.appliedOps, sum.deferredOps, kindsPart)
	} else {
		logf("summary: unchangedTables=%d tablesWithPendingRowOps=%d pendingRowOps=%d (dry-run: no writes; checkpoints not advanced for op tables)%s",
			sum.unchanged, sum.pendingTables, sum.pendingOps, kindsPart)
	}
	if sum.skippedEmptyName > 0 {
		logf("summary: skipped %d table entries with empty name", sum.skippedEmptyName)
	}
}

func logRunSummary(logf func(string, ...any), sum runSummary, d time.Duration, apply bool, oneWay string, filesProcessed, filesSucceeded int, failedFileIDs []string) {
	logScopeSummary("run summary", logf, sum, d, apply, oneWay)
	logf("summary: filesProcessed=%d filesSucceeded=%d filesFailed=%d", filesProcessed, filesSucceeded, len(failedFileIDs))
	if len(failedFileIDs) > 0 {
		logf("summary: failedFiles=%s", strings.Join(failedFileIDs, ","))
	}
}

func runFileGroup(ctx context.Context, cfg *config.Config, file config.FileConfig, opt Options, overlap, lookback, maxLookback time.Duration, schemaMode string, logf func(string, ...any), debugf func(string, ...any)) (runSummary, error) {
	var sum runSummary

	blueSrv, greenSrv, err := file.BlueGreen()
	if err != nil {
		return sum, err
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	blueClient := &odata.Client{BaseURL: odata.TrimBase(blueSrv.URL), Username: blueSrv.Username, Password: blueSrv.Password, HTTPClient: httpClient, Logf: logf}
	greenClient := &odata.Client{BaseURL: odata.TrimBase(greenSrv.URL), Username: greenSrv.Username, Password: greenSrv.Password, HTTPClient: httpClient, Logf: logf}

	now := time.Now().UTC()
	if schemaMode != "intersection" {
		return sum, fmt.Errorf("unsupported schemaMode %q (only \"intersection\")", cfg.SchemaMode)
	}

	for _, tbl := range file.Tables {
		if tbl.Name == "" {
			sum.skippedEmptyName++
			continue
		}
		sum.tablesTotal++
		pk, mod := cfg.PKMod(file, tbl)

		var window domain.SyncWindow
		safe, ok, err := state.LoadSafeThrough(opt.StatePath, file.ID, tbl.Name)
		if err != nil {
			return sum, err
		}
		if !ok {
			start := now.Add(-lookback)
			if cfg.BootstrapBinary() {
				debugf("table %s: bootstrapMode=binary (lookback=%s maxLookback=%s)", tbl.Name, lookback, maxLookback)
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
			debugf("table %s: bootstrap window [%s .. %s]", tbl.Name, window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339))
		} else {
			window = domain.ComputeSyncWindow(safe, overlap, now)
			debugf("table %s: checkpoint window [%s .. %s] (safeThrough=%s)", tbl.Name, window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339), safe.Format(time.RFC3339))
		}

		tableStart := time.Now()
		mBlue, mGreen, err := fetchManifestPair(ctx, blueClient, greenClient, tbl.Name, window, pk, mod, logf, debugf)
		if err != nil {
			return sum, err
		}
		sum.manifestEntriesBlue += len(mBlue)
		sum.manifestEntriesGreen += len(mGreen)

		planAll := domain.BuildPlan(mBlue, mGreen, nil)
		plan := filterPlanOneWay(planAll, opt.OneWay)

		if len(plan) > 0 && !opt.Apply {
			sum.pendingTables++
			sum.pendingOps += len(plan)
			sum.addKinds(plan)
			logTablePlan(logf, tbl.Name, len(mBlue), len(mGreen), plan, opt.OneWay, len(planAll)-len(plan), tableStart)
			logf("table %s: dry-run only — not applying, verifying, or advancing checkpoint (use -apply)", tbl.Name)
			continue
		}
		if len(plan) == 0 {
			sum.unchanged++
			if err := state.SaveSafeThrough(opt.StatePath, file.ID, tbl.Name, window.End); err != nil {
				return sum, err
			}
			logf("table %s: scanned blue=%d green=%d ops=0 checkpoint=%s duration=%s", tbl.Name, len(mBlue), len(mGreen), window.End.Format(time.RFC3339), time.Since(tableStart).Round(time.Millisecond))
			continue
		}
		sum.appliedTables++
		sum.appliedOps += len(plan)
		sum.addKinds(plan)
		logTablePlan(logf, tbl.Name, len(mBlue), len(mGreen), plan, opt.OneWay, len(planAll)-len(plan), tableStart)

		blueProps, greenProps, err := entityPropertiesPair(ctx, blueClient, greenClient, tbl.Name, logf)
		if err != nil {
			return sum, err
		}
		allowed := buildSyncFieldAllowlist(blueProps, greenProps, tbl.FieldOverrides, tbl.IgnoreFields, pk, mod)
		if len(allowed) == 0 {
			return sum, fmt.Errorf("%s: no sync fields after metadata + overrides", tbl.Name)
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
				debugf("table %s: apply batch %d/%d ops=%d", tbl.Name, bi+1, totalBatches, len(chunk))
				batchDeferred, err := applyPlan(ctx, blueClient, greenClient, tbl.Name, chunk, allowed, pk, mod, maxWorkers, i, len(plan), logf)
				if err != nil {
					return sum, fmt.Errorf("%s apply batch %d: %w", tbl.Name, bi+1, err)
				}
				deferred = append(deferred, batchDeferred...)
			}
			if len(deferred) > 0 {
				logf("table %s: deferred %d ops for later retry (continuing run)", tbl.Name, len(deferred))
				for i := 0; i < len(deferred) && i < 10; i++ {
					logf("table %s: deferred %s %s reason=%s", tbl.Name, deferred[i].Kind, deferred[i].RecordID, deferred[i].Reason)
				}
				if err := appendDeferredIssues(opt.StatePath, file.ID, tbl.Name, deferred); err != nil {
					logf("table %s: warning: could not persist deferred issues: %v", tbl.Name, err)
				}
			}
			sum.deferredOps += len(deferred)
		}

		endThrough := window.End
		if opt.Apply && len(plan) > 0 {
			endThrough = time.Now().UTC()
		}

		if cfg.VerifyStrict() && len(deferred) == 0 {
			var mBlue2 map[string]time.Time
			debugf("table %s: verify fetch blue manifest...", tbl.Name)
			err = withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: verify blue request", tbl.Name), func() error {
				var fetchErr error
				mBlue2, fetchErr = odata.FetchManifestWithProgress(ctx, blueClient, tbl.Name, window.Start, endThrough, pk, mod, func(pageNum, pageRows, totalRows int) {
					debugf("table %s: verify blue page=%d rows=%d total=%d", tbl.Name, pageNum, pageRows, totalRows)
				})
				return fetchErr
			})
			if err != nil {
				return sum, fmt.Errorf("%s verify blue: %w", tbl.Name, err)
			}
			var mGreen2 map[string]time.Time
			debugf("table %s: verify fetch green manifest...", tbl.Name)
			err = withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: verify green request", tbl.Name), func() error {
				var fetchErr error
				mGreen2, fetchErr = odata.FetchManifestWithProgress(ctx, greenClient, tbl.Name, window.Start, endThrough, pk, mod, func(pageNum, pageRows, totalRows int) {
					debugf("table %s: verify green page=%d rows=%d total=%d", tbl.Name, pageNum, pageRows, totalRows)
				})
				return fetchErr
			})
			if err != nil {
				return sum, fmt.Errorf("%s verify green: %w", tbl.Name, err)
			}
			plan2All := domain.BuildPlan(mBlue2, mGreen2, nil)
			plan2 := filterPlanOneWay(plan2All, opt.OneWay)
			if len(plan2) > 0 {
				if err := verifyReplicaFields(ctx, blueClient, greenClient, tbl.Name, plan2, allowed, pk, mod); err != nil {
					return sum, fmt.Errorf("%s verify failed (%d pending time-order ops): %w", tbl.Name, len(plan2), err)
				}
				logf("table %s: verify ok (%d time-order ops ignored — replicated fields match)", tbl.Name, len(plan2))
			} else if opt.OneWay != "" && len(plan2All) > 0 {
				logf("table %s: verify ok (oneWay=%s filtered %d opposite-direction ops)", tbl.Name, opt.OneWay, len(plan2All))
			}
		} else if cfg.VerifyStrict() && len(deferred) > 0 {
			logf("table %s: verify skipped (deferredOps=%d)", tbl.Name, len(deferred))
		} else {
			debugf("table %s: verify skipped (verifyMode=off)", tbl.Name)
		}

		if err := state.SaveSafeThrough(opt.StatePath, file.ID, tbl.Name, endThrough); err != nil {
			return sum, err
		}
		logf("table %s: checkpoint advanced to %s duration=%s", tbl.Name, endThrough.Format(time.RFC3339), time.Since(tableStart).Round(time.Millisecond))
	}
	return sum, nil
}

// Once executes sync for all configured files (delete journal not wired — passes nil).
func Once(ctx context.Context, cfg *config.Config, opt Options) error {
	baseLogf := func(format string, args ...any) {
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

	schemaMode := strings.ToLower(strings.TrimSpace(cfg.SchemaMode))
	if schemaMode == "" {
		schemaMode = "intersection"
	}
	if schemaMode != "intersection" {
		return fmt.Errorf("unsupported schemaMode %q (only \"intersection\")", cfg.SchemaMode)
	}

	runStarted := time.Now()
	var total runSummary
	filesProcessed := 0
	filesSucceeded := 0
	var failedFileIDs []string

	for _, file := range cfg.Files {
		filesProcessed++
		fileStarted := time.Now()
		fileLogf := prefixLogf(file.ID, baseLogf)
		fileDebugf := func(format string, args ...any) {
			if opt.Verbose {
				fileLogf(format, args...)
			}
		}

		fileSum, err := runFileGroup(ctx, cfg, file, Options{
			Apply:     opt.Apply,
			OneWay:    mode,
			StatePath: opt.StatePath,
			Verbose:   opt.Verbose,
			Logger:    opt.Logger,
		}, overlap, lookback, maxLookback, schemaMode, fileLogf, fileDebugf)
		total.merge(fileSum)
		if err != nil {
			failedFileIDs = append(failedFileIDs, file.ID)
			fileLogf("run failed: %v", err)
			logScopeSummary("file summary", fileLogf, fileSum, time.Since(fileStarted), opt.Apply, mode)
			continue
		}
		filesSucceeded++
		logScopeSummary("file summary", fileLogf, fileSum, time.Since(fileStarted), opt.Apply, mode)
	}

	logRunSummary(baseLogf, total, time.Since(runStarted), opt.Apply, mode, filesProcessed, filesSucceeded, failedFileIDs)
	if len(failedFileIDs) > 0 {
		return fmt.Errorf("files failed: %s", strings.Join(failedFileIDs, ","))
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

func logTablePlan(logf func(string, ...any), table string, blueRows, greenRows int, plan []domain.Op, mode string, filtered int, start time.Time) {
	if mode == "" {
		logf("table %s: scanned blue=%d green=%d ops=%d duration=%s", table, blueRows, greenRows, len(plan), time.Since(start).Round(time.Millisecond))
	} else {
		logf("table %s: scanned blue=%d green=%d ops=%d oneWay=%s filtered=%d duration=%s", table, blueRows, greenRows, len(plan), mode, filtered, time.Since(start).Round(time.Millisecond))
	}
	for _, op := range plan {
		logf("table %s: %s %s", table, op.Kind, op.RecordID)
	}
}

func fetchManifestPair(ctx context.Context, blueClient, greenClient *odata.Client, entity string, window domain.SyncWindow, pk, mod string, logf func(string, ...any), debugf func(string, ...any)) (map[string]time.Time, map[string]time.Time, error) {
	type result struct {
		side string
		rows map[string]time.Time
		err  error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan result, 2)

	fetch := func(side string, cli *odata.Client) {
		if debugf != nil {
			debugf("table %s: fetching %s manifest...", entity, side)
		}
		var rows map[string]time.Time
		err := withHeartbeat(ctx, 10*time.Second, logf, fmt.Sprintf("table %s: %s manifest request", entity, side), func() error {
			var fetchErr error
			rows, fetchErr = odata.FetchManifestWithProgress(ctx, cli, entity, window.Start, window.End, pk, mod, func(pageNum, pageRows, totalRows int) {
				if debugf != nil {
					debugf("table %s: %s manifest page=%d rows=%d total=%d", entity, side, pageNum, pageRows, totalRows)
				}
			})
			return fetchErr
		})
		ch <- result{side: side, rows: rows, err: err}
	}

	go fetch("blue", blueClient)
	go fetch("green", greenClient)

	var blueRows, greenRows map[string]time.Time
	for i := 0; i < 2; i++ {
		res := <-ch
		if res.err != nil {
			cancel()
			return nil, nil, fmt.Errorf("%s %s manifest: %w", entity, res.side, res.err)
		}
		switch res.side {
		case "blue":
			blueRows = res.rows
		case "green":
			greenRows = res.rows
		}
	}
	return blueRows, greenRows, nil
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

func appendDeferredIssues(statePath, fileID, table string, issues []deferredIssue) error {
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
			"fileId":   fileID,
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

func entityPropertiesWithRetry(ctx context.Context, cli *odata.Client, entity, side string, logf func(string, ...any)) ([]odata.PropertySpec, error) {
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
		start := time.Now()
		props, source, err := odata.EntityPropertiesPreferThinSchema(ctx, cli, entity)
		if err == nil {
			if logf != nil {
				logf("table %s: %s schema source=%s fields=%d duration=%s", entity, side, source, len(props), time.Since(start).Round(time.Millisecond))
			}
			return props, nil
		}
		lastErr = err
		if !isTransientMetadataError(err) || attempt == maxAttempts {
			return nil, err
		}
		wait := time.Duration(attempt) * base
		if logf != nil {
			logf("table %s: %s schema transient error, retry %d/%d in %s (%v)", entity, side, attempt, maxAttempts, wait, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func entityPropertiesPair(ctx context.Context, blueClient, greenClient *odata.Client, entity string, logf func(string, ...any)) ([]odata.PropertySpec, []odata.PropertySpec, error) {
	type result struct {
		side  string
		props []odata.PropertySpec
		err   error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan result, 2)

	fetch := func(side string, cli *odata.Client) {
		props, err := entityPropertiesWithRetry(ctx, cli, entity, side, logf)
		ch <- result{side: side, props: props, err: err}
	}
	go fetch("blue", blueClient)
	go fetch("green", greenClient)

	var blueProps, greenProps []odata.PropertySpec
	for i := 0; i < 2; i++ {
		res := <-ch
		if res.err != nil {
			cancel()
			return nil, nil, fmt.Errorf("%s %s metadata: %w", entity, res.side, res.err)
		}
		switch res.side {
		case "blue":
			blueProps = res.props
		case "green":
			greenProps = res.props
		}
	}
	return blueProps, greenProps, nil
}

func isTransientMetadataError(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "client.timeout") || strings.Contains(msg, "timeout") {
		return true
	}
	return false
}
