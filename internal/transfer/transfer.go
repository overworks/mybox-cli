// Package transfer moves file contents to and from the storage URLs that the
// MYBOX API issues.
//
// The API documents how to *obtain* an upload or download URL but not what to do
// with it — no method, headers or body format is specified for the storage host.
// Downloading is unambiguous (a plain GET). Uploading is not, so this package
// keeps the wire format behind a Strategy that can be probed and overridden
// without touching the rest of the CLI. See docs/api-reference.md.
package transfer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultTimeout bounds a single transfer. Large files legitimately take a long
// time, so this is generous compared to the API client's timeout.
const DefaultTimeout = 2 * time.Hour

// Client performs storage-host transfers.
type Client struct {
	HTTPClient *http.Client
	UserAgent  string
	// Trace receives one redacted line per attempt when set. Storage URLs embed
	// short-lived credentials, so they are never passed through verbatim.
	Trace func(string)
}

// New builds a transfer client.
func New(userAgent string, trace func(string)) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: DefaultTimeout},
		UserAgent:  userAgent,
		Trace:      trace,
	}
}

// Download streams a file's contents from a download URL into dst.
//
// The URL is single-use and valid for ten minutes, so a failure here means the
// caller must request a fresh one rather than retrying the same URL.
func (c *Client) Download(ctx context.Context, url string, dst io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	c.tracef("GET %s", redactURL(url))
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()
	c.tracef("<- %s", resp.Status)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, storageError("download", resp)
	}

	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		return n, fmt.Errorf("download failed part way through: %w", err)
	}

	// A truncated transfer that still reports success would silently corrupt the
	// local file, so compare against the length the server promised.
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return n, fmt.Errorf("download was truncated: got %d bytes, expected %d", n, resp.ContentLength)
	}
	return n, nil
}

// storageError builds an error from a non-2xx storage response, quoting a little
// of the body since the storage host does not use the API's error envelope.
func storageError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	msg := fmt.Sprintf("%s failed: %s", op, resp.Status)
	if len(body) > 0 {
		msg += ": " + string(body)
	}
	return &StorageError{Status: resp.StatusCode, Message: msg}
}

// StorageError reports a failure from the storage host, which is a different
// service from the API and does not share its error format.
type StorageError struct {
	Status  int
	Message string
}

func (e *StorageError) Error() string { return e.Message }

func (c *Client) tracef(format string, args ...any) {
	if c.Trace == nil {
		return
	}
	c.Trace(fmt.Sprintf(format, args...))
}

// redactURL strips the query string from a storage URL. Those parameters carry
// short-lived access tokens (stoken, atoken) that must not reach a log.
func redactURL(url string) string {
	for i, r := range url {
		if r == '?' {
			return url[:i] + "?<redacted>"
		}
	}
	return url
}
