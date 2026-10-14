// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ datasource.DataSource              = &GuardrailsDataSource{}
	_ datasource.DataSourceWithConfigure = &GuardrailsDataSource{}
)

func NewGuardrailsDataSource() datasource.DataSource {
	return &GuardrailsDataSource{}
}

// GuardrailsDataSource lists the guardrails in force for every project
// through /v1/guardrails. It shows the effective values only: what each
// project overrides is what the project_* resources manage.
type GuardrailsDataSource struct {
	client *client.ClientWithResponses
}

// guardrailsDataSourceModel describes the data source data model.
type guardrailsDataSourceModel struct {
	Guardrails []guardrailModel `tfsdk:"guardrails"`
}

// guardrailModel is one project's effective guardrails.
type guardrailModel struct {
	ProjectID   types.String              `tfsdk:"project_id"`
	ProjectName types.String              `tfsdk:"project_name"`
	Controls    guardrailControlsModel    `tfsdk:"controls"`
	PISettings  types.Object              `tfsdk:"pi_settings"`
	DLPEntities []guardrailDLPEntityModel `tfsdk:"dlp_entities"`
}

// guardrailControlsModel is a project's effective controls.
type guardrailControlsModel struct {
	AllowedRegions          types.List `tfsdk:"allowed_regions"`
	IgnoredRegions          types.List `tfsdk:"ignored_regions"`
	AllowedVendors          types.List `tfsdk:"allowed_vendors"`
	IgnoredVendors          types.List `tfsdk:"ignored_vendors"`
	VendorRoutingPreference types.List `tfsdk:"vendor_routing_preference"`
	ZeroDataRetention       types.Bool `tfsdk:"zero_data_retention"`
}

// guardrailDLPEntityModel is how a project treats one DLP entity type.
type guardrailDLPEntityModel struct {
	EntityType      types.String `tfsdk:"entity_type"`
	Category        types.String `tfsdk:"category"`
	EffectiveAction types.String `tfsdk:"effective_action"`
	Overridden      types.Bool   `tfsdk:"overridden"`
}

func (d *GuardrailsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrails"
}

