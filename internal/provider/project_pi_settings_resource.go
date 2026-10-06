// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/oapi-codegen/nullable"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &ProjectPISettingsResource{}
	_ resource.ResourceWithConfigure      = &ProjectPISettingsResource{}
	_ resource.ResourceWithImportState    = &ProjectPISettingsResource{}
	_ resource.ResourceWithValidateConfig = &ProjectPISettingsResource{}
)

func NewProjectPISettingsResource() resource.Resource {
	return &ProjectPISettingsResource{}
}

// ProjectPISettingsResource manages a project's prompt-injection overrides
// through /v1/projects/{project_id}/pi-settings. There is one per project:
// create and update replace it with PUT, and delete resets it with DELETE.
type ProjectPISettingsResource struct {
	client *client.ClientWithResponses
}

// projectPISettingsResourceModel describes the resource data model. Every
// override is optional: null inherits the organization's value.
type projectPISettingsResourceModel struct {
	ID                     types.String  `tfsdk:"id"`
	ProjectID              types.String  `tfsdk:"project_id"`
	PiMode                 types.String  `tfsdk:"pi_mode"`
	PiIndirectMode         types.String  `tfsdk:"pi_indirect_mode"`
	PiOutputAction         types.String  `tfsdk:"pi_output_action"`
	PiBlockThreshold       types.Float64 `tfsdk:"pi_block_threshold"`
	PiTier2aBlockThreshold types.Float64 `tfsdk:"pi_tier2a_block_threshold"`
	PiTier2bBlockThreshold types.Float64 `tfsdk:"pi_tier2b_block_threshold"`
	PiFailClosed           types.Bool    `tfsdk:"pi_fail_closed"`
	PiAllowlistPatterns    types.List    `tfsdk:"pi_allowlist_patterns"`
	Effective              types.Object  `tfsdk:"effective"`
}

// piEffectiveAttrTypes are the attributes of effective: the overridable
// fields, after inheritance.
var piEffectiveAttrTypes = map[string]attr.Type{
	"pi_mode":                   types.StringType,
	"pi_indirect_mode":          types.StringType,
	"pi_output_action":          types.StringType,
	"pi_block_threshold":        types.Float64Type,
	"pi_tier2a_block_threshold": types.Float64Type,
	"pi_tier2b_block_threshold": types.Float64Type,
	"pi_fail_closed":            types.BoolType,
	"pi_allowlist_patterns":     types.ListType{ElemType: types.StringType},
}

func (r *ProjectPISettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_pi_settings"
}

func (r *ProjectPISettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	modes := []string{
		string(client.PIProtectionOverrideV1PiModeOff),
		string(client.PIProtectionOverrideV1PiModeAlert),
		string(client.PIProtectionOverrideV1PiModeBlock),
	}
	threshold := func(description string) schema.Float64Attribute {
		return schema.Float64Attribute{
			MarkdownDescription: description + " Between 0 and 1. Leave it out to inherit the organization's value.",
			Optional:            true,
			Validators:          []validator.Float64{float64validator.Between(0, 1)},
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a project's prompt-injection (PI) protection overrides. Each setting left out inherits " +
			"the organization's value. There is one per project: destroying it resets the project to inherit everything.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project's ID, the same as `project_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the project the overrides apply to. Changing it moves the overrides: " +
					"the old project is reset and the new one is set.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"pi_mode": schema.StringAttribute{
				MarkdownDescription: "Prompt-injection detection on the request: `off`, `alert` (score but never block) or " +
					"`block` (block when the score reaches `pi_block_threshold`). Leave it out to inherit the organization's value.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.OneOf(modes...)},
			},
			"pi_indirect_mode": schema.StringAttribute{
				MarkdownDescription: "Detection of indirect prompt injection: `off`, `alert` or `block`. " +
					"Leave it out to inherit the organization's value.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.OneOf(modes...)},
			},
			"pi_output_action": schema.StringAttribute{
				MarkdownDescription: "What to do when a response is flagged: `observe`, `redact`, `route`, `block` or `escalate`. " +
					"Leave it out to inherit the organization's value.",
				Optional: true,
				Validators: []validator.String{stringvalidator.OneOf(
					string(client.PIProtectionOverrideV1PiOutputActionObserve),
					string(client.PIProtectionOverrideV1PiOutputActionRedact),
					string(client.PIProtectionOverrideV1PiOutputActionRoute),
					string(client.PIProtectionOverrideV1PiOutputActionBlock),
					string(client.PIProtectionOverrideV1PiOutputActionEscalate),
				)},
			},
			"pi_block_threshold":        threshold("The Tier 1 score at or above which `block` mode blocks a request."),
			"pi_tier2a_block_threshold": threshold("The Tier 2A block threshold."),
			"pi_tier2b_block_threshold": threshold("The Tier 2B block threshold."),
			"pi_fail_closed": schema.BoolAttribute{
				MarkdownDescription: "Block requests when PI detection fails, instead of letting them through. " +
					"Leave it out to inherit the organization's value.",
				Optional: true,
			},
			"pi_allowlist_patterns": schema.ListAttribute{
				MarkdownDescription: "Patterns for text that is never flagged, kept in the order given. " +
					"`[]` overrides the organization's patterns with none; leave it out to inherit them.",
				ElementType: types.StringType,
				Optional:    true,
			},
			"effective": schema.SingleNestedAttribute{
				MarkdownDescription: "The settings the project ends up with: each override, or the organization's value where there is none.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"pi_mode":                   schema.StringAttribute{Computed: true, MarkdownDescription: "The effective `pi_mode`."},
					"pi_indirect_mode":          schema.StringAttribute{Computed: true, MarkdownDescription: "The effective `pi_indirect_mode`."},
					"pi_output_action":          schema.StringAttribute{Computed: true, MarkdownDescription: "The effective `pi_output_action`."},
					"pi_block_threshold":        schema.Float64Attribute{Computed: true, MarkdownDescription: "The effective `pi_block_threshold`."},
					"pi_tier2a_block_threshold": schema.Float64Attribute{Computed: true, MarkdownDescription: "The effective `pi_tier2a_block_threshold`."},
					"pi_tier2b_block_threshold": schema.Float64Attribute{Computed: true, MarkdownDescription: "The effective `pi_tier2b_block_threshold`."},
					"pi_fail_closed":            schema.BoolAttribute{Computed: true, MarkdownDescription: "The effective `pi_fail_closed`."},
					"pi_allowlist_patterns": schema.ListAttribute{
						Computed:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "The effective `pi_allowlist_patterns`.",
					},
				},
			},
		},
	}
}

