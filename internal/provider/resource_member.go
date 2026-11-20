package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource                = &memberResource{}
	_ resource.ResourceWithConfigure   = &memberResource{}
	_ resource.ResourceWithImportState = &memberResource{}
	_ resource.ResourceWithModifyPlan  = &memberResource{}
)

// NewMemberResource is awacino_member.
func NewMemberResource() resource.Resource { return &memberResource{} }

type memberResource struct{ c *client.Client }

type memberModel struct {
	ID                  types.String `tfsdk:"id"`
	Email               types.String `tfsdk:"email"`
	Role                types.String `tfsdk:"role"`
	DisplayName         types.String `tfsdk:"display_name"`
	ExternalID          types.String `tfsdk:"external_id"`
	CanAutomation       types.Bool   `tfsdk:"can_automation"`
	CanAgentic          types.Bool   `tfsdk:"can_agentic"`
	Active              types.Bool   `tfsdk:"active"`
	SendInvitation      types.Object `tfsdk:"send_invitation"`
	Status              types.String `tfsdk:"status"`
	InvitationCode      types.String `tfsdk:"invitation_code"`
	JoinURL             types.String `tfsdk:"join_url"`
	InvitationExpiresAt types.String `tfsdk:"invitation_expires_at"`
	CreatedAt           types.String `tfsdk:"created_at"`
	AcceptedAt          types.String `tfsdk:"accepted_at"`
}

var sendInvitationAttrTypes = map[string]attr.Type{
	"days":   types.Int64Type,
	"locale": types.StringType,
}

func (r *memberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (r *memberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A member of the Awacino company account: a company-owned Awacino account for one person, " +
			"with its first invitation code. The person pastes the code into the Awacino app (or opens `join_url`) and signs " +
			"in with Apple, Google or Microsoft. Each active member or open invitation takes one user seat.\n\n" +
			"Destroying the resource **offboards** the member: every session and live connection ends, the member's Beam-in " +
			"codes move to the company, their Beam-out devices leave the company and the seat is freed. The person's private " +
			"Awacino account is not touched.\n\nNeeds the key section **Users** (Write).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Member id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"email": schema.StringAttribute{
				Required: true, MarkdownDescription: "The invited address. Changing it replaces the member (offboard + new invitation).",
				PlanModifiers: []planmodifier.String{caseInsensitive{}, stringplanmodifier.RequiresReplaceIf(emailChanged,
					"A new address is a new member", "A new address is a new member")},
			},
			"role": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("read"),
				MarkdownDescription: "`read` (default - uses only the devices you grant), `user`, or `auditor` (audit log only; needs the Compliance package). " +
					"An API key cannot create ADMIN accounts.",
				Validators: []validatorString{stringvalidator.OneOf("read", "user", "auditor")},
			},
			"display_name": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Name shown in the app (up to 120 characters).",
				Validators: []validatorString{stringvalidator.LengthBetween(1, 120)},
			},
			"external_id": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Your own id for the member (for example the employee number), unique per company. " +
					"`terraform import awacino_member.x external_id:<value>` finds the member by it.",
				Validators: []validatorString{stringvalidator.LengthBetween(1, 128)},
			},
			"can_automation": schema.BoolAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Role `read` only: may use automation (default `false`). Members with role `user` or `auditor` always have it (`true`).",
			},
			"can_agentic": schema.BoolAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Role `read` only: may use agentic AI (default `false`). Members with role `user` or `auditor` always have it (`true`).",
			},
			"active": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "`false` deactivates the member (sessions and live connections end, the seat is freed); `true` reactivates (needs a free seat).",
			},
			"send_invitation": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Awacino mails the invitation to `email`. Without this block the code is created as well but not mailed - " +
					"send `join_url` in your own mail. Only used when the member is created.",
				Attributes: map[string]schema.Attribute{
					"days": schema.Int64Attribute{
						Optional: true, Computed: true, Default: int64default.StaticInt64(7),
						MarkdownDescription: "Days the code is valid (1-14, default 7).",
						Validators:          []validatorInt64{int64validator.Between(1, 14)},
					},
					"locale": schema.StringAttribute{
						Optional: true, MarkdownDescription: "Mail language, ISO 639-1 (`en`, `de`, `nb`, `pt`, ...). Default `en`.",
					},
				},
			},
			"status": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`invited`, `active` or `deactivated`.",
				PlanModifiers: []planmodifier.String{keepUnlessActiveChanges{}},
			},
			"invitation_code": schema.StringAttribute{
				Computed: true, Sensitive: true,
				MarkdownDescription: "The first invitation code (`AWA-XXXX-XXXX-XXXX`). Known only right after the member was created; " +
					"keep your state in an encrypted backend. Use `awacino_invitation` for a new code.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"join_url": schema.StringAttribute{
				Computed: true, Sensitive: true,
				MarkdownDescription: "`https://www.awacino.com/join/#<code>` for your own mail (the code is in the fragment and never reaches a server log). Known only on create.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"invitation_expires_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the open invitation ends (null once the member has joined).",
				PlanModifiers: []planmodifier.String{keepUnlessActiveChanges{}},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the member was created.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"accepted_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the person joined.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ModifyPlan: the server sets can_automation / can_agentic to true for every role but read, and to the
// given value (default false) for read - plan exactly that, so an apply never surprises.
func (r *memberResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, cfg memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || plan.Role.IsUnknown() {
		return
	}
	read := plan.Role.ValueString() == "read"
	for _, a := range []struct {
		name string
		cfg  types.Bool
	}{{"can_automation", cfg.CanAutomation}, {"can_agentic", cfg.CanAgentic}} {
		switch {
		case !read && !a.cfg.IsNull() && !a.cfg.IsUnknown() && !a.cfg.ValueBool():
			resp.Diagnostics.AddAttributeError(path.Root(a.name), "Only for role read",
				fmt.Sprintf("%s = false only works for members with role read; role %s always has it.", a.name, plan.Role.ValueString()))
		case !read:
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(a.name), true)...)
		case a.cfg.IsNull():
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(a.name), false)...)
		}
	}
}

