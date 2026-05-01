// Package odata talks to FileMaker Server OData API (Basic auth).
package odata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client performs authenticated GET/PATCH/POST against one database base URL
// (e.g. https://host/fmi/odata/v4/MyDatabase).
type Client struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// GetJSON performs GET with Accept application/json.
func (c *Client) GetJSON(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.StatusCode, err
	}
	return b, res.StatusCode, nil
}

// TrimBase trims trailing slash from OData database base URL.
func TrimBase(base string) string {
	return strings.TrimRight(base, "/")
}

// JoinPath appends a relative OData path (may include query).
func JoinPath(base, path string) string {
	base = TrimBase(base)
	path = strings.TrimLeft(path, "/")
	return base + "/" + path
}

// RecordPath builds People('id') with OData single-quote escaping.
func RecordPath(entitySet, id string) string {
	id = strings.ReplaceAll(id, "'", "''")
	return fmt.Sprintf("%s('%s')", entitySet, id)
}

// SleepThrottle optional tiny pause between pages (avoid hammering dev server).
func SleepThrottle(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}
