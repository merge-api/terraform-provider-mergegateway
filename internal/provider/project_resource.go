// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &ProjectResource{}
	_ resource.ResourceWithConfigure      = &ProjectResource{}
	_ resource.ResourceWithImportState    = &ProjectResource{}
	_ resource.ResourceWithValidateConfig = &ProjectResource{}
)

func NewProjectResource() resource.Resource {
	return &ProjectResource{}
}

// ProjectResource manages a Merge Gateway project through /v1/projects.
type ProjectResource struct {
	client *client.ClientWithResponses
}

// projectResourceModel describes the resource data model. Nested objects are
// pointers: nil is a null (unset) block.
type projectResourceModel struct {
	ID                             types.String               `tfsdk:"id"`
	OrganizationID                 types.String               `tfsdk:"organization_id"`
	Name                           types.String               `tfsdk:"name"`
	Description                    types.String               `tfsdk:"description"`
	Slug                           types.String               `tfsdk:"slug"`
	IsActive                       types.Bool                 `tfsdk:"is_active"`
	BudgetConfig                   *projectBudgetConfigModel  `tfsdk:"budget_config"`
	RoutingPolicy                  *projectRoutingPolicyModel `tfsdk:"routing_policy"`
	UsesOrganizationDefaultRouting types.Bool                 `tfsdk:"uses_organization_default_routing"`
	CreatedAt                      types.String               `tfsdk:"created_at"`
	UpdatedAt                      types.String               `tfsdk:"updated_at"`
}

type projectBudgetConfigModel struct {
	Amount             types.Float64 `tfsdk:"amount"`
	Period             types.String  `tfsdk:"period"`
	EnforcementMode    types.String  `tfsdk:"enforcement_mode"`
	AlertThresholds    types.Set     `tfsdk:"alert_thresholds"`
	NotificationEmails types.Set     `tfsdk:"notification_emails"`
	NotifyOrgAdmins    types.Bool    `tfsdk:"notify_org_admins"`
}

type projectRoutingPolicyModel struct {
	Strategy         types.String `tfsdk:"strategy"`
	Axis             types.String `tfsdk:"axis"`
	AllowedModels    types.List   `tfsdk:"allowed_models"`
	PriorityOrder    types.Set    `tfsdk:"priority_order"`
	AllowedProviders types.List   `tfsdk:"allowed_providers"`
	ID               types.String `tfsdk:"id"`
	Slug             types.String `tfsdk:"slug"`
	Model            types.String `tfsdk:"model"`
	CreatedAt        types.String `tfsdk:"created_at"`
	ModifiedAt       types.String `tfsdk:"modified_at"`
}

type projectPriorityModel struct {
	Model    types.String `tfsdk:"model"`
	Priority types.Int64  `tfsdk:"priority"`
}

var projectPriorityAttrTypes = map[string]attr.Type{
	"model":    types.StringType,
	"priority": types.Int64Type,
}

// axisCost is the axis the API uses for an INTELLIGENT policy that doesn't set one.
const axisCost = string(client.ProjectPolicyV1AxisCOST)

