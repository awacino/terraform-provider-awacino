package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// base is the shared Configure of every data source.
type base struct{ c *client.Client }

func (b *base) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	b.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// ================================================================== awacino_company

func NewCompanyDataSource() datasource.DataSource { return &companyDS{} }

type companyDS struct{ base }

type companyModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Plan               types.String `tfsdk:"plan"`
	Packages           types.List   `tfsdk:"packages"`
	SeatsUsed          types.Int64  `tfsdk:"seats_used"`
	SeatsTotal         types.Int64  `tfsdk:"seats_total"`
	BeaminDevicesUsed  types.Int64  `tfsdk:"beamin_devices_used"`
	BeaminDevicesTotal types.Int64  `tfsdk:"beamin_devices_total"`
	AuditRetentionDays types.Int64  `tfsdk:"audit_retention_days"`
	AccessCapabilities types.List   `tfsdk:"access_capabilities"`
	GrantsAvailable    types.Bool   `tfsdk:"grants_available"`
}

func (d *companyDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_company"
}

func (d *companyDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The company the API key belongs to: plan, packages, seats, Beam-in devices and the access rules the " +
			"server enforces today (`access_capabilities`). Any key with the Users or Connections section.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Computed: true, MarkdownDescription: "Company id."},
			"name":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Company name."},
			"plan":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Plan (`basic`, `standard`, `premium`)."},
			"packages":             schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Packages (`management`, `security`, `identity`, `compliance`, `branding`, `api_access`)."},
			"seats_used":           schema.Int64Attribute{Computed: true, MarkdownDescription: "User seats taken (active members + open invitations)."},
			"seats_total":          schema.Int64Attribute{Computed: true, MarkdownDescription: "User seats of the plan."},
			"beamin_devices_used":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Beam-in devices used (null when the API does not report it)."},
			"beamin_devices_total": schema.Int64Attribute{Computed: true, MarkdownDescription: "Beam-in devices of the plan (null when the API does not report it)."},
			"audit_retention_days": schema.Int64Attribute{Computed: true, MarkdownDescription: "Audit log retention in days."},
			"access_capabilities": schema.ListAttribute{Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Reach / internet values the server enforces and therefore accepts in grants and codes (for example `network`, `device`)."},
			"grants_available": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether access grants are available (Management package)."},
		},
	}
}

func (d *companyDS) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	co, err := d.c.GetCompany(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the company", err)
		return
	}
	caps, grants := co.Capabilities()
	m := companyModel{
		ID: types.StringValue(co.ID), Name: types.StringValue(co.Name), Plan: types.StringValue(co.Plan),
		Packages:  stringList(ctx, co.Packages, &resp.Diagnostics),
		SeatsUsed: types.Int64Null(), SeatsTotal: types.Int64Null(),
		BeaminDevicesUsed: types.Int64Null(), BeaminDevicesTotal: types.Int64Null(),
		AuditRetentionDays: int64OrNull(co.AuditRetentionDays),
		AccessCapabilities: stringList(ctx, caps, &resp.Diagnostics),
		GrantsAvailable:    types.BoolValue(grants),
	}
	if co.Seats != nil {
		m.SeatsUsed, m.SeatsTotal = int64OrNull(co.Seats.Used), int64OrNull(co.Seats.Total)
	}
	if co.BeaminDevices != nil {
		m.BeaminDevicesUsed, m.BeaminDevicesTotal = int64OrNull(co.BeaminDevices.Used), int64OrNull(co.BeaminDevices.Total)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ================================================================== awacino_api_key

func NewAPIKeyDataSource() datasource.DataSource { return &apiKeyDS{} }

type apiKeyDS struct{ base }

type apiKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Description types.String `tfsdk:"description"`
	Scopes      types.List   `tfsdk:"scopes"`
	Sections    types.Map    `tfsdk:"sections"`
	CompanyID   types.String `tfsdk:"company_id"`
	OwnerKind   types.String `tfsdk:"owner_kind"`
	CreatedAt   types.String `tfsdk:"created_at"`
	ExpiresAt   types.String `tfsdk:"expires_at"`
}

