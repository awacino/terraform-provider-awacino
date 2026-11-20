package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &beaminDeviceResource{}
	_ resource.ResourceWithConfigure   = &beaminDeviceResource{}
	_ resource.ResourceWithImportState = &beaminDeviceResource{}
)

// NewBeaminDeviceResource is awacino_beamin_device.
func NewBeaminDeviceResource() resource.Resource { return &beaminDeviceResource{} }

type beaminDeviceResource struct {
	c *client.Client
	// poll bounds the back-off of wait_for_device (tests shorten it)
	pollMin, pollMax time.Duration
}

type beaminDeviceModel struct {
	ID             types.String `tfsdk:"id"`
	Label          types.String `tfsdk:"label"`
	ExternalID     types.String `tfsdk:"external_id"`
	Hours          types.Int64  `tfsdk:"hours"`
	WaitForDevice  types.Bool   `tfsdk:"wait_for_device"`
	WaitTimeout    types.String `tfsdk:"wait_timeout"`
	Name           types.String `tfsdk:"name"`
	SetupCode      types.String `tfsdk:"setup_code"`
	InstallDocker  types.String `tfsdk:"install_docker"`
	InstallApt     types.String `tfsdk:"install_apt"`
	CloudInit      types.String `tfsdk:"cloud_init"`
	EnrolmentState types.String `tfsdk:"enrolment_state"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	DeviceID       types.String `tfsdk:"device_id"`
	Status         types.String `tfsdk:"status"`
	Platform       types.String `tfsdk:"platform"`
}

func (r *beaminDeviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_beamin_device"
}

func (r *beaminDeviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An unattended server as a Beam-in device of the company. Creating the resource makes a **one-time " +
			"setup code** (an enrolment) with ready install lines (`install_docker`, `install_apt`, `cloud_init`). Put `cloud_init` " +
			"into the server's user data (or run one of the install lines); the server installs the Awacino client, connects outbound " +
			"only and registers as a Beam-in device within minutes. With `wait_for_device = true` the apply waits for that and " +
			"`device_id` is known afterwards.\n\nDestroying the resource removes the registered device from the company, or revokes the " +
			"setup code while it is unused. A device beyond the plan's Beam-in limit waits in the Lobby (`status = \"lobby\"`).\n\n" +
			"Needs the key section **Connections** (Write).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, PlanModifiers: keep,
				MarkdownDescription: "Enrolment id (`enr_...`); the device id when the resource was imported by device id.",
			},
			"label": schema.StringAttribute{
				Required: true, PlanModifiers: replace,
				MarkdownDescription: "Name of the new device (1-64 characters, unique among the company's devices), for example its host name.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 64)},
			},
			"external_id": schema.StringAttribute{
				Optional: true, PlanModifiers: replace,
				MarkdownDescription: "Your own id for the server (up to 128 characters); the `awacino_devices` data source filters by it.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 128)},
			},
			"hours": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(2),
				MarkdownDescription: "Hours the setup code pairs (1-24, default 2). An unused code that ran out never pairs.",
				Validators:          []validator.Int64{int64validator.Between(1, 24)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"wait_for_device": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Wait in apply until the server has registered (polls with back-off 2 to 30 s). Default `false`.",
			},
			"wait_timeout": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("15m"),
				MarkdownDescription: "How long `wait_for_device` waits (Go duration, default `15m`).",
				Validators:          []validator.String{durationValidator{}},
			},
			"name": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				MarkdownDescription: "The device's name once it registered (starts as `label`). Set it to rename the device.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 64)},
			},
			"setup_code": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep,
				MarkdownDescription: "The one-time setup code (Docker `AWACINO_SETUP_CODE`). Known only on create.",
			},
			"install_docker": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep, MarkdownDescription: "A `docker run` line with the setup code.",
			},
			"install_apt": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep, MarkdownDescription: "Shell lines for Debian / Ubuntu with the setup code.",
			},
			"cloud_init": schema.StringAttribute{
				Computed: true, Sensitive: true, PlanModifiers: keep,
				MarkdownDescription: "A `#cloud-config` document with the setup code, for the server's `user_data`.",
			},
			"enrolment_state": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`open`, `used` (the server registered), `expired` or `revoked`.",
			},
			"expires_at": schema.StringAttribute{
				Computed: true, PlanModifiers: keep, MarkdownDescription: "When the setup code stops pairing.",
			},
			"device_id": schema.StringAttribute{
				Computed: true, PlanModifiers: keep,
				MarkdownDescription: "The registered device's id - known once the server registered (use `wait_for_device`).",
			},
			"status": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`active` or `lobby` once registered; `pending` before.",
			},
			"platform": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Platform the device reported (`linux`, `docker`, ...).",
			},
		},
	}
}

