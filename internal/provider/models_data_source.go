// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ datasource.DataSource              = &ModelsDataSource{}
	_ datasource.DataSourceWithConfigure = &ModelsDataSource{}
)

func NewModelsDataSource() datasource.DataSource {
	return &ModelsDataSource{}
}

// ModelsDataSource lists the catalog models the organization can route to,
// through /v1/organization/models.
type ModelsDataSource struct {
	client *client.ClientWithResponses
}

// modelsDataSourceModel describes the data source data model.
type modelsDataSourceModel struct {
	MinContextLength types.Int64  `tfsdk:"min_context_length"`
	InputModality    types.String `tfsdk:"input_modality"`
	OutputModality   types.String `tfsdk:"output_modality"`
	Models           []modelModel `tfsdk:"models"`
}

// modelModel is one model.
type modelModel struct {
	ID                   types.String  `tfsdk:"id"`
	ModelProvider        types.String  `tfsdk:"model_provider"`
	DisplayName          types.String  `tfsdk:"display_name"`
	ContextWindow        types.Int64   `tfsdk:"context_window"`
	MaxOutputTokens      types.Int64   `tfsdk:"max_output_tokens"`
	InputModalities      types.Set     `tfsdk:"input_modalities"`
	OutputModalities     types.Set     `tfsdk:"output_modalities"`
	InputCostPerMillion  types.Float64 `tfsdk:"input_cost_per_million"`
	OutputCostPerMillion types.Float64 `tfsdk:"output_cost_per_million"`
	Status               types.String  `tfsdk:"status"`
	ExpirationDate       types.String  `tfsdk:"expiration_date"`
	ReplacementModel     types.String  `tfsdk:"replacement_model"`
}

// maxPageSize is the most items a list request may ask for.
const maxPageSize = 100

func (d *ModelsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_models"
}

func (d *ModelsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the catalog models the organization can route to, optionally filtered.",
		Attributes: map[string]schema.Attribute{
			"min_context_length": schema.Int64Attribute{
				MarkdownDescription: "Only models whose context window is at least this many tokens.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"input_modality": schema.StringAttribute{
				MarkdownDescription: "Only models that accept this input, such as `image`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"output_modality": schema.StringAttribute{
				MarkdownDescription: "Only models that return this output, such as `audio`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"models": schema.ListNestedAttribute{
				MarkdownDescription: "The models, in the API's order.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "The model's `provider/model` ID, the value to send as `model` in a request.",
							Computed:            true,
						},
						"model_provider": schema.StringAttribute{
							MarkdownDescription: "The model's creator, such as `openai`. (Terraform reserves the name `provider`.)",
							Computed:            true,
						},
						"display_name":      schema.StringAttribute{MarkdownDescription: "The model's name for display.", Computed: true},
						"context_window":    schema.Int64Attribute{MarkdownDescription: "The maximum input context, in tokens.", Computed: true},
						"max_output_tokens": schema.Int64Attribute{MarkdownDescription: "The most tokens a response can have.", Computed: true},
						"input_modalities": schema.SetAttribute{
							MarkdownDescription: "What the model accepts, such as `text` and `image`.",
							ElementType:         types.StringType,
							Computed:            true,
						},
						"output_modalities": schema.SetAttribute{
							MarkdownDescription: "What the model returns.",
							ElementType:         types.StringType,
							Computed:            true,
						},
						"input_cost_per_million": schema.Float64Attribute{
							MarkdownDescription: "The list price per million input tokens, in USD.",
							Computed:            true,
						},
						"output_cost_per_million": schema.Float64Attribute{
							MarkdownDescription: "The list price per million output tokens, in USD.",
							Computed:            true,
						},
						"status": schema.StringAttribute{
							MarkdownDescription: "`available`, `beta` or `deprecated`.",
							Computed:            true,
						},
						"expiration_date": schema.StringAttribute{
							MarkdownDescription: "The day the model is retired (YYYY-MM-DD). Null when no date is set.",
							Computed:            true,
						},
						"replacement_model": schema.StringAttribute{
							MarkdownDescription: "The model to move to once this one is retired.",
							Computed:            true,
						},
					},
				},
			},
		},
	}
}

func (d *ModelsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data modelsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	limit := maxPageSize
	params := client.ListModelsParams{
		InputModality:  data.InputModality.ValueStringPointer(),
		OutputModality: data.OutputModality.ValueStringPointer(),
		Limit:          &limit,
	}
	if !data.MinContextLength.IsNull() {
		minContextLength := int(data.MinContextLength.ValueInt64())
		params.MinContextLength = &minContextLength
	}

	data.Models = []modelModel{}
	for {
		page, err := d.client.ListModelsWithResponse(ctx, &params)
		if err != nil {
			resp.Diagnostics.AddError("Unable to list models", err.Error())
			return
		}
		if page.JSON200 == nil {
			resp.Diagnostics.AddError("Unable to list models", apiError(page.StatusCode(), page.Body))
			return
		}
		for _, m := range page.JSON200.Results {
			model, diags := modelFromAPI(ctx, m)
			resp.Diagnostics.Append(diags...)
			data.Models = append(data.Models, model)
		}
		next, err := page.JSON200.Next.Get()
		if err != nil {
			break
		}
		params.Cursor = &next
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// modelFromAPI maps an API model to the Terraform model.
func modelFromAPI(ctx context.Context, m client.ModelV1Response) (modelModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	model := modelModel{
		ID:                   types.StringValue(m.Id),
		ModelProvider:        types.StringValue(m.Provider),
		DisplayName:          types.StringValue(m.DisplayName),
		ContextWindow:        types.Int64Value(int64(m.ContextWindow)),
		MaxOutputTokens:      types.Int64Value(int64(m.MaxOutputTokens)),
		InputCostPerMillion:  types.Float64Value(m.InputCostPerMillion),
		OutputCostPerMillion: types.Float64Value(m.OutputCostPerMillion),
		Status:               types.StringValue(string(m.Status)),
		ExpirationDate:       types.StringNull(),
		ReplacementModel:     nullableString(m.ReplacementModel),
	}
	if date, err := m.ExpirationDate.Get(); err == nil {
		model.ExpirationDate = types.StringValue(date.String())
	}
	var d diag.Diagnostics
	model.InputModalities, d = types.SetValueFrom(ctx, types.StringType, nonNil(m.InputModalities))
	diags.Append(d...)
	model.OutputModalities, d = types.SetValueFrom(ctx, types.StringType, nonNil(m.OutputModalities))
	diags.Append(d...)
	return model, diags
}

// nonNil returns values, or [] for nil, so an empty list isn't null.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
