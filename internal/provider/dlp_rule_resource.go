package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/oapi-codegen/nullable"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &DLPRuleResource{}
	_ resource.ResourceWithConfigure      = &DLPRuleResource{}
	_ resource.ResourceWithImportState    = &DLPRuleResource{}
	_ resource.ResourceWithValidateConfig = &DLPRuleResource{}
)

func NewDLPRuleResource() resource.Resource {
	return &DLPRuleResource{}
}

// DLPRuleResource manages one of the organization's custom DLP rules through
// /v1/dlp-rules. Seeded rules can't be created or deleted, so they don't fit
// a resource and aren't supported.
type DLPRuleResource struct {
	client *client.ClientWithResponses
}

// dlpRuleResourceModel describes the resource data model.
type dlpRuleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	EntityType  types.String `tfsdk:"entity_type"`
	Description types.String `tfsdk:"description"`
	Pattern     types.String `tfsdk:"pattern"`
	Keywords    types.List   `tfsdk:"keywords"`
	Action      types.String `tfsdk:"action"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func (r *DLPRuleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dlp_rule"
}

func (r *DLPRuleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a custom data loss prevention (DLP) rule: a pattern or keywords the Gateway looks " +
			"for in requests, and what it does when one matches. The built-in (seeded) rules can't be created or deleted, " +
			"so this resource doesn't manage them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The rule's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"entity_type": schema.StringAttribute{
				MarkdownDescription: "The rule's name, unique among the organization's custom rules.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 80)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "A description of the rule. Defaults to none (`\"\"`).",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"pattern": schema.StringAttribute{
				MarkdownDescription: "A regular expression (Python syntax) the rule matches. Set `pattern`, `keywords` or both.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
			},
			"keywords": schema.ListAttribute{
				MarkdownDescription: "Words the rule matches, at most 50, each up to 100 characters, kept in the order given. " +
					"Set `pattern`, `keywords` or both.",
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.List{
					listvalidator.SizeBetween(1, 50),
					listvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 100)),
				},
			},
			"action": schema.StringAttribute{
				MarkdownDescription: "What to do when the rule matches: `log`, `redact` or `block`. Defaults to `log`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(string(client.DLPRuleV1CreateActionLog)),
				Validators: []validator.String{stringvalidator.OneOf(
					string(client.DLPRuleV1CreateActionLog),
					string(client.DLPRuleV1CreateActionRedact),
					string(client.DLPRuleV1CreateActionBlock),
				)},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the rule is on. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the rule was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the rule was last changed (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (r *DLPRuleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.ClientWithResponses, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = c
}

// ValidateConfig requires a pattern or keywords, as the API does.
func (r *DLPRuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config dlpRuleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Pattern.IsNull() && config.Keywords.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("pattern"), "Missing pattern or keywords",
			"A DLP rule needs a pattern, keywords or both.")
	}
}

