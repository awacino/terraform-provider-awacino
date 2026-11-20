package provider

import (
	"context"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &accessGrantResource{}
	_ resource.ResourceWithConfigure   = &accessGrantResource{}
	_ resource.ResourceWithImportState = &accessGrantResource{}
)

// NewAccessGrantResource is awacino_access_grant.
func NewAccessGrantResource() resource.Resource { return &accessGrantResource{} }

type accessGrantResource struct{ c *client.Client }

type accessGrantModel struct {
	ID         types.String `tfsdk:"id"`
	MemberID   types.String `tfsdk:"member_id"`
	DeviceID   types.String `tfsdk:"device_id"`
	Reach      types.Object `tfsdk:"reach"`
	Internet   types.String `tfsdk:"internet"`
	ValidUntil types.String `tfsdk:"valid_until"`
	State      types.String `tfsdk:"state"`
	CreatedAt  types.String `tfsdk:"created_at"`
}

func (r *accessGrantResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_grant"
}

func internetAttribute(mods []planmodifier.String) schema.StringAttribute {
	return schema.StringAttribute{
		Optional: true, Computed: true, Default: stringdefault.StaticString("none"),
		MarkdownDescription: "Use the device's internet: `none` (default) never, `choice` the person decides, `full` always.",
		Validators:          []validator.String{stringvalidator.OneOf("none", "choice", "full")},
		PlanModifiers:       mods,
	}
}

func (r *accessGrantResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A member's access to one Beam-in device: how far (`reach`), whether through the device's internet, " +
			"and until when. Devices that are not granted are not visible to a member with role `read` and cannot be reached. " +
			"One grant per member and device; several networks = one grant per Beam-in device.\n\n" +
			"The server accepts only what it enforces (`422 reach_not_enforced_yet` otherwise) - read `access_capabilities` of " +
			"`awacino_company`. Destroying the grant ends live connections covered only by it within 5 seconds.\n\n" +
			"Needs the key section **Connections** (Write) and the **Management** package. API: `/v1/connections/grants`.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep, MarkdownDescription: "Grant id (`grt_...`)."},
			"member_id": schema.StringAttribute{Required: true, PlanModifiers: replace, MarkdownDescription: "The member (`awacino_member.x.id`)."},
			"device_id": schema.StringAttribute{Required: true, PlanModifiers: replace, MarkdownDescription: "The Beam-in device."},
			"reach":     reachAttribute("What the member reaches in the device's network.", nil),
			"internet": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "Use the device's internet: `none`, `choice` (the member decides) or `full`. Leave it out today: the " +
					"server enforces internet routing for grants only from phase B (`422 reach_not_enforced_yet`).",
				Validators: []validator.String{stringvalidator.OneOf("none", "choice", "full")},
			},
			"valid_until": schema.StringAttribute{
				Optional: true, MarkdownDescription: "The grant ends at this instant (RFC 3339, for example the contract end `2027-06-30T00:00:00Z`).",
				Validators: []validator.String{rfc3339Validator{}},
			},
			"state":      schema.StringAttribute{Computed: true, MarkdownDescription: "`active` or `expired`."},
			"created_at": schema.StringAttribute{Computed: true, PlanModifiers: keep, MarkdownDescription: "When the grant was created."},
		},
	}
}

func (r *accessGrantResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *accessGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan accessGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RequireScope(ctx, "connections:write"); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the access grant", err)
		return
	}
	in := client.GrantCreate{
		MemberID:   plan.MemberID.ValueString(),
		DeviceID:   plan.DeviceID.ValueString(),
		Reach:      reachFromObject(ctx, plan.Reach, &resp.Diagnostics),
		Internet:   plan.Internet.ValueString(), // "" (unknown / not set) is left out
		ValidUntil: strPtr(plan.ValidUntil),
	}
	g, err := r.c.CreateGrant(ctx, in, client.NewIdempotencyKey())
	if client.IsNotFound(err) {
		notLive(&resp.Diagnostics, "Cannot create the access grant", "POST /v1/connections/grants", r.c, err)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the access grant", err)
		return
	}
	st := plan
	grantToState(ctx, g, &st, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func grantToState(ctx context.Context, g *client.Grant, st *accessGrantModel, diags *diagnostics) {
	st.ID = types.StringValue(g.ID)
	if g.MemberID != "" {
		st.MemberID = types.StringValue(g.MemberID.String())
	}
	if g.DeviceID != "" {
		st.DeviceID = types.StringValue(g.DeviceID)
	}
	if g.Reach != nil && !sameReach(ctx, st.Reach, g.Reach, diags) {
		st.Reach = reachToObject(g.Reach, diags)
	}
	if g.Internet != "" {
		st.Internet = types.StringValue(g.Internet)
	} else {
		st.Internet = types.StringNull()
	}
	st.ValidUntil = keepTime(st.ValidUntil, g.ValidUntil)
	st.State = strOrNullEmpty(g.State)
	st.CreatedAt = strOrNull(g.CreatedAt)
}

func (r *accessGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st accessGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.c.GetGrant(ctx, st.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the access grant", err)
		return
	}
	grantToState(ctx, g, &st, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *accessGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior accessGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	patch := map[string]any{}
	if !plan.Reach.Equal(prior.Reach) {
		patch["reach"] = reachFromObject(ctx, plan.Reach, &resp.Diagnostics)
	}
	if !plan.Internet.IsUnknown() && !plan.Internet.Equal(prior.Internet) {
		patch["internet"] = nullable(plan.Internet)
	}
	if !plan.ValidUntil.Equal(prior.ValidUntil) {
		patch["valid_until"] = nullable(plan.ValidUntil)
	}
	st := plan
	st.ID = prior.ID
	var g *client.Grant
	var err error
	if len(patch) > 0 {
		g, err = r.c.UpdateGrant(ctx, prior.ID.ValueString(), patch)
	} else {
		g, err = r.c.GetGrant(ctx, prior.ID.ValueString())
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot update the access grant", err)
		return
	}
	grantToState(ctx, g, &st, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *accessGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st accessGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteGrant(ctx, st.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot revoke the access grant", err)
	}
}

func (r *accessGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
