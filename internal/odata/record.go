package odata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	return GetRecordPath(ctx, cli, RecordPath(entitySet, id))
}

// GetRecordPath loads one row from a concrete OData record path (e.g. People(42)).
func GetRecordPath(ctx context.Context, cli *Client, recordPath string) (map[string]any, error) {
	return GetRecordPathSelected(ctx, cli, recordPath, nil)
}

// GetRecordPathSelected loads one row from a concrete OData record path with an optional $select.
func GetRecordPathSelected(ctx context.Context, cli *Client, recordPath string, fields []string) (map[string]any, error) {
	path := JoinPath(cli.BaseURL, recordPath) + selectQuery(fields)
	b, code, err := cli.GetJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if code >= 300 {
		return nil, &HTTPStatusError{Method: "GET", StatusCode: code, Body: truncate(string(b), 400)}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// GetRecordByPK loads one row by filtering on a logical primary key field.
// It returns the row plus its concrete OData record path when available.
func GetRecordByPK(ctx context.Context, cli *Client, entitySet, pkField, pkValue string) (map[string]any, string, error) {
	return GetRecordByPKSelected(ctx, cli, entitySet, pkField, pkValue, nil)
}

// GetRecordByPKSelected loads one row by logical primary key with an optional $select.
func GetRecordByPKSelected(ctx context.Context, cli *Client, entitySet, pkField, pkValue string, fields []string) (map[string]any, string, error) {
	filter := fmt.Sprintf("%s eq '%s'", quoteFilterField(pkField), escapeODataString(pkValue))
	q := "$filter=" + encodeSpaces(filter) + "&$top=1"
	if sel := selectQuery(fields); sel != "" {
		q += "&" + strings.TrimPrefix(sel, "?")
	}
	path := JoinPath(cli.BaseURL, url.PathEscape(entitySet)) + "?" + q
	b, code, err := cli.GetJSON(ctx, path)
	if err != nil {
		return nil, "", err
	}
	if code >= 300 {
		if code == http.StatusNotFound {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("get by pk %d: %s", code, truncate(string(b), 400))
	}
	var envelope struct {
		Value []map[string]any `json:"value"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, "", err
	}
	if len(envelope.Value) == 0 {
		return nil, "", ErrNotFound
	}
	rec := envelope.Value[0]
	return rec, RecordPathFromMetadata(entitySet, rec), nil
}

func selectQuery(fields []string) string {
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, quoteSelectField(f))
	}
	if len(out) == 0 {
		return ""
	}
	return "?$select=" + strings.Join(out, ",")
}

// RecordPathFromMetadata extracts a concrete record path (e.g. People(42)) from OData metadata.
// Returns empty string when no usable path is present.
func RecordPathFromMetadata(entitySet string, rec map[string]any) string {
	for _, k := range []string{"@odata.editLink", "@odata.id", "@id"} {
		v, ok := rec[k]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		p := normalizeRecordPathFromMetadata(s)
		if p == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(entitySet)+"(") {
			return p
		}
	}
	return ""
}

func normalizeRecordPathFromMetadata(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil {
		if u.Path != "" {
			s = u.Path
		}
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" || !strings.Contains(s, "(") || !strings.HasSuffix(s, ")") {
		return ""
	}
	return s
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
	return PatchRecordPath(ctx, cli, RecordPath(entitySet, id), fields)
}

// PatchRecordPath sends merge-patch to a concrete OData record path (e.g. People(42)).
func PatchRecordPath(ctx context.Context, cli *Client, recordPath string, fields map[string]any) error {
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	path := JoinPath(cli.BaseURL, recordPath)
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
