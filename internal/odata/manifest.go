package odata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// FetchManifest returns id -> ModificationTimestamp for rows in [start, end] inclusive window.
func FetchManifest(ctx context.Context, cli *Client, entitySet string, start, end time.Time, pkField, modField string) (map[string]time.Time, error) {
	out := make(map[string]time.Time)
	pageURL, err := manifestPageURL(cli.BaseURL, entitySet, start, end, modField, "")
	if err != nil {
		return nil, err
	}
	for pageURL != "" {
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
		pageURL = envelope.NextLink
		SleepThrottle(50 * time.Millisecond)
	}
	return out, nil
}

func manifestPageURL(base, entitySet string, start, end time.Time, modField string, pageToken string) (string, error) {
	if pageToken != "" {
		return pageToken, nil
	}
	// FileMaker rejects url.Values-style encoding (e.g. %3A in timestamps). Encode only spaces and commas.
	filter := fmt.Sprintf("%s ge %s and %s le %s",
		modField,
		start.UTC().Format(time.RFC3339),
		modField,
		end.UTC().Format(time.RFC3339),
	)
	// FileMaker OData rejects $select lists (comma) and multi-field $orderby (comma).
	// Omit $select — read pk/mod from full rows (slightly larger payloads).
	ord := modField + " asc"
	q := "$filter=" + encodeSpaces(filter) +
		"&$orderby=" + encodeSpaces(ord) +
		"&$top=500"
	return JoinPath(base, url.PathEscape(entitySet)) + "?" + q, nil
}

// encodeSpaces only — FileMaker rejects comma-encoding in $select/$orderby (see live tests).
func encodeSpaces(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
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
