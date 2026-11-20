package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL, KeyID: "awk_" + strings.Repeat("a", 26), KeySecret: "aws_" + t.Name(),
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond, MaxRetryAfter: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func token(w http.ResponseWriter, tok string, ttl int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": ttl, "scope": "users:read"})
}

func problem(w http.ResponseWriter, status int, code string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Request-Id", "req-1")
	b := map[string]any{"type": "https://www.awacino.com/support/docs/api/errors#" + code, "title": "T " + code, "status": status, "code": code, "request_id": "req-1"}
	for k, v := range extra {
		b[k] = v
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(b)
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{KeyID: "", KeySecret: "x"}); err == nil {
		t.Fatal("missing key id accepted")
	}
	if _, err := New(Config{Endpoint: "ftp://x", KeyID: "a", KeySecret: "b"}); err == nil {
		t.Fatal("ftp endpoint accepted")
	}
	c, err := New(Config{KeyID: "a", KeySecret: "b"})
	if err != nil || c.Endpoint() != DefaultEndpoint {
		t.Fatalf("default endpoint: %v %v", c, err)
	}
}

func TestBearerTokenIsCachedAndUsed(t *testing.T) {
	var tokens int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/token":
			atomic.AddInt32(&tokens, 1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") == "" || r.Form.Get("client_secret") == "" {
				t.Errorf("token form %v", r.Form)
			}
			token(w, "tok-1", 900)
		case "/v1/company":
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				t.Errorf("auth header %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Awacino-Key-Secret") != "" {
				t.Error("secret sent next to the token")
			}
			_, _ = w.Write([]byte(`{"id":"co","name":"Co","plan":"premium","packages":["management"],"access_capabilities":{"reach":["device","network"],"grants":true}}`))
		}
	})
	for i := 0; i < 3; i++ {
		co, err := c.GetCompany(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		caps, grants := co.Capabilities()
		if len(caps) != 2 || !grants {
			t.Fatalf("caps %v %v", caps, grants)
		}
	}
	if tokens != 1 {
		t.Fatalf("%d token requests, want 1", tokens)
	}
}

func TestTokenRenewedBeforeExpiry(t *testing.T) {
	var tokens int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			n := atomic.AddInt32(&tokens, 1)
			token(w, "tok-"+string(rune('0'+n)), 60) // ends within renewBefore: renewed on every call
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	_, _ = c.GetCompany(context.Background())
	_, _ = c.GetCompany(context.Background())
	if tokens != 2 {
		t.Fatalf("%d token requests, want 2 (a token that ends soon is renewed)", tokens)
	}
}

func TestUnauthorizedRefreshesTokenOnce(t *testing.T) {
	var tokens, calls int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			n := atomic.AddInt32(&tokens, 1)
			token(w, map[int32]string{1: "old", 2: "new"}[n], 900)
			return
		}
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") == "Bearer old" {
			problem(w, 401, "invalid_token", nil)
			return
		}
		_, _ = w.Write([]byte(`{"key_id":"awk_x","scopes":["users:read"]}`))
	})
	k, err := c.GetKey(context.Background())
	if err != nil || k.KeyID != "awk_x" {
		t.Fatalf("%v %v", k, err)
	}
	if tokens != 2 || calls != 2 {
		t.Fatalf("tokens=%d calls=%d", tokens, calls)
	}
}

