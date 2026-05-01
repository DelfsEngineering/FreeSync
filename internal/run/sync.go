// Package run orchestrates one sync pass (manifest → plan → optional apply → verify → checkpoint).
package run

import (
	"context"
	"fmt"
	"log"
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
	blueClient := &odata.Client{BaseURL: odata.TrimBase(blueSrv.URL), Username: blueSrv.Username, Password: blueSrv.Password}
	greenClient := &odata.Client{BaseURL: odata.TrimBase(greenSrv.URL), Username: greenSrv.Username, Password: greenSrv.Password}

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

		mBlue, err := odata.FetchManifest(ctx, blueClient, tbl.Name, window.Start, window.End, pk, mod)
		if err != nil {
			return fmt.Errorf("%s blue manifest: %w", tbl.Name, err)
		}
		mGreen, err := odata.FetchManifest(ctx, greenClient, tbl.Name, window.Start, window.End, pk, mod)
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

		if opt.Apply && len(plan) > 0 {
			if err := applyPlan(ctx, blueClient, greenClient, tbl.Name, plan); err != nil {
				return fmt.Errorf("%s apply: %w", tbl.Name, err)
			}
		}

		// Verify: re-fetch manifests; expect empty plan.
		mBlue2, err := odata.FetchManifest(ctx, blueClient, tbl.Name, window.Start, window.End, pk, mod)
		if err != nil {
			return fmt.Errorf("%s verify blue: %w", tbl.Name, err)
		}
		mGreen2, err := odata.FetchManifest(ctx, greenClient, tbl.Name, window.Start, window.End, pk, mod)
		if err != nil {
			return fmt.Errorf("%s verify green: %w", tbl.Name, err)
		}
		plan2 := domain.BuildPlan(mBlue2, mGreen2, nil)
		if len(plan2) > 0 {
			return fmt.Errorf("%s verify failed: still %d pending ops after sync", tbl.Name, len(plan2))
		}

		if err := state.SaveSafeThrough(opt.StatePath, tbl.Name, window.End); err != nil {
			return err
		}
		logf("table %s: checkpoint advanced to %s", tbl.Name, window.End.Format(time.RFC3339))
	}
	return nil
}

func applyPlan(ctx context.Context, blue, green *odata.Client, entity string, plan []domain.Op) error {
	for _, op := range plan {
		switch op.Kind {
		case domain.CopyToGreen:
			rec, err := odata.GetRecord(ctx, blue, entity, op.RecordID)
			if err != nil {
				return fmt.Errorf("get blue %s: %w", op.RecordID, err)
			}
			odata.StripMetadata(rec)
			_, err = odata.GetRecord(ctx, green, entity, op.RecordID)
			switch {
			case err == nil:
				if err := odata.PatchRecord(ctx, green, entity, op.RecordID, rec); err != nil {
					return fmt.Errorf("patch green %s: %w", op.RecordID, err)
				}
			case err == odata.ErrNotFound:
				if err := odata.PostRecord(ctx, green, entity, rec); err != nil {
					return fmt.Errorf("post green %s: %w", op.RecordID, err)
				}
			default:
				return err
			}
		case domain.CopyToBlue:
			rec, err := odata.GetRecord(ctx, green, entity, op.RecordID)
			if err != nil {
				return fmt.Errorf("get green %s: %w", op.RecordID, err)
			}
			odata.StripMetadata(rec)
			_, err = odata.GetRecord(ctx, blue, entity, op.RecordID)
			switch {
			case err == nil:
				if err := odata.PatchRecord(ctx, blue, entity, op.RecordID, rec); err != nil {
					return fmt.Errorf("patch blue %s: %w", op.RecordID, err)
				}
			case err == odata.ErrNotFound:
				if err := odata.PostRecord(ctx, blue, entity, rec); err != nil {
					return fmt.Errorf("post blue %s: %w", op.RecordID, err)
				}
			default:
				return err
			}
		case domain.DeleteFromBlue, domain.DeleteFromGreen:
			return fmt.Errorf("delete ops not implemented yet (%s)", op.Kind)
		}
	}
	return nil
}
