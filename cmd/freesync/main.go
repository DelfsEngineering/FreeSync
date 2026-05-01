// Free Sync — CLI entrypoint (see SPEC.md).
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/DelfsEngineering/FreeSync/internal/config"
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
	flag.StringVar(&configPath, "config", configPath, "path to JSON config")
	flag.StringVar(&statePath, "state", statePath, "path to checkpoint file (JSON)")
	flag.Parse()
	if len(flag.Args()) < 1 || flag.Args()[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: freesync [-config path] [-state path] run")
		os.Exit(2)
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Free Sync — config %q\n", configPath)
	for _, s := range cfg.Servers {
		fmt.Printf("  server %s: %s (user %s)\n", s.ID, s.URL, s.Username)
	}
	fmt.Printf("  overlap: %s  tables: %d  schemaMode: %s\n", cfg.Overlap(), len(cfg.Tables), cfg.SchemaMode)

	if _, err := os.Stat(statePath); err == nil {
		fmt.Printf("  checkpoint file: %s\n", statePath)
	} else {
		fmt.Printf("  checkpoint file: %s (will create on first advance)\n", statePath)
	}

	// Smoke: checkpoint IO (no OData yet)
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

	fmt.Println("run: OData sync not wired yet — domain tests cover window/plan/checkpoint.")
}
