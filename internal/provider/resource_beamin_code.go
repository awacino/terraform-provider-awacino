package provider

import (
	"context"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &beaminCodeResource{}
	_ resource.ResourceWithConfigure   = &beaminCodeResource{}
	_ resource.ResourceWithImportState = &beaminCodeResource{}
)

// NewBeaminCodeResource is awacino_beamin_code.
func NewBeaminCodeResource() resource.Resource { return &beaminCodeResource{} }

type beaminCodeResource struct{ c *client.Client }

type beaminCodeModel struct {
	ID           types.String `tfsdk:"id"`
	DeviceID     types.String `tfsdk:"device_id"`
	Reach        types.Object `tfsdk:"reach"`
	Internet     types.String `tfsdk:"internet"`
	ValidDays    types.Int64  `tfsdk:"valid_days"`
	ExpiresAt    types.String `tfsdk:"expires_at"`
	Label        types.String `tfsdk:"label"`
	ExternalID   types.String `tfsdk:"external_id"`
	Paused       types.Bool   `tfsdk:"paused"`
	Code         types.String `tfsdk:"code"`
	ConnectURL   types.String `tfsdk:"connect_url"`
	CodeHint     types.String `tfsdk:"code_hint"`
	DeviceName   types.String `tfsdk:"device_name"`
	State        types.String `tfsdk:"state"`
	PausedReason types.String `tfsdk:"paused_reason"`
	BoundDevice  types.Object `tfsdk:"bound_device"`
	Uses         types.Int64  `tfsdk:"uses"`
	CreatedAt    types.String `tfsdk:"created_at"`
}

var boundDeviceAttrTypes = map[string]attr.Type{
	"label":    types.StringType,
	"platform": types.StringType,
	"bound_at": types.StringType,
}

func (r *beaminCodeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_beamin_code"
}

func (r *beaminCodeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Beam-in code for one of the company's Beam-in devices: a person or a machine with the code reaches " +
			"exactly `reach` in the device's network (several networks: one code per network). Hand `connect_url` (or `code`) to your " +
			"customer - both are returned **once**, on create, and are sensitive.\n\nThe code belongs to the company and is bound to the " +
			"first device that redeems it. A code beyond the plan's active codes is created paused (`paused_reason = \"lobby\"`). " +
			"Changing `device_id`, `reach`, `internet` or `valid_days` makes a **new code** (the old one is revoked). Destroying the " +
			"resource revokes the code; a device connected with it is disconnected at once.\n\n" +
			"Needs the key section **Connections** (Write). API: `/v1/connections/beamin-codes`.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep, MarkdownDescription: "Code id (`bic_...`), never the code itself."},
			"device_id": schema.StringAttribute{Required: true, PlanModifiers: replace, MarkdownDescription: "The Beam-in device the code reaches."},
			"reach": reachAttribute("What the code reaches in the device's network. Changing it makes a new code.",
				[]planmodifier.Object{objectplanmodifier.RequiresReplace()}),
			"internet": internetAttribute(replace),
			"valid_days": schema.Int64Attribute{
				Optional: true, MarkdownDescription: "Days the code works (1-365). Give this or `expires_at`; neither = no end where the plan allows it.",
				Validators: []validator.Int64{int64validator.Between(1, 365),
					int64validator.ConflictsWith(path.MatchRoot("expires_at"))},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"expires_at": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "End of the code (RFC 3339). It can later only move earlier.",
				Validators:          []validator.String{rfc3339Validator{}},
			},
			"label": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "Label shown in the app and the audit log, for example `Customer 4711 - web-01` (1-120 characters).",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 120)},
			},
			"external_id": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Your own id (unique per company among live codes, up to 128 characters).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 128)},
			},
			"paused": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "`true` stops new connections and ends live ones; `false` resumes. Default `false`.",
			},
			"code": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep,
				MarkdownDescription: "The Beam-in code (16 characters). Known only on create; keep your state in an encrypted backend.",
			},
			"connect_url": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep,
				MarkdownDescription: "`https://www.awacino.com/beamin/#<code>` - opens the Awacino app with the code.",
			},
			"code_hint":     schema.StringAttribute{Computed: true, PlanModifiers: keep, MarkdownDescription: "Last 4 characters of the code."},
			"device_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Name of the Beam-in device."},
			"state":         schema.StringAttribute{Computed: true, MarkdownDescription: "`active`, `paused`, `expired` or `revoked`."},
			"paused_reason": schema.StringAttribute{Computed: true, MarkdownDescription: "`admin` (paused = true) or `lobby` (beyond the plan's active codes)."},
			"bound_device": schema.SingleNestedAttribute{
				Computed: true, MarkdownDescription: "The device that redeemed the code first; only it can use the code.",
				Attributes: map[string]schema.Attribute{
					"label":    schema.StringAttribute{Computed: true, MarkdownDescription: "Name the device reported."},
					"platform": schema.StringAttribute{Computed: true, MarkdownDescription: "Its platform."},
					"bound_at": schema.StringAttribute{Computed: true, MarkdownDescription: "When it redeemed the code."},
				},
			},
			"uses":       schema.Int64Attribute{Computed: true, MarkdownDescription: "How often the code was used."},
			"created_at": schema.StringAttribute{Computed: true, PlanModifiers: keep, MarkdownDescription: "When the code was created."},
		},
	}
}

