package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

type (
	validatorString = validator.String
	validatorInt64  = validator.Int64
	diagnostics     = diag.Diagnostics
)

// rfc3339Validator checks an RFC 3339 time ("2027-06-30T00:00:00Z").
type rfc3339Validator struct{}

func (rfc3339Validator) Description(context.Context) string {
	return "an RFC 3339 time such as 2027-06-30T00:00:00Z"
}
func (v rfc3339Validator) MarkdownDescription(c context.Context) string { return v.Description(c) }
func (rfc3339Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid time", "Use RFC 3339, for example 2027-06-30T00:00:00Z.")
	}
}

// clientFrom reads the configured client from ProviderData (nil before the provider is configured).
func clientFrom(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", data))
		return nil
	}
	return c
}

// addAPIError adds err as an error diagnostic; API errors carry their code, hint and request id.
func addAPIError(diags *diag.Diagnostics, summary string, err error) {
	diags.AddError(summary, err.Error())
}

// notLive explains a 404 on a collection endpoint of the connection API (Beam-in codes, grants, active connections), which
// is being rolled out after the members and devices API.
func notLive(diags *diag.Diagnostics, what, path string, c *client.Client, err error) {
	diags.AddError(what+": the endpoint is not available",
		fmt.Sprintf("%s answered 404 for %s. Either this part of the Awacino API (the connection API: Beam-in codes, "+
			"access grants, active connections) is not live on this endpoint yet, or an id in the request does not exist.\n\n%s",
			c.Endpoint(), path, err.Error()))
}

func strOrNull(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func strOrNullEmpty(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func boolOr(p *bool, def bool) types.Bool {
	if p == nil {
		return types.BoolValue(def)
	}
	return types.BoolValue(*p)
}

func int64OrNull(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

func idOrNull(p *client.ID) types.String {
	if p == nil || *p == "" {
		return types.StringNull()
	}
	return types.StringValue(string(*p))
}

// ptr returns a pointer to the value of a known, non-null string (else nil).
func strPtr(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

func boolPtr(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

func int64Ptr(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	n := v.ValueInt64()
	return &n
}

// sameInstant is true when two RFC 3339 times are the same instant ("Z" vs "+00:00", fractions).
func sameInstant(a, b string) bool {
	if a == b {
		return true
	}
	ta, err1 := time.Parse(time.RFC3339Nano, a)
	tb, err2 := time.Parse(time.RFC3339Nano, b)
	return err1 == nil && err2 == nil && ta.Equal(tb)
}

// keepTime keeps the configured spelling of a time when the server answers the same instant.
func keepTime(prior types.String, server *string) types.String {
	if server == nil {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() && sameInstant(prior.ValueString(), *server) {
		return prior
	}
	return types.StringValue(*server)
}

// keepFold keeps the configured spelling of a case-insensitive value (an e-mail address).
func keepFold(prior types.String, server string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() && strings.EqualFold(prior.ValueString(), server) {
		return prior
	}
	return types.StringValue(server)
}

func stringList(ctx context.Context, in []string, diags *diag.Diagnostics) types.List {
	if in == nil {
		in = []string{}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, in)
	diags.Append(d...)
	return l
}

// ------------------------------------------------------------------ reach { kind, ports }

var reachAttrTypes = map[string]attr.Type{
	"kind":  types.StringType,
	"ports": types.ListType{ElemType: types.Int64Type},
}

func reachToObject(r *client.Reach, diags *diag.Diagnostics) types.Object {
	if r == nil {
		return types.ObjectNull(reachAttrTypes)
	}
	ports := types.ListNull(types.Int64Type)
	if len(r.Ports) > 0 {
		vals := make([]attr.Value, 0, len(r.Ports))
		for _, p := range r.Ports {
			vals = append(vals, types.Int64Value(p))
		}
		l, d := types.ListValue(types.Int64Type, vals)
		diags.Append(d...)
		ports = l
	}
	o, d := types.ObjectValue(reachAttrTypes, map[string]attr.Value{"kind": types.StringValue(r.Kind), "ports": ports})
	diags.Append(d...)
	return o
}

func reachFromObject(ctx context.Context, o types.Object, diags *diag.Diagnostics) client.Reach {
	var r client.Reach
	if o.IsNull() || o.IsUnknown() {
		return r
	}
	attrs := o.Attributes()
	if k, ok := attrs["kind"].(basetypes.StringValue); ok {
		r.Kind = k.ValueString()
	}
	if p, ok := attrs["ports"].(basetypes.ListValue); ok && !p.IsNull() && !p.IsUnknown() {
		var ports []int64
		diags.Append(p.ElementsAs(ctx, &ports, false)...)
		r.Ports = ports
	}
	return r
}

// sameReach: the server may list ports in another order or drop an empty list.
func sameReach(ctx context.Context, prior types.Object, server *client.Reach, diags *diag.Diagnostics) bool {
	if server == nil || prior.IsNull() || prior.IsUnknown() {
		return false
	}
	p := reachFromObject(ctx, prior, diags)
	if p.Kind != server.Kind || len(p.Ports) != len(server.Ports) {
		return false
	}
	seen := map[int64]int{}
	for _, x := range p.Ports {
		seen[x]++
	}
	for _, x := range server.Ports {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