func emailChanged(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	resp.RequiresReplace = !strings.EqualFold(req.StateValue.ValueString(), req.PlanValue.ValueString())
}

func (r *memberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *memberResource) apply(m *client.Member, st *memberModel) {
	st.ID = types.StringValue(m.ID.String())
	st.Email = keepFold(st.Email, m.Email)
	st.Role = types.StringValue(m.Role)
	st.DisplayName = strOrNull(m.DisplayName)
	st.ExternalID = strOrNull(m.ExternalID)
	st.CanAutomation = boolOr(m.CanAutomation, false)
	st.CanAgentic = boolOr(m.CanAgentic, false)
	st.Active = types.BoolValue(m.Active())
	st.Status = types.StringValue(m.Status)
	st.CreatedAt = strOrNull(m.CreatedAt)
	st.AcceptedAt = strOrNull(m.AcceptedAt)
	if m.Invitation != nil && m.Invitation.IsOpen() {
		st.InvitationExpiresAt = strOrNull(m.Invitation.ExpiresAt)
	} else {
		st.InvitationExpiresAt = types.StringNull()
	}
}

func (r *memberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RequireScope(ctx, "users:write"); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the member", err)
		return
	}
	in := client.MemberCreate{
		Email:       plan.Email.ValueString(),
		Role:        plan.Role.ValueString(),
		DisplayName: strPtr(plan.DisplayName),
		ExternalID:  strPtr(plan.ExternalID),
		Invitation:  &client.InvitationRequest{Days: ptrInt64(7), SendMail: ptrBool(false)},
	}
	if plan.Role.ValueString() == "read" {
		in.CanAutomation, in.CanAgentic = boolPtr(plan.CanAutomation), boolPtr(plan.CanAgentic)
	}
	if !plan.SendInvitation.IsNull() && !plan.SendInvitation.IsUnknown() {
		a := plan.SendInvitation.Attributes()
		if d, ok := a["days"].(basetypes.Int64Value); ok && !d.IsNull() && !d.IsUnknown() {
			in.Invitation.Days = ptrInt64(d.ValueInt64())
		}
		if l, ok := a["locale"].(basetypes.StringValue); ok && !l.IsNull() && !l.IsUnknown() {
			in.Invitation.Locale = strPtr(l)
		}
		in.Invitation.SendMail = ptrBool(true)
	}
	m, err := r.c.CreateMember(ctx, in, client.NewIdempotencyKey())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the member", err)
		return
	}
	st := plan
	r.apply(m, &st)
	st.InvitationCode = types.StringNull()
	st.JoinURL = types.StringNull()
	if m.Invitation != nil && !client.Redacted(m.Invitation.Code) {
		st.InvitationCode = types.StringValue(m.Invitation.Code)
		st.JoinURL = strOrNullEmpty(m.Invitation.JoinURL)
	} else if m.Invitation != nil {
		resp.Diagnostics.AddWarning("Invitation code not returned",
			"The API answered an earlier identical request again and does not repeat the code. Use awacino_invitation to issue a new code.")
	}
	// save the member before anything else can fail, so it is never orphaned
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Active.IsNull() && !plan.Active.ValueBool() && m.Active() {
		m2, err := r.c.UpdateMember(ctx, m.ID.String(), map[string]any{"active": false})
		if err != nil {
			addAPIError(&resp.Diagnostics, "The member was created but could not be deactivated", err)
			return
		}
		r.apply(m2, &st)
		resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
	}
}