func (d *apiKeyDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (d *apiKeyDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The calling API key: its sections and when it ends, so a pipeline can warn before its key expires. " +
			"Keys themselves are created by a human admin in the Awacino app, never by Terraform.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Key id (`awk_...`)."},
			"description": schema.StringAttribute{Computed: true, MarkdownDescription: "The key's description."},
			"scopes":      schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Scopes (`users:read`, `users:write`, `connections:read`, `connections:write`, `audit:read`)."},
			"sections": schema.MapAttribute{Computed: true, ElementType: types.StringType,
				MarkdownDescription: "Per section (`users`, `connections`, `audit`): `none`, `read` or `write`."},
			"company_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The company the key belongs to."},
			"owner_kind": schema.StringAttribute{Computed: true, MarkdownDescription: "`company` or `partner`."},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the key was created (when the API reports it)."},
			"expires_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When the key ends (when the API reports it)."},
		},
	}
}

func (d *apiKeyDS) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	k, err := d.c.GetKey(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the API key", err)
		return
	}
	sections := k.Sections
	if len(sections) == 0 {
		sections = map[string]string{"users": "none", "connections": "none", "audit": "none"}
		for _, sec := range []string{"users", "connections", "audit"} {
			if k.HasScope(sec + ":write") {
				sections[sec] = "write"
			} else if k.HasScope(sec + ":read") {
				sections[sec] = "read"
			}
		}
	}
	sm, dd := types.MapValueFrom(ctx, types.StringType, sections)
	resp.Diagnostics.Append(dd...)
	m := apiKeyModel{
		ID: types.StringValue(k.KeyID), Description: types.StringValue(k.Description),
		Scopes: stringList(ctx, k.Scopes, &resp.Diagnostics), Sections: sm,
		CompanyID: types.StringValue(k.CompanyID), OwnerKind: strOrNullEmpty(k.OwnerKind),
		CreatedAt: strOrNull(k.CreatedAt), ExpiresAt: strOrNull(k.ExpiresAt),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ================================================================== members

type memberItem struct {
	ID            types.String `tfsdk:"id"`
	Email         types.String `tfsdk:"email"`
	DisplayName   types.String `tfsdk:"display_name"`
	ExternalID    types.String `tfsdk:"external_id"`
	Role          types.String `tfsdk:"role"`
	Status        types.String `tfsdk:"status"`
	CanAutomation types.Bool   `tfsdk:"can_automation"`
	CanAgentic    types.Bool   `tfsdk:"can_agentic"`
	CreatedAt     types.String `tfsdk:"created_at"`
	AcceptedAt    types.String `tfsdk:"accepted_at"`
}

func memberItemOf(m client.Member) memberItem {
	return memberItem{
		ID: types.StringValue(m.ID.String()), Email: types.StringValue(m.Email), DisplayName: strOrNull(m.DisplayName),
		ExternalID: strOrNull(m.ExternalID), Role: types.StringValue(m.Role), Status: types.StringValue(m.Status),
		CanAutomation: boolOr(m.CanAutomation, false), CanAgentic: boolOr(m.CanAgentic, false),
		CreatedAt: strOrNull(m.CreatedAt), AcceptedAt: strOrNull(m.AcceptedAt),
	}
}

func memberItemAttrs(lookup bool) map[string]schema.Attribute {
	a := map[string]schema.Attribute{
		"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Member id."},
		"email":          schema.StringAttribute{Computed: true, MarkdownDescription: "Invited address."},
		"display_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
		"external_id":    schema.StringAttribute{Computed: true, MarkdownDescription: "Your own id."},
		"role":           schema.StringAttribute{Computed: true, MarkdownDescription: "`main_admin`, `admin`, `auditor`, `user` or `read`."},
		"status":         schema.StringAttribute{Computed: true, MarkdownDescription: "`invited`, `active` or `deactivated`."},
		"can_automation": schema.BoolAttribute{Computed: true, MarkdownDescription: "May use automation."},
		"can_agentic":    schema.BoolAttribute{Computed: true, MarkdownDescription: "May use agentic AI."},
		"created_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "Created."},
		"accepted_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "Joined."},
	}
	if lookup {
		one := []validator.String{stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("email"), path.MatchRoot("external_id"))}
		a["id"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Member id. Give exactly one of `id`, `email`, `external_id`.", Validators: one}
		a["email"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Invited address (case-insensitive)."}
		a["external_id"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Your own id."}
	}
	return a
}

func NewMembersDataSource() datasource.DataSource { return &membersDS{} }

type membersDS struct{ base }

type membersModel struct {
	Role       types.String `tfsdk:"role"`
	Status     types.String `tfsdk:"status"`
	Email      types.String `tfsdk:"email"`
	ExternalID types.String `tfsdk:"external_id"`
	Members    []memberItem `tfsdk:"members"`
}

func (d *membersDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_members"
}

func (d *membersDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The company's members (not removed ones), optionally filtered. Key section **Users** (Read).",
		Attributes: map[string]schema.Attribute{
			"role":        schema.StringAttribute{Optional: true, MarkdownDescription: "Only this role."},
			"status":      schema.StringAttribute{Optional: true, MarkdownDescription: "Only this status (`invited`, `active`, `deactivated`)."},
			"email":       schema.StringAttribute{Optional: true, MarkdownDescription: "Only this invited address."},
			"external_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only this external_id."},
			"members": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The members, oldest first.",
				NestedObject: schema.NestedAttributeObject{Attributes: memberItemAttrs(false)},
			},
		},
	}
}

