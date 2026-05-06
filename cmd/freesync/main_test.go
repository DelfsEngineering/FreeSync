package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DelfsEngineering/FreeSync/internal/config"
)

func TestParseCommand_flexibleArgs(t *testing.T) {
	cmd, before, after, ok := parseCommand([]string{"-config", "x.json", "run", "-apply"})
	if !ok {
		t.Fatal("expected parse success")
	}
	if cmd != "run" {
		t.Fatalf("cmd: got %q want run", cmd)
	}
	if len(before) != 2 || before[0] != "-config" || before[1] != "x.json" {
		t.Fatalf("before args mismatch: %#v", before)
	}
	if len(after) != 1 || after[0] != "-apply" {
		t.Fatalf("after args mismatch: %#v", after)
	}
}

func TestAuthorized(t *testing.T) {
	req := httptest.NewRequest("POST", "/run", nil)
	req.Header.Set("Authorization", "Bearer abc")
	if !authorized(req, "abc") {
		t.Fatal("expected bearer token to authorize")
	}

	req2 := httptest.NewRequest("POST", "/run", nil)
	req2.Header.Set("X-FreeSync-Token", "xyz")
	if !authorized(req2, "xyz") {
		t.Fatal("expected header token to authorize")
	}

	req3 := httptest.NewRequest("POST", "/run", nil)
	if authorized(req3, "xyz") {
		t.Fatal("expected missing token to fail")
	}
}

func TestNormalizeOneWay(t *testing.T) {
	got, err := normalizeOneWay("TO-BLUE")
	if err != nil {
		t.Fatalf("normalize one-way returned error: %v", err)
	}
	if got != "to-blue" {
		t.Fatalf("got %q want to-blue", got)
	}

	got, err = normalizeOneWay("")
	if err != nil || got != "" {
		t.Fatalf("expected empty mode, got mode=%q err=%v", got, err)
	}

	if _, err := normalizeOneWay("left"); err == nil {
		t.Fatal("expected invalid mode to error")
	}
}

func TestResolveFileTables_AutoDiscoversBlueGreenIntersection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blue/FileMaker_BaseTables":
			_, _ = w.Write([]byte(`{"value":[{"BaseTableName":"Inbox"},{"BaseTableName":"Users"},{"BaseTableName":"OnlyBlue"}]}`))
		case "/green/FileMaker_BaseTables":
			_, _ = w.Write([]byte(`{"value":[{"BaseTableName":"Users"},{"BaseTableName":"Inbox"},{"BaseTableName":"OnlyGreen"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{
		Files: []config.FileConfig{
			{
				ID: "auto",
				Servers: []config.Server{
					{ID: "blue", URL: srv.URL + "/blue", Username: "u", Password: "p"},
					{ID: "green", URL: srv.URL + "/green", Username: "u", Password: "p"},
				},
			},
		},
	}
	if err := resolveFileTables(context.Background(), cfg, log.New(io.Discard, "", 0), false); err != nil {
		t.Fatal(err)
	}
	got := cfg.Files[0].Tables
	if len(got) != 2 || got[0].Name != "Inbox" || got[1].Name != "Users" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveFileTables_ExplicitTablesTakePrecedence(t *testing.T) {
	cfg := &config.Config{
		Files: []config.FileConfig{
			{
				ID: "manual",
				Servers: []config.Server{
					{ID: "blue", URL: "https://blue.example/db", Username: "u", Password: "p"},
					{ID: "green", URL: "https://green.example/db", Username: "u", Password: "p"},
				},
				Tables: []config.TableSpec{{Name: "Forms"}},
			},
		},
	}
	if err := resolveFileTables(context.Background(), cfg, log.New(io.Discard, "", 0), false); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Files[0].Tables) != 1 || cfg.Files[0].Tables[0].Name != "Forms" {
		t.Fatalf("got %+v", cfg.Files[0].Tables)
	}
}
