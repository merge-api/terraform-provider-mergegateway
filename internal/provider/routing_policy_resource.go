package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
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
	_ resource.Resource                   = &RoutingPolicyResource{}
	_ resource.ResourceWithConfigure      = &RoutingPolicyResource{}
	_ resource.ResourceWithImportState    = &RoutingPolicyResource{}
	_ resource.ResourceWithValidateConfig = &RoutingPolicyResource{}
)

func NewRoutingPolicyResource() resource.Resource {
	return &RoutingPolicyResource{}
}

// RoutingPolicyResource manages an organization routing policy through /v1/routing-policies.
type RoutingPolicyResource struct {
	client *client.ClientWithResponses
}

// slugPattern is the form the API stores a slug in.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,62}[a-z0-9]$`)

// routingPolicyResourceModel describes the resource data model.
type routingPolicyResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Slug             types.String `tfsdk:"slug"`
	Strategy         types.String `tfsdk:"strategy"`
	Axis             types.String `tfsdk:"axis"`
	AllowedModels    types.List   `tfsdk:"allowed_models"`
	PriorityOrder    types.Set    `tfsdk:"priority_order"`
	AllowedProviders types.List   `tfsdk:"allowed_providers"`
	Model            types.String `tfsdk:"model"`
	IsDefault        types.Bool   `tfsdk:"is_default"`
	CreatedAt        types.String `tfsdk:"created_at"`
	ModifiedAt       types.String `tfsdk:"modified_at"`
}

func (r *RoutingPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_routing_policy"
}

func (r *RoutingPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an organization routing policy. Requests route with it by sending its `model` (`@alias/<slug>`) as the model.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The routing policy's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The routing policy's name.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "A description of the routing policy. Leave it out for none.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "The policy's handle, used in `model`. Changing it renames the handle. " +
					"3 to 64 lowercase letters, digits, `.`, `_` or `-`, starting and ending with a letter or digit; " +
					"`default_routing` and `fusion` are reserved. It must not be taken by another policy or model alias. " +
					"When left out, Merge Gateway picks one or leaves it unset; removing it later keeps the current slug, since the API can't clear it.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{
					// The API lowercases and trims the slug, so only its normalized form is accepted here.
					stringvalidator.RegexMatches(slugPattern,
						"must be 3 to 64 lowercase letters, digits, '.', '_' or '-', starting and ending with a letter or digit"),
					stringvalidator.NoneOf("default_routing", "fusion"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"strategy": schema.StringAttribute{
				MarkdownDescription: "`PRIORITY` tries the models in `priority_order`; `INTELLIGENT` picks among `allowed_models` along `axis`.",
				Required:            true,
				Validators: []validator.String{stringvalidator.OneOf(
					string(client.RoutingPolicyV1CreateStrategyPRIORITY),
					string(client.RoutingPolicyV1CreateStrategyINTELLIGENT),
				)},
			},
			"axis": schema.StringAttribute{
				MarkdownDescription: "`INTELLIGENT` only. What to optimize for: `COST`, `PERFORMANCE` or `CAPABILITY`. Defaults to `COST`.",
				Optional:            true,
				Validators: []validator.String{stringvalidator.OneOf(
					string(client.RoutingPolicyV1CreateAxisCOST),
					string(client.RoutingPolicyV1CreateAxisPERFORMANCE),
					string(client.RoutingPolicyV1CreateAxisCAPABILITY),
				)},
			},
			"allowed_models": schema.ListAttribute{
				MarkdownDescription: "`INTELLIGENT` only. The models to choose from, each listed once. " +
					"Every model must have a capability tier: the API drops models without one, and the provider reports that as an error.",
				ElementType: types.StringType,
				Optional:    true,
				Validators:  []validator.List{listvalidator.SizeAtLeast(1), listvalidator.UniqueValues()},
			},
			"priority_order": schema.SetNestedAttribute{
				MarkdownDescription: "`PRIORITY` only. The models to try, each listed once; lower `priority` is tried first.",
				Optional:            true,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"model": schema.StringAttribute{
							MarkdownDescription: "The model.",
							Required:            true,
						},
						"priority": schema.Int64Attribute{
							MarkdownDescription: "The model's priority.",
							Required:            true,
						},
					},
				},
			},
			"allowed_providers": schema.ListAttribute{
				MarkdownDescription: "`INTELLIGENT` only. The providers of `allowed_models`, sorted.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"model": schema.StringAttribute{
				MarkdownDescription: "The value to send as `model` in a request to route with this policy, such as `@alias/<slug>`.",
				Computed:            true,
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: "Whether this is the organization's default routing policy. " +
					"The organization's first policy becomes the default; this resource doesn't change it.",
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the routing policy was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"modified_at": schema.StringAttribute{
				MarkdownDescription: "When the routing policy was last changed (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (r *RoutingPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *RoutingPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var strategy, axis types.String
	var allowedModels types.List
	var priorityOrder types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("strategy"), &strategy)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("axis"), &axis)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("allowed_models"), &allowedModels)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("priority_order"), &priorityOrder)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateRoutingPolicy(ctx, path.Empty(), strategy, axis, allowedModels, priorityOrder)...)
}