func (r *beaminCodeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func codeToState(ctx context.Context, b *client.BeaminCode, st *beaminCodeModel, diags *diagnostics) {
	st.ID = types.StringValue(b.ID)
	if b.DeviceID != "" {
		st.DeviceID = types.StringValue(b.DeviceID)
	}
	if b.Reach != nil && !sameReach(ctx, st.Reach, b.Reach, diags) {
		st.Reach = reachToObject(b.Reach, diags)
	}
	if b.Internet != "" {
		st.Internet = types.StringValue(b.Internet)
	} else if st.Internet.IsUnknown() || st.Internet.IsNull() {
		st.Internet = types.StringValue("none")
	}
	st.ExpiresAt = keepTime(st.ExpiresAt, b.ExpiresAt)
	if b.Label != "" || st.Label.IsUnknown() {
		st.Label = strOrNullEmpty(b.Label)
	}
	st.ExternalID = strOrNull(b.ExternalID)
	reason := ""
	if b.PausedReason != nil {
		reason = *b.PausedReason
	}
	st.Paused = types.BoolValue(b.State == "paused" && reason != "lobby")
	st.CodeHint = strOrNullEmpty(b.CodeHint)
	st.DeviceName = strOrNullEmpty(b.DeviceName)
	st.State = strOrNullEmpty(b.State)
	st.PausedReason = strOrNullEmpty(reason)
	if b.BoundDevice != nil {
		o, d := types.ObjectValue(boundDeviceAttrTypes, map[string]attr.Value{
			"label": strOrNullEmpty(b.BoundDevice.Label), "platform": strOrNullEmpty(b.BoundDevice.Platform),
			"bound_at": strOrNull(b.BoundDevice.BoundAt),
		})
		diags.Append(d...)
		st.BoundDevice = o
	} else {
		st.BoundDevice = types.ObjectNull(boundDeviceAttrTypes)
	}
	st.Uses = int64OrNull(b.Uses)
	if st.Uses.IsNull() {
		st.Uses = types.Int64Value(0)
	}
	st.CreatedAt = strOrNull(b.CreatedAt)
}

func (r *beaminCodeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan beaminCodeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RequireScope(ctx, "connections:write"); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the Beam-in code", err)
		return
	}
	in := client.BeaminCodeCreate{
		DeviceID:   plan.DeviceID.ValueString(),
		Reach:      reachFromObject(ctx, plan.Reach, &resp.Diagnostics),
		Internet:   plan.Internet.ValueString(),
		ExpiresAt:  strPtr(plan.ExpiresAt),
		ValidDays:  int64Ptr(plan.ValidDays),
		Label:      strPtr(plan.Label),
		ExternalID: strPtr(plan.ExternalID),
	}
	b, err := r.c.CreateBeaminCode(ctx, in, client.NewIdempotencyKey())
	if client.IsNotFound(err) {
		notLive(&resp.Diagnostics, "Cannot create the Beam-in code", "POST /v1/connections/beamin-codes", r.c, err)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the Beam-in code", err)
		return
	}
	st := plan
	codeToState(ctx, b, &st, &resp.Diagnostics)
	st.Code, st.ConnectURL = types.StringNull(), types.StringNull()
	if !client.Redacted(b.Code) && !b.CodeRedacted {
		st.Code = types.StringValue(b.Code)
		st.ConnectURL = strOrNullEmpty(b.ConnectURL)
	} else {
		resp.Diagnostics.AddWarning("Beam-in code not returned",
			"The API answered an earlier identical request again and does not repeat the code. Taint the resource for a new code.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Paused.ValueBool() && !st.Paused.ValueBool() {
		b2, err := r.c.UpdateBeaminCode(ctx, b.ID, map[string]any{"paused": true})
		if err != nil {
			addAPIError(&resp.Diagnostics, "The code was created but could not be paused", err)
			return
		}
		codeToState(ctx, b2, &st, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
	}
}

func (r *beaminCodeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st beaminCodeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b, err := r.c.GetBeaminCode(ctx, st.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && b.State == "revoked") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the Beam-in code", err)
		return
	}
	codeToState(ctx, b, &st, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *beaminCodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior beaminCodeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	patch := map[string]any{}
	if !plan.Label.IsUnknown() && !plan.Label.Equal(prior.Label) && !plan.Label.IsNull() {
		patch["label"] = plan.Label.ValueString()
	}
	if !plan.Paused.Equal(prior.Paused) {
		patch["paused"] = plan.Paused.ValueBool()
	}
	if !plan.ExpiresAt.IsUnknown() && !plan.ExpiresAt.IsNull() && !plan.ExpiresAt.Equal(prior.ExpiresAt) &&
		!(prior.ExpiresAt.ValueString() != "" && sameInstant(plan.ExpiresAt.ValueString(), prior.ExpiresAt.ValueString())) {
		patch["expires_at"] = plan.ExpiresAt.ValueString()
	}
	if !plan.ExternalID.Equal(prior.ExternalID) {
		patch["external_id"] = nullable(plan.ExternalID)
	}
	st := plan
	st.ID, st.Code, st.ConnectURL = prior.ID, prior.Code, prior.ConnectURL
	var b *client.BeaminCode
	var err error
	if len(patch) > 0 {
		b, err = r.c.UpdateBeaminCode(ctx, prior.ID.ValueString(), patch)
	} else {
		b, err = r.c.GetBeaminCode(ctx, prior.ID.ValueString())
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot update the Beam-in code", err)
		return
	}
	codeToState(ctx, b, &st, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *beaminCodeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st beaminCodeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.DeleteBeaminCode(ctx, st.ID.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot revoke the Beam-in code", err)
	}
}

// ImportState: by code id (bic_...). The code itself is not importable.
func (r *beaminCodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