func (d *GuardrailsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	list := func(description string) schema.ListAttribute {
		return schema.ListAttribute{MarkdownDescription: description, ElementType: types.StringType, Computed: true}
	}
	piSetting := func(name string) string { return "The effective `" + name + "`." }

	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the guardrails in force for every project: its controls, prompt-injection (PI) settings " +
			"and DLP rules, after applying the project's overrides to the organization's settings.",
		Attributes: map[string]schema.Attribute{
			"guardrails": schema.ListNestedAttribute{
				MarkdownDescription: "One entry per project, in the API's order.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"project_id":   schema.StringAttribute{MarkdownDescription: "The project's ID.", Computed: true},
						"project_name": schema.StringAttribute{MarkdownDescription: "The project's name.", Computed: true},
						"controls": schema.SingleNestedAttribute{
							MarkdownDescription: "The vendor, region and zero data retention controls in force.",
							Computed:            true,
							Attributes: map[string]schema.Attribute{
								"allowed_regions":           list("The only regions requests may be served from; empty for any."),
								"ignored_regions":           list("The regions requests are never served from."),
								"allowed_vendors":           list("The only vendors requests may be served by; empty for any."),
								"ignored_vendors":           list("The vendors requests are never served by."),
								"vendor_routing_preference": list("The vendors to prefer, most preferred first."),
								"zero_data_retention": schema.BoolAttribute{
									MarkdownDescription: "Whether requests go only to vendors that retain no data.",
									Computed:            true,
								},
							},
						},
						"pi_settings": schema.SingleNestedAttribute{
							MarkdownDescription: "The prompt-injection settings in force.",
							Computed:            true,
							Attributes: map[string]schema.Attribute{
								"pi_mode":                   schema.StringAttribute{Computed: true, MarkdownDescription: piSetting("pi_mode")},
								"pi_indirect_mode":          schema.StringAttribute{Computed: true, MarkdownDescription: piSetting("pi_indirect_mode")},
								"pi_output_action":          schema.StringAttribute{Computed: true, MarkdownDescription: piSetting("pi_output_action")},
								"pi_block_threshold":        schema.Float64Attribute{Computed: true, MarkdownDescription: piSetting("pi_block_threshold")},
								"pi_tier2a_block_threshold": schema.Float64Attribute{Computed: true, MarkdownDescription: piSetting("pi_tier2a_block_threshold")},
								"pi_tier2b_block_threshold": schema.Float64Attribute{Computed: true, MarkdownDescription: piSetting("pi_tier2b_block_threshold")},
								"pi_fail_closed":            schema.BoolAttribute{Computed: true, MarkdownDescription: piSetting("pi_fail_closed")},
								"pi_allowlist_patterns": schema.ListAttribute{
									Computed:            true,
									ElementType:         types.StringType,
									MarkdownDescription: piSetting("pi_allowlist_patterns"),
								},
							},
						},
						"dlp_entities": schema.ListNestedAttribute{
							MarkdownDescription: "How the project treats each DLP entity type, seeded and custom.",
							Computed:            true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"entity_type": schema.StringAttribute{MarkdownDescription: "The entity type, such as `EMAIL_ADDRESS`.", Computed: true},
									"category": schema.StringAttribute{
										MarkdownDescription: "`custom` for the organization's own rules; `global` and `usa` are seeded.",
										Computed:            true,
									},
									"effective_action": schema.StringAttribute{
										MarkdownDescription: "What happens when it matches: `log`, `redact` or `block`. Null when the rule is off.",
										Computed:            true,
									},
									"overridden": schema.BoolAttribute{
										MarkdownDescription: "Whether the project overrides the organization's rule.",
										Computed:            true,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *GuardrailsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.ClientWithResponses, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.client = c
}

func (d *GuardrailsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	limit := maxPageSize
	params := client.ListGuardrailsParams{Limit: &limit}
	data := guardrailsDataSourceModel{Guardrails: []guardrailModel{}}
	for {
		page, err := d.client.ListGuardrailsWithResponse(ctx, &params)
		if err != nil {
			resp.Diagnostics.AddError("Unable to list guardrails", err.Error())
			return
		}
		if page.JSON200 == nil {
			resp.Diagnostics.AddError("Unable to list guardrails", apiError(page.StatusCode(), page.Body))
			return
		}
		for _, g := range page.JSON200.Results {
			guardrail, diags := guardrailFromAPI(ctx, g)
			resp.Diagnostics.Append(diags...)
			data.Guardrails = append(data.Guardrails, guardrail)
		}
		next, err := page.JSON200.Next.Get()
		if err != nil {
			break
		}
		params.Cursor = &next
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// guardrailFromAPI maps a project's guardrails to the Terraform model. The
// PI settings reuse mergegateway_project_pi_settings' effective block.
func guardrailFromAPI(ctx context.Context, g client.GuardrailV1Response) (guardrailModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	e := g.Controls.Effective
	pi, d := projectPISettingsFromAPI(ctx, g.ProjectId.String(), &g.PiSettings)
	diags.Append(d...)

	m := guardrailModel{
		ProjectID:   types.StringValue(g.ProjectId.String()),
		ProjectName: types.StringValue(g.ProjectName),
		Controls: guardrailControlsModel{
			AllowedRegions:          stringsFromAPI(ctx, &e.AllowedRegions, &diags),
			IgnoredRegions:          stringsFromAPI(ctx, &e.IgnoredRegions, &diags),
			AllowedVendors:          stringsFromAPI(ctx, &e.AllowedVendors, &diags),
			IgnoredVendors:          stringsFromAPI(ctx, &e.IgnoredVendors, &diags),
			VendorRoutingPreference: stringsFromAPI(ctx, &e.VendorRoutingPreference, &diags),
			ZeroDataRetention:       types.BoolValue(e.ZeroDataRetention),
		},
		PISettings:  pi.Effective,
		DLPEntities: make([]guardrailDLPEntityModel, 0, len(g.DlpSettings.Entities)),
	}
	for _, entity := range g.DlpSettings.Entities {
		m.DLPEntities = append(m.DLPEntities, guardrailDLPEntityModel{
			EntityType:      types.StringValue(entity.EntityType),
			Category:        types.StringValue(entity.Category),
			EffectiveAction: nullableString(entity.EffectiveAction),
			Overridden:      types.BoolValue(entity.Overridden),
		})
	}
	return m, diags
}