func (r *ProjectResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *ProjectResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Merge Gateway project, including its budget and routing policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The project's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the organization that owns the project.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The project's name.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "A description of the project. Leave it out for none.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Derived from the name when the project is created. It does not change when the project is renamed.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"is_active": schema.BoolAttribute{
				MarkdownDescription: "Whether the project is active. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"budget_config": schema.SingleNestedAttribute{
				MarkdownDescription: "The project's spending budget. Remove the block to remove the budget.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"amount": schema.Float64Attribute{
						MarkdownDescription: "Budget amount in USD. Must be greater than 0.",
						Required:            true,
					},
					"period": schema.StringAttribute{
						MarkdownDescription: "The budget period: `daily`, `weekly`, `monthly`, `quarterly` or `yearly`.",
						Required:            true,
						Validators: []validator.String{stringvalidator.OneOf(
							string(client.ProjectBudgetConfigSchemaPeriodDaily),
							string(client.ProjectBudgetConfigSchemaPeriodWeekly),
							string(client.ProjectBudgetConfigSchemaPeriodMonthly),
							string(client.ProjectBudgetConfigSchemaPeriodQuarterly),
							string(client.ProjectBudgetConfigSchemaPeriodYearly),
						)},
					},
					"enforcement_mode": schema.StringAttribute{
						MarkdownDescription: "`soft_limit` only alerts when the budget is reached; `hard_limit` also blocks requests.",
						Required:            true,
						Validators: []validator.String{stringvalidator.OneOf(
							string(client.ProjectBudgetConfigSchemaEnforcementModeSoftLimit),
							string(client.ProjectBudgetConfigSchemaEnforcementModeHardLimit),
						)},
					},
					"alert_thresholds": schema.SetAttribute{
						MarkdownDescription: "Percentages of the budget that trigger an alert, such as `[50, 80, 90]`. Defaults to none.",
						ElementType:         types.Int64Type,
						Optional:            true,
						Computed:            true,
						Default:             setdefault.StaticValue(types.SetValueMust(types.Int64Type, []attr.Value{})),
					},
					"notification_emails": schema.SetAttribute{
						MarkdownDescription: "Addresses emailed when an alert threshold is crossed, at most 20. " +
							"Addresses are compared ignoring case, so don't list one twice in different cases. Defaults to none.",
						ElementType: types.StringType,
						Optional:    true,
						Computed:    true,
						Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
						Validators:  []validator.Set{setvalidator.SizeAtMost(20)},
					},
					"notify_org_admins": schema.BoolAttribute{
						MarkdownDescription: "Also email every organization admin when an alert threshold is crossed. Defaults to `true`.",
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(true),
					},
				},
			},
			"routing_policy": schema.SingleNestedAttribute{
				MarkdownDescription: "The project's own routing policy. Leave it out to route with the organization's default policy; " +
					"removing it deletes the project's policy.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"strategy": schema.StringAttribute{
						MarkdownDescription: "`PRIORITY` tries the models in `priority_order`; `INTELLIGENT` picks among `allowed_models` along `axis`.",
						Required:            true,
						Validators: []validator.String{stringvalidator.OneOf(
							string(client.ProjectPolicyV1StrategyPRIORITY),
							string(client.ProjectPolicyV1StrategyINTELLIGENT),
						)},
					},
					"axis": schema.StringAttribute{
						MarkdownDescription: "`INTELLIGENT` only. What to optimize for: `COST`, `PERFORMANCE` or `CAPABILITY`. Defaults to `COST`.",
						Optional:            true,
						Validators: []validator.String{stringvalidator.OneOf(
							string(client.ProjectPolicyV1AxisCOST),
							string(client.ProjectPolicyV1AxisPERFORMANCE),
							string(client.ProjectPolicyV1AxisCAPABILITY),
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
					"id": schema.StringAttribute{
						MarkdownDescription: "The routing policy's ID.",
						Computed:            true,
					},
					"slug": schema.StringAttribute{
						MarkdownDescription: "The routing policy's handle, when it has one.",
						Computed:            true,
					},
					"model": schema.StringAttribute{
						MarkdownDescription: "The value to send as `model` in a request to route with this policy, such as `@alias/<slug>`.",
						Computed:            true,
					},
					"created_at": schema.StringAttribute{
						MarkdownDescription: "When the routing policy was created (RFC 3339).",
						Computed:            true,
					},
					"modified_at": schema.StringAttribute{
						MarkdownDescription: "When the routing policy was last changed (RFC 3339).",
						Computed:            true,
					},
				},
			},
			"uses_organization_default_routing": schema.BoolAttribute{
				MarkdownDescription: "Whether the project routes with the organization's default policy, that is, `routing_policy` is not set.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the project was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the project was last changed (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (r *ProjectResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *ProjectResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var budget, policy types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("budget_config"), &budget)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("routing_policy"), &policy)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !budget.IsNull() && !budget.IsUnknown() {
		var b projectBudgetConfigModel
		resp.Diagnostics.Append(budget.As(ctx, &b, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})...)
		budgetPath := path.Root("budget_config")

		if !b.Amount.IsUnknown() && !b.Amount.IsNull() && b.Amount.ValueFloat64() <= 0 {
			resp.Diagnostics.AddAttributeError(budgetPath.AtName("amount"), "Invalid budget amount", "amount must be greater than 0.")
		}
		// The API drops addresses that differ only in case.
		seen := map[string]bool{}
		for _, v := range b.NotificationEmails.Elements() {
			if s, ok := v.(types.String); ok && !s.IsUnknown() {
				key := strings.ToLower(s.ValueString())
				if seen[key] {
					resp.Diagnostics.AddAttributeError(budgetPath.AtName("notification_emails"), "Duplicate notification email",
						fmt.Sprintf("%q is listed more than once (addresses are compared ignoring case).", s.ValueString()))
				}
				seen[key] = true
			}
		}
	}

	if !policy.IsNull() && !policy.IsUnknown() {
		var p projectRoutingPolicyModel
		resp.Diagnostics.Append(policy.As(ctx, &p, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})...)
		policyPath := path.Root("routing_policy")

		switch p.Strategy.ValueString() {
		case string(client.ProjectPolicyV1StrategyPRIORITY):
			if p.PriorityOrder.IsNull() {
				resp.Diagnostics.AddAttributeError(policyPath.AtName("priority_order"), "Missing priority_order",
					"A PRIORITY routing policy needs priority_order.")
			}
			if !p.AllowedModels.IsNull() {
				resp.Diagnostics.AddAttributeError(policyPath.AtName("allowed_models"), "Invalid routing policy",
					"allowed_models applies only to INTELLIGENT routing policies.")
			}
			if !p.Axis.IsNull() {
				resp.Diagnostics.AddAttributeError(policyPath.AtName("axis"), "Invalid routing policy",
					"axis applies only to INTELLIGENT routing policies.")
			}
		case string(client.ProjectPolicyV1StrategyINTELLIGENT):
			if !p.PriorityOrder.IsNull() {
				resp.Diagnostics.AddAttributeError(policyPath.AtName("priority_order"), "Invalid routing policy",
					"priority_order applies only to PRIORITY routing policies.")
			}
		}

		// Two entries for one model are distinct set elements when their priorities differ.
		if !p.PriorityOrder.IsNull() && !p.PriorityOrder.IsUnknown() {
			var entries []projectPriorityModel
			resp.Diagnostics.Append(p.PriorityOrder.ElementsAs(ctx, &entries, true)...)
			seen := map[string]bool{}
			for _, e := range entries {
				if e.Model.IsUnknown() {
					continue
				}
				if seen[e.Model.ValueString()] {
					resp.Diagnostics.AddAttributeError(policyPath.AtName("priority_order"), "Duplicate model in priority_order",
						fmt.Sprintf("%q is listed more than once.", e.Model.ValueString()))
				}
				seen[e.Model.ValueString()] = true
			}
		}
	}
}

