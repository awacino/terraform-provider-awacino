package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// ID is an object id. The API sends some ids as JSON numbers (members) and others as strings;
// ID accepts both and is always a string here.
type ID string

func (i *ID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*i = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*i = ID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*i = ID(n.String())
	return nil
}

func (i ID) String() string { return string(i) }

// Redacted is true for a secret the API did not repeat (an idempotent replay).
func Redacted(s string) bool { return s == "" || s == "code_redacted" }

// ------------------------------------------------------------------ key + company

// KeyInfo is GET /v1/key.
type KeyInfo struct {
	KeyID       string            `json:"key_id"`
	Description string            `json:"description"`
	Scopes      []string          `json:"scopes"`
	Sections    map[string]string `json:"sections,omitempty"`
	OwnerKind   string            `json:"owner_kind,omitempty"`
	CompanyID   string            `json:"company_id"`
	CreatedAt   *string           `json:"created_at,omitempty"`
	ExpiresAt   *string           `json:"expires_at,omitempty"`
	IPAllow     []string          `json:"ip_allow,omitempty"`
}

// HasScope: write includes read.
func (k *KeyInfo) HasScope(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
		if sec, mode, ok := strings.Cut(scope, ":"); ok && mode == "read" && s == sec+":write" {
			return true
		}
	}
	return false
}