func (r *RoutingPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan routingPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := routingPolicyCreateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateRoutingPolicyWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create routing policy", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create routing policy", apiError(created.StatusCode(), created.Body))
		return
	}
	tflog.Trace(ctx, "created a routing policy", map[string]any{"id": created.JSON201.Id})

	state, diags := routingPolicyResourceFromAPI(ctx, created.JSON201, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(checkSlug(plan, state)...)
	resp.Diagnostics.Append(checkDroppedModels(path.Root("allowed_models"), plan.AllowedModels, state.AllowedModels)...)
}

func (r *RoutingPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state routingPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseRoutingPolicyID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetRoutingPolicyWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read routing policy", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "routing policy not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read routing policy", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags = routingPolicyResourceFromAPI(ctx, got.JSON200, state)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *RoutingPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state routingPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseRoutingPolicyID(state.ID)
	resp.Diagnostics.Append(diags...)
	body, diags := routingPolicyUpdateRequest(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateRoutingPolicyWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update routing policy", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update routing policy", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = routingPolicyResourceFromAPI(ctx, updated.JSON200, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(checkDroppedModels(path.Root("allowed_models"), plan.AllowedModels, state.AllowedModels)...)
}

func (r *RoutingPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state routingPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseRoutingPolicyID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteRoutingPolicyWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete routing policy", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete routing policy", apiError(deleted.StatusCode(), deleted.Body))
	}
}

