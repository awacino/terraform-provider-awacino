package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError is an RFC 9457 problem answer (or any other non-2xx answer) of the Awacino API.
type APIError struct {
	Status        int          `json:"status"`
	Code          string       `json:"code"`
	Title         string       `json:"title"`
	Detail        string       `json:"detail,omitempty"`
	Type          string       `json:"type,omitempty"`
	RequestID     string       `json:"request_id,omitempty"`
	RequiredScope string       `json:"required_scope,omitempty"`
	Errors        []FieldError `json:"errors,omitempty"`
	Method        string       `json:"-"`
	Path          string       `json:"-"`
	Header        http.Header  `json:"-"`
}

// FieldError is one entry of a validation_failed answer.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	title := e.Title
	if title == "" {
		title = http.StatusText(e.Status)
	}
	fmt.Fprintf(&b, "%s %s: %s", e.Method, e.Path, title)
	if e.Code != "" {
		fmt.Fprintf(&b, " (%s, HTTP %d)", e.Code, e.Status)
	} else {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	for _, f := range e.Errors {
		fmt.Fprintf(&b, "\n  - %s: %s", f.Field, f.Message)
	}
	if h := e.Hint(); h != "" {
		fmt.Fprintf(&b, "\n\n%s", h)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, "\n\nRequest id: %s (quote it when you contact support@awacino.com)", e.RequestID)
	}
	return b.String()
}

// Hint is a sentence telling the user what to do about a known error code.
func (e *APIError) Hint() string {
	switch e.Code {
	case "invalid_credentials", "invalid_client", "invalid_token":
		return "The key id or secret is wrong. Check key_id / key_secret (or AWACINO_KEY_ID / AWACINO_KEY_SECRET)."
	case "key_expired", "key_revoked", "invalid_key":
		return "This API key has ended or was revoked. Create a new key in the Awacino app (Company settings > Awacino API)."
	case "insufficient_scope":
		if s := SectionHint(e.RequiredScope); s != "" {
			return s
		}
		return "This key lacks the section this call needs - tick it on the Awacino API page (Company settings > Awacino API > Edit access)."
	case "plan":
		return "The company's plan does not include this. The API needs the API Access package (included in Premium); access grants also need Management."
	case "ip_not_allowed":
		return "This key has an IP allow-list that does not contain the address Terraform runs from."
	case "role_not_allowed":
		return "An API key cannot create or change ADMIN / MAIN-ADMIN accounts. Use role read, user or auditor."
	case "seats_full":
		return "All user seats are taken. Add users on awacino.com or offboard a member first."
	case "beamin_full":
		return "All Beam-in devices of the plan are used; the device waits in the Lobby. Add Beam-in devices on awacino.com."
	case "reach_not_enforced_yet":
		return "The server does not enforce this reach / internet option yet. Read access_capabilities of the awacino_company data source and use one of those."
	case "role_sees_all_devices":
		return "Members with role user see all devices, so a grant does not restrict them. Provision the member with role read."
	case "idempotency_key_reused":
		return "The same Idempotency-Key was sent with a different request. Run the apply again."
	case "already_member", "external_id_taken":
		return "Another member already has this e-mail or external_id. Import it with `terraform import` (id or external_id:<x>) instead of creating it."
	case "name_taken":
		return "Another device of the company already has this name. Choose another label / name."
	case "no_certificate":
		return "The company has no certificate yet. Open the Awacino app once with the company account, then try again."
	case "auditor_needs_compliance":
		return "Role auditor needs the Compliance package."
	case "too_many_open_enrolments":
		return "At most 20 setup codes may be open at once. Revoke unused ones (or let them expire) first."
	case "rate_limited", "too_many_failed_attempts":
		return "Too many requests for this key. The provider retried with back-off; try again in a minute."
	}
	return ""
}

// SectionHint names the Awacino API page checkbox that grants scope ("connections:write" ...).
func SectionHint(scope string) string {
	sec, mode, ok := strings.Cut(scope, ":")
	if !ok {
		return ""
	}
	name := map[string]string{"users": "Users", "connections": "Connections", "audit": "Audit"}[sec]
	if name == "" {
		return ""
	}
	m := "Read"
	if mode == "write" {
		m = "Write"
	}
	return fmt.Sprintf("This key has no %s section with %s access - tick it on the Awacino API page "+
		"(Company settings > Awacino API > Edit access). Needed scope: %s.", name, m, scope)
}

func parseError(status int, method, path string, h http.Header, body []byte) *APIError {
	e := &APIError{Status: status, Method: method, Path: path, Header: h}
	var raw map[string]any
	if json.Unmarshal(body, &raw) == nil {
		_ = json.Unmarshal(body, e)
		e.Status = status
		// OAuth errors: {"error": "...", "error_description": "..."}
		if e.Code == "" {
			if s, ok := raw["error"].(string); ok {
				e.Code = s
				if d, ok := raw["error_description"].(string); ok && e.Detail == "" {
					e.Detail = d
				}
			}
		}
	} else if t := strings.TrimSpace(string(body)); t != "" {
		if len(t) > 300 {
			t = t[:300] + "..."
		}
		e.Detail = t
	}
	if e.RequestID == "" && h != nil {
		e.RequestID = h.Get("X-Request-Id")
	}
	return e
}

// AsAPIError unwraps an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsNotFound is true for a 404 answer.
func IsNotFound(err error) bool {
	e, ok := AsAPIError(err)
	return ok && e.Status == http.StatusNotFound
}

// HasCode is true when err is an API error with this code.
func HasCode(err error, code string) bool {
	e, ok := AsAPIError(err)
	return ok && e.Code == code
}
