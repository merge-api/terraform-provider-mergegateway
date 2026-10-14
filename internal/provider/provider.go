// Package provider implements the Merge Gateway Terraform provider.
package provider

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

const (
	defaultBaseURL = "https://api-gateway.merge.dev"
	apiKeyEnvVar   = "MERGE_GATEWAY_MANAGEMENT_KEY"

	// requestTimeout bounds each API request so an unresponsive server cannot hang a plan or apply.
	requestTimeout = 60 * time.Second
)

// New returns a constructor for the provider. version is "dev" for local
// builds, "test" in tests and the release version in published binaries.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &mergeGatewayProvider{version: version}
	}
}

var _ provider.Provider = (*mergeGatewayProvider)(nil)

type mergeGatewayProvider struct {
	version string
}

// providerConfig holds the provider block's attributes.
type providerConfig struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

func (p *mergeGatewayProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "mergegateway"
	resp.Version = p.version
}

func (p *mergeGatewayProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Merge Gateway configuration as code.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "Merge Gateway management key. Can also be set with the `" + apiKeyEnvVar + "` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Merge Gateway API. Defaults to `" + defaultBaseURL + "`.",
				Optional:            true,
			},
		},
	}
}

// Configure builds the API client that every resource and data source uses.
func (p *mergeGatewayProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerConfig
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if cfg.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Unknown Merge Gateway API key",
			"The provider cannot create the Merge Gateway API client because api_key is unknown until apply. "+
				"Set it to a known value or use the "+apiKeyEnvVar+" environment variable.",
		)
	}
	if cfg.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("base_url"),
			"Unknown Merge Gateway base URL",
			"The provider cannot create the Merge Gateway API client because base_url is unknown until apply. "+
				"Set it to a known value or leave it unset to use "+defaultBaseURL+".",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// An empty string in config counts as unset.
	apiKey := cfg.APIKey.ValueString()
	if apiKey == "" {
		apiKey = os.Getenv(apiKeyEnvVar)
	}
	baseURL := cfg.BaseURL.ValueString()
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("base_url"),
			"Invalid Merge Gateway base URL",
			"base_url must be an http or https URL such as "+defaultBaseURL+", got: "+baseURL,
		)
		return
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Missing Merge Gateway API key",
			"Set api_key in the provider configuration or the "+apiKeyEnvVar+" environment variable.",
		)
		return
	}

	userAgent := "terraform-provider-mergegateway/" + p.version
	c, err := client.NewClientWithResponses(baseURL,
		client.WithHTTPClient(&http.Client{Timeout: requestTimeout}),
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+apiKey)
			r.Header.Set("User-Agent", userAgent)
			return nil
		}),
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to create Merge Gateway API client",
			"An unexpected error occurred when creating the Merge Gateway API client: "+err.Error(),
		)
		return
	}

	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *mergeGatewayProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewProjectResource,
		NewProjectPISettingsResource,
		NewProjectDLPSettingsResource,
		NewProjectControlsResource,
		NewRoutingPolicyResource,
		NewAPIKeyResource,
		NewCredentialResource,
		NewExportDestinationResource,
		NewOrganizationSettingsResource,
		NewOrganizationPISettingsResource,
		NewDLPRuleResource,
		NewBlocklistRuleResource,
	}
}

func (p *mergeGatewayProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewModelsDataSource,
		NewVendorsDataSource,
		NewGuardrailsDataSource,
	}
}
