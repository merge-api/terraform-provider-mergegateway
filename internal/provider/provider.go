// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

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

// Ensure MergeGatewayProvider satisfies various provider interfaces.
var _ provider.Provider = &MergeGatewayProvider{}

// MergeGatewayProvider defines the provider implementation.
type MergeGatewayProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// MergeGatewayProviderModel describes the provider data model.
type MergeGatewayProviderModel struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

func (p *MergeGatewayProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "mergegateway"
	resp.Version = p.version
}

func (p *MergeGatewayProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
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

func (p *MergeGatewayProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data MergeGatewayProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if data.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_key"),
			"Unknown Merge Gateway API key",
			"The provider cannot create the Merge Gateway API client because api_key is unknown until apply. "+
				"Set it to a known value or use the "+apiKeyEnvVar+" environment variable.",
		)
	}

	if data.BaseURL.IsUnknown() {
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

	apiKey := os.Getenv(apiKeyEnvVar)
	if data.APIKey.ValueString() != "" {
		apiKey = data.APIKey.ValueString()
	}

	baseURL := defaultBaseURL
	if data.BaseURL.ValueString() != "" {
		baseURL = data.BaseURL.ValueString()
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
		client.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("User-Agent", userAgent)
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

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *MergeGatewayProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewProjectResource,
	}
}

func (p *MergeGatewayProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &MergeGatewayProvider{
			version: version,
		}
	}
}
