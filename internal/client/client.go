// Package client is a small client for the Awacino API (https://api.awacino.net, paths /v1/...).
//
// It authenticates with an API key (key id + secret) exchanged at POST /v1/oauth/token for a
// 15-minute bearer token, renews that token before it ends, sends an Idempotency-Key on every
// write, retries 429 / 5xx with back-off (honouring Retry-After) and turns RFC 9457 problem
// details into *APIError.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultEndpoint is the official API host.
const DefaultEndpoint = "https://api.awacino.net"

// Config configures a Client.
type Config struct {
	Endpoint   string
	KeyID      string
	KeySecret  string
	UserAgent  string
	HTTPClient *http.Client
	// MaxRetries is the number of retries after the first attempt (default 6).
	MaxRetries int
	// MinBackoff / MaxBackoff bound the wait between retries (defaults 1 s / 30 s).
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// MaxRetryAfter caps a server's Retry-After (default 90 s).
	MaxRetryAfter time.Duration
}

// Client talks to the Awacino API. It is safe for concurrent use.
type Client struct {
	cfg   Config
	http  *http.Client
	token *tokenState

	keyMu   sync.Mutex
	keyInfo *KeyInfo
}

// tokenState is shared by every Client with the same endpoint + key (one Terraform run starts
// the provider several times; the token endpoint allows 10 requests a minute).
type tokenState struct {
	mu        sync.Mutex
	token     string
	expires   time.Time
	headersTo time.Time // until then: send the key headers instead of a token (token endpoint throttled / down)
}

var (
	tokensMu sync.Mutex
	tokens   = map[string]*tokenState{}
)

func sharedToken(endpoint, id, secret string) *tokenState {
	h := sha256.Sum256([]byte(endpoint + "\x00" + id + "\x00" + secret))
	k := hex.EncodeToString(h[:])
	tokensMu.Lock()
	defer tokensMu.Unlock()
	if t, ok := tokens[k]; ok {
		return t
	}
	t := &tokenState{}
	tokens[k] = t
	return t
}

