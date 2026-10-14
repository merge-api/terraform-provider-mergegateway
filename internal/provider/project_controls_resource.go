package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	_ resource.Resource                   = &ProjectControlsResource{}
	_ resource.ResourceWithConfigure      = &ProjectControlsResource{}
	_ resource.ResourceWithImportState    = &ProjectControlsResource{}
	_ resource.ResourceWithValidateConfig = &ProjectControlsResource{}
)

func NewProjectControlsResource() resource.Resource {
	return &ProjectControlsResource{}
}

// ProjectControlsResource manages a project's vendor, region and zero data
// retention overrides through /v1/projects/{project_id}/controls. The
// override is a singleton per project: create and update replace it with a
// PUT, and delete clears it so the project inherits the organization's controls.
type ProjectControlsResource struct {
	client *client.ClientWithResponses
}

// projectControlsResourceModel describes the resource data model. A null
// attribute is not overridden: the project inherits the organization's value.
type projectControlsResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	ProjectID               types.String `tfsdk:"project_id"`
	AllowedRegions          types.List   `tfsdk:"allowed_regions"`
	IgnoredRegions          types.List   `tfsdk:"ignored_regions"`
	AllowedVendors          types.List   `tfsdk:"allowed_vendors"`
	IgnoredVendors          types.List   `tfsdk:"ignored_vendors"`
	VendorRoutingPreference types.List   `tfsdk:"vendor_routing_preference"`
	ZeroDataRetention       types.Bool   `tfsdk:"zero_data_retention"`
}

func (r *ProjectControlsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_controls"
}

// controlsListAttribute is one of the override's lists. Lists, not sets: the
// API keeps them as sent, and vendor_routing_preference is an order.
func controlsListAttribute(description string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: description,
		ElementType:         types.StringType,
		Optional:            true,
		Validators:          []validator.List{listvalidator.NoNullValues(), listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
	}
}

func (r *ProjectControlsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	const regionsUnit = " A region is any active vendor's `country_code` or `geo_region`, such as `US`, `CA` or `NA`, " +
		"and is case-sensitive; see the `mergegateway_vendors` data source." +
		" Regions are overridden together: setting either `allowed_regions` or `ignored_regions` " +
		"overrides both, and the one left out is empty rather than the organization's."
	const vendorsUnit = " Vendors are overridden together: setting any of `allowed_vendors`, `ignored_vendors` or " +
		"`vendor_routing_preference` overrides all three, and those left out are empty rather than the organization's."

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Merge Gateway project's controls: the vendors, regions and zero data retention setting " +
			"that override the organization's for this project. Leave an attribute out to inherit the organization's value. " +
			"Destroying the resource removes every override, so the project inherits all of the organization's controls again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project's ID, the same as `project_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the project whose controls to manage. Changing it moves the overrides to the other project.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"allowed_regions":           controlsListAttribute("The regions requests may be served from." + regionsUnit),
			"ignored_regions":           controlsListAttribute("The regions requests are never served from." + regionsUnit),
			"allowed_vendors":           controlsListAttribute("The vendors requests may be served by." + vendorsUnit),
			"ignored_vendors":           controlsListAttribute("The vendors requests are never served by." + vendorsUnit),
			"vendor_routing_preference": controlsListAttribute("The vendors to prefer, most preferred first. When `allowed_vendors` is not empty, each must be in it." + vendorsUnit),
			"zero_data_retention": schema.BoolAttribute{
				MarkdownDescription: "Whether requests may only go to vendors that retain no data. Leave it out to inherit the organization's setting.",
				Optional:            true,
			},
		},
	}
}

func (r *ProjectControlsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig catches at plan time what the API would reject at apply.
func (r *ProjectControlsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var allowed, preference types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("allowed_vendors"), &allowed)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("vendor_routing_preference"), &preference)...)
	if resp.Diagnostics.HasError() || allowed.IsUnknown() || len(allowed.Elements()) == 0 {
		return
	}

	// The API only takes preferred vendors that are allowed.
	allowedSet := map[string]bool{}
	for _, v := range allowed.Elements() {
		s, ok := v.(types.String)
		if !ok || s.IsUnknown() {
			return
		}
		allowedSet[s.ValueString()] = true
	}
	for _, v := range preference.Elements() {
		if s, ok := v.(types.String); ok && !s.IsUnknown() && !s.IsNull() && !allowedSet[s.ValueString()] {
			resp.Diagnostics.AddAttributeError(path.Root("vendor_routing_preference"), "Preferred vendor not allowed",
				fmt.Sprintf("%q is not in allowed_vendors. Preferred vendors must be allowed.", s.ValueString()))
		}
	}
}

