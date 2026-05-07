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
	"github.com/DelfsEngineering/FreeSync/internal/odata"
	"github.com/DelfsEngineering/FreeSync/internal/run"
	"github.com/DelfsEngineering/FreeSync/internal/state"
)

type runOnceFunc func(ctx context.Context, configPath, statePath string, apply bool, oneWay string, verbose bool, logger *log.Logger) error

func main() {
	cmd, before, after, ok := parseCommand(os.Args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: freesync <run|serve> [flags]")
		fmt.Fprintln(os.Stderr, "  run   [-config path] [-state path] [-apply] [-one-way to-blue|to-green] [-verbose]")
		fmt.Fprintln(os.Stderr, "  serve [-config path] [-state path] [-listen :8080] [-apply] [-one-way to-blue|to-green] [-token secret] [-verbose]")
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
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var statePathFlag optionalString
	var oneWayFlag optionalString
	var verboseFlag optionalBool
	var applyFlag optionalBool
	fs.Var(&applyFlag, "apply", "execute writes (PATCH/POST); default is dry-run")
	fs.Var(&oneWayFlag, "one-way", "optional write direction filter: to-blue or to-green")
	fs.Var(&verboseFlag, "verbose", "show page-level manifest and diagnostic logs")
	fs.StringVar(&configPath, "config", configPath, "path to JSON config")
	fs.Var(&statePathFlag, "state", "path to checkpoint file (JSON)")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	statePath := firstNonEmpty(
		statePathFlag.Value(),
		strings.TrimSpace(os.Getenv("FREESYNC_STATE")),
		cfg.StateFilePath(),
	)
	oneWay := firstNonEmpty(
		oneWayFlag.Value(),
		strings.TrimSpace(os.Getenv("FREESYNC_ONE_WAY")),
		cfg.OneWayMode(),
	)
	verbose := firstBool(verboseFlag, envOptionalBool("FREESYNC_VERBOSE"), cfg.VerboseLogging())
	mode, err := normalizeOneWay(oneWay)
	if err != nil {
		return err
	}
	// Table logs and run summary use the default logger; send to stdout so
	// pipelines like `./freesync run ... | tail -n 30` include them (stderr is not piped).
	log.SetOutput(os.Stdout)
	return loadAndRunOnce(context.Background(), configPath, statePath, applyFlag.value, mode, verbose, log.Default())
}

func serveHTTP(flagArgs []string) error {
	configPath := defaultConfigPath()
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var statePathFlag optionalString
	var listenFlag optionalString
	var tokenFlag optionalString
	var applyFlag optionalBool
	var oneWayFlag optionalString
	var verboseFlag optionalBool
	fs.StringVar(&configPath, "config", configPath, "path to JSON config")
	fs.Var(&statePathFlag, "state", "path to checkpoint file (JSON)")
	fs.Var(&listenFlag, "listen", "HTTP listen address")
	fs.Var(&tokenFlag, "token", "optional bearer token for POST /run")
	fs.Var(&applyFlag, "apply", "default apply mode for POST /run")
	fs.Var(&oneWayFlag, "one-way", "default direction filter for POST /run: to-blue or to-green")
	fs.Var(&verboseFlag, "verbose", "show page-level manifest and diagnostic logs")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	statePath := firstNonEmpty(
		statePathFlag.Value(),
		strings.TrimSpace(os.Getenv("FREESYNC_STATE")),
		cfg.StateFilePath(),
	)
	listen := firstNonEmpty(
		listenFlag.Value(),
		strings.TrimSpace(os.Getenv("FREESYNC_LISTEN")),
		cfg.ListenAddr(),
	)
	token := firstNonEmpty(
		tokenFlag.Value(),
		cfg.TriggerBearerToken(),
	)
	oneWayDefault := firstNonEmpty(
		oneWayFlag.Value(),
		strings.TrimSpace(os.Getenv("FREESYNC_ONE_WAY")),
		cfg.OneWayMode(),
	)
	verboseDefault := firstBool(verboseFlag, envOptionalBool("FREESYNC_VERBOSE"), cfg.VerboseLogging())
	applyDefault := firstBool(applyFlag, envOptionalBool("FREESYNC_APPLY"), cfg.ApplyDefault())

	oneWayDefault, err = normalizeOneWay(oneWayDefault)
	if err != nil {
		return err
	}

	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	logger := log.Default()
	logger.Printf("Free Sync API listening on %s (defaultApply=%v oneWay=%q verbose=%v)", listen, applyDefault, oneWayDefault, verboseDefault)

	return http.ListenAndServe(listen, newServerHandler(configPath, statePath, token, applyDefault, oneWayDefault, verboseDefault, logger, loadAndRunOnce))
}

func loadAndRunOnce(ctx context.Context, configPath, statePath string, apply bool, oneWay string, verbose bool, logger *log.Logger) error {
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := resolveFileTables(ctx, cfg, logger, verbose); err != nil {
		return fmt.Errorf("resolve tables: %w", err)
	}

	logger.Printf("Free Sync — config %q apply=%v oneWay=%q verbose=%v", configPath, apply, oneWay, verbose)
	for _, f := range cfg.Files {
		logger.Printf("  file %s:", f.ID)
		for _, s := range f.Servers {
			logger.Printf("    server %s: %s (user %s)", s.ID, s.URL, s.Username)
		}
	}
	logger.Printf("  overlap: %s  files: %d  tables: %d  schemaMode: %s", cfg.Overlap(), len(cfg.Files), cfg.TotalTableCount(), cfg.SchemaMode)
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
	for _, f := range cfg.Files {
		for _, tbl := range f.Tables {
			if tbl.Name == "" {
				continue
			}
			ts, ok, err := state.LoadSafeThrough(statePath, f.ID, tbl.Name)
			if err != nil {
				return fmt.Errorf("checkpoint: %w", err)
			}
			if ok && verbose {
				logger.Printf("  [%s/%s] safeThrough=%s", f.ID, tbl.Name, ts.UTC().Format(time.RFC3339))
			}
		}
	}
	if err := run.Once(ctx, cfg, run.Options{Apply: apply, OneWay: oneWay, StatePath: statePath, Verbose: verbose, Logger: logger}); err != nil {
		return err
	}
	return nil
}

func resolveFileTables(ctx context.Context, cfg *config.Config, logger *log.Logger, verbose bool) error {
	for i := range cfg.Files {
		file := &cfg.Files[i]
		if len(file.Tables) > 0 {
			continue
		}
		blue, green, err := file.BlueGreen()
		if err != nil {
			return fmt.Errorf("file %s: %w", file.ID, err)
		}
		httpClient := &http.Client{Timeout: 2 * time.Minute}
		blueCli := &odata.Client{BaseURL: odata.TrimBase(blue.URL), Username: blue.Username, Password: blue.Password, HTTPClient: httpClient}
		greenCli := &odata.Client{BaseURL: odata.TrimBase(green.URL), Username: green.Username, Password: green.Password, HTTPClient: httpClient}

		blueTables, err := odata.BaseTableNames(ctx, blueCli)
		if err != nil {
			return fmt.Errorf("file %s blue base tables: %w", file.ID, err)
		}
		greenTables, err := odata.BaseTableNames(ctx, greenCli)
		if err != nil {
			return fmt.Errorf("file %s green base tables: %w", file.ID, err)
		}

		greenSet := make(map[string]struct{}, len(greenTables))
		for _, name := range greenTables {
			greenSet[name] = struct{}{}
		}
		discovered := make([]config.TableSpec, 0, len(blueTables))
		for _, name := range blueTables {
			if _, ok := greenSet[name]; ok {
				discovered = append(discovered, config.TableSpec{Name: name})
			}
		}
		file.Tables = discovered
		if logger != nil {
			logger.Printf("  file %s: auto-discovered %d base tables from blue/green intersection", file.ID, len(discovered))
			if verbose && len(discovered) > 0 {
				names := make([]string, 0, len(discovered))
				for _, tbl := range discovered {
					names = append(names, tbl.Name)
				}
				logger.Printf("  file %s: base tables = %s", file.ID, strings.Join(names, ", "))
			}
		}
	}
	return nil
}

func newServerHandler(configPath, statePath, token string, applyDefault bool, oneWayDefault string, verboseDefault bool, logger *log.Logger, runner runOnceFunc) http.Handler {
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
		verbose := verboseDefault
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
		if q := strings.TrimSpace(r.URL.Query().Get("verbose")); q != "" {
			b, err := strconv.ParseBool(q)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid verbose query parameter"})
				return
			}
			verbose = b
		}

		start := time.Now().UTC()
		err := runner(r.Context(), configPath, statePath, apply, oneWay, verbose, logger)
		dur := time.Since(start)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok":       false,
				"apply":    apply,
				"oneWay":   oneWay,
				"verbose":  verbose,
				"duration": dur.String(),
				"error":    err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"apply":    apply,
			"oneWay":   oneWay,
			"verbose":  verbose,
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
	for _, candidate := range []string{
		"/app/config/dev.local.json",
		"/app/config/prod.local.json",
		"config/dev.local.json",
		"config/prod.local.json",
		"config/dev.example.json",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "config/dev.example.json"
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

type optionalString struct {
	set   bool
	value string
}

func (o *optionalString) String() string {
	return o.value
}

func (o *optionalString) Set(v string) error {
	o.set = true
	o.value = v
	return nil
}

func (o *optionalString) Value() string {
	if !o.set {
		return ""
	}
	return o.value
}

type optionalBool struct {
	set   bool
	value bool
}

func (o *optionalBool) String() string {
	if !o.set {
		return ""
	}
	return strconv.FormatBool(o.value)
}

func (o *optionalBool) Set(v string) error {
	o.set = true
	if v == "" {
		o.value = true
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return err
	}
	o.value = b
	return nil
}

func (o *optionalBool) IsBoolFlag() bool {
	return true
}

func envOptionalBool(name string) *bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil
	}
	return &b
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstBool(flag optionalBool, env *bool, fallback bool) bool {
	if flag.set {
		return flag.value
	}
	if env != nil {
		return *env
	}
	return fallback
}
