package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockAPI is an in-memory Awacino API with the answers of the live server (members with numeric ids,
// {"open": ...} invitations, enrolments that become devices, idempotent replays without codes) and the
// connection API of openapi.yaml. Unit tests run Terraform against it.
type mockAPI struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	scopes       []string
	noConnAPI    bool // the connection API answers 404 (not live yet)
	nextID       int
	members      map[string]map[string]any
	invitations  map[string]map[string]any // member id -> open invitation
	devices      map[string]map[string]any
	enrolments   map[string]map[string]any
	grants       map[string]map[string]any
	codes        map[string]map[string]any
	idem         map[string][]byte
	registerNext bool   // a new enrolment registers its device on the first GET
	lateDevice   string // a device of this name appears on the second GET /v1/devices
	deviceLists  int
	calls        []string
	tokenCalls   int
}

func newMockAPI(t *testing.T) *mockAPI {
	m := &mockAPI{t: t, scopes: []string{"users:read", "users:write", "connections:read", "connections:write"},
		members: map[string]map[string]any{}, invitations: map[string]map[string]any{}, devices: map[string]map[string]any{},
		enrolments: map[string]map[string]any{}, grants: map[string]map[string]any{}, codes: map[string]map[string]any{},
		idem: map[string][]byte{}, nextID: 100}
	m.devices["srv-1"] = map[string]any{"id": "srv-1", "name": "web-01", "role": "beamin", "kind": "unattended", "status": "active",
		"platform": "linux", "member_id": nil, "external_id": nil, "first_seen": "2026-11-01T00:00:00+00:00", "last_seen": "2026-11-17T00:00:00+00:00"}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAPI) id(prefix string) string {
	m.nextID++
	if prefix == "" {
		return fmt.Sprint(m.nextID)
	}
	return fmt.Sprintf("%s%d", prefix, m.nextID)
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00") }

func (m *mockAPI) problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://www.awacino.com/support/docs/api/errors#" + code,
		"title": code, "status": status, "code": code, "detail": detail, "request_id": "00000000-0000-4000-8000-000000000000"})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func clone(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (m *mockAPI) memberJSON(id string) map[string]any {
	x := clone(m.members[id])
	if inv, ok := m.invitations[id]; ok {
		x["invitation"] = map[string]any{"open": true, "expires_at": inv["expires_at"], "expired": false}
	} else {
		x["invitation"] = nil
	}
	n := 0
	fmt.Sscan(id, &n)
	x["id"] = n // the live server answers member ids as numbers
	return x
}

var (
	reMember     = regexp.MustCompile(`^/v1/members/(\d+)$`)
	reInvitation = regexp.MustCompile(`^/v1/members/(\d+)/invitation$`)
	reDevice     = regexp.MustCompile(`^/v1/devices/([^/]+)$`)
	reEnrolment  = regexp.MustCompile(`^/v1/enrolments/(enr_[0-9]+)$`)
	reGrant      = regexp.MustCompile(`^/v1/connections/grants/([^/]+)$`)
	reCode       = regexp.MustCompile(`^/v1/connections/beamin-codes/([^/]+)$`)
)

func (m *mockAPI) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := r.URL.Path
	m.calls = append(m.calls, r.Method+" "+p)
	if p == "/v1/oauth/token" {
		m.tokenCalls++
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "aws_test" {
			reply(w, 401, map[string]any{"error": "invalid_client"})
			return
		}
		reply(w, 200, map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 900, "scope": strings.Join(m.scopes, " ")})
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		m.problem(w, 401, "invalid_credentials", "")
		return
	}
	var body map[string]any
	if r.Body != nil && r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.ContentLength > 0 && !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			m.problem(w, 415, "json_required", "")
			return
		}
	}
	idem := r.Header.Get("Idempotency-Key")
	if (r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete) && idem == "" {
		m.problem(w, 400, "idempotency_key_required", "")
		return
	}
	if r.Method == http.MethodPost && idem != "" {
		if b, ok := m.idem[idem]; ok { // replay: without the codes
			w.Header().Set("Idempotent-Replayed", "true")
			reply(w, 200, json.RawMessage(b))
			return
		}
	}
	remember := func(v map[string]any, secrets ...string) {
		if idem == "" {
			return
		}
		c := clone(v)
		for _, s := range secrets {
			if strings.Contains(s, ".") {
				a, b, _ := strings.Cut(s, ".")
				if sub, ok := c[a].(map[string]any); ok {
					sub[b] = "code_redacted"
				}
			} else if _, ok := c[s]; ok {
				c[s] = "code_redacted"
			}
		}
		buf, _ := json.Marshal(c)
		m.idem[idem] = buf
	}
	if strings.HasPrefix(p, "/v1/connections/") && m.noConnAPI {
		m.problem(w, 404, "not_found", "")
		return
	}
	need := func(scope string) bool {
		for _, s := range m.scopes {
			if s == scope {
				return true
			}
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(403)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 403, "code": "insufficient_scope", "title": "scope", "required_scope": scope})
		return false
	}

	switch {
	case p == "/v1/key":
		reply(w, 200, map[string]any{"key_id": "awk_test", "description": "unit test", "scopes": m.scopes, "company_id": "co-1", "owner_kind": "company"})
	case p == "/v1/company":
		reply(w, 200, map[string]any{"id": "co-1", "name": "Test GmbH", "plan": "premium", "packages": []string{"api_access", "management"},
			"seats": map[string]any{"used": len(m.members), "total": 20}, "audit_retention_days": 180,
			"access_capabilities": map[string]any{"reach": []string{"device", "network"}, "grants": true}})

	// ---------------------------------------------------------------- members
	case p == "/v1/members" && r.Method == http.MethodGet:
		out := []any{}
		for id, x := range m.members {
			if e := r.URL.Query().Get("external_id"); e != "" && x["external_id"] != e {
				continue
			}
			if e := r.URL.Query().Get("email"); e != "" && !strings.EqualFold(fmt.Sprint(x["email"]), e) {
				continue
			}
			if e := r.URL.Query().Get("role"); e != "" && x["role"] != e {
				continue
			}
			out = append(out, m.memberJSON(id))
		}
		reply(w, 200, map[string]any{"data": out})
	case p == "/v1/members" && r.Method == http.MethodPost:
		if !need("users:write") {
			return
		}
		for _, x := range m.members {
			if strings.EqualFold(fmt.Sprint(x["email"]), fmt.Sprint(body["email"])) {
				m.problem(w, 409, "already_member", "")
				return
			}
		}
		id := m.id("")
		role, _ := body["role"].(string)
		if role == "" {
			role = "user"
		}
		x := map[string]any{"email": strings.ToLower(fmt.Sprint(body["email"])), "role": role, "status": "invited",
			"display_name": body["display_name"], "external_id": body["external_id"], "can_automation": body["can_automation"] == true,
			"can_agentic": body["can_agentic"] == true, "created_at": now(), "accepted_at": nil, "deactivated_at": nil, "source": "manual"}
		if role != "read" { // company_member_add_core: every role but read has both rights
			x["can_automation"], x["can_agentic"] = true, true
		}
		m.members[id] = x
		inv := map[string]any{"expires_at": time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)}
		m.invitations[id] = inv
		out := m.memberJSON(id)
		mailed := false
		if iv, ok := body["invitation"].(map[string]any); ok && iv["send_mail"] == true {
			mailed = true
		}
		code := fmt.Sprintf("AWA-%04d-QQQQ-RRRR", m.nextID)
		out["invitation"] = map[string]any{"open": true, "code": code, "join_url": "https://www.awacino.com/join/#" + code,
			"expires_at": inv["expires_at"], "mailed": mailed}
		remember(out, "invitation.code", "invitation.join_url")
		reply(w, 201, out)
	case reMember.MatchString(p):
		id := reMember.FindStringSubmatch(p)[1]
		x, ok := m.members[id]
		if !ok {
			m.problem(w, 404, "not_found", "")
			return
		}
		switch r.Method {
		case http.MethodGet:
			reply(w, 200, m.memberJSON(id))
		case http.MethodPatch:
			for k, v := range body {
				switch k {
				case "active":
					if v == false {
						x["status"] = "deactivated"
						delete(m.invitations, id)
					} else if x["status"] == "deactivated" {
						x["status"] = "active"
					}
				case "role", "can_automation", "can_agentic", "display_name", "external_id":
					x[k] = v
					if x["role"] != "read" { // company_member_update_core
						x["can_automation"], x["can_agentic"] = true, true
					}
				default:
					m.problem(w, 400, "unknown_field", k)
					return
				}
			}
			reply(w, 200, m.memberJSON(id))
		case http.MethodDelete:
			delete(m.members, id)
			delete(m.invitations, id)
			reply(w, 200, map[string]any{"id": id, "status": "removed"})
		}
	case reInvitation.MatchString(p):
		id := reInvitation.FindStringSubmatch(p)[1]
		x, ok := m.members[id]
		if !ok {
			m.problem(w, 404, "not_found", "")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if inv, ok := m.invitations[id]; ok {
				reply(w, 200, map[string]any{"member_id": id, "open": true, "expires_at": inv["expires_at"], "expired": false})
			} else {
				reply(w, 200, map[string]any{"member_id": id, "open": false})
			}
		case http.MethodPost:
			if x["status"] != "invited" {
				m.problem(w, 404, "not_found", "")
				return
			}
			days := 7.0
			if d, ok := body["days"].(float64); ok {
				days = d
			}
			inv := map[string]any{"expires_at": time.Now().Add(time.Duration(days*24) * time.Hour).UTC().Truncate(time.Second).Add(time.Duration(m.nextID) * time.Second).Format(time.RFC3339)}
			m.invitations[id] = inv
			code := "AWA-" + m.id("") + "-SSSS-TTTT"
			out := map[string]any{"member_id": id, "code": code, "join_url": "https://www.awacino.com/join/#" + code,
				"expires_at": inv["expires_at"], "email": x["email"], "role": x["role"], "mailed": body["send_mail"] == true}
			remember(out, "code", "join_url")
			reply(w, 201, out)
		case http.MethodDelete:
			_, had := m.invitations[id]
			delete(m.invitations, id)
			reply(w, 200, map[string]any{"member_id": id, "revoked": had})
		}

	// ---------------------------------------------------------------- devices + enrolments
	case p == "/v1/devices":
		m.deviceLists++
		if m.lateDevice != "" && m.deviceLists >= 2 {
			m.devices["late-1"] = map[string]any{"id": "late-1", "name": m.lateDevice, "role": "beamin", "kind": "unattended", "status": "active", "platform": "linux"}
		}
		out := []any{}
		for _, d := range m.devices {
			q := r.URL.Query()
			if (q.Get("role") != "" && d["role"] != q.Get("role")) || (q.Get("name") != "" && !strings.EqualFold(fmt.Sprint(d["name"]), q.Get("name"))) ||
				(q.Get("external_id") != "" && d["external_id"] != q.Get("external_id")) {
				continue
			}
			out = append(out, d)
		}
		reply(w, 200, map[string]any{"data": out, "next_cursor": nil})
	case reDevice.MatchString(p):
		id := reDevice.FindStringSubmatch(p)[1]
		d, ok := m.devices[id]
		if !ok {
			m.problem(w, 404, "not_found", "")
			return
		}
		switch r.Method {
		case http.MethodGet:
			reply(w, 200, d)
		case http.MethodPatch:
			if n, ok := body["name"].(string); ok {
				d["name"] = n
			}
			reply(w, 200, d)
		case http.MethodDelete:
			delete(m.devices, id)
			reply(w, 204, nil)
		}
	case p == "/v1/enrolments" && r.Method == http.MethodPost:
		if !need("connections:write") {
			return
		}
		id := m.id("enr_")
		code := fmt.Sprintf("0000%04d-1111-4222-8333-444455556666", m.nextID)
		e := map[string]any{"id": id, "label": body["label"], "external_id": body["external_id"], "state": "open", "device_id": nil,
			"expires_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339), "created_at": now(), "code": code,
			"register": m.registerNext}
		m.enrolments[id] = e
		out := clone(e)
		delete(out, "register")
		out["install"] = map[string]any{"docker": "docker run -e AWACINO_SETUP_CODE=" + code, "apt": "curl ... devid=" + code,
			"cloud_init": "#cloud-config\n# " + code}
		remember(out, "code", "install.docker", "install.apt", "install.cloud_init")
		reply(w, 201, out)
	case reEnrolment.MatchString(p):
		id := reEnrolment.FindStringSubmatch(p)[1]
		e, ok := m.enrolments[id]
		if !ok {
			m.problem(w, 404, "not_found", "")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if e["register"] == true && e["state"] == "open" { // the server installed the client and registered
				dev := fmt.Sprint(e["code"])
				m.devices[dev] = map[string]any{"id": dev, "name": e["label"], "role": "beamin", "kind": "unattended", "status": "active",
					"platform": "linux", "member_id": nil, "external_id": e["external_id"], "first_seen": now(), "last_seen": now()}
				e["state"], e["device_id"] = "used", dev
			}
			if dev, _ := e["device_id"].(string); dev != "" {
				if _, still := m.devices[dev]; !still {
					out := clone(e)
					out["device_id"] = nil
					delete(out, "code")
					delete(out, "register")
					reply(w, 200, out)
					return
				}
			}
			out := clone(e)
			delete(out, "code")
			delete(out, "register")
			reply(w, 200, out)
		case http.MethodDelete:
			if e["state"] == "used" {
				m.problem(w, 409, "enrolment_used", "")
				return
			}
			e["state"] = "revoked"
			reply(w, 204, nil)
		}

	// ---------------------------------------------------------------- connection API (openapi.yaml)
	case p == "/v1/connections/grants" && r.Method == http.MethodPost:
		for _, g := range m.grants {
			if g["member_id"] == body["member_id"] && g["device_id"] == body["device_id"] {
				m.problem(w, 409, "conflict", fmt.Sprint(g["id"]))
				return
			}
		}
		if _, ok := m.devices[fmt.Sprint(body["device_id"])]; !ok {
			m.problem(w, 404, "not_found", "device")
			return
		}
		if body["internet"] != nil { // phase A: no internet setting on grants (api_grant_check)
			m.problem(w, 422, "reach_not_enforced_yet", "internet")
			return
		}
		if rk, _ := body["reach"].(map[string]any); rk["kind"] != "network" {
			m.problem(w, 422, "reach_not_enforced_yet", "reach")
			return
		}
		id := m.id("grt_")
		var internet any
		vu := body["valid_until"]
		if s, ok := vu.(string); ok { // the server spells times its own way
			t, _ := time.Parse(time.RFC3339, s)
			vu = t.UTC().Format("2006-01-02T15:04:05+00:00")
		}
		g := map[string]any{"id": id, "member_id": body["member_id"], "device_id": body["device_id"], "reach": body["reach"],
			"internet": internet, "valid_until": vu, "state": "active", "created_at": now()}
		m.grants[id] = g
		remember(g)
		reply(w, 201, g)
	case reGrant.MatchString(p):
		id := reGrant.FindStringSubmatch(p)[1]
		g, ok := m.grants[id]
		if !ok {
			m.problem(w, 404, "not_found", "")
			return
		}
		switch r.Method {
		case http.MethodGet:
			reply(w, 200, g)
		case http.MethodPatch:
			for k, v := range body {
				g[k] = v
			}
			reply(w, 200, g)
		case http.MethodDelete:
			delete(m.grants, id)
			reply(w, 204, nil)
		}
	case p == "/v1/connections/beamin-codes" && r.Method == http.MethodPost:
		id := m.id("bic_")
		code := fmt.Sprintf("7K4M92PDXR01%04d", m.nextID)
		internet := body["internet"]
		if internet == nil {
			internet = "none"
		}
		exp := body["expires_at"]
		if d, ok := body["valid_days"].(float64); ok {
			exp = time.Now().Add(time.Duration(d*24) * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
		}
		label := body["label"]
		if label == nil {
			label = "Beam-in code"
		}
		b := map[string]any{"id": id, "device_id": body["device_id"], "device_name": "web-01", "label": label,
			"external_id": body["external_id"], "reach": body["reach"], "internet": internet, "state": "active", "paused_reason": nil,
			"code_hint": code[12:], "expires_at": exp, "bound_device": nil, "uses": 0, "created_at": now()}
		m.codes[id] = b
		out := clone(b)
		out["code"], out["connect_url"] = code, "https://www.awacino.com/beamin/#"+code
		remember(out, "code", "connect_url")
		reply(w, 201, out)
	case reCode.MatchString(p):
		id := reCode.FindStringSubmatch(p)[1]
		b, ok := m.codes[id]
		if !ok {
			m.problem(w, 404, "not_found", "")
			return
		}
		switch r.Method {
		case http.MethodGet:
			reply(w, 200, b)
		case http.MethodPatch:
			for k, v := range body {
				switch k {
				case "paused":
					if v == true {
						b["state"], b["paused_reason"] = "paused", "admin"
					} else {
						b["state"], b["paused_reason"] = "active", nil
					}
				default:
					b[k] = v
				}
			}
			reply(w, 200, b)
		case http.MethodDelete:
			delete(m.codes, id)
			reply(w, 204, nil)
		}
	case p == "/v1/connections/active":
		reply(w, 200, map[string]any{"data": []any{map[string]any{"id": "lc_1", "device_id": "srv-1", "device_name": "web-01", "via": "member",
			"member_id": 101, "peer_name": "Dieter's ThinkPad", "peer_platform": "windows", "since": "2026-11-17T09:41:57Z"}}, "next_cursor": nil})
	default:
		m.problem(w, 404, "not_found", "")
	}
}

// providerConfig points the provider at the mock.
func (m *mockAPI) providerConfig() string {
	return fmt.Sprintf(`
provider "awacino" {
  endpoint   = %q
  key_id     = "awk_test0000000000000000000000"
  key_secret = "aws_test"
}
`, m.srv.URL)
}
