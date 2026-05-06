package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServeHandler_Healthz(t *testing.T) {
	h := newServerHandler("cfg.json", "state.json", "", true, "", log.New(io.Discard, "", 0), func(context.Context, string, string, bool, string, *log.Logger) error {
		return nil
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", res.StatusCode)
	}
}

func TestServeHandler_RunUnauthorized(t *testing.T) {
	h := newServerHandler("cfg.json", "state.json", "secret", true, "", log.New(io.Discard, "", 0), func(context.Context, string, string, bool, string, *log.Logger) error {
		t.Fatal("runner should not be called when unauthorized")
		return nil
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", res.StatusCode)
	}
}

func TestServeHandler_RunApplyOverride(t *testing.T) {
	var calledApply bool
	h := newServerHandler("cfg.json", "state.json", "", true, "", log.New(io.Discard, "", 0), func(_ context.Context, _, _ string, apply bool, _ string, _ *log.Logger) error {
		calledApply = apply
		return nil
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run?apply=false", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", res.StatusCode)
	}
	if calledApply {
		t.Fatal("expected apply=false override")
	}
}

func TestServeHandler_RunConflict(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	h := newServerHandler("cfg.json", "state.json", "", true, "", log.New(io.Discard, "", 0), func(_ context.Context, _, _ string, _ bool, _ string, _ *log.Logger) error {
		started <- struct{}{}
		<-release
		return nil
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	done1 := make(chan *http.Response, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run", nil)
		res, _ := http.DefaultClient.Do(req)
		done1 <- res
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not start")
	}

	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/run", nil)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusConflict {
		t.Fatalf("status: got %d want 409", res2.StatusCode)
	}

	close(release)
	res1 := <-done1
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("first run status: got %d want 200", res1.StatusCode)
	}
}

func TestServeHandler_RunReturnsErrorJSON(t *testing.T) {
	h := newServerHandler("cfg.json", "state.json", "", true, "", log.New(io.Discard, "", 0), func(context.Context, string, string, bool, string, *log.Logger) error {
		return context.DeadlineExceeded
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", res.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Fatalf("expected ok=false, got %+v", body)
	}
	if body["error"] == "" {
		t.Fatalf("expected error string, got %+v", body)
	}
}

func TestServeHandler_RunOneWayOverride(t *testing.T) {
	var calledOneWay string
	h := newServerHandler("cfg.json", "state.json", "", true, "to-blue", log.New(io.Discard, "", 0), func(_ context.Context, _, _ string, _ bool, oneWay string, _ *log.Logger) error {
		calledOneWay = oneWay
		return nil
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run?oneWay=to-green", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	if calledOneWay != "to-green" {
		t.Fatalf("expected oneWay override, got %q", calledOneWay)
	}
}
