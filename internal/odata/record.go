package odata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotFound is returned when a record GET returns 404.
var ErrNotFound = errors.New("odata: record not found")

// GetRecord loads one row as a JSON object (not wrapped in value array).
func GetRecord(ctx context.Context, cli *Client, entitySet, id string) (map[string]any, error) {
	path := JoinPath(cli.BaseURL, RecordPath(entitySet, id))
	b, code, err := cli.GetJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if code >= 300 {
		return nil, fmt.Errorf("get record %d: %s", code, truncate(string(b), 400))
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// StripMetadata removes OData / JSON-LD keys for write payloads.
func StripMetadata(m map[string]any) {
	for k := range m {
		if strings.HasPrefix(k, "@") {
			delete(m, k)
		}
	}
}

// PatchRecord sends merge-patch for an existing record.
func PatchRecord(ctx context.Context, cli *Client, entitySet, id string, fields map[string]any) error {
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	path := JoinPath(cli.BaseURL, RecordPath(entitySet, id))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(cli.Username, cli.Password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := cli.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("PATCH %d: %s", res.StatusCode, truncate(string(rb), 400))
	}
	return nil
}

// PostRecord creates a record (collection POST).
func PostRecord(ctx context.Context, cli *Client, entitySet string, fields map[string]any) error {
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	path := JoinPath(cli.BaseURL, strings.TrimLeft(entitySet, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(cli.Username, cli.Password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := cli.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("POST %d: %s", res.StatusCode, truncate(string(rb), 400))
	}
	return nil
}
