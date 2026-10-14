package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
	_ resource.Resource                   = &APIKeyResource{}
	_ resource.ResourceWithConfigure      = &APIKeyResource{}
	_ resource.ResourceWithImportState    = &APIKeyResource{}
	_ resource.ResourceWithValidateConfig = &APIKeyResource{}
)

func NewAPIKeyResource() resource.Resource {
	return &APIKeyResource{}
}

// APIKeyResource manages a Merge Gateway API key through /v1/keys.
type APIKeyResource struct {
	client *client.ClientWithResponses
}

// apiKeyResourceModel describes the resource data model.
type apiKeyResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	Key             types.String  `tfsdk:"key"`
	Name            types.String  `tfsdk:"name"`
	ProjectID       types.String  `tfsdk:"project_id"`
	Limit           types.Float64 `tfsdk:"limit"`
	LimitReset      types.String  `tfsdk:"limit_reset"`
	AllowedModels   types.Set     `tfsdk:"allowed_models"`
	Disabled        types.Bool    `tfsdk:"disabled"`
	RoutingPolicyID types.String  `tfsdk:"routing_policy_id"`
	BudgetGroupID   types.String  `tfsdk:"budget_group_id"`
	Label           types.String  `tfsdk:"label"`
	Usage           types.Float64 `tfsdk:"usage"`
	LimitRemaining  types.Float64 `tfsdk:"limit_remaining"`
	CreatedAt       types.String  `tfsdk:"created_at"`
	CreatorUserID   types.String  `tfsdk:"creator_user_id"`
	ExpiresAt       types.String  `tfsdk:"expires_at"`
}

// limitResetMonthly is the window the API uses for a key that doesn't set one.
const limitResetMonthly = string(client.KeyObjectLimitResetMonthly)

func (r *APIKeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *APIKeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Merge Gateway API key, used to send requests through the Gateway.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The key's ID: the SHA-256 hash of the raw key.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "The raw API key. The API returns it only when the key is created, so it is null after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The key's name. Leave it out for none. Once set, it can be changed but not removed: " +
					"the API can't clear it yet, so leaving it out keeps the current name. A key replaced " +
					"because `project_id` changed gets no name unless one is set.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "The project the key is scoped to. Leave it out for a key that can use any project. " +
					"It can't be changed after creation: changing it replaces the key.",
				Optional:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"limit": schema.Float64Attribute{
				MarkdownDescription: "USD spend cap over the `limit_reset` window. Must be greater than 0. Leave it out for no cap: " +
					"removing it from the config removes the key's cap.",
				Optional: true,
			},
			"limit_reset": schema.StringAttribute{
				MarkdownDescription: "The window `limit` applies to: `daily`, `weekly` or `monthly`. Defaults to `monthly`. " +
					"Removing `limit` also clears the window in the API; this attribute keeps its configured value, " +
					"which is sent again when a limit is set.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(limitResetMonthly),
				Validators: []validator.String{stringvalidator.OneOf(
					string(client.KeyObjectLimitResetDaily),
					string(client.KeyObjectLimitResetWeekly),
					string(client.KeyObjectLimitResetMonthly),
				)},
			},
			"allowed_models": schema.SetAttribute{
				MarkdownDescription: "The only models the key may use, at most 200, each written as `provider/model`. " +
					"A single trailing `*` matches a prefix, such as `openai/*`; model aliases aren't allowed. " +
					"Leave it out to allow every model. An empty list, which the API treats as allowing no model, isn't supported: " +
					"set `disabled = true` instead.",
				ElementType: types.StringType,
				Optional:    true,
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, 200),
					// The API trims the names, which would show as an inconsistent result.
					setvalidator.ValueStringsAre(
						stringvalidator.RegexMatches(regexp.MustCompile(`^\S(.*\S)?$`), "must not start or end with whitespace"),
						stringvalidator.RegexMatches(regexp.MustCompile(`/`), "must be written as provider/model, such as openai/gpt-4o"),
					),
				},
			},
			"disabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the key is disabled. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"routing_policy_id": schema.StringAttribute{
				MarkdownDescription: "A routing policy to bind the key to: an organization-level policy or one scoped to the key's project.",
				Optional:            true,
			},
			"budget_group_id": schema.StringAttribute{
				MarkdownDescription: "The Merge for Workforce budget group the key draws down. Managed by Merge for Workforce.",
				Computed:            true,
			},
			"label": schema.StringAttribute{
				MarkdownDescription: "The key's first characters, for display.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"usage": schema.Float64Attribute{
				MarkdownDescription: "USD spend in the current `limit_reset` window, as of the last refresh.",
				Computed:            true,
			},
			"limit_remaining": schema.Float64Attribute{
				MarkdownDescription: "`limit` minus `usage`, never below 0, as of the last refresh. Null when there is no limit.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the key was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"creator_user_id": schema.StringAttribute{
				MarkdownDescription: "The dashboard user who created the key. Null for keys created with a management key, as Terraform does.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "When the key stops working (RFC 3339). Null means it never expires. Not settable yet.",
				Computed:            true,
			},
		},
	}
}