func (r *ProjectPISettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig requires at least one override. Without any, the project
// inherits everything, which reads back the same as overrides reset outside
// Terraform, so every plan would create the resource again.
func (r *ProjectPISettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config projectPISettingsResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.hasOverride() {
		resp.Diagnostics.AddError("No PI settings overrides",
			"Set at least one pi_* attribute. To have the project inherit every organization setting, remove the resource instead.")
	}
}

func (r *ProjectPISettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectPISettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	body, diags := projectPISettingsRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateProjectPiSettingsWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to set project PI settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to set project PI settings", apiError(updated.StatusCode(), updated.Body))
		return
	}
	tflog.Trace(ctx, "set project PI settings", map[string]any{"project_id": id.String()})

	state, diags := projectPISettingsFromAPI(ctx, id.String(), updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectPISettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectPISettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetProjectPiSettingsWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project PI settings", err.Error())
		return
	}
	// The project was deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "project not found, removing its PI settings from state", map[string]any{"project_id": id.String()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read project PI settings", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags = projectPISettingsFromAPI(ctx, id.String(), got.JSON200)
	resp.Diagnostics.Append(diags...)
	// No overrides means they were reset outside Terraform (the config always
	// has at least one): drop it from state so the next plan sets them again.
	if !state.hasOverride() {
		tflog.Warn(ctx, "project PI settings were reset, removing them from state", map[string]any{"project_id": id.String()})
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectPISettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectPISettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	body, diags := projectPISettingsRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateProjectPiSettingsWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update project PI settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update project PI settings", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags := projectPISettingsFromAPI(ctx, id.String(), updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete resets the project to inherit every organization setting.
func (r *ProjectPISettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectPISettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteProjectPiSettingsWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to reset project PI settings", err.Error())
		return
	}
	// A deleted project has nothing left to reset.
	if deleted.StatusCode() != http.StatusOK && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to reset project PI settings", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports by project ID.
func (r *ProjectPISettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), req.ID)...)
}

// hasOverride reports whether any override is set (or unknown, in a config).
func (m projectPISettingsResourceModel) hasOverride() bool {
	return !m.PiMode.IsNull() || !m.PiIndirectMode.IsNull() || !m.PiOutputAction.IsNull() ||
		!m.PiBlockThreshold.IsNull() || !m.PiTier2aBlockThreshold.IsNull() || !m.PiTier2bBlockThreshold.IsNull() ||
		!m.PiFailClosed.IsNull() || !m.PiAllowlistPatterns.IsNull()
}