func (d *membersDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m membersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.c.ListMembers(ctx, client.MemberFilter{Role: m.Role.ValueString(), Status: m.Status.ValueString(),
		Email: m.Email.ValueString(), ExternalID: m.ExternalID.ValueString()})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot list the members", err)
		return
	}
	m.Members = make([]memberItem, 0, len(list))
	for _, x := range list {
		m.Members = append(m.Members, memberItemOf(x))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func NewMemberDataSource() datasource.DataSource { return &memberDS{} }

type memberDS struct{ base }

func (d *memberDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (d *memberDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One member, found by `id`, `email` or `external_id`. Key section **Users** (Read).",
		Attributes:          memberItemAttrs(true),
	}
}

func (d *memberDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m memberItem
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var found *client.Member
	if !m.ID.IsNull() {
		x, err := d.c.GetMember(ctx, m.ID.ValueString())
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot read the member", err)
			return
		}
		found = x
	} else {
		list, err := d.c.ListMembers(ctx, client.MemberFilter{Email: m.Email.ValueString(), ExternalID: m.ExternalID.ValueString()})
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot look up the member", err)
			return
		}
		if len(list) != 1 {
			resp.Diagnostics.AddError("Member not found", fmt.Sprintf("%d members match; give an id, email or external_id that matches exactly one.", len(list)))
			return
		}
		found = &list[0]
	}
	out := memberItemOf(*found)
	if !m.Email.IsNull() {
		out.Email = keepFold(m.Email, found.Email)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

// ================================================================== devices

type deviceItem struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Role       types.String `tfsdk:"role"`
	Kind       types.String `tfsdk:"kind"`
	Status     types.String `tfsdk:"status"`
	Platform   types.String `tfsdk:"platform"`
	MemberID   types.String `tfsdk:"member_id"`
	ExternalID types.String `tfsdk:"external_id"`
	FirstSeen  types.String `tfsdk:"first_seen"`
	LastSeen   types.String `tfsdk:"last_seen"`
}

func deviceItemOf(d client.Device) deviceItem {
	return deviceItem{
		ID: types.StringValue(d.ID), Name: types.StringValue(d.Name), Role: types.StringValue(d.Role),
		Kind: types.StringValue(d.Kind), Status: types.StringValue(d.Status), Platform: strOrNullEmpty(d.Platform),
		MemberID: idOrNull(d.MemberID), ExternalID: strOrNull(d.ExternalID), FirstSeen: strOrNull(d.FirstSeen), LastSeen: strOrNull(d.LastSeen),
	}
}

