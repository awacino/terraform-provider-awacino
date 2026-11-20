// Package provider is the Awacino Terraform provider (terraform-plugin-framework, protocol 6).
package provider

import (
	"context"
	"os"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &awacinoProvider{}

type awacinoProvider struct {
	version string
}

// New returns the provider factory for providerserver.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &awacinoProvider{version: version} }
}

type providerModel struct {
	KeyID     types.String `tfsdk:"key_id"`
	KeySecret types.String `tfsdk:"key_secret"`
	Endpoint  types.String `tfsdk:"endpoint"`
}

func (p *awacinoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "awacino"
	resp.Version = p.version
}

func (p *awacinoProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The official provider for the [Awacino API](https://www.awacino.com): members and their " +
			"invitation codes, Beam-in devices (server enrolment), access grants and Beam-in codes of an Awacino company account.\n\n" +
			"Create an API key in the Awacino app (**Company settings > Awacino API**, MAIN-ADMIN or ADMIN). Tick the sections " +
			"the configuration needs: **Users** (members, invitations) and **Connections** (devices, enrolments, Beam-in codes, " +
			"access grants), each with Read and Write. The provider exchanges the key for a 15-minute bearer token " +
			"(`POST /v1/oauth/token`) and renews it before it ends.",
		Attributes: map[string]schema.Attribute{
			"key_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "API key id (`awk_...`). Can also be set with the `AWACINO_KEY_ID` environment variable.",
			},
			"key_secret": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "API key secret (`aws_...`, shown once when the key is created). Can also be set with the `AWACINO_KEY_SECRET` environment variable.",
			},
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "API base URL. Default `https://api.awacino.net`; the `AWACINO_ENDPOINT` environment variable overrides the default.",
			},
		},
	}
}

func (p *awacinoProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, v := range map[string]types.String{"key_id": cfg.KeyID, "key_secret": cfg.KeySecret, "endpoint": cfg.Endpoint} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown Awacino provider setting",
				"The provider cannot be configured with a value that is only known after apply. Set "+name+
					" statically or with its environment variable.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	pick := func(v types.String, env string) string {
		if !v.IsNull() && v.ValueString() != "" {
			return v.ValueString()
		}
		return os.Getenv(env)
	}
	keyID := pick(cfg.KeyID, "AWACINO_KEY_ID")
	secret := pick(cfg.KeySecret, "AWACINO_KEY_SECRET")
	endpoint := pick(cfg.Endpoint, "AWACINO_ENDPOINT")
	if keyID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("key_id"), "Missing Awacino API key id",
			"Set key_id in the provider block or the AWACINO_KEY_ID environment variable. An admin creates keys in the "+
				"Awacino app: Company settings > Awacino API.")
	}
	if secret == "" {
		resp.Diagnostics.AddAttributeError(path.Root("key_secret"), "Missing Awacino API key secret",
			"Set key_secret in the provider block or the AWACINO_KEY_SECRET environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := client.New(client.Config{
		Endpoint:  endpoint,
		KeyID:     keyID,
		KeySecret: secret,
		UserAgent: "terraform-provider-awacino/" + p.version + " (+https://registry.terraform.io/providers/awacino/awacino)",
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid Awacino provider configuration", err.Error())
		return
	}
	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *awacinoProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewMemberResource,
		NewInvitationResource,
		NewBeaminDeviceResource,
		NewAccessGrantResource,
		NewBeaminCodeResource,
	}
}

func (p *awacinoProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCompanyDataSource,
		NewMembersDataSource,
		NewMemberDataSource,
		NewDevicesDataSource,
		NewDeviceDataSource,
		NewActiveConnectionsDataSource,
		NewAPIKeyDataSource,
	}
}