func (r *beaminDeviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
	if r.pollMin == 0 {
		r.pollMin, r.pollMax = 2*time.Second, 30*time.Second
	}
}

func (r *beaminDeviceResource) applyEnrolment(e *client.Enrolment, st *beaminDeviceModel) {
	st.ID = types.StringValue(e.ID)
	st.EnrolmentState = types.StringValue(e.State)
	st.ExpiresAt = strOrNull(e.ExpiresAt)
	if e.DeviceID != nil && *e.DeviceID != "" {
		st.DeviceID = types.StringValue(*e.DeviceID)
	} else if st.DeviceID.IsUnknown() {
		st.DeviceID = types.StringNull()
	}
}

func (r *beaminDeviceResource) applyDevice(d *client.Device, st *beaminDeviceModel) {
	st.DeviceID = types.StringValue(d.ID)
	st.Name = types.StringValue(d.Name)
	st.Status = types.StringValue(d.Status)
	st.Platform = strOrNullEmpty(d.Platform)
}

func (r *beaminDeviceResource) pending(st *beaminDeviceModel) {
	if st.Status.IsUnknown() || st.Status.IsNull() {
		st.Status = types.StringValue("pending")
	}
	if st.Platform.IsUnknown() {
		st.Platform = types.StringNull()
	}
	if st.Name.IsUnknown() || st.Name.IsNull() {
		st.Name = st.Label
	}
	if st.DeviceID.IsUnknown() {
		st.DeviceID = types.StringNull()
	}
}

// settle reads the device once registered and renames it when name asks for it.
func (r *beaminDeviceResource) settle(ctx context.Context, want types.String, st *beaminDeviceModel, diags *diag.Diagnostics) {
	if st.DeviceID.IsNull() || st.DeviceID.IsUnknown() {
		if !want.IsNull() && !want.IsUnknown() {
			st.Name = want // applied once the server registered
		}
		r.pending(st)
		return
	}
	d, err := r.c.GetDevice(ctx, st.DeviceID.ValueString())
	if err != nil {
		addAPIError(diags, "Cannot read the registered device", err)
		return
	}
	if !want.IsNull() && !want.IsUnknown() && want.ValueString() != d.Name {
		d, err = r.c.UpdateDevice(ctx, d.ID, map[string]any{"name": want.ValueString()})
		if err != nil {
			addAPIError(diags, "Cannot rename the device", err)
			return
		}
	}
	r.applyDevice(d, st)
}