func (r *ProjectControlsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectControlsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	body, diags := projectControlsRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateProjectControlsWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create project controls", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to create project controls", apiError(updated.StatusCode(), updated.Body))
		return
	}
	tflog.Trace(ctx, "set project controls", map[string]any{"project_id": id.String()})

	state, diags := projectControlsFromAPI(ctx, updated.JSON200, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectControlsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectControlsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetProjectControlsWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project controls", err.Error())
		return
	}
	// The project was deleted outside Terraform: drop the controls from state
	// so the next plan creates them again.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "project not found, removing its controls from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read project controls", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags = projectControlsFromAPI(ctx, got.JSON200, state)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectControlsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectControlsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	// PUT replaces the whole override, so the body is the whole plan, not
	// only what changed: a field left out of it stops being overridden.
	body, diags := projectControlsRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateProjectControlsWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update project controls", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update project controls", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = projectControlsFromAPI(ctx, updated.JSON200, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete clears the override: the project inherits the organization's controls.
func (r *ProjectControlsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectControlsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteProjectControlsWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete project controls", err.Error())
		return
	}
	// A deleted project has no controls left to clear.
	if deleted.StatusCode() != http.StatusOK && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete project controls", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports by project ID; Read fills in project_id.
func (r *ProjectControlsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// projectControlsRequest builds the PUT body from the plan. It is flat and
// sends only the overridden fields; a field left out inherits the
// organization's value (unless another field of its unit is sent).
func projectControlsRequest(ctx context.Context, plan projectControlsResourceModel) (client.ProjectControlsV1Update, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.ProjectControlsV1Update

	body.AllowedRegions = controlsListToAPI(ctx, plan.AllowedRegions, &diags)
	body.IgnoredRegions = controlsListToAPI(ctx, plan.IgnoredRegions, &diags)
	body.AllowedVendors = controlsListToAPI(ctx, plan.AllowedVendors, &diags)
	body.IgnoredVendors = controlsListToAPI(ctx, plan.IgnoredVendors, &diags)
	body.VendorRoutingPreference = controlsListToAPI(ctx, plan.VendorRoutingPreference, &diags)
	if !plan.ZeroDataRetention.IsNull() {
		body.ZeroDataRetention = nullable.NewNullableWithValue(plan.ZeroDataRetention.ValueBool())
	}
	return body, diags
}

// controlsListToAPI leaves a null list out of the request and sends an empty
// one as [].
func controlsListToAPI(ctx context.Context, l types.List, diags *diag.Diagnostics) nullable.Nullable[[]string] {
	if l.IsNull() {
		return nil
	}
	values := []string{}
	diags.Append(l.ElementsAs(ctx, &values, false)...)
	return nullable.NewNullableWithValue(values)
}

// projectControlsFromAPI maps the response's override to the Terraform model.
// The override is grouped into units, and a unit that is present has every
// field, [] for the ones the PUT didn't send. prior is the plan (after create
// or update) or the state (after read): a [] whose prior value is null stays
// null, since sending [] and leaving the field out are the same within a unit.
// A unit that is absent reads as null, so overrides removed outside Terraform
// show up as a diff that sets them again. A unit that is present with nothing
// in it reads its first field as [], so it can't pass for an inherited one.
func projectControlsFromAPI(ctx context.Context, c *client.ProjectControlsV1Response, prior projectControlsResourceModel) (projectControlsResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := projectControlsResourceModel{
		ID:                      types.StringValue(c.ScopeId.String()),
		ProjectID:               types.StringValue(c.ScopeId.String()),
		AllowedRegions:          types.ListNull(types.StringType),
		IgnoredRegions:          types.ListNull(types.StringType),
		AllowedVendors:          types.ListNull(types.StringType),
		IgnoredVendors:          types.ListNull(types.StringType),
		VendorRoutingPreference: types.ListNull(types.StringType),
		ZeroDataRetention:       types.BoolNull(),
	}

	// The API returns the canonical UUID; keep project_id as configured.
	if priorID, err := uuid.Parse(prior.ProjectID.ValueString()); err == nil && priorID == c.ScopeId {
		m.ProjectID = prior.ProjectID
	}

	if regions := c.Override.Regions; regions != nil {
		m.AllowedRegions = controlsListFromAPI(ctx, regions.AllowedRegions, prior.AllowedRegions, &diags)
		m.IgnoredRegions = controlsListFromAPI(ctx, regions.IgnoredRegions, prior.IgnoredRegions, &diags)
		if m.AllowedRegions.IsNull() && m.IgnoredRegions.IsNull() {
			m.AllowedRegions = types.ListValueMust(types.StringType, []attr.Value{})
		}
	}
	if vendors := c.Override.Vendors; vendors != nil {
		m.AllowedVendors = controlsListFromAPI(ctx, vendors.AllowedVendors, prior.AllowedVendors, &diags)
		m.IgnoredVendors = controlsListFromAPI(ctx, vendors.IgnoredVendors, prior.IgnoredVendors, &diags)
		m.VendorRoutingPreference = controlsListFromAPI(ctx, vendors.VendorRoutingPreference, prior.VendorRoutingPreference, &diags)
		if m.AllowedVendors.IsNull() && m.IgnoredVendors.IsNull() && m.VendorRoutingPreference.IsNull() {
			m.AllowedVendors = types.ListValueMust(types.StringType, []attr.Value{})
		}
	}
	if zdr := c.Override.ZeroDataRetention; zdr != nil {
		m.ZeroDataRetention = types.BoolValue(*zdr)
	}
	return m, diags
}

func controlsListFromAPI(ctx context.Context, values []string, prior types.List, diags *diag.Diagnostics) types.List {
	if len(values) == 0 && prior.IsNull() {
		return types.ListNull(types.StringType)
	}
	if values == nil {
		values = []string{}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return l
}