func (r *memberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.c.GetMember(ctx, st.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && m.Status == "removed") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the member", err)
		return
	}
	r.apply(m, &st)
	if st.SendInvitation.IsUnknown() {
		st.SendInvitation = types.ObjectNull(sendInvitationAttrTypes)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *memberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	patch := map[string]any{}
	if !plan.Role.Equal(prior.Role) {
		patch["role"] = plan.Role.ValueString()
	}
	// the rights only mean something for role read; a change to read sends them, or the server keeps the old ones
	if plan.Role.ValueString() == "read" && (!plan.Role.Equal(prior.Role) || !plan.CanAutomation.Equal(prior.CanAutomation) ||
		!plan.CanAgentic.Equal(prior.CanAgentic)) {
		patch["can_automation"] = plan.CanAutomation.ValueBool()
		patch["can_agentic"] = plan.CanAgentic.ValueBool()
	}
	if !plan.DisplayName.Equal(prior.DisplayName) {
		patch["display_name"] = nullable(plan.DisplayName)
	}
	if !plan.ExternalID.Equal(prior.ExternalID) {
		patch["external_id"] = nullable(plan.ExternalID)
	}
	if !plan.Active.Equal(prior.Active) {
		patch["active"] = plan.Active.ValueBool()
	}
	st := plan
	st.InvitationCode = prior.InvitationCode
	st.JoinURL = prior.JoinURL
	var m *client.Member
	var err error
	if len(patch) > 0 {
		m, err = r.c.UpdateMember(ctx, prior.ID.ValueString(), patch)
	} else {
		m, err = r.c.GetMember(ctx, prior.ID.ValueString())
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot update the member", err)
		return
	}
	r.apply(m, &st)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *memberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteMember(ctx, st.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot offboard the member", err)
	}
}

func (r *memberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if ext, ok := strings.CutPrefix(id, "external_id:"); ok {
		ms, err := r.c.ListMembers(ctx, client.MemberFilter{ExternalID: ext})
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot look up the member", err)
			return
		}
		if len(ms) != 1 {
			resp.Diagnostics.AddError("Member not found", fmt.Sprintf("%d members have external_id %q", len(ms), ext))
			return
		}
		id = ms[0].ID.String()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("send_invitation"), types.ObjectNull(sendInvitationAttrTypes))...)
}

// nullable is the JSON value of an optional attribute: nil (null) clears the field.
func nullable(v types.String) any {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return v.ValueString()
}

func ptrInt64(n int64) *int64 { return &n }
func ptrBool(b bool) *bool    { return &b }

// caseInsensitive keeps the prior value when only the case of an e-mail address changed.
type caseInsensitive struct{}

func (caseInsensitive) Description(context.Context) string             { return "ignores case-only changes" }
func (m caseInsensitive) MarkdownDescription(c context.Context) string { return m.Description(c) }
func (caseInsensitive) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}
	if strings.EqualFold(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}

// keepUnlessActiveChanges keeps a known status / invitation end unless `active` changes in this plan
// (deactivating ends the open invitation).
type keepUnlessActiveChanges struct{}

func (keepUnlessActiveChanges) Description(context.Context) string {
	return "keeps the status unless active changes"
}
func (m keepUnlessActiveChanges) MarkdownDescription(c context.Context) string {
	return m.Description(c)
}
func (keepUnlessActiveChanges) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	var planActive, stateActive types.Bool
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("active"), &planActive)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("active"), &stateActive)...)
	if planActive.Equal(stateActive) {
		resp.PlanValue = req.StateValue
	}
}