// New returns a Client. Endpoint defaults to DefaultEndpoint.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q is not an http(s) URL", cfg.Endpoint)
	}
	if cfg.KeyID == "" || cfg.KeySecret == "" {
		return nil, errors.New("key_id and key_secret are required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 6
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.MaxRetryAfter == 0 {
		cfg.MaxRetryAfter = 90 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "terraform-provider-awacino"
	}
	return &Client{cfg: cfg, http: cfg.HTTPClient, token: sharedToken(cfg.Endpoint, cfg.KeyID, cfg.KeySecret)}, nil
}

// Endpoint is the API base URL the client uses.
func (c *Client) Endpoint() string { return c.cfg.Endpoint }

// NewIdempotencyKey returns a random UUID (v4) for the Idempotency-Key header.
func NewIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ------------------------------------------------------------------ token

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// renewBefore: a token is renewed this long before it ends.
const renewBefore = 2 * time.Minute

// bearer returns a valid token, or "" when the key headers are to be used for now.
func (c *Client) bearer(ctx context.Context, force bool) (string, error) {
	t := c.token
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if !force && t.token != "" && now.Add(renewBefore).Before(t.expires) {
		return t.token, nil
	}
	if now.Before(t.headersTo) {
		return "", nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.cfg.KeyID}, "client_secret": {c.cfg.KeySecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint+"/v1/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	res, err := c.http.Do(req)
	if err != nil {
		// the token endpoint is unreachable for now: the key headers work as well
		t.headersTo = now.Add(30 * time.Second)
		return "", nil
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode == http.StatusOK:
		var tr tokenResponse
		if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
			return "", fmt.Errorf("awacino: token endpoint answered without a token")
		}
		if tr.ExpiresIn <= 0 {
			tr.ExpiresIn = 900
		}
		t.token = tr.AccessToken
		t.expires = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
		return t.token, nil
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 || res.StatusCode == http.StatusNotFound:
		// throttled (10 tokens / minute) or not available: use the key headers until it may be asked again
		wait := retryAfter(res.Header.Get("Retry-After"), c.cfg.MaxRetryAfter)
		if wait <= 0 {
			wait = 30 * time.Second
		}
		t.headersTo = now.Add(wait)
		t.token = ""
		return "", nil
	default:
		return "", parseError(res.StatusCode, http.MethodPost, "/v1/oauth/token", res.Header, body)
	}
}

func (c *Client) invalidateToken(tok string) {
	t := c.token
	t.mu.Lock()
	if t.token == tok {
		t.token = ""
	}
	t.mu.Unlock()
}

// ------------------------------------------------------------------ requests

// Request describes one API call.
type Request struct {
	Method string
	Path   string // e.g. "/v1/members/12"
	Query  url.Values
	Body   any
	// Idempotency is the Idempotency-Key; writes get a fresh one when empty. The same key is
	// sent on every retry of this call.
	Idempotency string
	// ContentType defaults to application/json.
	ContentType string
}

// Do sends r and decodes a 2xx JSON answer into out (when out is not nil). It returns the HTTP
// status and the response headers.
func (c *Client) Do(ctx context.Context, r Request, out any) (int, http.Header, error) {
	var payload []byte
	if r.Body != nil {
		b, err := json.Marshal(r.Body)
		if err != nil {
			return 0, nil, err
		}
		payload = b
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if write && r.Idempotency == "" {
		r.Idempotency = NewIdempotencyKey()
	}
	ctype := r.ContentType
	if ctype == "" && payload != nil {
		// PATCH bodies are merge patches; the API takes them as application/json (the live
		// server refuses application/merge-patch+json with 415)
		ctype = "application/json"
	}
	u := c.cfg.Endpoint + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}

	retried401 := false
	var lastErr error
	for attempt := 0; ; attempt++ {
		tok, err := c.bearer(ctx, false)
		if err != nil {
			return 0, nil, err
		}
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/json, application/problem+json")
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if r.Idempotency != "" {
			req.Header.Set("Idempotency-Key", r.Idempotency)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		} else {
			req.Header.Set("X-Awacino-Key-Id", c.cfg.KeyID)
			req.Header.Set("X-Awacino-Key-Secret", c.cfg.KeySecret)
		}

		res, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil, ctx.Err()
			}
			lastErr = fmt.Errorf("awacino: %s %s: %w", r.Method, r.Path, err)
			// every write carries an Idempotency-Key, so a retry never creates a second object
			if attempt >= c.cfg.MaxRetries {
				return 0, nil, lastErr
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return 0, nil, err
			}
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		res.Body.Close()
		if rerr != nil {
			lastErr = rerr
			if attempt >= c.cfg.MaxRetries {
				return 0, nil, lastErr
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return 0, nil, err
			}
			continue
		}

		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out != nil && len(bytes.TrimSpace(data)) > 0 && res.StatusCode != http.StatusNoContent {
				if err := json.Unmarshal(data, out); err != nil {
					return res.StatusCode, res.Header, fmt.Errorf("awacino: %s %s: cannot read the answer: %w", r.Method, r.Path, err)
				}
			}
			return res.StatusCode, res.Header, nil
		}

		apiErr := parseError(res.StatusCode, r.Method, r.Path, res.Header, data)
		if res.StatusCode == http.StatusUnauthorized && tok != "" && !retried401 {
			// the token ended early (key edited / revoked): ask for a new one once
			retried401 = true
			c.invalidateToken(tok)
			continue
		}
		if retryable(res.StatusCode, apiErr) && attempt < c.cfg.MaxRetries {
			wait := retryAfter(res.Header.Get("Retry-After"), c.cfg.MaxRetryAfter)
			if wait <= 0 {
				wait = c.backoff(attempt)
			}
			if err := c.sleep(ctx, wait); err != nil {
				return 0, nil, err
			}
			continue
		}
		return res.StatusCode, res.Header, apiErr
	}
}

func retryable(status int, e *APIError) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusConflict:
		return e != nil && e.Code == "idempotency_in_progress"
	}
	return false
}

func (c *Client) backoff(attempt int) time.Duration {
	d := float64(c.cfg.MinBackoff) * math.Pow(2, float64(attempt))
	if d > float64(c.cfg.MaxBackoff) {
		d = float64(c.cfg.MaxBackoff)
	}
	// +-20 % jitter
	d *= 0.8 + 0.4*mrand.Float64()
	return time.Duration(d)
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryAfter reads Retry-After (seconds or an HTTP date), capped at max.
func retryAfter(v string, max time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.Atoi(v); err == nil {
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	}
	if d < 0 {
		d = 0
	}
	if d > max {
		d = max
	}
	return d
}

// ------------------------------------------------------------------ helpers for the typed calls

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodGet, Path: path, Query: q}, out)
	return err
}

// page is the list envelope {"data": [...], "next_cursor": "..."}.
type page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

// listAll follows next_cursor until the last page.
func listAll[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "200")
	var all []T
	seen := map[string]bool{}
	for {
		var p page[T]
		if err := c.get(ctx, path, q, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Data...)
		if p.NextCursor == nil || *p.NextCursor == "" || seen[*p.NextCursor] {
			return all, nil
		}
		seen[*p.NextCursor] = true
		q.Set("cursor", *p.NextCursor)
	}
}

func seg(s string) string { return url.PathEscape(s) }