func (r *beaminDeviceResource) wait(ctx context.Context, st *beaminDeviceModel, diags *diag.Diagnostics) {
	timeout, _ := time.ParseDuration(st.WaitTimeout.ValueString())
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	delay := r.pollMin
	for {
		e, err := r.c.GetEnrolment(ctx, st.ID.ValueString())
		if err != nil {
			addAPIError(diags, "Cannot read the enrolment while waiting for the device", err)
			return
		}
		r.applyEnrolment(e, st)
		if e.State == "used" && e.DeviceID != nil && *e.DeviceID != "" {
			return
		}
		if e.State == "expired" || e.State == "revoked" {
			diags.AddError("The server did not register", fmt.Sprintf("The setup code is %s before a server used it.", e.State))
			return
		}
		if time.Now().Add(delay).After(deadline) {
			diags.AddError("Timed out waiting for the server to register",
				fmt.Sprintf("The setup code %s was not used within %s. Check that the server ran the install lines (cloud_init / install_apt / "+
					"install_docker) and can reach the internet; then apply again (the resource is tainted and makes a new code) or raise wait_timeout.",
					st.ID.ValueString(), timeout))
			return
		}
		select {
		case <-ctx.Done():
			diags.AddError("Waiting for the server was cancelled", ctx.Err().Error())
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > r.pollMax {
			delay = r.pollMax
		}
	}
}

func (r *beaminDeviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan beaminDeviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.RequireScope(ctx, "connections:write"); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the setup code", err)
		return
	}
	e, err := r.c.CreateEnrolment(ctx, client.EnrolmentCreate{
		Label: plan.Label.ValueString(), ExternalID: strPtr(plan.ExternalID), Hours: int64Ptr(plan.Hours),
	}, client.NewIdempotencyKey())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create the setup code", err)
		return
	}
	st := plan
	r.applyEnrolment(e, &st)
	st.SetupCode = strOrNullEmpty(e.Code)
	if client.Redacted(e.Code) {
		st.SetupCode = types.StringNull()
	}
	st.InstallDocker, st.InstallApt, st.CloudInit = types.StringNull(), types.StringNull(), types.StringNull()
	if e.Install != nil {
		red := func(s string) types.String {
			if client.Redacted(s) {
				return types.StringNull()
			}
			return types.StringValue(s)
		}
		st.InstallDocker, st.InstallApt, st.CloudInit = red(e.Install.Docker), red(e.Install.Apt), red(e.Install.CloudInit)
	}
	r.pending(&st)
	if !plan.Name.IsUnknown() {
		st.Name = plan.Name
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.WaitForDevice.ValueBool() {
		r.wait(ctx, &st, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
			return
		}
	}
	want := plan.Name
	r.settle(ctx, want, &st, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *beaminDeviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var st beaminDeviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.ID.ValueString()
	if strings.HasPrefix(id, "enr_") {
		e, err := r.c.GetEnrolment(ctx, id)
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot read the enrolment", err)
			return
		}
		hadDevice := !st.DeviceID.IsNull() && st.DeviceID.ValueString() != ""
		r.applyEnrolment(e, &st)
		if e.State == "used" && (e.DeviceID == nil || *e.DeviceID == "") {
			// the registered device was removed from the company
			resp.State.RemoveResource(ctx)
			return
		}
		if e.DeviceID == nil || *e.DeviceID == "" {
			if hadDevice {
				resp.State.RemoveResource(ctx)
				return
			}
			r.pending(&st)
			resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
			return
		}
	} else {
		st.DeviceID = st.ID
	}
	d, err := r.c.GetDevice(ctx, st.DeviceID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read the device", err)
		return
	}
	r.applyDevice(d, &st)
	if st.Label.IsNull() {
		st.Label = types.StringValue(d.Name)
	}
	if st.ExternalID.IsNull() && d.ExternalID != nil && !strings.HasPrefix(id, "enr_") {
		st.ExternalID = strOrNull(d.ExternalID)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *beaminDeviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior beaminDeviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	st := prior
	st.WaitForDevice, st.WaitTimeout = plan.WaitForDevice, plan.WaitTimeout
	if plan.WaitForDevice.ValueBool() && (st.DeviceID.IsNull() || st.DeviceID.ValueString() == "") && strings.HasPrefix(st.ID.ValueString(), "enr_") {
		r.wait(ctx, &st, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	want := plan.Name
	if want.IsUnknown() {
		want = types.StringNull()
	}
	r.settle(ctx, want, &st, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &st)...)
}

func (r *beaminDeviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var st beaminDeviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.ID.ValueString()
	deviceID := st.DeviceID.ValueString()
	if strings.HasPrefix(id, "enr_") && deviceID == "" {
		err := r.c.DeleteEnrolment(ctx, id)
		if err == nil {
			return
		}
		if !client.HasCode(err, "enrolment_used") {
			addAPIError(&resp.Diagnostics, "Cannot revoke the setup code", err)
			return
		}
		// the server registered meanwhile: remove the device instead
		e, gerr := r.c.GetEnrolment(ctx, id)
		if gerr != nil || e.DeviceID == nil {
			addAPIError(&resp.Diagnostics, "Cannot revoke the setup code", err)
			return
		}
		deviceID = *e.DeviceID
	}
	if deviceID == "" {
		deviceID = id
	}
	if err := r.c.DeleteDevice(ctx, deviceID); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot remove the device", err)
	}
}

// ImportState: by enrolment id (enr_...) or by device id (an existing Beam-in device).
func (r *beaminDeviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("hours"), int64(2))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("wait_for_device"), false)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("wait_timeout"), "15m")...)
	if strings.HasPrefix(req.ID, "enr_") {
		e, err := r.c.GetEnrolment(ctx, req.ID)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot read the enrolment", err)
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("label"), e.Label)...)
		if e.ExternalID != nil {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("external_id"), *e.ExternalID)...)
		}
	}
}

// durationValidator checks a Go duration ("15m", "1h30m").
type durationValidator struct{}

func (durationValidator) Description(context.Context) string {
	return "a Go duration such as 15m or 1h"
}
func (v durationValidator) MarkdownDescription(c context.Context) string { return v.Description(c) }
func (durationValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if d, err := time.ParseDuration(req.ConfigValue.ValueString()); err != nil || d <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid duration", "Use a Go duration such as 15m, 45s or 1h.")
	}
}