func TestTokenThrottledFallsBackToKeyHeaders(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			w.Header().Set("Retry-After", "60")
			problem(w, 429, "rate_limited", nil)
			return
		}
		if r.Header.Get("Authorization") != "" || !strings.HasPrefix(r.Header.Get("X-Awacino-Key-Id"), "awk_") || r.Header.Get("X-Awacino-Key-Secret") == "" {
			t.Errorf("want key headers, got auth=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.GetCompany(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBadSecretIsAnError(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	})
	_, err := c.GetCompany(context.Background())
	e, ok := AsAPIError(err)
	if !ok || e.Status != 401 || e.Code != "invalid_client" || !strings.Contains(err.Error(), "key id or secret is wrong") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetriesKeepTheIdempotencyKey(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	var calls int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			token(w, "t", 900)
			return
		}
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			problem(w, 429, "rate_limited", nil)
		case 2:
			problem(w, 503, "internal", nil)
		case 3:
			problem(w, 409, "idempotency_in_progress", nil)
		default:
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id": 12, "email": "a@b.c", "role": "read", "status": "invited", "invitation": {"code": "AWA-1111-2222-3333", "open": true}}`))
		}
	})
	m, err := c.CreateMember(context.Background(), MemberCreate{Email: "a@b.c", Role: "read"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "12" || m.Invitation.Code != "AWA-1111-2222-3333" {
		t.Fatalf("member %+v", m)
	}
	if len(keys) != 4 || keys[0] == "" {
		t.Fatalf("keys %v", keys)
	}
	for _, k := range keys {
		if k != keys[0] {
			t.Fatalf("retries changed the Idempotency-Key: %v", keys)
		}
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			token(w, "t", 900)
			return
		}
		atomic.AddInt32(&calls, 1)
		problem(w, 502, "internal", nil)
	})
	_, err := c.GetMember(context.Background(), "1")
	if err == nil || calls != 7 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var calls int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			token(w, "t", 900)
			return
		}
		atomic.AddInt32(&calls, 1)
		problem(w, 409, "seats_full", map[string]any{"detail": "5 of 5 users are taken."})
	})
	_, err := c.CreateMember(context.Background(), MemberCreate{Email: "a@b.c"}, "k-12345678")
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	e, ok := AsAPIError(err)
	if !ok || e.Code != "seats_full" || e.RequestID != "req-1" {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"seats_full", "HTTP 409", "5 of 5 users", "Add users on awacino.com", "req-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %s", want, err)
		}
	}
}

func TestInsufficientScopeNamesTheCheckbox(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/token":
			token(w, "t", 900)
		case "/v1/key":
			_, _ = w.Write([]byte(`{"key_id":"awk_x","scopes":["users:read","users:write"]}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	if err := c.RequireScope(context.Background(), "users:read"); err != nil {
		t.Fatal(err)
	}
	err := c.RequireScope(context.Background(), "connections:write")
	if err == nil || !strings.Contains(err.Error(), "no Connections section with Write access") {
		t.Fatalf("%v", err)
	}
}

func TestDeleteNotFoundIsDone(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			token(w, "t", 900)
			return
		}
		if r.Method != http.MethodDelete || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("%s without Idempotency-Key", r.Method)
		}
		problem(w, 404, "not_found", nil)
	})
	for _, f := range []func(context.Context, string) error{c.DeleteMember, c.DeleteDevice, c.DeleteEnrolment, c.DeleteGrant, c.DeleteBeaminCode, c.RevokeInvitation} {
		if err := f(context.Background(), "x"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListFollowsCursor(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			token(w, "t", 900)
			return
		}
		if r.URL.Query().Get("role") != "beamin" || r.URL.Query().Get("limit") != "200" {
			t.Errorf("query %v", r.URL.Query())
		}
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"data":[{"id":"a","member_id":7}],"next_cursor":"c2"}`))
		case "c2":
			_, _ = w.Write([]byte(`{"data":[{"id":"b","member_id":"mem_8"}],"next_cursor":null}`))
		}
	})
	ds, err := c.ListDevices(context.Background(), DeviceFilter{Role: "beamin"})
	if err != nil || len(ds) != 2 || *ds[0].MemberID != "7" || *ds[1].MemberID != "mem_8" {
		t.Fatalf("%v %v", ds, err)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth/token" {
			token(w, "t", 900)
			return
		}
		problem(w, 503, "internal", nil)
	})
	c.cfg.MinBackoff, c.cfg.MaxBackoff = time.Second, time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.GetCompany(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if d := retryAfter("3", time.Minute); d != 3*time.Second {
		t.Fatal(d)
	}
	if d := retryAfter("600", time.Minute); d != time.Minute {
		t.Fatal(d)
	}
	if d := retryAfter("", time.Minute); d != 0 {
		t.Fatal(d)
	}
	if d := retryAfter(time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat), time.Minute); d <= 0 || d > 11*time.Second {
		t.Fatal(d)
	}
}

func TestIdempotencyKeyIsUUID(t *testing.T) {
	k := NewIdempotencyKey()
	if len(k) != 36 || k[14] != '4' || k == NewIdempotencyKey() {
		t.Fatal(k)
	}
}

func TestCapabilitiesListForm(t *testing.T) {
	co := Company{AccessCapabilities: json.RawMessage(`["device_network","ports"]`), Packages: []string{"management"}}
	caps, grants := co.Capabilities()
	if len(caps) != 2 || caps[1] != "ports" || !grants {
		t.Fatal(caps, grants)
	}
}

func TestRedacted(t *testing.T) {
	if !Redacted("code_redacted") || !Redacted("") || Redacted("AWA-1") {
		t.Fatal("Redacted")
	}
}