func (r *DLPRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dlpRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := dlpRuleCreateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateDlpRuleWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create DLP rule", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create DLP rule", apiError(created.StatusCode(), created.Body))
		return
	}
	tflog.Trace(ctx, "created a DLP rule", map[string]any{"id": created.JSON201.Id.String()})

	state, diags := dlpRuleFromAPI(ctx, created.JSON201)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DLPRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dlpRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "DLP rule")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetDlpRuleWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read DLP rule", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "DLP rule not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read DLP rule", apiError(got.StatusCode(), got.Body))
		return
	}
	// Only reachable by importing a seeded rule's ID.
	if got.JSON200.IsSeeded {
		resp.Diagnostics.AddAttributeError(path.Root("id"), "Seeded DLP rule",
			fmt.Sprintf("%s is a seeded DLP rule (%s). mergegateway_dlp_rule manages custom rules only: "+
				"seeded rules can't be created or deleted.", state.ID.ValueString(), got.JSON200.EntityType))
		return
	}

	state, diags = dlpRuleFromAPI(ctx, got.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DLPRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dlpRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "DLP rule")
	resp.Diagnostics.Append(diags...)
	body, diags := dlpRuleUpdateRequest(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateDlpRuleWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update DLP rule", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update DLP rule", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = dlpRuleFromAPI(ctx, updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DLPRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dlpRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "DLP rule")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteDlpRuleWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete DLP rule", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete DLP rule", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports a custom rule by its ID.
func (r *DLPRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// dlpRuleCreateRequest builds the POST /v1/dlp-rules body from the plan.
func dlpRuleCreateRequest(ctx context.Context, plan dlpRuleResourceModel) (client.DLPRuleV1Create, diag.Diagnostics) {
	var diags diag.Diagnostics
	description := plan.Description.ValueString()
	action := client.DLPRuleV1CreateAction(plan.Action.ValueString())
	enabled := plan.Enabled.ValueBool()
	body := client.DLPRuleV1Create{
		EntityType:  plan.EntityType.ValueString(),
		Description: &description,
		Pattern:     optionalString(plan.Pattern),
		Action:      &action,
		Enabled:     &enabled,
	}
	if !plan.Keywords.IsNull() {
		var keywords []string
		diags.Append(plan.Keywords.ElementsAs(ctx, &keywords, false)...)
		body.Keywords = nullable.NewNullableWithValue(keywords)
	}
	return body, diags
}

// dlpRuleUpdateRequest builds the PATCH /v1/dlp-rules/{rule_id} body. It sends
// only the fields that differ between plan and state. The API reads null as
// "keep", so a removed pattern is sent as "" and removed keywords as [], which
// clear them.
func dlpRuleUpdateRequest(ctx context.Context, plan, state dlpRuleResourceModel) (client.DLPRuleV1Update, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.DLPRuleV1Update

	if !plan.EntityType.Equal(state.EntityType) {
		body.EntityType = nullable.NewNullableWithValue(plan.EntityType.ValueString())
	}
	if !plan.Description.Equal(state.Description) {
		body.Description = nullable.NewNullableWithValue(plan.Description.ValueString())
	}
	if !plan.Pattern.Equal(state.Pattern) {
		body.Pattern = nullable.NewNullableWithValue(plan.Pattern.ValueString())
	}
	if !plan.Keywords.Equal(state.Keywords) {
		keywords := []string{}
		if !plan.Keywords.IsNull() {
			diags.Append(plan.Keywords.ElementsAs(ctx, &keywords, false)...)
		}
		body.Keywords = nullable.NewNullableWithValue(keywords)
	}
	if !plan.Action.Equal(state.Action) {
		body.Action = nullable.NewNullableWithValue(client.DLPRuleV1UpdateAction(plan.Action.ValueString()))
	}
	if !plan.Enabled.Equal(state.Enabled) {
		body.Enabled = nullable.NewNullableWithValue(plan.Enabled.ValueBool())
	}
	return body, diags
}

// dlpRuleFromAPI maps an API rule to the Terraform model. A cleared pattern
// ("") or keywords ([]) reads as null, the same as one never set.
func dlpRuleFromAPI(ctx context.Context, r *client.DLPRuleV1Response) (dlpRuleResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := dlpRuleResourceModel{
		ID:          types.StringValue(r.Id.String()),
		EntityType:  types.StringValue(r.EntityType),
		Description: types.StringValue(r.Description),
		Pattern:     types.StringNull(),
		Keywords:    types.ListNull(types.StringType),
		Action:      types.StringValue(string(r.Action)),
		Enabled:     types.BoolValue(r.Enabled),
		CreatedAt:   types.StringValue(r.CreatedAt.Format(time.RFC3339)),
		UpdatedAt:   types.StringValue(r.UpdatedAt.Format(time.RFC3339)),
	}
	if pattern, err := r.Pattern.Get(); err == nil && pattern != "" {
		m.Pattern = types.StringValue(pattern)
	}
	if keywords, err := r.Keywords.Get(); err == nil && len(keywords) > 0 {
		var d diag.Diagnostics
		m.Keywords, d = types.ListValueFrom(ctx, types.StringType, keywords)
		diags.Append(d...)
	}
	return m, diags
}
