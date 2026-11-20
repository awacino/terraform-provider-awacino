package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// reachAttribute is `reach = { kind = "network" | "device" | "ports", ports = [22, 443] }`.
func reachAttribute(desc string, mods []planmodifier.Object) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: desc,
		PlanModifiers:       mods,
		Validators:          []validator.Object{reachPortsValidator{}},
		Attributes: map[string]schema.Attribute{
			"kind": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "`network` - the device and the whole network it sits in; `device` - this machine only; " +
					"`ports` - only `ports` on this machine.",
				Validators: []validator.String{stringvalidator.OneOf("network", "device", "ports")},
			},
			"ports": schema.ListAttribute{
				Optional: true, ElementType: types.Int64Type,
				MarkdownDescription: "With `kind = \"ports\"`: the TCP / UDP ports (1-65535, at most 16).",
				Validators: []validator.List{
					listvalidator.SizeBetween(1, 16),
					listvalidator.ValueInt64sAre(int64validator.Between(1, 65535)),
				},
			},
		},
	}
}

// reachPortsValidator: ports are required for kind "ports" and not allowed otherwise.
type reachPortsValidator struct{}

func (reachPortsValidator) Description(context.Context) string {
	return "ports must be set exactly when kind is \"ports\""
}
func (v reachPortsValidator) MarkdownDescription(c context.Context) string { return v.Description(c) }
func (reachPortsValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	a := req.ConfigValue.Attributes()
	kind, _ := a["kind"].(basetypes.StringValue)
	ports, _ := a["ports"].(basetypes.ListValue)
	if kind.IsUnknown() || ports.IsUnknown() {
		return
	}
	hasPorts := !ports.IsNull() && len(ports.Elements()) > 0
	if kind.ValueString() == "ports" && !hasPorts {
		resp.Diagnostics.AddAttributeError(req.Path.AtName("ports"), "Missing ports", "reach.kind = \"ports\" needs a ports list.")
	}
	if kind.ValueString() != "ports" && hasPorts {
		resp.Diagnostics.AddAttributeError(req.Path.AtName("ports"), "Ports not allowed",
			"ports are only used with reach.kind = \"ports\".")
	}
}
