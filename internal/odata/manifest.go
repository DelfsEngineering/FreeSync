package odata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const manifestPageSize = 50

// ManifestRow is a lightweight manifest item (id + modification timestamp).
type ManifestRow struct {
	ID  string
	Mod time.Time
}

// FetchManifest returns id -> ModificationTimestamp for rows in [start, end] inclusive window.
func FetchManifest(ctx context.Context, cli *Client, entitySet string, start, end time.Time, pkField, modField string) (map[string]time.Time, error) {
	return FetchManifestWithProgress(ctx, cli, entitySet, start, end, pkField, modField, nil)
}

// FetchManifestWithProgress is FetchManifest with optional per-page callback.
func FetchManifestWithProgress(ctx context.Context, cli *Client, entitySet string, start, end time.Time, pkField, modField string, onPage func(pageNum, pageRows, totalRows int)) (map[string]time.Time, error) {
	out := make(map[string]time.Time)
	pageURL := ""
	var cursorID string
	var cursorMod time.Time
	var hasCursor bool
	pageNum := 0
	for {
		var err error
		pageURL, err = manifestPageURL(cli.BaseURL, entitySet, start, end, pkField, modField, pageURL, manifestPageSize, cursorID, cursorMod, hasCursor)
		if err != nil {
			return nil, err
		}
		b, code, err := cli.GetJSON(ctx, pageURL)
		if err != nil {
			return nil, err
		}
		if code >= 300 {
			return nil, fmt.Errorf("manifest GET %d: %s", code, truncate(string(b), 500))
		}
		var envelope struct {
			Value    []map[string]any `json:"value"`
			NextLink string           `json:"@odata.nextLink"`
		}
		if err := json.Unmarshal(b, &envelope); err != nil {
			return nil, fmt.Errorf("decode manifest: %w", err)
		}
		for _, row := range envelope.Value {
			idv, ok := row[pkField]
			if !ok {
				continue
			}
			id, ok := idv.(string)
			if !ok {
				continue
			}
			modv, ok := row[modField]
			if !ok {
				continue
			}
			mod, err := parseODataTime(modv)
			if err != nil {
				continue
			}
			out[id] = mod
		}
		pageNum++
		if onPage != nil {
			onPage(pageNum, len(envelope.Value), len(out))
		}
		if envelope.NextLink != "" {
			pageURL = envelope.NextLink
			continue
		}
		// FileMaker may omit @odata.nextLink for plain $top usage; page manually via keyset cursor.
		if len(envelope.Value) < manifestPageSize {
			break
		}
		cursorID, cursorMod, hasCursor, err = manifestCursor(envelope.Value, pkField, modField)
		if err != nil {
			return nil, err
		}
		pageURL = ""
		SleepThrottle(50 * time.Millisecond)
	}
	return out, nil
}

// FetchManifestHead returns up to top rows from a manifest window ordered by mod asc.
// This is a lightweight probe helper for bootstrap strategies.
func FetchManifestHead(ctx context.Context, cli *Client, entitySet string, start, end time.Time, pkField, modField string, top int) ([]ManifestRow, error) {
	if top <= 0 {
		top = 1
	}
	pageURL, err := manifestPageURL(cli.BaseURL, entitySet, start, end, pkField, modField, "", top, "", time.Time{}, false)
	if err != nil {
		return nil, err
	}
	b, code, err := cli.GetJSON(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("manifest head GET %d: %s", code, truncate(string(b), 500))
	}
	var envelope struct {
		Value []map[string]any `json:"value"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, fmt.Errorf("decode manifest head: %w", err)
	}
	out := make([]ManifestRow, 0, len(envelope.Value))
	for _, row := range envelope.Value {
		idv, ok := row[pkField]
		if !ok {
			continue
		}
		id, ok := idv.(string)
		if !ok {
			continue
		}
		modv, ok := row[modField]
		if !ok {
			continue
		}
		mod, err := parseODataTime(modv)
		if err != nil {
			continue
		}
		out = append(out, ManifestRow{ID: id, Mod: mod})
	}
	return out, nil
}

func manifestPageURL(base, entitySet string, start, end time.Time, pkField, modField string, pageToken string, top int, cursorID string, cursorMod time.Time, hasCursor bool) (string, error) {
	if pageToken != "" {
		return pageToken, nil
	}
	pkRef := quoteFilterField(pkField)
	modRef := quoteFilterField(modField)
	// FileMaker rejects url.Values-style encoding (e.g. %3A in timestamps). Encode only spaces and commas.
	filter := fmt.Sprintf("%s ge %s and %s le %s",
		modRef,
		start.UTC().Format(time.RFC3339),
		modRef,
		end.UTC().Format(time.RFC3339),
	)
	if hasCursor {
		cursor := fmt.Sprintf("(%s gt %s or (%s eq %s and %s gt '%s'))",
			modRef,
			cursorMod.UTC().Format(time.RFC3339),
			modRef,
			cursorMod.UTC().Format(time.RFC3339),
			pkRef,
			escapeODataString(cursorID),
		)
		filter = filter + " and " + cursor
	}
	// On FileMaker servers tested, comma-separated $select/$orderby works when each
	// field name is wrapped in double quotes.
	// Stable pagination requires ordering by both modification field and primary key.
	ord := quoteSelectField(modField) + "%20asc," + quoteSelectField(pkField) + "%20asc"
	q := "$filter=" + encodeSpaces(filter) +
		"&$orderby=" + ord +
		"&$select=" + quoteSelectField(pkField) + "," + quoteSelectField(modField) +
		fmt.Sprintf("&$top=%d", top)
	return JoinPath(base, url.PathEscape(entitySet)) + "?" + q, nil
}

// encodeSpaces only — FileMaker rejects comma-encoding in $select/$orderby (see live tests).
func encodeSpaces(s string) string {
	s = strings.ReplaceAll(s, " ", "%20")
	// Cursor filters compare string PK values (id gt '...'); encode literal quote marks.
	s = strings.ReplaceAll(s, "'", "%27")
	return s
}

func quoteSelectField(field string) string {
	return "%22" + encodeSpaces(field) + "%22"
}

func quoteFilterField(field string) string {
	return quoteSelectField(field)
}

func escapeODataString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func manifestCursor(rows []map[string]any, pkField, modField string) (string, time.Time, bool, error) {
	for i := len(rows) - 1; i >= 0; i-- {
		idv, ok := rows[i][pkField]
		if !ok {
			continue
		}
		id, ok := idv.(string)
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		modv, ok := rows[i][modField]
		if !ok {
			continue
		}
		mod, err := parseODataTime(modv)
		if err != nil {
			continue
		}
		return id, mod, true, nil
	}
	return "", time.Time{}, false, fmt.Errorf("manifest page missing cursor fields %q/%q", pkField, modField)
}

func parseODataTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case string:
		return time.Parse(time.RFC3339, x)
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", v)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
