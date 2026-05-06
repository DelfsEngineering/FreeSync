// Free Sync — CLI entrypoint (see SPEC.md).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DelfsEngineering/FreeSync/internal/config"
	"github.com/DelfsEngineering/FreeSync/internal/run"
	"github.com/DelfsEngineering/FreeSync/internal/state"
)

type runOnceFunc func(ctx context.Context, configPath, statePath string, apply bool, oneWay string, logger *log.Logger) error

func main() {
	cmd, before, after, ok := parseCommand(os.Args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: freesync <run|serve> [flags]")
		fmt.Fprintln(os.Stderr, "  run   [-config path] [-state path] [-apply] [-one-way to-blue|to-green]")
		fmt.Fprintln(os.Stderr, "  serve [-config path] [-state path] [-listen :8080] [-apply] [-one-way to-blue|to-green] [-token secret]")
		os.Exit(2)
	}
	flagArgs := append(append([]string{}, before...), after...)

	switch cmd {
	case "run":
		if err := runCLI(flagArgs); err != nil {
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("run: ok")
	case "serve":
		if err := serveHTTP(flagArgs); err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		os.Exit(2)
	}
}

func runCLI(flagArgs []string) error {
	configPath := defaultConfigPath()
	statePath := defaultStatePath()
	oneWay := strings.TrimSpace(os.Getenv("FREESYNC_ONE_WAY"))
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	apply := fs.Bool("apply", false, "execute writes (PATCH/POST); default is dry-run")
	fs.StringVar(&oneWay, "one-way", oneWay, "optional write direction filter: to-blue or to-green")
	fs.StringVar(&configPath, "config", configPath, "path to JSON config")
	fs.StringVar(&statePath, "state", statePath, "path to checkpoint file (JSON)")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	mode, err := normalizeOneWay(oneWay)
	if err != nil {
		return err
	}
	return loadAndRunOnce(context.Background(), configPath, statePath, *apply, mode, log.Default())
}

func serveHTTP(flagArgs []string) error {
	configPath := defaultConfigPath()
	statePath := defaultStatePath()
	listen := os.Getenv("FREESYNC_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	token := os.Getenv("FREESYNC_TRIGGER_TOKEN")
	oneWayDefault := strings.TrimSpace(os.Getenv("FREESYNC_ONE_WAY"))
	applyDefault := true
	if v := strings.TrimSpace(os.Getenv("FREESYNC_APPLY")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("FREESYNC_APPLY must be true/false: %w", err)
		}
		applyDefault = b
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.StringVar(&configPath, "config", configPath, "path to JSON config")
	fs.StringVar(&statePath, "state", statePath, "path to checkpoint file (JSON)")
	fs.StringVar(&listen, "listen", listen, "HTTP listen address")
	fs.StringVar(&token, "token", token, "optional bearer token for POST /run")
	fs.BoolVar(&applyDefault, "apply", applyDefault, "default apply mode for POST /run")
	fs.StringVar(&oneWayDefault, "one-way", oneWayDefault, "default direction filter for POST /run: to-blue or to-green")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	var err error
	oneWayDefault, err = normalizeOneWay(oneWayDefault)
	if err != nil {
		return err
	}

	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	logger := log.Default()
	logger.Printf("Free Sync API listening on %s (defaultApply=%v oneWay=%q)", listen, applyDefault, oneWayDefault)

	return http.ListenAndServe(listen, newServerHandler(configPath, statePath, token, applyDefault, oneWayDefault, logger, loadAndRunOnce))
}

func loadAndRunOnce(ctx context.Context, configPath, statePath string, apply bool, oneWay string, logger *log.Logger) error {
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger.Printf("Free Sync — config %q apply=%v oneWay=%q", configPath, apply, oneWay)
	for _, s := range cfg.Servers {
		logger.Printf("  server %s: %s (user %s)", s.ID, s.URL, s.Username)
	}
	logger.Printf("  overlap: %s  tables: %d  schemaMode: %s", cfg.Overlap(), len(cfg.Tables), cfg.SchemaMode)
	verifyMode := "off"
	if cfg.VerifyStrict() {
		verifyMode = "strict"
	}
	logger.Printf("  applyTuning: batchSize=%d maxWorkers=%d verifyMode=%s", cfg.ApplyBatchSize(), cfg.ApplyWorkers(), verifyMode)

	if _, err := os.Stat(statePath); err == nil {
		logger.Printf("  checkpoint file: %s", statePath)
	} else {
		logger.Printf("  checkpoint file: %s (will create on first advance)", statePath)
	}
	for _, tbl := range cfg.Tables {
		if tbl.Name == "" {
			continue
		}
		ts, ok, err := state.LoadSafeThrough(statePath, tbl.Name)
		if err != nil {
			return fmt.Errorf("checkpoint: %w", err)
		}
		if ok {
			logger.Printf("  [%s] safeThrough=%s", tbl.Name, ts.UTC().Format(time.RFC3339))
		}
	}
	if err := run.Once(ctx, cfg, run.Options{Apply: apply, OneWay: oneWay, StatePath: statePath, Logger: logger}); err != nil {
		return err
	}
	return nil
}

func newServerHandler(configPath, statePath, token string, applyDefault bool, oneWayDefault string, logger *log.Logger, runner runOnceFunc) http.Handler {
	var running atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		if token != "" && !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
			return
		}
		if !running.CompareAndSwap(false, true) {
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "sync already running"})
			return
		}
		defer running.Store(false)

		apply := applyDefault
		oneWay := oneWayDefault
		if q := strings.TrimSpace(r.URL.Query().Get("apply")); q != "" {
			b, err := strconv.ParseBool(q)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid apply query parameter"})
				return
			}
			apply = b
		}
		if q := strings.TrimSpace(r.URL.Query().Get("oneWay")); q != "" {
			mode, err := normalizeOneWay(q)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid oneWay query parameter"})
				return
			}
			oneWay = mode
		}

		start := time.Now().UTC()
		err := runner(r.Context(), configPath, statePath, apply, oneWay, logger)
		dur := time.Since(start)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok":       false,
				"apply":    apply,
				"oneWay":   oneWay,
				"duration": dur.String(),
				"error":    err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"apply":    apply,
			"oneWay":   oneWay,
			"duration": dur.String(),
		})
	})
	return mux
}

func normalizeOneWay(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return "", nil
	case "to-blue", "to-green":
		return strings.TrimSpace(strings.ToLower(raw)), nil
	default:
		return "", fmt.Errorf("invalid one-way mode %q (expected to-blue or to-green)", raw)
	}
}

func parseCommand(args []string) (cmd string, before, after []string, ok bool) {
	for i, a := range args {
		if a == "run" || a == "serve" {
			return a, args[:i], args[i+1:], true
		}
	}
	return "", nil, nil, false
}

func defaultConfigPath() string {
	if p := os.Getenv("FREESYNC_CONFIG"); p != "" {
		return p
	}
	return "config/dev.example.json"
}

func defaultStatePath() string {
	if p := os.Getenv("FREESYNC_STATE"); p != "" {
		return p
	}
	return "data/sync-state.json"
}

func authorized(r *http.Request, token string) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(auth, "Bearer ") && strings.TrimPrefix(auth, "Bearer ") == token {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-FreeSync-Token")) == token {
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
