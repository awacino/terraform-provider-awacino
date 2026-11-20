package provider

import (
	"context"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &invitationResource{}
	_ resource.ResourceWithConfigure   = &invitationResource{}
	_ resource.ResourceWithImportState = &invitationResource{}
)

// NewInvitationResource is awacino_invitation.
func NewInvitationResource() resource.Resource { return &invitationResource{} }

type invitationResource struct{ c *client.Client }

type invitationModel struct {
	ID        types.String `tfsdk:"id"`
	MemberID  types.String `tfsdk:"member_id"`
	Days      types.Int64  `tfsdk:"days"`
	SendMail  types.Bool   `tfsdk:"send_mail"`
	Locale    types.String `tfsdk:"locale"`
	Triggers  types.Map    `tfsdk:"triggers"`
	Code      types.String `tfsdk:"code"`
	JoinURL   types.String `tfsdk:"join_url"`
	ExpiresAt types.String `tfsdk:"expires_at"`
	Mailed    types.Bool   `tfsdk:"mailed"`
	State     types.String `tfsdk:"state"`
}

func (r *invitationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invitation"
}

func (r *invitationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceS := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keepS := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A new invitation code for a member who has not joined yet (status `invited`). Issuing it revokes the " +
			"member's older code. Change `triggers` to issue another code. Destroying the resource revokes the open code; the member " +
			"stays `invited` until a new code is issued or the member is removed.\n\nNeeds the key section **Users** (Write).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The member id.", PlanModifiers: keepS},
			"member_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "The invited member (`awacino_member.x.id`).", PlanModifiers: replaceS,
			},
			"days": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(7),
				MarkdownDescription: "Days the code is valid (1-14, default 7).",
				Validators:          []validatorInt64{int64validator.Between(1, 14)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"send_mail": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "`true`: Awacino mails the invited address. Default `false` (send `join_url` yourself).",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"locale": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Mail language (ISO 639-1). Default `en`.", PlanModifiers: replaceS,
			},
			"triggers": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Any values; a change issues a new code (and revokes the old one).",
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"code": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keepS,
				MarkdownDescription: "The invitation code (`AWA-XXXX-XXXX-XXXX`). Only known when it was issued; keep your state in an encrypted backend.",
			},
			"join_url": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keepS,
				MarkdownDescription: "`https://www.awacino.com/join/#<code>`.",
			},
			"expires_at": schema.StringAttribute{Computed: true, PlanModifiers: keepS, MarkdownDescription: "When the code ends."},
			"mailed": schema.BoolAttribute{
				Computed: true, MarkdownDescription: "Whether Awacino mailed it.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`open`, `expired` or `used` (the member joined).",
			},
		},
	}
}

func (r *invitationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *invitationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan invitationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RequireScope(ctx, "users:write"); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot issue the invitation", err)
		return
	}
	in := client.InvitationRequest{Days: int64Ptr(plan.Days), SendMail: boolPtr(plan.SendMail), Locale: strPtr(plan.Locale)}
	inv, err := r.c.IssueInvitation(ctx, plan.MemberID.ValueString(), in, client.NewIdempotencyKey())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot issue the invitation", err)
		return
	}
	st := plan
	st.ID = plan.MemberID
	st.Code, st.JoinURL = types.StringNull(), types.StringNull()
	if !client.Redacted(inv.Code) {
		st.Code = types.StringValue(inv.Code)
		st.JoinURL = strOrNullEmpty(inv.JoinURL)
	}
	st.ExpiresAt = strOrNull(inv.ExpiresAt)
	st.Mailed = boolOr(inv.Mailed, false)
	st.State = types.StringValue("open")
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *invitationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st invitationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	memberID := st.MemberID.ValueString()
	if memberID == "" {
		memberID = st.ID.ValueString()
		st.MemberID = st.ID
	}
	inv, err := r.c.GetInvitation(ctx, memberID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the invitation", err)
		return
	}
	switch {
	case inv.IsOpen():
		// another code was issued since (it revoked this one): issue ours again
		if !st.ExpiresAt.IsNull() && inv.ExpiresAt != nil && !sameInstant(st.ExpiresAt.ValueString(), *inv.ExpiresAt) && !st.Code.IsNull() {
			resp.State.RemoveResource(ctx)
			return
		}
		st.ExpiresAt = keepTime(st.ExpiresAt, inv.ExpiresAt)
		st.State = types.StringValue("open")
		if inv.IsExpired() {
			st.State = types.StringValue("expired")
		}
	default:
		m, err := r.c.GetMember(ctx, memberID)
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot read the member of the invitation", err)
			return
		}
		if m.Status == "invited" {
			// revoked outside Terraform: a new code is issued on the next apply
			resp.State.RemoveResource(ctx)
			return
		}
		st.State = types.StringValue("used")
	}
	if st.Mailed.IsNull() || st.Mailed.IsUnknown() {
		st.Mailed = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

// Update: every argument replaces the code; only the state is carried over.
func (r *invitationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior invitationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.Code, plan.JoinURL, plan.ExpiresAt, plan.Mailed, plan.State =
		prior.ID, prior.Code, prior.JoinURL, prior.ExpiresAt, prior.Mailed, prior.State
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *invitationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st invitationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if st.State.ValueString() == "used" {
		return
	}
	if err := r.c.RevokeInvitation(ctx, st.MemberID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot revoke the invitation", err)
	}
}

// ImportState: by member id. The code itself cannot be imported (it is stored as a hash only).
func (r *invitationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("member_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("days"), int64(7))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("send_mail"), false)...)
}