func (r *RoutingPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// checkSlug reports a configured slug the API didn't use on create: it adds a
// suffix to a taken slug (foo-2), and leaves the slug unset when the
// organization is at its alias limit. The policy is already saved to state,
// so Terraform taints it rather than losing track of it.
func checkSlug(plan, state routingPolicyResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if plan.Slug.IsNull() || plan.Slug.IsUnknown() || plan.Slug.Equal(state.Slug) {
		return diags
	}
	got := "without a slug (the organization may be at its alias limit)"
	if !state.Slug.IsNull() {
		got = fmt.Sprintf("with the slug %q", state.Slug.ValueString())
	}
	diags.AddAttributeError(path.Root("slug"), "Slug not available",
		fmt.Sprintf("The slug %q is taken, so the routing policy was created %s. "+
			"Choose another slug; the next apply replaces the policy.", plan.Slug.ValueString(), got))
	return diags
}

// parseRoutingPolicyID parses the ID for a /v1/routing-policies/{policy_id}
// call. Responses type the ID as a string, but the path only takes a UUID, so
// a policy with another kind of ID can't be managed through the API.
func parseRoutingPolicyID(id types.String) (uuid.UUID, diag.Diagnostics) {
	return parseUUID(id, "routing policy")
}

// routingPolicyCreateRequest builds the POST /v1/routing-policies body from the
// plan. is_default is never sent: it would change the organization's other policies.
func routingPolicyCreateRequest(ctx context.Context, plan routingPolicyResourceModel) (client.RoutingPolicyV1Create, diag.Diagnostics) {
	body := client.RoutingPolicyV1Create{
		Name:     plan.Name.ValueString(),
		Strategy: client.RoutingPolicyV1CreateStrategy(plan.Strategy.ValueString()),
	}

	if !plan.Description.IsNull() {
		body.Description = nullable.NewNullableWithValue(plan.Description.ValueString())
	}
	if !plan.Slug.IsNull() && !plan.Slug.IsUnknown() {
		body.Slug = nullable.NewNullableWithValue(plan.Slug.ValueString())
	}
	if !plan.Axis.IsNull() {
		body.Axis = nullable.NewNullableWithValue(client.RoutingPolicyV1CreateAxis(plan.Axis.ValueString()))
	}
	models, order, diags := routingPolicyRoutingToAPI(ctx, plan)
	if models != nil {
		body.AllowedModels = nullable.NewNullableWithValue(models)
	}
	if order != nil {
		body.PriorityOrder = nullable.NewNullableWithValue(order)
	}
	return body, diags
}

// routingPolicyUpdateRequest builds the PATCH /v1/routing-policies/{id} body.
// It sends only the fields that differ between plan and state. The API ignores
// the routing fields unless strategy is sent, so a change to any of them sends
// strategy and all of them. A removed description is sent as "", which clears
// it (the API treats null as no change).
func routingPolicyUpdateRequest(ctx context.Context, plan, state routingPolicyResourceModel) (client.RoutingPolicyV1Update, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.RoutingPolicyV1Update

	if !plan.Name.Equal(state.Name) {
		body.Name = nullable.NewNullableWithValue(plan.Name.ValueString())
	}
	if !plan.Description.Equal(state.Description) {
		body.Description = nullable.NewNullableWithValue(plan.Description.ValueString())
	}
	// An unknown slug was left out of the config: keep the current one.
	if !plan.Slug.Equal(state.Slug) && !plan.Slug.IsNull() && !plan.Slug.IsUnknown() {
		body.Slug = nullable.NewNullableWithValue(plan.Slug.ValueString())
	}
	if !plan.Strategy.Equal(state.Strategy) || !plan.Axis.Equal(state.Axis) ||
		!plan.AllowedModels.Equal(state.AllowedModels) || !plan.PriorityOrder.Equal(state.PriorityOrder) {
		body.Strategy = nullable.NewNullableWithValue(client.RoutingPolicyV1UpdateStrategy(plan.Strategy.ValueString()))
		if !plan.Axis.IsNull() {
			body.Axis = nullable.NewNullableWithValue(client.RoutingPolicyV1UpdateAxis(plan.Axis.ValueString()))
		}
		models, order, d := routingPolicyRoutingToAPI(ctx, plan)
		diags.Append(d...)
		if models != nil {
			body.AllowedModels = nullable.NewNullableWithValue(models)
		}
		if order != nil {
			body.PriorityOrder = nullable.NewNullableWithValue(order)
		}
	}
	return body, diags
}

// routingPolicyRoutingToAPI converts allowed_models and priority_order; a nil
// slice is a field that isn't set.
func routingPolicyRoutingToAPI(ctx context.Context, m routingPolicyResourceModel) ([]string, []client.PriorityModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	var models []string
	var order []client.PriorityModel

	if !m.AllowedModels.IsNull() {
		diags.Append(m.AllowedModels.ElementsAs(ctx, &models, false)...)
	}
	if !m.PriorityOrder.IsNull() {
		var entries []priorityOrderModel
		diags.Append(m.PriorityOrder.ElementsAs(ctx, &entries, false)...)
		order = make([]client.PriorityModel, 0, len(entries))
		for _, e := range entries {
			order = append(order, client.PriorityModel{Model: e.Model.ValueString(), Priority: int(e.Priority.ValueInt64())})
		}
	}
	return models, order, diags
}

// routingPolicyResourceFromAPI maps an API routing policy to the Terraform
// model. prior is the plan (after create or update) or the state (after read);
// it is only used to keep values the API reports differently from how they
// were configured.
func routingPolicyResourceFromAPI(ctx context.Context, p *client.RoutingPolicyV1Response, prior routingPolicyResourceModel) (routingPolicyResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := routingPolicyResourceModel{
		ID:          types.StringValue(p.Id),
		Name:        types.StringValue(p.Name),
		Description: nullableString(p.Description),
		Slug:        nullableString(p.Slug),
		Strategy:    types.StringValue(string(p.Strategy)),
		Axis:        types.StringNull(),
		Model:       nullableString(p.Model),
		IsDefault:   types.BoolValue(p.IsDefault),
		CreatedAt:   types.StringValue(p.CreatedAt.Format(time.RFC3339)),
		ModifiedAt:  types.StringValue(p.ModifiedAt.Format(time.RFC3339)),
	}

	// A cleared description reads back as "": the schema requires at least one character.
	if m.Description.ValueString() == "" {
		m.Description = types.StringNull()
	}

	// An INTELLIGENT policy without an axis reads back as COST. Keep it unset
	// when it was unset, so leaving axis out doesn't show a diff.
	if axis, err := p.Axis.Get(); err == nil {
		if string(axis) != axisCost || !prior.Axis.IsNull() {
			m.Axis = types.StringValue(string(axis))
		}
	}

	// Empty lists read back as null: the schema requires at least one element.
	var d diag.Diagnostics
	m.AllowedModels = types.ListNull(types.StringType)
	if models, err := p.AllowedModels.Get(); err == nil && len(models) > 0 {
		m.AllowedModels, d = types.ListValueFrom(ctx, types.StringType, models)
		diags.Append(d...)
	}
	m.AllowedProviders = types.ListNull(types.StringType)
	if providers, err := p.AllowedProviders.Get(); err == nil {
		m.AllowedProviders, d = types.ListValueFrom(ctx, types.StringType, providers)
		diags.Append(d...)
	}
	m.PriorityOrder = types.SetNull(types.ObjectType{AttrTypes: priorityOrderAttrTypes})
	if order, err := p.PriorityOrder.Get(); err == nil && len(order) > 0 {
		entries := make([]priorityOrderModel, 0, len(order))
		for _, e := range order {
			entries = append(entries, priorityOrderModel{Model: types.StringValue(e.Model), Priority: types.Int64Value(int64(e.Priority))})
		}
		m.PriorityOrder, d = types.SetValueFrom(ctx, types.ObjectType{AttrTypes: priorityOrderAttrTypes}, entries)
		diags.Append(d...)
	}
	return m, diags
}