func (c *Client) GetKey(ctx context.Context) (*KeyInfo, error) {
	var k KeyInfo
	if err := c.get(ctx, "/v1/key", nil, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// RequireScope fails early with the checkbox to tick when the calling key lacks scope. When the
// key cannot be read the check is skipped (the call itself then answers).
func (c *Client) RequireScope(ctx context.Context, scope string) error {
	c.keyMu.Lock()
	k := c.keyInfo
	c.keyMu.Unlock()
	if k == nil {
		kk, err := c.GetKey(ctx)
		if err != nil {
			if e, ok := AsAPIError(err); ok && e.Status == http.StatusUnauthorized {
				return err
			}
			return nil
		}
		c.keyMu.Lock()
		c.keyInfo = kk
		c.keyMu.Unlock()
		k = kk
	}
	if len(k.Scopes) == 0 || k.HasScope(scope) {
		return nil
	}
	return &APIError{Status: http.StatusForbidden, Code: "insufficient_scope", Title: "The API key lacks a section",
		RequiredScope: scope, Method: "GET", Path: "/v1/key"}
}

// Counter is {used, total}.
type Counter struct {
	Used  *int64 `json:"used"`
	Total *int64 `json:"total"`
}

// Company is GET /v1/company.
type Company struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Plan               string          `json:"plan"`
	Packages           []string        `json:"packages"`
	Seats              *Counter        `json:"seats"`
	BeaminDevices      *Counter        `json:"beamin_devices"`
	AuditRetentionDays *int64          `json:"audit_retention_days"`
	AccessCapabilities json.RawMessage `json:"access_capabilities"`
}

// Capabilities returns the reach / internet values the server enforces and whether access
// grants are available. It reads both forms of access_capabilities: a list of values
// (openapi.yaml) and {"reach": [...], "grants": bool} (the live server).
func (co *Company) Capabilities() (values []string, grants bool) {
	grants = false
	for _, p := range co.Packages {
		if p == "management" {
			grants = true
		}
	}
	raw := bytes.TrimSpace(co.AccessCapabilities)
	if len(raw) == 0 {
		return nil, grants
	}
	var list []string
	if raw[0] == '[' && json.Unmarshal(raw, &list) == nil {
		return list, grants
	}
	var obj struct {
		Reach    []string `json:"reach"`
		Internet []string `json:"internet"`
		Grants   *bool    `json:"grants"`
		Extra    []string `json:"other"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		values = append(values, obj.Reach...)
		if len(obj.Internet) > 0 {
			values = append(values, "internet")
		}
		values = append(values, obj.Extra...)
		if obj.Grants != nil {
			grants = *obj.Grants
		}
	}
	return values, grants
}

func (c *Client) GetCompany(ctx context.Context) (*Company, error) {
	var co Company
	if err := c.get(ctx, "/v1/company", nil, &co); err != nil {
		return nil, err
	}
	return &co, nil
}

// ------------------------------------------------------------------ members

// Member is a company member account.
type Member struct {
	ID            ID          `json:"id"`
	Email         string      `json:"email"`
	DisplayName   *string     `json:"display_name"`
	ExternalID    *string     `json:"external_id"`
	Role          string      `json:"role"`
	Status        string      `json:"status"`
	CanAutomation *bool       `json:"can_automation"`
	CanAgentic    *bool       `json:"can_agentic"`
	Source        *string     `json:"source"`
	CreatedAt     *string     `json:"created_at"`
	AcceptedAt    *string     `json:"accepted_at"`
	DeactivatedAt *string     `json:"deactivated_at"`
	Invitation    *Invitation `json:"invitation"`
}

// Active: an invited or active member holds a seat; deactivated does not.
func (m *Member) Active() bool { return m.Status == "active" || m.Status == "invited" }

// Invitation is an invitation code answer (code + join_url only in the first answer) or an
// invitation status.
type Invitation struct {
	MemberID     ID      `json:"member_id"`
	Code         string  `json:"code"`
	JoinURL      string  `json:"join_url"`
	ExpiresAt    *string `json:"expires_at"`
	CreatedAt    *string `json:"created_at"`
	UsedAt       *string `json:"used_at"`
	Mailed       *bool   `json:"mailed"`
	Open         *bool   `json:"open"`
	Expired      *bool   `json:"expired"`
	State        string  `json:"state"`
	CodeRedacted bool    `json:"code_redacted"`
}

// IsOpen reads both forms ({"open": true} | "state": "open"); an invitation object without
// either (a create answer as openapi.yaml shows it) is open.
func (i *Invitation) IsOpen() bool {
	if i.Open != nil {
		return *i.Open
	}
	return i.State == "" || i.State == "open" || i.State == "expired"
}

// IsExpired reads both forms.
func (i *Invitation) IsExpired() bool {
	if i.Expired != nil {
		return *i.Expired
	}
	return i.State == "expired"
}

// InvitationRequest is the invitation part of a create / issue call.
type InvitationRequest struct {
	Days     *int64  `json:"days,omitempty"`
	SendMail *bool   `json:"send_mail,omitempty"`
	Locale   *string `json:"locale,omitempty"`
}

// MemberCreate is POST /v1/members.
type MemberCreate struct {
	Email         string             `json:"email"`
	DisplayName   *string            `json:"display_name,omitempty"`
	ExternalID    *string            `json:"external_id,omitempty"`
	Role          string             `json:"role,omitempty"`
	CanAutomation *bool              `json:"can_automation,omitempty"`
	CanAgentic    *bool              `json:"can_agentic,omitempty"`
	Invitation    *InvitationRequest `json:"invitation,omitempty"`
}

// MemberFilter filters GET /v1/members.
type MemberFilter struct {
	Status, Role, Email, ExternalID string
}

func (c *Client) ListMembers(ctx context.Context, f MemberFilter) ([]Member, error) {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("status", f.Status)
	set("role", f.Role)
	set("email", f.Email)
	set("external_id", f.ExternalID)
	return listAll[Member](ctx, c, "/v1/members", q)
}

func (c *Client) GetMember(ctx context.Context, id string) (*Member, error) {
	var m Member
	if err := c.get(ctx, "/v1/members/"+seg(id), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) CreateMember(ctx context.Context, in MemberCreate, idem string) (*Member, error) {
	var m Member
	_, _, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/members", Body: in, Idempotency: idem}, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateMember sends a merge patch (role, can_automation, can_agentic, display_name,
// external_id, active); a nil value clears the field.
func (c *Client) UpdateMember(ctx context.Context, id string, patch map[string]any) (*Member, error) {
	var m Member
	_, _, err := c.Do(ctx, Request{Method: http.MethodPatch, Path: "/v1/members/" + seg(id), Body: patch}, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteMember offboards a member. A member that is already gone is not an error.
func (c *Client) DeleteMember(ctx context.Context, id string) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: "/v1/members/" + seg(id)}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) GetInvitation(ctx context.Context, memberID string) (*Invitation, error) {
	var i Invitation
	if err := c.get(ctx, "/v1/members/"+seg(memberID)+"/invitation", nil, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

func (c *Client) IssueInvitation(ctx context.Context, memberID string, in InvitationRequest, idem string) (*Invitation, error) {
	var i Invitation
	_, _, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/members/" + seg(memberID) + "/invitation", Body: in, Idempotency: idem}, &i)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

func (c *Client) RevokeInvitation(ctx context.Context, memberID string) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: "/v1/members/" + seg(memberID) + "/invitation"}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ------------------------------------------------------------------ devices + enrolments

// Device is a Beam-in or Beam-out device of the company.
type Device struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Role       string  `json:"role"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	Platform   string  `json:"platform"`
	MemberID   *ID     `json:"member_id"`
	ExternalID *string `json:"external_id"`
	FirstSeen  *string `json:"first_seen"`
	LastSeen   *string `json:"last_seen"`
}

// DeviceFilter filters GET /v1/devices.
type DeviceFilter struct {
	Role, Kind, Status, MemberID, ExternalID, Name string
}

func (c *Client) ListDevices(ctx context.Context, f DeviceFilter) ([]Device, error) {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("role", f.Role)
	set("kind", f.Kind)
	set("status", f.Status)
	set("member_id", f.MemberID)
	set("external_id", f.ExternalID)
	set("name", f.Name)
	return listAll[Device](ctx, c, "/v1/devices", q)
}

func (c *Client) GetDevice(ctx context.Context, id string) (*Device, error) {
	var d Device
	if err := c.get(ctx, "/v1/devices/"+seg(id), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateDevice renames a device or changes its role ({"name": ..., "role": ...}).
func (c *Client) UpdateDevice(ctx context.Context, id string, patch map[string]any) (*Device, error) {
	var d Device
	_, _, err := c.Do(ctx, Request{Method: http.MethodPatch, Path: "/v1/devices/" + seg(id), Body: patch}, &d)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *Client) DeleteDevice(ctx context.Context, id string) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: "/v1/devices/" + seg(id)}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// InstallLines are the ready install lines of an enrolment (answered once).
type InstallLines struct {
	Docker    string `json:"docker"`
	Apt       string `json:"apt"`
	CloudInit string `json:"cloud_init"`
}

// Enrolment is a one-time setup code for an unattended server.
type Enrolment struct {
	ID         string        `json:"id"`
	Label      string        `json:"label"`
	ExternalID *string       `json:"external_id"`
	ExpiresAt  *string       `json:"expires_at"`
	CreatedAt  *string       `json:"created_at"`
	State      string        `json:"state"`
	DeviceID   *string       `json:"device_id"`
	Code       string        `json:"code"`
	Install    *InstallLines `json:"install"`
}

// EnrolmentCreate is POST /v1/enrolments.
type EnrolmentCreate struct {
	Label      string  `json:"label"`
	ExternalID *string `json:"external_id,omitempty"`
	Hours      *int64  `json:"hours,omitempty"`
}

func (c *Client) CreateEnrolment(ctx context.Context, in EnrolmentCreate, idem string) (*Enrolment, error) {
	var e Enrolment
	_, _, err := c.Do(ctx, Request{Method: http.MethodPost, Path: "/v1/enrolments", Body: in, Idempotency: idem}, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (c *Client) GetEnrolment(ctx context.Context, id string) (*Enrolment, error) {
	var e Enrolment
	if err := c.get(ctx, "/v1/enrolments/"+seg(id), nil, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// DeleteEnrolment revokes an unused setup code (404 = already gone).
func (c *Client) DeleteEnrolment(ctx context.Context, id string) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: "/v1/enrolments/" + seg(id)}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ------------------------------------------------------------------ connections

// Reach is what a grant or a Beam-in code reaches in the device's network.
type Reach struct {
	Kind  string  `json:"kind"`
	Ports []int64 `json:"ports,omitempty"`
}

// CreatedBy names who created an object.
type CreatedBy struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Grant is a member's access to one Beam-in device (/v1/connections/grants).
type Grant struct {
	ID         string     `json:"id"`
	MemberID   ID         `json:"member_id"`
	DeviceID   string     `json:"device_id"`
	Reach      *Reach     `json:"reach"`
	Internet   string     `json:"internet"`
	ValidUntil *string    `json:"valid_until"`
	State      string     `json:"state"`
	CreatedAt  *string    `json:"created_at"`
	CreatedBy  *CreatedBy `json:"created_by"`
}

// GrantCreate is POST /v1/connections/grants.
type GrantCreate struct {
	MemberID   string  `json:"member_id"`
	DeviceID   string  `json:"device_id"`
	Reach      Reach   `json:"reach"`
	Internet   string  `json:"internet,omitempty"`
	ValidUntil *string `json:"valid_until,omitempty"`
}

const grantsPath = "/v1/connections/grants"

func (c *Client) CreateGrant(ctx context.Context, in GrantCreate, idem string) (*Grant, error) {
	var g Grant
	_, _, err := c.Do(ctx, Request{Method: http.MethodPost, Path: grantsPath, Body: in, Idempotency: idem}, &g)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (c *Client) GetGrant(ctx context.Context, id string) (*Grant, error) {
	var g Grant
	if err := c.get(ctx, grantsPath+"/"+seg(id), nil, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func (c *Client) ListGrants(ctx context.Context, memberID, deviceID string) ([]Grant, error) {
	q := url.Values{}
	if memberID != "" {
		q.Set("member_id", memberID)
	}
	if deviceID != "" {
		q.Set("device_id", deviceID)
	}
	return listAll[Grant](ctx, c, grantsPath, q)
}

// UpdateGrant patches reach, internet and valid_until (nil clears valid_until).
func (c *Client) UpdateGrant(ctx context.Context, id string, patch map[string]any) (*Grant, error) {
	var g Grant
	_, _, err := c.Do(ctx, Request{Method: http.MethodPatch, Path: grantsPath + "/" + seg(id), Body: patch}, &g)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (c *Client) DeleteGrant(ctx context.Context, id string) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: grantsPath + "/" + seg(id)}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// BoundDevice is the device that redeemed a Beam-in code first.
type BoundDevice struct {
	Label    string  `json:"label"`
	Platform string  `json:"platform"`
	BoundAt  *string `json:"bound_at"`
}

// BeaminCode is a Beam-in code (/v1/connections/beamin-codes). Code and ConnectURL are only in
// the create answer.
type BeaminCode struct {
	ID           string       `json:"id"`
	DeviceID     string       `json:"device_id"`
	DeviceName   string       `json:"device_name"`
	Label        string       `json:"label"`
	ExternalID   *string      `json:"external_id"`
	Reach        *Reach       `json:"reach"`
	Internet     string       `json:"internet"`
	State        string       `json:"state"`
	PausedReason *string      `json:"paused_reason"`
	CodeHint     string       `json:"code_hint"`
	ExpiresAt    *string      `json:"expires_at"`
	BoundDevice  *BoundDevice `json:"bound_device"`
	Uses         *int64       `json:"uses"`
	LastUsedAt   *string      `json:"last_used_at"`
	CreatedAt    *string      `json:"created_at"`
	RevokedAt    *string      `json:"revoked_at"`
	Code         string       `json:"code"`
	ConnectURL   string       `json:"connect_url"`
	CodeRedacted bool         `json:"code_redacted"`
}

// BeaminCodeCreate is POST /v1/connections/beamin-codes.
type BeaminCodeCreate struct {
	DeviceID   string  `json:"device_id"`
	Reach      Reach   `json:"reach"`
	Internet   string  `json:"internet,omitempty"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
	ValidDays  *int64  `json:"valid_days,omitempty"`
	Label      *string `json:"label,omitempty"`
	ExternalID *string `json:"external_id,omitempty"`
}

const codesPath = "/v1/connections/beamin-codes"

func (c *Client) CreateBeaminCode(ctx context.Context, in BeaminCodeCreate, idem string) (*BeaminCode, error) {
	var b BeaminCode
	_, _, err := c.Do(ctx, Request{Method: http.MethodPost, Path: codesPath, Body: in, Idempotency: idem}, &b)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) GetBeaminCode(ctx context.Context, id string) (*BeaminCode, error) {
	var b BeaminCode
	if err := c.get(ctx, codesPath+"/"+seg(id), nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) ListBeaminCodes(ctx context.Context, deviceID, state string) ([]BeaminCode, error) {
	q := url.Values{}
	if deviceID != "" {
		q.Set("device_id", deviceID)
	}
	if state != "" {
		q.Set("state", state)
	}
	return listAll[BeaminCode](ctx, c, codesPath, q)
}

// UpdateBeaminCode patches label, paused, expires_at (earlier only) and external_id.
func (c *Client) UpdateBeaminCode(ctx context.Context, id string, patch map[string]any) (*BeaminCode, error) {
	var b BeaminCode
	_, _, err := c.Do(ctx, Request{Method: http.MethodPatch, Path: codesPath + "/" + seg(id), Body: patch}, &b)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) DeleteBeaminCode(ctx context.Context, id string) error {
	_, _, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: codesPath + "/" + seg(id)}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ActiveConnection is one live connection to a Beam-in device.
type ActiveConnection struct {
	ID           string  `json:"id"`
	DeviceID     string  `json:"device_id"`
	DeviceName   string  `json:"device_name"`
	Via          string  `json:"via"`
	MemberID     *ID     `json:"member_id"`
	CodeID       *string `json:"code_id"`
	CodeLabel    *string `json:"code_label"`
	PeerName     string  `json:"peer_name"`
	PeerPlatform string  `json:"peer_platform"`
	Since        *string `json:"since"`
}

func (c *Client) ListActiveConnections(ctx context.Context, deviceID, memberID, via string) ([]ActiveConnection, error) {
	q := url.Values{}
	if deviceID != "" {
		q.Set("device_id", deviceID)
	}
	if memberID != "" {
		q.Set("member_id", memberID)
	}
	if via != "" {
		q.Set("via", via)
	}
	return listAll[ActiveConnection](ctx, c, "/v1/connections/active", q)
}