func (r *APIKeyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *APIKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var limit types.Float64
	var projectID types.String
	var models types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("limit"), &limit)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project_id"), &projectID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("allowed_models"), &models)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !limit.IsUnknown() && !limit.IsNull() && limit.ValueFloat64() <= 0 {
		resp.Diagnostics.AddAttributeError(path.Root("limit"), "Invalid limit", "limit must be greater than 0.")
	}
	// The API rejects aliases, which never match, and a "*" anywhere but at the end.
	for _, v := range models.Elements() {
		s, ok := v.(types.String)
		if !ok || s.IsUnknown() || s.IsNull() {
			continue
		}
		model := s.ValueString()
		if strings.HasPrefix(model, "@alias/") {
			resp.Diagnostics.AddAttributeError(path.Root("allowed_models"), "Invalid model",
				fmt.Sprintf("%q is a model alias; list the models it routes to instead.", model))
		}
		if strings.Contains(strings.TrimSuffix(model, "*"), "*") {
			resp.Diagnostics.AddAttributeError(path.Root("allowed_models"), "Invalid model",
				fmt.Sprintf("%q: \"*\" is allowed only once, as the last character, such as openai/*.", model))
		}
	}

	// Only the lowercase, hyphenated form the API returns, so the ID reads back unchanged.
	if !projectID.IsUnknown() && !projectID.IsNull() {
		parsed, err := uuid.Parse(projectID.ValueString())
		if err == nil && parsed.String() != projectID.ValueString() {
			err = fmt.Errorf("write it in lowercase with hyphens, as %s", parsed)
		}
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("project_id"), "Invalid project ID",
				fmt.Sprintf("Expected a project UUID, got %q: %s", projectID.ValueString(), err))
		}
	}
}

func (r *APIKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := apiKeyCreateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateKeyWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create API key", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create API key", apiError(created.StatusCode(), created.Body))
		return
	}
	// The create response is a key object plus the raw key, so it decodes as one.
	var key client.KeyObject
	if err := json.Unmarshal(created.Body, &key); err != nil {
		resp.Diagnostics.AddError("Unable to create API key", "Unable to decode the created key: "+err.Error())
		return
	}
	tflog.Trace(ctx, "created an API key", map[string]any{"id": key.Hash})

	plan.Key = types.StringValue(created.JSON201.Key)
	state, diags := apiKeyFromAPI(ctx, &key, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *APIKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetKeyWithResponse(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read API key", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "API key not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read API key", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags := apiKeyFromAPI(ctx, got.JSON200, state)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *APIKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := apiKeyUpdateRequest(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateKeyWithResponse(ctx, state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update API key", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update API key", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = apiKeyFromAPI(ctx, updated.JSON200, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *APIKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteKeyWithResponse(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete API key", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete API key", apiError(deleted.StatusCode(), deleted.Body))
	}
}

func (r *APIKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apiKeyCreateRequest builds the POST /v1/keys body from the plan.
func apiKeyCreateRequest(ctx context.Context, plan apiKeyResourceModel) (client.KeyCreateRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	disabled := plan.Disabled.ValueBool()
	body := client.KeyCreateRequest{
		LimitReset: nullable.NewNullableWithValue(client.KeyCreateRequestLimitReset(plan.LimitReset.ValueString())),
		Disabled:   &disabled,
	}

	// Unknown when left out of the config: the key gets no name.
	if !plan.Name.IsUnknown() && !plan.Name.IsNull() {
		body.Name = nullable.NewNullableWithValue(plan.Name.ValueString())
	}
	if !plan.ProjectID.IsNull() {
		projectID, err := uuid.Parse(plan.ProjectID.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("project_id"), "Invalid project ID",
				fmt.Sprintf("Expected a project UUID, got %q: %s", plan.ProjectID.ValueString(), err))
		}
		body.ProjectId = nullable.NewNullableWithValue(projectID)
	}
	if !plan.Limit.IsNull() {
		body.Limit = nullable.NewNullableWithValue(plan.Limit.ValueFloat64())
	}
	if !plan.AllowedModels.IsNull() {
		var models []string
		diags.Append(plan.AllowedModels.ElementsAs(ctx, &models, false)...)
		body.AllowedModels = nullable.NewNullableWithValue(models)
	}
	if !plan.RoutingPolicyID.IsNull() {
		body.RoutingPolicyId = nullable.NewNullableWithValue(plan.RoutingPolicyID.ValueString())
	}
	return body, diags
}

