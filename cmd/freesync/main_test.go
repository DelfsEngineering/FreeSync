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