func (r *ProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := projectCreateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateProjectWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create project", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create project", apiError(created.StatusCode(), created.Body))
		return
	}
	project := created.JSON201
	tflog.Trace(ctx, "created a project", map[string]any{"id": project.Id.String()})

	// The API only takes is_active on update, so an inactive project is
	// created and then deactivated.
	if !plan.IsActive.ValueBool() {
		updated, err := r.client.UpdateProjectWithResponse(ctx, project.Id, client.ProjectV1Update{
			IsActive: nullable.NewNullableWithValue(false),
		})
		if err != nil || updated.JSON200 == nil {
			var detail string
			if err != nil {
				detail = err.Error()
			} else {
				detail = apiError(updated.StatusCode(), updated.Body)
			}
			// Save the project anyway, so Terraform taints it rather than losing track of it.
			state, diags := projectFromAPI(ctx, project, plan)
			resp.Diagnostics.Append(diags...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			resp.Diagnostics.AddError("Unable to deactivate project", "The project was created but could not be deactivated: "+detail)
			return
		}
		project = updated.JSON200
	}

	state, diags := projectFromAPI(ctx, project, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(checkAllowedModels(plan, state)...)
}

func (r *ProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetProjectWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "project not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read project", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags = projectFromAPI(ctx, got.JSON200, state)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	body, diags := projectUpdateRequest(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateProjectWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update project", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update project", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = projectFromAPI(ctx, updated.JSON200, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(checkAllowedModels(plan, state)...)
}

func (r *ProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseProjectID(state.ID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteProjectWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete project", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete project", apiError(deleted.StatusCode(), deleted.Body))
	}
}

func (r *ProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func parseProjectID(id types.String) (uuid.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	parsed, err := uuid.Parse(id.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("id"), "Invalid project ID",
			fmt.Sprintf("Expected a project UUID, got %q: %s", id.ValueString(), err))
	}
	return parsed, diags
}

// projectCreateRequest builds the POST /v1/projects body from the plan.
// is_active is not part of it: the API only takes it on update.
func projectCreateRequest(ctx context.Context, plan projectResourceModel) (client.ProjectV1Create, diag.Diagnostics) {
	var diags diag.Diagnostics
	body := client.ProjectV1Create{Name: plan.Name.ValueString()}

	if !plan.Description.IsNull() {
		body.Description = nullable.NewNullableWithValue(plan.Description.ValueString())
	}
	if plan.BudgetConfig != nil {
		budget, d := budgetConfigToAPI(ctx, plan.BudgetConfig)
		diags.Append(d...)
		body.BudgetConfig = nullable.NewNullableWithValue(budget)
	}
	if plan.RoutingPolicy != nil {
		policy, d := routingPolicyToAPI(ctx, plan.RoutingPolicy)
		diags.Append(d...)
		body.RoutingPolicy = nullable.NewNullableWithValue(policy)
		useOrgDefault := false
		body.UsesOrganizationDefaultRouting = &useOrgDefault
	}
	return body, diags
}

// projectUpdateRequest builds the PATCH /v1/projects/{id} body. It sends only
// the fields that differ between plan and state; a removed description or
// budget_config is sent as null, which clears it. name is never sent as null.
func projectUpdateRequest(ctx context.Context, plan, state projectResourceModel) (client.ProjectV1Update, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.ProjectV1Update

	if !plan.Name.Equal(state.Name) {
		body.Name = nullable.NewNullableWithValue(plan.Name.ValueString())
	}
	if !plan.Description.Equal(state.Description) {
		if plan.Description.IsNull() {
			body.Description = nullable.NewNullNullable[string]()
		} else {
			body.Description = nullable.NewNullableWithValue(plan.Description.ValueString())
		}
	}
	if !plan.IsActive.Equal(state.IsActive) {
		body.IsActive = nullable.NewNullableWithValue(plan.IsActive.ValueBool())
	}
	if !plan.BudgetConfig.equal(state.BudgetConfig) {
		if plan.BudgetConfig == nil {
			body.BudgetConfig = nullable.NewNullNullable[client.ProjectBudgetConfigSchema]()
		} else {
			budget, d := budgetConfigToAPI(ctx, plan.BudgetConfig)
			diags.Append(d...)
			body.BudgetConfig = nullable.NewNullableWithValue(budget)
		}
	}
	if !plan.RoutingPolicy.equal(state.RoutingPolicy) {
		// The API ignores routing_policy: null; uses_organization_default_routing
		// is what deletes the project's policy.
		body.UsesOrganizationDefaultRouting = nullable.NewNullableWithValue(plan.RoutingPolicy == nil)
		if plan.RoutingPolicy != nil {
			policy, d := routingPolicyToAPI(ctx, plan.RoutingPolicy)
			diags.Append(d...)
			body.RoutingPolicy = nullable.NewNullableWithValue(policy)
		}
	}
	return body, diags
}

// equal reports whether two budgets would send the same request.
func (b *projectBudgetConfigModel) equal(o *projectBudgetConfigModel) bool {
	if b == nil || o == nil {
		return b == o
	}
	return b.Amount.Equal(o.Amount) &&
		b.Period.Equal(o.Period) &&
		b.EnforcementMode.Equal(o.EnforcementMode) &&
		b.AlertThresholds.Equal(o.AlertThresholds) &&
		b.NotificationEmails.Equal(o.NotificationEmails) &&
		b.NotifyOrgAdmins.Equal(o.NotifyOrgAdmins)
}

// equal reports whether two routing policies would send the same request.
// Only the configurable fields count: the computed ones are unknown in a plan.
func (p *projectRoutingPolicyModel) equal(o *projectRoutingPolicyModel) bool {
	if p == nil || o == nil {
		return p == o
	}
	return p.Strategy.Equal(o.Strategy) &&
		p.Axis.Equal(o.Axis) &&
		p.AllowedModels.Equal(o.AllowedModels) &&
		p.PriorityOrder.Equal(o.PriorityOrder)
}

func budgetConfigToAPI(ctx context.Context, b *projectBudgetConfigModel) (client.ProjectBudgetConfigSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	var thresholds []int64
	diags.Append(b.AlertThresholds.ElementsAs(ctx, &thresholds, false)...)
	alertThresholds := make([]int, 0, len(thresholds))
	for _, t := range thresholds {
		alertThresholds = append(alertThresholds, int(t))
	}

	var emails []string
	diags.Append(b.NotificationEmails.ElementsAs(ctx, &emails, false)...)
	// Send [] rather than omitting it, so removing every address clears them.
	notificationEmails := make([]openapi_types.Email, 0, len(emails))
	for _, e := range emails {
		notificationEmails = append(notificationEmails, openapi_types.Email(e))
	}

	notifyOrgAdmins := b.NotifyOrgAdmins.ValueBool()
	return client.ProjectBudgetConfigSchema{
		Amount:             b.Amount.ValueFloat64(),
		Period:             client.ProjectBudgetConfigSchemaPeriod(b.Period.ValueString()),
		EnforcementMode:    client.ProjectBudgetConfigSchemaEnforcementMode(b.EnforcementMode.ValueString()),
		AlertThresholds:    &alertThresholds,
		NotificationEmails: &notificationEmails,
		NotifyOrgAdmins:    &notifyOrgAdmins,
	}, diags
}

func routingPolicyToAPI(ctx context.Context, p *projectRoutingPolicyModel) (client.ProjectPolicyV1, diag.Diagnostics) {
	var diags diag.Diagnostics
	policy := client.ProjectPolicyV1{Strategy: client.ProjectPolicyV1Strategy(p.Strategy.ValueString())}

	if !p.Axis.IsNull() {
		policy.Axis = nullable.NewNullableWithValue(client.ProjectPolicyV1Axis(p.Axis.ValueString()))
	}
	if !p.AllowedModels.IsNull() {
		var models []string
		diags.Append(p.AllowedModels.ElementsAs(ctx, &models, false)...)
		policy.AllowedModels = nullable.NewNullableWithValue(models)
	}
	if !p.PriorityOrder.IsNull() {
		var entries []projectPriorityModel
		diags.Append(p.PriorityOrder.ElementsAs(ctx, &entries, false)...)
		order := make([]client.PriorityModel, 0, len(entries))
		for _, e := range entries {
			order = append(order, client.PriorityModel{Model: e.Model.ValueString(), Priority: int(e.Priority.ValueInt64())})
		}
		policy.PriorityOrder = nullable.NewNullableWithValue(order)
	}
	return policy, diags
}

// projectFromAPI maps an API project to the Terraform model. prior is the plan
// (after create or update) or the state (after read); it is only used to keep
// values the API reports differently from how they were configured.
func projectFromAPI(ctx context.Context, p *client.ProjectV1Response, prior projectResourceModel) (projectResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := projectResourceModel{
		ID:                             types.StringValue(p.Id.String()),
		OrganizationID:                 types.StringValue(p.OrganizationId.String()),
		Name:                           types.StringValue(p.Name),
		Description:                    nullableString(p.Description),
		Slug:                           types.StringValue(p.Slug),
		IsActive:                       types.BoolValue(p.IsActive),
		UsesOrganizationDefaultRouting: types.BoolValue(p.UsesOrganizationDefaultRouting),
		CreatedAt:                      types.StringValue(p.CreatedAt.Format(time.RFC3339)),
		UpdatedAt:                      types.StringValue(p.UpdatedAt.Format(time.RFC3339)),
	}

	if budget, err := p.BudgetConfig.Get(); err == nil {
		b, d := budgetConfigFromAPI(budget, prior.BudgetConfig)
		diags.Append(d...)
		m.BudgetConfig = b
	}
	if policy, err := p.RoutingPolicy.Get(); err == nil {
		rp, d := routingPolicyFromAPI(ctx, policy, prior.RoutingPolicy)
		diags.Append(d...)
		m.RoutingPolicy = rp
	}
	return m, diags
}

func budgetConfigFromAPI(b client.ProjectBudgetConfigV1Response, prior *projectBudgetConfigModel) (*projectBudgetConfigModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	thresholds := []attr.Value{}
	if b.AlertThresholds != nil {
		for _, t := range *b.AlertThresholds {
			thresholds = append(thresholds, types.Int64Value(int64(t)))
		}
	}
	alertThresholds, d := types.SetValue(types.Int64Type, thresholds)
	diags.Append(d...)

	// The API compares addresses ignoring case and may change their case, so
	// keep an address as configured when it matches ignoring case.
	var priorEmails []attr.Value
	if prior != nil {
		priorEmails = prior.NotificationEmails.Elements()
	}
	emails := []attr.Value{}
	if b.NotificationEmails != nil {
		for _, e := range *b.NotificationEmails {
			value := types.StringValue(e)
			for _, pe := range priorEmails {
				if s, ok := pe.(types.String); ok && strings.EqualFold(s.ValueString(), e) {
					value = s
				}
			}
			emails = append(emails, value)
		}
	}
	notificationEmails, d := types.SetValue(types.StringType, emails)
	diags.Append(d...)

	notifyOrgAdmins := true
	if b.NotifyOrgAdmins != nil {
		notifyOrgAdmins = *b.NotifyOrgAdmins
	}

	return &projectBudgetConfigModel{
		Amount:             types.Float64Value(b.Amount),
		Period:             types.StringValue(string(b.Period)),
		EnforcementMode:    types.StringValue(string(b.EnforcementMode)),
		AlertThresholds:    alertThresholds,
		NotificationEmails: notificationEmails,
		NotifyOrgAdmins:    types.BoolValue(notifyOrgAdmins),
	}, diags
}

func routingPolicyFromAPI(ctx context.Context, p client.PolicyV2Response, prior *projectRoutingPolicyModel) (*projectRoutingPolicyModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := &projectRoutingPolicyModel{
		Strategy:   types.StringValue(string(p.Strategy)),
		Axis:       types.StringNull(),
		ID:         types.StringValue(p.Id),
		Slug:       nullableString(p.Slug),
		Model:      nullableString(p.Model),
		CreatedAt:  types.StringValue(p.CreatedAt.Format(time.RFC3339)),
		ModifiedAt: types.StringValue(p.ModifiedAt.Format(time.RFC3339)),
	}

	// An INTELLIGENT policy without an axis reads back as COST. Keep it unset
	// when it was unset, so leaving axis out doesn't show a diff.
	if axis, err := p.Axis.Get(); err == nil {
		priorUnset := prior == nil || prior.Axis.IsNull()
		if string(axis) != axisCost || !priorUnset {
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
	m.PriorityOrder = types.SetNull(types.ObjectType{AttrTypes: projectPriorityAttrTypes})
	if order, err := p.PriorityOrder.Get(); err == nil && len(order) > 0 {
		entries := make([]projectPriorityModel, 0, len(order))
		for _, e := range order {
			entries = append(entries, projectPriorityModel{Model: types.StringValue(e.Model), Priority: types.Int64Value(int64(e.Priority))})
		}
		m.PriorityOrder, d = types.SetValueFrom(ctx, types.ObjectType{AttrTypes: projectPriorityAttrTypes}, entries)
		diags.Append(d...)
	}
	return m, diags
}

// checkAllowedModels reports models the API dropped from an INTELLIGENT
// policy's allowed_models (it drops models with no capability tier), instead
// of letting Terraform fail with an inconsistent result.
func checkAllowedModels(plan, state projectResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if plan.RoutingPolicy == nil || state.RoutingPolicy == nil || plan.RoutingPolicy.AllowedModels.IsNull() ||
		plan.RoutingPolicy.AllowedModels.Equal(state.RoutingPolicy.AllowedModels) {
		return diags
	}

	kept := map[string]bool{}
	for _, v := range state.RoutingPolicy.AllowedModels.Elements() {
		if s, ok := v.(types.String); ok {
			kept[s.ValueString()] = true
		}
	}
	var dropped []string
	for _, v := range plan.RoutingPolicy.AllowedModels.Elements() {
		if s, ok := v.(types.String); ok && !kept[s.ValueString()] {
			dropped = append(dropped, s.ValueString())
		}
	}
	diags.AddAttributeError(path.Root("routing_policy").AtName("allowed_models"),
		"Models dropped from allowed_models",
		fmt.Sprintf("Merge Gateway dropped these models from the routing policy because they have no capability tier: %s. "+
			"Remove them from routing_policy.allowed_models.", strings.Join(dropped, ", ")))
	return diags
}

func nullableString(v nullable.Nullable[string]) types.String {
	if s, err := v.Get(); err == nil {
		return types.StringValue(s)
	}
	return types.StringNull()
}
