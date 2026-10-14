package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/oapi-codegen/nullable"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &ProjectDLPSettingsResource{}
	_ resource.ResourceWithConfigure      = &ProjectDLPSettingsResource{}
	_ resource.ResourceWithImportState    = &ProjectDLPSettingsResource{}
	_ resource.ResourceWithValidateConfig = &ProjectDLPSettingsResource{}
)

func NewProjectDLPSettingsResource() resource.Resource {
	return &ProjectDLPSettingsResource{}
}

// ProjectDLPSettingsResource manages a project's DLP override through
// /v1/projects/{project_id}/dlp-settings. There is one per project: PUT
// replaces it and DELETE resets the project to the organization's DLP rules.
type ProjectDLPSettingsResource struct {
	client *client.ClientWithResponses
}

// projectDLPSettingsResourceModel describes the resource data model. The
// response's resolved entities list is left out: it changes with the
// organization's settings, so it would show diffs Terraform can't fix.
type projectDLPSettingsResourceModel struct {
	ID        types.String                             `tfsdk:"id"`
	ProjectID types.String                             `tfsdk:"project_id"`
	Override  map[string]projectDLPEntityOverrideModel `tfsdk:"override"`
}

type projectDLPEntityOverrideModel struct {
	Enabled types.Bool   `tfsdk:"enabled"`
	Action  types.String `tfsdk:"action"`
}

func (r *ProjectDLPSettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_dlp_settings"
}

func (r *ProjectDLPSettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Overrides the organization's DLP (data loss prevention) rules for one project, entity type by entity type. " +
			"A project has one set of DLP settings: creating this resource replaces any override the project already has, " +
			"and destroying it resets the project to the organization's rules.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project's ID, the same as `project_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the project. Changing it moves the override to another project.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"override": schema.MapNestedAttribute{
				MarkdownDescription: "The project's overrides, keyed by entity type (the `entity_type` values that " +
					"`GET /v1/projects/{project_id}/dlp-settings` lists). Each one sets `enabled`, `action` or both. " +
					"Entity types left out follow the organization's rules; removing one from the map removes its override.",
				Required:   true,
				Validators: []validator.Map{mapvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"enabled": schema.BoolAttribute{
							MarkdownDescription: "Whether the entity type is detected in this project. Leave it out to follow the organization.",
							Optional:            true,
						},
						"action": schema.StringAttribute{
							MarkdownDescription: "What happens when the entity type is detected: `log`, `redact` or `block`. " +
								"Leave it out to follow the organization.",
							Optional: true,
							Validators: []validator.String{stringvalidator.OneOf(
								string(client.DLPEntityOverrideV1ActionLog),
								string(client.DLPEntityOverrideV1ActionRedact),
								string(client.DLPEntityOverrideV1ActionBlock),
							)},
						},
					},
				},
			},
		},
	}
}

func (r *ProjectDLPSettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig catches at plan time what the API would reject or rewrite at apply.
func (r *ProjectDLPSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var projectID types.String
	var override types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project_id"), &projectID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("override"), &override)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !projectID.IsNull() && !projectID.IsUnknown() {
		if _, err := uuid.Parse(projectID.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("project_id"), "Invalid project ID",
				fmt.Sprintf("Expected a project UUID, got %q.", projectID.ValueString()))
		}
	}

	if override.IsNull() || override.IsUnknown() {
		return
	}
	// An entity with neither field overrides nothing. Skip entities not known yet.
	for entityType, v := range override.Elements() {
		obj, ok := v.(types.Object)
		if !ok || obj.IsUnknown() {
			continue
		}
		var e projectDLPEntityOverrideModel
		resp.Diagnostics.Append(obj.As(ctx, &e, basetypes.ObjectAsOptions{})...)
		if e.Enabled.IsNull() && e.Action.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("override").AtMapKey(entityType), "Empty DLP override",
				fmt.Sprintf("Set enabled, action or both for %q, or remove it to follow the organization's rules.", entityType))
		}
	}
}

func (r *ProjectDLPSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectDLPSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateProjectDlpSettingsWithResponse(ctx, id, projectDLPSettingsRequest(plan))
	if err != nil {
		resp.Diagnostics.AddError("Unable to create project DLP settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to create project DLP settings", apiError(updated.StatusCode(), updated.Body))
		return
	}
	tflog.Trace(ctx, "created project DLP settings", map[string]any{"project_id": id.String()})

	state := projectDLPSettingsFromAPI(plan.ProjectID, updated.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectDLPSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectDLPSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetProjectDlpSettingsWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project DLP settings", err.Error())
		return
	}
	// The project was deleted outside Terraform: drop the settings from state
	// so the next plan recreates them.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "project not found, removing its DLP settings from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read project DLP settings", apiError(got.StatusCode(), got.Body))
		return
	}
	// No override means it was reset outside Terraform: same as deleted.
	if len(got.JSON200.Override) == 0 {
		tflog.Warn(ctx, "project DLP override is empty, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	state = projectDLPSettingsFromAPI(state.ProjectID, got.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectDLPSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectDLPSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// PUT replaces the whole override, so entities removed from the map lose theirs.
	updated, err := r.client.UpdateProjectDlpSettingsWithResponse(ctx, id, projectDLPSettingsRequest(plan))
	if err != nil {
		resp.Diagnostics.AddError("Unable to update project DLP settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update project DLP settings", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state := projectDLPSettingsFromAPI(plan.ProjectID, updated.JSON200)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectDLPSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectDLPSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteProjectDlpSettingsWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete project DLP settings", err.Error())
		return
	}
	// A deleted project has no override left to reset.
	if deleted.StatusCode() != http.StatusOK && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete project DLP settings", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports by project ID, which is both id and project_id.
func (r *ProjectDLPSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resource.ImportStatePassthroughID(ctx, path.Root("project_id"), req, resp)
}

// projectDLPSettingsRequest builds the PUT body from the plan. A field left
// out of an entity is omitted, which follows the organization.
func projectDLPSettingsRequest(plan projectDLPSettingsResourceModel) client.ProjectDLPOverrideV1 {
	override := make(map[string]client.DLPEntityOverrideV1, len(plan.Override))
	for entityType, e := range plan.Override {
		var entity client.DLPEntityOverrideV1
		if !e.Enabled.IsNull() {
			entity.Enabled = nullable.NewNullableWithValue(e.Enabled.ValueBool())
		}
		if !e.Action.IsNull() {
			entity.Action = nullable.NewNullableWithValue(client.DLPEntityOverrideV1Action(e.Action.ValueString()))
		}
		override[entityType] = entity
	}
	return client.ProjectDLPOverrideV1{Override: &override}
}

// projectDLPSettingsFromAPI maps an API response to the Terraform model.
// projectID comes from the plan or state, so a UUID configured in another
// case or format is kept as configured.
func projectDLPSettingsFromAPI(projectID types.String, s *client.ProjectDLPSettingsV1Response) projectDLPSettingsResourceModel {
	override := make(map[string]projectDLPEntityOverrideModel, len(s.Override))
	for entityType, e := range s.Override {
		m := projectDLPEntityOverrideModel{Enabled: types.BoolNull(), Action: types.StringNull()}
		if enabled, err := e.Enabled.Get(); err == nil {
			m.Enabled = types.BoolValue(enabled)
		}
		if action, err := e.Action.Get(); err == nil {
			m.Action = types.StringValue(string(action))
		}
		override[entityType] = m
	}
	return projectDLPSettingsResourceModel{
		ID:        projectID,
		ProjectID: projectID,
		Override:  override,
	}
}
