package odata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotFound is returned when a record GET returns 404.
var ErrNotFound = errors.New("odata: record not found")

// HTTPStatusError wraps non-2xx OData responses with method/status/body.
type HTTPStatusError struct {
	Method     string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s %d: %s", e.Method, e.StatusCode, e.Body)
}

// IsHTTPStatus reports whether err contains an HTTPStatusError with status code.
func IsHTTPStatus(err error, status int) bool {
	var hs *HTTPStatusError
	return errors.As(err, &hs) && hs.StatusCode == status
}

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
	rb, code, err := cli.doRequest(ctx, http.MethodPatch, path, body, "application/json", "application/json")
	if err != nil {
		return err
	}
	if code >= 300 {
		return &HTTPStatusError{Method: "PATCH", StatusCode: code, Body: truncate(string(rb), 400)}
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
	rb, code, err := cli.doRequest(ctx, http.MethodPost, path, body, "application/json", "application/json")
	if err != nil {
		return err
	}
	if code >= 300 {
		return &HTTPStatusError{Method: "POST", StatusCode: code, Body: truncate(string(rb), 400)}
	}
	return nil
}
