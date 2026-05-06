// Package odata talks to FileMaker Server OData API (Basic auth).
package odata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
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
	Logf       func(format string, args ...any)

	// Retry settings for transient transport/server failures.
	RetryMaxAttempts int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// GetJSON performs GET with Accept application/json.
func (c *Client) GetJSON(ctx context.Context, url string) ([]byte, int, error) {
	return c.doRequest(ctx, http.MethodGet, url, nil, "application/json", "")
}

func (c *Client) doRequest(ctx context.Context, method, url string, body []byte, accept, contentType string) ([]byte, int, error) {
	maxAttempts, baseDelay, maxDelay := c.retrySettings()
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastStatus int
	var lastBody []byte
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		req.SetBasicAuth(c.Username, c.Password)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if method == http.MethodPatch || method == http.MethodPost {
			req.Header.Set("Prefer", "return=minimal")
		}

		res, err := c.httpClient().Do(req)
		if err != nil {
			if attempt < maxAttempts && isRetryableTransportError(err) {
				wait := nextBackoff(attempt, baseDelay, maxDelay)
				c.logRetry(method, url, attempt, maxAttempts, wait, err.Error())
				if err := sleepCtx(ctx, wait); err != nil {
					return nil, 0, err
				}
				continue
			}
			return nil, 0, err
		}

		lastStatus = res.StatusCode
		rb, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return nil, res.StatusCode, readErr
		}
		lastBody = rb

		if attempt < maxAttempts && isRetryableStatus(res.StatusCode) {
			wait := retryAfterDelay(res.Header.Get("Retry-After"), baseDelay, maxDelay, attempt)
			c.logRetry(method, url, attempt, maxAttempts, wait, fmt.Sprintf("status=%d", res.StatusCode))
			if err := sleepCtx(ctx, wait); err != nil {
				return nil, 0, err
			}
			continue
		}
		return rb, res.StatusCode, nil
	}
	return lastBody, lastStatus, nil
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

func (c *Client) retrySettings() (maxAttempts int, baseDelay, maxDelay time.Duration) {
	maxAttempts = c.RetryMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 6
	}
	baseDelay = c.RetryBaseDelay
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}
	maxDelay = c.RetryMaxDelay
	if maxDelay <= 0 {
		maxDelay = 8 * time.Second
	}
	if maxDelay < baseDelay {
		maxDelay = baseDelay
	}
	return maxAttempts, baseDelay, maxDelay
}

func (c *Client) logRetry(method, url string, attempt, maxAttempts int, wait time.Duration, reason string) {
	if c.Logf == nil {
		return
	}
	c.Logf("odata retry: method=%s attempt=%d/%d wait=%s reason=%s url=%s", method, attempt, maxAttempts, wait, reason, url)
}

func isRetryableStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500 && code <= 599
}

func isRetryableTransportError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "timeout") {
		return true
	}
	return false
}

func nextBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	d := baseDelay
	for i := 1; i < attempt; i++ {
		if d >= maxDelay {
			break
		}
		d *= 2
		if d > maxDelay {
			d = maxDelay
		}
	}
	return jitter(d)
}

func retryAfterDelay(h string, baseDelay, maxDelay time.Duration, attempt int) time.Duration {
	if h != "" {
		if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s >= 0 {
			d := time.Duration(s) * time.Second
			if d > maxDelay {
				return maxDelay
			}
			if d < baseDelay {
				return baseDelay
			}
			return d
		}
	}
	return nextBackoff(attempt, baseDelay, maxDelay)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	// Add up to 10% jitter to avoid synchronized retries.
	j := time.Duration(rand.Int63n(int64(d / 10)))
	return d + j
}
