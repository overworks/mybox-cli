// Package api is a client for the MYBOX Open API.
//
// See docs/api-reference.md for the endpoint inventory this package implements
// and for the documented limits it defends against.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the production API root. Override it with MYBOX_API_BASE or
// Options.BaseURL, which is also how tests point the client at an httptest server.
const DefaultBaseURL = "https://open-api.mybox.naver.com/v1"

const (
	defaultMaxRetries = 3
	defaultTimeout    = 60 * time.Second
	maxBackoff        = 20 * time.Second
)

// Options configures a Client. The zero value is usable except for Token.
type Options struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
	// Limits overrides the per-minute call budgets; see DefaultLimits.
	Limits map[Group]int
	// MaxRetries caps retries of transient failures. Zero means defaultMaxRetries;
	// a negative value disables retrying.
	MaxRetries int
	// Trace, when non-nil, receives a redacted line per HTTP attempt. It never
	// receives the access token.
	Trace func(string)
}

// Client talks to the MYBOX Open API. It is safe for concurrent use.
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
	userAgent  string
	limiter    *Limiter
	maxRetries int
	trace      func(string)

	// sleep is overridable so retry tests do not spend real time.
	sleep func(context.Context, time.Duration) error
	// jitter returns a factor in [0,1); overridable for deterministic tests.
	jitter func() float64
}

// New builds a client. It returns an error only if the base URL is unusable.
func New(opts Options) (*Client, error) {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("invalid base URL %q: %w", base, err)
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "mybox-cli"
	}
	retries := opts.MaxRetries
	switch {
	case retries == 0:
		retries = defaultMaxRetries
	case retries < 0:
		retries = 0
	}

	return &Client{
		token:      opts.Token,
		baseURL:    base,
		httpClient: hc,
		userAgent:  ua,
		limiter:    NewLimiter(opts.Limits),
		maxRetries: retries,
		trace:      opts.Trace,
		sleep:      sleepCtx,
		jitter:     rand.Float64,
	}, nil
}

// BaseURL reports the API root in use.
func (c *Client) BaseURL() string { return c.baseURL }

// request describes one API call.
type request struct {
	method string
	path   string // joined onto baseURL, e.g. "/drive/resources"
	query  url.Values
	body   any   // marshalled as JSON when non-nil
	group  Group // rate-limit bucket
	// out receives the decoded response body. Leave nil for endpoints that
	// answer 204 or 200-with-no-body.
	out any
}

// do performs a request with rate limiting and bounded retries, decoding the
// response into req.out when set.
func (c *Client) do(ctx context.Context, req request) error {
	var payload []byte
	if req.body != nil {
		var err error
		if payload, err = json.Marshal(req.body); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	endpoint := c.baseURL + req.path
	if len(req.query) > 0 {
		endpoint += "?" + req.query.Encode()
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx, req.group); err != nil {
			return err
		}

		resp, err := c.attempt(ctx, req.method, endpoint, payload)
		if err != nil {
			// Transport failures (connection reset, timeout) are worth one more
			// try; a cancelled context is not.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
		} else {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return decodeBody(resp, req.out)
			}
			apiErr := parseError(resp)
			resp.Body.Close()
			if !apiErr.Retryable() {
				return apiErr
			}
			lastErr = apiErr
			if d, ok := retryAfter(resp.Header); ok {
				if attempt >= c.maxRetries {
					return lastErr
				}
				c.tracef("retry in %s after %s", d, apiErr.Error())
				if err := c.sleep(ctx, d); err != nil {
					return joinRetryError(lastErr, err)
				}
				continue
			}
		}

		if attempt >= c.maxRetries {
			return lastErr
		}
		d := c.backoff(attempt)
		c.tracef("retry in %s after %v", d, lastErr)
		if err := c.sleep(ctx, d); err != nil {
			return joinRetryError(lastErr, err)
		}
	}
}

func (c *Client) attempt(ctx context.Context, method, endpoint string, payload []byte) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.ContentLength = int64(len(payload))
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	c.tracef("%s %s", method, endpoint)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	c.tracef("<- %s %s (x-request-id: %s)", resp.Status, endpoint, resp.Header.Get("x-request-id"))
	return resp, nil
}

// backoff returns an exponentially growing delay with full jitter, capped at
// maxBackoff.
func (c *Client) backoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d > maxBackoff {
		d = maxBackoff
	}
	// Full jitter keeps concurrent workers from retrying in lockstep.
	return time.Duration(float64(d) * (0.5 + 0.5*c.jitter()))
}

// retryAfter reads a Retry-After header in either of its documented forms.
func retryAfter(h http.Header) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxBackoff), true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, maxBackoff), true
		}
		return 0, true
	}
	return 0, false
}

// decodeBody reads a successful response into out, tolerating the endpoints that
// answer 200 with an empty body (move and restore both do).
func decodeBody(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

// joinRetryError keeps the failure that triggered the retry alongside the reason
// the retry was abandoned. Without it, a rate-limited call whose deadline
// expires during backoff would surface only as "context deadline exceeded",
// hiding the 429 the user actually needs to act on.
func joinRetryError(lastErr, ctxErr error) error {
	if lastErr == nil {
		return ctxErr
	}
	return errors.Join(lastErr, ctxErr)
}

func (c *Client) tracef(format string, args ...any) {
	if c.trace == nil {
		return
	}
	c.trace(fmt.Sprintf(format, args...))
}