func deviceItemAttrs(lookup bool) map[string]schema.Attribute {
	a := map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Device id."},
		"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Device name."},
		"role":        schema.StringAttribute{Computed: true, MarkdownDescription: "`beamin` (can be reached) or `beamout` (connects out)."},
		"kind":        schema.StringAttribute{Computed: true, MarkdownDescription: "`app` or `unattended`."},
		"status":      schema.StringAttribute{Computed: true, MarkdownDescription: "`active` or `lobby`."},
		"platform":    schema.StringAttribute{Computed: true, MarkdownDescription: "`macos`, `windows`, `linux`, `ios`, `android`, `docker`, ..."},
		"member_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "The member who registered it."},
		"external_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The external_id given at enrolment."},
		"first_seen":  schema.StringAttribute{Computed: true, MarkdownDescription: "First seen."},
		"last_seen":   schema.StringAttribute{Computed: true, MarkdownDescription: "Last seen (Beam-in devices)."},
	}
	if lookup {
		one := []validator.String{stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"), path.MatchRoot("external_id"))}
		a["id"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Device id. Give exactly one of `id`, `name`, `external_id`.", Validators: one}
		a["name"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Exact device name (case-insensitive)."}
		a["external_id"] = schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "The external_id given at enrolment."}
	}
	return a
}

func NewDevicesDataSource() datasource.DataSource { return &devicesDS{} }

type devicesDS struct{ base }

type devicesModel struct {
	Role       types.String `tfsdk:"role"`
	Kind       types.String `tfsdk:"kind"`
	Status     types.String `tfsdk:"status"`
	MemberID   types.String `tfsdk:"member_id"`
	ExternalID types.String `tfsdk:"external_id"`
	Name       types.String `tfsdk:"name"`
	Devices    []deviceItem `tfsdk:"devices"`
}

func (d *devicesDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_devices"
}

func (d *devicesDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The company's Beam-in and Beam-out devices, optionally filtered. Key section **Connections** (Read).",
		Attributes: map[string]schema.Attribute{
			"role": schema.StringAttribute{Optional: true, MarkdownDescription: "`beamin` or `beamout`.",
				Validators: []validator.String{stringvalidator.OneOf("beamin", "beamout")}},
			"kind": schema.StringAttribute{Optional: true, MarkdownDescription: "`app` or `unattended`.",
				Validators: []validator.String{stringvalidator.OneOf("app", "unattended")}},
			"status": schema.StringAttribute{Optional: true, MarkdownDescription: "`active` or `lobby`.",
				Validators: []validator.String{stringvalidator.OneOf("active", "lobby")}},
			"member_id":   schema.StringAttribute{Optional: true, MarkdownDescription: "Devices registered by this member."},
			"external_id": schema.StringAttribute{Optional: true, MarkdownDescription: "The external_id given at enrolment."},
			"name":        schema.StringAttribute{Optional: true, MarkdownDescription: "Exact device name (case-insensitive)."},
			"devices": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The devices.",
				NestedObject: schema.NestedAttributeObject{Attributes: deviceItemAttrs(false)},
			},
		},
	}
}

func (d *devicesDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m devicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.c.ListDevices(ctx, client.DeviceFilter{Role: m.Role.ValueString(), Kind: m.Kind.ValueString(),
		Status: m.Status.ValueString(), MemberID: m.MemberID.ValueString(), ExternalID: m.ExternalID.ValueString(), Name: m.Name.ValueString()})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot list the devices", err)
		return
	}
	m.Devices = make([]deviceItem, 0, len(list))
	for _, x := range list {
		m.Devices = append(m.Devices, deviceItemOf(x))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func NewDeviceDataSource() datasource.DataSource { return &deviceDS{} }

type deviceDS struct {
	base
	pollMin, pollMax time.Duration
}

type deviceLookup struct {
	deviceItem
	WaitTimeout types.String `tfsdk:"wait_timeout"`
}

func (d *deviceDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device"
}

func (d *deviceDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One device, found by `id`, `name` or `external_id`. Key section **Connections** (Read).\n\n" +
			"With `wait_timeout` the data source waits until the device exists - use it with `depends_on` on the server that " +
			"runs an `awacino_beamin_device` setup code in the same configuration, to get the registered device's id in one apply.",
		Attributes: withWait(deviceItemAttrs(true)),
	}
}

func withWait(a map[string]schema.Attribute) map[string]schema.Attribute {
	a["wait_timeout"] = schema.StringAttribute{
		Optional: true,
		MarkdownDescription: "Wait up to this long (Go duration, for example `15m`) for the device to appear, polling with back-off " +
			"2 to 30 s. Without it a missing device is an error at once.",
		Validators: []validator.String{durationValidator{}},
	}
	return a
}