// apiKeyUpdateRequest builds the PATCH /v1/keys/{key_hash} body. It sends only
// the fields that differ between plan and state; a removed allowed_models,
// limit, routing_policy_id is sent as null, which clears it. name is never sent
// as null (the API ignores it: a name left out keeps the state's), and
// project_id is never sent: changing it replaces the key. limit_reset is also
// sent when a limit is set on a key without one, since removing a limit
// clears its window in the API.
func apiKeyUpdateRequest(ctx context.Context, plan, state apiKeyResourceModel) (client.KeyUpdateRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.KeyUpdateRequest

	if !plan.Name.Equal(state.Name) && !plan.Name.IsUnknown() && !plan.Name.IsNull() {
		body.Name = nullable.NewNullableWithValue(plan.Name.ValueString())
	}
	if !plan.Limit.Equal(state.Limit) {
		if plan.Limit.IsNull() {
			body.Limit = nullable.NewNullNullable[float64]()
		} else {
			body.Limit = nullable.NewNullableWithValue(plan.Limit.ValueFloat64())
		}
	}
	if !plan.LimitReset.Equal(state.LimitReset) || (state.Limit.IsNull() && !plan.Limit.IsNull()) {
		body.LimitReset = nullable.NewNullableWithValue(client.KeyUpdateRequestLimitReset(plan.LimitReset.ValueString()))
	}
	if !plan.AllowedModels.Equal(state.AllowedModels) {
		if plan.AllowedModels.IsNull() {
			body.AllowedModels = nullable.NewNullNullable[[]string]()
		} else {
			var models []string
			diags.Append(plan.AllowedModels.ElementsAs(ctx, &models, false)...)
			body.AllowedModels = nullable.NewNullableWithValue(models)
		}
	}
	if !plan.Disabled.Equal(state.Disabled) {
		body.Disabled = nullable.NewNullableWithValue(plan.Disabled.ValueBool())
	}
	if !plan.RoutingPolicyID.Equal(state.RoutingPolicyID) {
		if plan.RoutingPolicyID.IsNull() {
			body.RoutingPolicyId = nullable.NewNullNullable[string]()
		} else {
			body.RoutingPolicyId = nullable.NewNullableWithValue(plan.RoutingPolicyID.ValueString())
		}
	}
	return body, diags
}

// apiKeyFromAPI maps an API key to the Terraform model. prior is the plan
// (after create or update) or the state (after read); the raw key comes from
// it, since the API returns the key only on create.
func apiKeyFromAPI(ctx context.Context, k *client.KeyObject, prior apiKeyResourceModel) (apiKeyResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := apiKeyResourceModel{
		ID:              types.StringValue(k.Hash),
		Key:             prior.Key,
		Name:            nullableString(k.Name),
		ProjectID:       types.StringNull(),
		Limit:           nullableFloat64(k.Limit),
		LimitReset:      types.StringValue(limitResetMonthly),
		AllowedModels:   types.SetNull(types.StringType),
		Disabled:        types.BoolValue(k.Disabled),
		RoutingPolicyID: nullableString(k.RoutingPolicyId),
		BudgetGroupID:   nullableString(k.BudgetGroupId),
		Label:           types.StringValue(k.Label),
		Usage:           types.Float64Value(k.Usage),
		LimitRemaining:  nullableFloat64(k.LimitRemaining),
		CreatedAt:       nullableTime(k.CreatedAt),
		CreatorUserID:   types.StringNull(),
		ExpiresAt:       nullableTime(k.ExpiresAt),
	}
	// An import has no prior key: it stays null rather than unknown.
	if m.Key.IsUnknown() {
		m.Key = types.StringNull()
	}

	if projectID, err := k.ProjectId.Get(); err == nil {
		m.ProjectID = types.StringValue(projectID.String())
	}
	// A key without a window uses monthly, so null and monthly are the same.
	// Removing a limit clears its window too: a key without a limit keeps the
	// prior window, which apiKeyUpdateRequest sends again with a new limit.
	if limitReset, err := k.LimitReset.Get(); err == nil {
		m.LimitReset = types.StringValue(string(limitReset))
	} else if m.Limit.IsNull() && !prior.LimitReset.IsNull() && !prior.LimitReset.IsUnknown() {
		m.LimitReset = prior.LimitReset
	}
	// [] allows no model and null allows every one, so [] stays an empty set:
	// the config can't hold one, and the plan sets it back to null.
	if models, err := k.AllowedModels.Get(); err == nil {
		var d diag.Diagnostics
		m.AllowedModels, d = types.SetValueFrom(ctx, types.StringType, models)
		diags.Append(d...)
	}
	if creator, err := k.CreatorUserId.Get(); err == nil {
		m.CreatorUserID = types.StringValue(creator.String())
	}
	return m, diags
}

func nullableTime(v nullable.Nullable[time.Time]) types.String {
	if t, err := v.Get(); err == nil {
		return types.StringValue(t.Format(time.RFC3339))
	}
	return types.StringNull()
}
