// Free Sync — CLI entrypoint (see SPEC.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/DelfsEngineering/FreeSync/internal/config"
	"github.com/DelfsEngineering/FreeSync/internal/run"
	"github.com/DelfsEngineering/FreeSync/internal/state"
)

func main() {
	configPath := os.Getenv("FREESYNC_CONFIG")
	if configPath == "" {
		configPath = "config/dev.example.json"
	}
	statePath := os.Getenv("FREESYNC_STATE")
	if statePath == "" {
		statePath = "data/sync-state.json"
	}

	runIdx := -1
	for i, a := range os.Args[1:] {
		if a == "run" {
			runIdx = i + 1 // index into os.Args
			break
		}
	}
	if runIdx < 0 {
		fmt.Fprintln(os.Stderr, "usage: freesync run [-config path] [-state path] [-apply]")
		os.Exit(2)
	}
	before := os.Args[1:runIdx]
	after := os.Args[runIdx+1:]
	flagArgs := append(append([]string{}, before...), after...)

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	apply := fs.Bool("apply", false, "execute writes (PATCH/POST); default is dry-run")
	fs.StringVar(&configPath, "config", configPath, "path to JSON config")
	fs.StringVar(&statePath, "state", statePath, "path to checkpoint file (JSON)")
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(2)
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Free Sync — config %q apply=%v\n", configPath, *apply)
	for _, s := range cfg.Servers {
		fmt.Printf("  server %s: %s (user %s)\n", s.ID, s.URL, s.Username)
	}
	fmt.Printf("  overlap: %s  tables: %d  schemaMode: %s\n", cfg.Overlap(), len(cfg.Tables), cfg.SchemaMode)
	verifyMode := "off"
	if cfg.VerifyStrict() {
		verifyMode = "strict"
	}
	fmt.Printf("  applyTuning: batchSize=%d maxWorkers=%d verifyMode=%s\n", cfg.ApplyBatchSize(), cfg.ApplyWorkers(), verifyMode)

	if _, err := os.Stat(statePath); err == nil {
		fmt.Printf("  checkpoint file: %s\n", statePath)
	} else {
		fmt.Printf("  checkpoint file: %s (will create on first advance)\n", statePath)
	}

	for _, tbl := range cfg.Tables {
		if tbl.Name == "" {
			continue
		}
		if ts, ok, err := state.LoadSafeThrough(statePath, tbl.Name); err != nil {
			fmt.Fprintf(os.Stderr, "checkpoint: %v\n", err)
			os.Exit(1)
		} else if ok {
			fmt.Printf("  [%s] safeThrough=%s\n", tbl.Name, ts.UTC().Format(time.RFC3339))
		}
	}

	ctx := context.Background()
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	err = run.Once(ctx, cfg, run.Options{Apply: *apply, StatePath: statePath, Logger: log.Default()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("run: ok")
}