func (d *deviceDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m deviceLookup
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var timeout time.Duration
	if !m.WaitTimeout.IsNull() {
		timeout, _ = time.ParseDuration(m.WaitTimeout.ValueString())
	}
	if d.pollMin == 0 {
		d.pollMin, d.pollMax = 2*time.Second, 30*time.Second
	}
	deadline := time.Now().Add(timeout)
	delay := d.pollMin
	for {
		found, n, err := d.lookup(ctx, m.deviceItem)
		if err != nil && !client.IsNotFound(err) {
			addAPIError(&resp.Diagnostics, "Cannot look up the device", err)
			return
		}
		if found != nil {
			out := deviceLookup{deviceItem: deviceItemOf(*found), WaitTimeout: m.WaitTimeout}
			if !m.Name.IsNull() {
				out.Name = keepFold(m.Name, found.Name)
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
			return
		}
		if n > 1 {
			resp.Diagnostics.AddError("Device not unique", fmt.Sprintf("%d devices match; give an id, name or external_id that matches exactly one.", n))
			return
		}
		if time.Now().Add(delay).After(deadline) {
			msg := "No device matches."
			if timeout > 0 {
				msg = fmt.Sprintf("No device matched within %s. Check that the server ran its install lines and can reach the internet.", timeout)
			}
			resp.Diagnostics.AddError("Device not found", msg)
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Waiting for the device was cancelled", ctx.Err().Error())
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > d.pollMax {
			delay = d.pollMax
		}
	}
}

// lookup returns the one matching device, or nil and the number of matches.
func (d *deviceDS) lookup(ctx context.Context, m deviceItem) (*client.Device, int, error) {
	if !m.ID.IsNull() {
		x, err := d.c.GetDevice(ctx, m.ID.ValueString())
		if err != nil {
			return nil, 0, err
		}
		return x, 1, nil
	}
	list, err := d.c.ListDevices(ctx, client.DeviceFilter{Name: m.Name.ValueString(), ExternalID: m.ExternalID.ValueString()})
	if err != nil {
		return nil, 0, err
	}
	if len(list) == 1 {
		return &list[0], 1, nil
	}
	return nil, len(list), nil
}

// ================================================================== awacino_active_connections

func NewActiveConnectionsDataSource() datasource.DataSource { return &activeDS{} }

type activeDS struct{ base }

type activeItem struct {
	ID           types.String `tfsdk:"id"`
	DeviceID     types.String `tfsdk:"device_id"`
	DeviceName   types.String `tfsdk:"device_name"`
	Via          types.String `tfsdk:"via"`
	MemberID     types.String `tfsdk:"member_id"`
	CodeID       types.String `tfsdk:"code_id"`
	CodeLabel    types.String `tfsdk:"code_label"`
	PeerName     types.String `tfsdk:"peer_name"`
	PeerPlatform types.String `tfsdk:"peer_platform"`
	Since        types.String `tfsdk:"since"`
}

type activeModel struct {
	DeviceID    types.String `tfsdk:"device_id"`
	MemberID    types.String `tfsdk:"member_id"`
	Via         types.String `tfsdk:"via"`
	Connections []activeItem `tfsdk:"connections"`
}

func (d *activeDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_active_connections"
}

func (d *activeDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Who is connected to which Beam-in device right now (read-only, for checks and outputs). Members are " +
			"shown by id, code holders by the code's label and their device name - never another account's e-mail. Key section " +
			"**Connections** (Read). API: `/v1/connections/active`.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only this Beam-in device."},
			"member_id": schema.StringAttribute{Optional: true, MarkdownDescription: "Only this member."},
			"via": schema.StringAttribute{Optional: true, MarkdownDescription: "`member`, `own_device` or `beamin_code`.",
				Validators: []validator.String{stringvalidator.OneOf("member", "own_device", "beamin_code")}},
			"connections": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The live connections.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":            schema.StringAttribute{Computed: true, MarkdownDescription: "Connection id."},
					"device_id":     schema.StringAttribute{Computed: true, MarkdownDescription: "The Beam-in device reached."},
					"device_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Its name."},
					"via":           schema.StringAttribute{Computed: true, MarkdownDescription: "`member`, `own_device` or `beamin_code`."},
					"member_id":     schema.StringAttribute{Computed: true, MarkdownDescription: "The member (via = member)."},
					"code_id":       schema.StringAttribute{Computed: true, MarkdownDescription: "The Beam-in code (via = beamin_code)."},
					"code_label":    schema.StringAttribute{Computed: true, MarkdownDescription: "The code's label."},
					"peer_name":     schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the connecting device."},
					"peer_platform": schema.StringAttribute{Computed: true, MarkdownDescription: "Its platform."},
					"since":         schema.StringAttribute{Computed: true, MarkdownDescription: "Connected since."},
				}},
			},
		},
	}
}

func (d *activeDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m activeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.c.ListActiveConnections(ctx, m.DeviceID.ValueString(), m.MemberID.ValueString(), m.Via.ValueString())
	if client.IsNotFound(err) {
		notLive(&resp.Diagnostics, "Cannot list the active connections", "GET /v1/connections/active", d.c, err)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot list the active connections", err)
		return
	}
	m.Connections = make([]activeItem, 0, len(list))
	for _, x := range list {
		m.Connections = append(m.Connections, activeItem{
			ID: types.StringValue(x.ID), DeviceID: types.StringValue(x.DeviceID), DeviceName: strOrNullEmpty(x.DeviceName),
			Via: types.StringValue(x.Via), MemberID: idOrNull(x.MemberID), CodeID: strOrNull(x.CodeID), CodeLabel: strOrNull(x.CodeLabel),
			PeerName: strOrNullEmpty(x.PeerName), PeerPlatform: strOrNullEmpty(x.PeerPlatform), Since: strOrNull(x.Since),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