// projectPISettingsRequest builds the PUT body from the plan. PUT replaces
// the whole override, so it sends every override that is set; one left out
// (or removed from the config) goes back to inheriting.
func projectPISettingsRequest(ctx context.Context, plan projectPISettingsResourceModel) (client.PIProtectionOverrideV1, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.PIProtectionOverrideV1

	if !plan.PiMode.IsNull() {
		body.PiMode = nullable.NewNullableWithValue(client.PIProtectionOverrideV1PiMode(plan.PiMode.ValueString()))
	}
	if !plan.PiIndirectMode.IsNull() {
		body.PiIndirectMode = nullable.NewNullableWithValue(client.PIProtectionOverrideV1PiIndirectMode(plan.PiIndirectMode.ValueString()))
	}
	if !plan.PiOutputAction.IsNull() {
		body.PiOutputAction = nullable.NewNullableWithValue(client.PIProtectionOverrideV1PiOutputAction(plan.PiOutputAction.ValueString()))
	}
	if !plan.PiBlockThreshold.IsNull() {
		body.PiBlockThreshold = nullable.NewNullableWithValue(plan.PiBlockThreshold.ValueFloat64())
	}
	if !plan.PiTier2aBlockThreshold.IsNull() {
		body.PiTier2aBlockThreshold = nullable.NewNullableWithValue(plan.PiTier2aBlockThreshold.ValueFloat64())
	}
	if !plan.PiTier2bBlockThreshold.IsNull() {
		body.PiTier2bBlockThreshold = nullable.NewNullableWithValue(plan.PiTier2bBlockThreshold.ValueFloat64())
	}
	if !plan.PiFailClosed.IsNull() {
		body.PiFailClosed = nullable.NewNullableWithValue(plan.PiFailClosed.ValueBool())
	}
	if !plan.PiAllowlistPatterns.IsNull() {
		// Send [] rather than omitting it: an empty list overrides the org's patterns.
		patterns := []string{}
		diags.Append(plan.PiAllowlistPatterns.ElementsAs(ctx, &patterns, false)...)
		body.PiAllowlistPatterns = nullable.NewNullableWithValue(patterns)
	}
	return body, diags
}

// projectPISettingsFromAPI maps an API response to the Terraform model.
func projectPISettingsFromAPI(ctx context.Context, projectID string, r *client.ProjectPISettingsV1Response) (projectPISettingsResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	o := r.Override
	m := projectPISettingsResourceModel{
		ID:                     types.StringValue(projectID),
		ProjectID:              types.StringValue(projectID),
		PiMode:                 types.StringNull(),
		PiIndirectMode:         types.StringNull(),
		PiOutputAction:         types.StringNull(),
		PiBlockThreshold:       nullableFloat64(o.PiBlockThreshold),
		PiTier2aBlockThreshold: nullableFloat64(o.PiTier2aBlockThreshold),
		PiTier2bBlockThreshold: nullableFloat64(o.PiTier2bBlockThreshold),
		PiFailClosed:           types.BoolNull(),
		PiAllowlistPatterns:    types.ListNull(types.StringType),
	}
	if v, err := o.PiMode.Get(); err == nil {
		m.PiMode = types.StringValue(string(v))
	}
	if v, err := o.PiIndirectMode.Get(); err == nil {
		m.PiIndirectMode = types.StringValue(string(v))
	}
	if v, err := o.PiOutputAction.Get(); err == nil {
		m.PiOutputAction = types.StringValue(string(v))
	}
	if v, err := o.PiFailClosed.Get(); err == nil {
		m.PiFailClosed = types.BoolValue(v)
	}
	var d diag.Diagnostics
	if v, err := o.PiAllowlistPatterns.Get(); err == nil {
		if v == nil {
			v = []string{}
		}
		m.PiAllowlistPatterns, d = types.ListValueFrom(ctx, types.StringType, v)
		diags.Append(d...)
	}

	e := r.Effective
	patterns := []string{}
	if e.PiAllowlistPatterns != nil {
		patterns = *e.PiAllowlistPatterns
	}
	effectivePatterns, d := types.ListValueFrom(ctx, types.StringType, patterns)
	diags.Append(d...)
	m.Effective, d = types.ObjectValue(piEffectiveAttrTypes, map[string]attr.Value{
		"pi_mode":                   types.StringPointerValue(e.PiMode),
		"pi_indirect_mode":          types.StringPointerValue(e.PiIndirectMode),
		"pi_output_action":          types.StringPointerValue(e.PiOutputAction),
		"pi_block_threshold":        types.Float64PointerValue(e.PiBlockThreshold),
		"pi_tier2a_block_threshold": types.Float64PointerValue(e.PiTier2aBlockThreshold),
		"pi_tier2b_block_threshold": types.Float64PointerValue(e.PiTier2bBlockThreshold),
		"pi_fail_closed":            types.BoolPointerValue(e.PiFailClosed),
		"pi_allowlist_patterns":     effectivePatterns,
	})
	diags.Append(d...)
	return m, diags
}

func nullableFloat64(v nullable.Nullable[float64]) types.Float64 {
	if f, err := v.Get(); err == nil {
		return types.Float64Value(f)
	}
	return types.Float64Null()
}
