package main

import (
	"net/http/httptest"
	"testing"
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
