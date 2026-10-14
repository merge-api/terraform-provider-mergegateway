// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
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
	_ resource.Resource                   = &BlocklistRuleResource{}
	_ resource.ResourceWithConfigure      = &BlocklistRuleResource{}
	_ resource.ResourceWithImportState    = &BlocklistRuleResource{}
	_ resource.ResourceWithValidateConfig = &BlocklistRuleResource{}
)

func NewBlocklistRuleResource() resource.Resource {
	return &BlocklistRuleResource{}
}

// BlocklistRuleResource manages a blocklist rule through /v1/blocklist-rules.
type BlocklistRuleResource struct {
	client *client.ClientWithResponses
}

// blocklistRuleResourceModel describes the resource data model. The lists
// are never null: the API reads a missing list as [].
type blocklistRuleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	RuleType    types.String `tfsdk:"rule_type"`
	CustomerIDs types.List   `tfsdk:"customer_ids"`
	Providers   types.List   `tfsdk:"providers"`
	Models      types.List   `tfsdk:"models"`
	Reason      types.String `tfsdk:"reason"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

const (
	ruleTypeBlock = string(client.BlocklistRuleV1CreateRuleTypeBlock)
	ruleTypePin   = string(client.BlocklistRuleV1CreateRuleTypePin)
)

func (r *BlocklistRuleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_blocklist_rule"
}

func (r *BlocklistRuleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	scope := func(description string) schema.ListAttribute {
		return schema.ListAttribute{
			MarkdownDescription: description + " Kept in the order given. Defaults to none.",
			ElementType:         types.StringType,
			Optional:            true,
			Computed:            true,
			Default:             listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
			// The API drops blank entries, which would read back as a different list.
			Validators: []validator.List{listvalidator.ValueStringsAre(
				stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be blank"))},
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a blocklist rule: which end customers, providers or models the organization's " +
			"requests may not use (`block`), or the only providers or models some customers may use (`pin`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The rule's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rule_type": schema.StringAttribute{
				MarkdownDescription: "`block` denies requests that match the rule: it needs at least one of `customer_ids`, " +
					"`providers` and `models`. `pin` lets the listed customers use only the listed providers or models: it " +
					"needs `customer_ids` plus `providers` or `models`. Defaults to `block`.",
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString(ruleTypeBlock),
				Validators: []validator.String{stringvalidator.OneOf(ruleTypeBlock, ruleTypePin)},
			},
			"customer_ids": scope("The end customers the rule applies to."),
			"providers":    scope("The providers the rule matches, such as `openai`."),
			"models":       scope("The models the rule matches, such as `openai/gpt-4o`."),
			"reason": schema.StringAttribute{
				MarkdownDescription: "Why the rule exists, up to 500 characters. Defaults to none (`\"\"`).",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				Validators:          []validator.String{stringvalidator.LengthAtMost(500)},
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

func (r *BlocklistRuleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig checks at plan time that the rule has the lists its type needs.
func (r *BlocklistRuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config blocklistRuleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(blocklistRuleScopeErrors(config)...)
}

func (r *BlocklistRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan blocklistRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := blocklistRuleCreateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateBlocklistRuleWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create blocklist rule", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create blocklist rule", apiError(created.StatusCode(), created.Body))
		return
	}
	tflog.Trace(ctx, "created a blocklist rule", map[string]any{"id": created.JSON201.Id.String()})

	state, diags := blocklistRuleFromAPI(ctx, created.JSON201)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BlocklistRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state blocklistRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "blocklist rule")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetBlocklistRuleWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read blocklist rule", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "blocklist rule not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read blocklist rule", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags = blocklistRuleFromAPI(ctx, got.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BlocklistRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state blocklistRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "blocklist rule")
	resp.Diagnostics.Append(diags...)
	body, diags := blocklistRuleUpdateRequest(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateBlocklistRuleWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update blocklist rule", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update blocklist rule", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = blocklistRuleFromAPI(ctx, updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BlocklistRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state blocklistRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "blocklist rule")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteBlocklistRuleWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete blocklist rule", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete blocklist rule", apiError(deleted.StatusCode(), deleted.Body))
	}
}

func (r *BlocklistRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// blocklistRuleScopeErrors reports a rule without the lists its type needs.
// A list that isn't known yet counts as set.
func blocklistRuleScopeErrors(m blocklistRuleResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	set := func(l types.List) bool {
		return l.IsUnknown() || len(l.Elements()) > 0
	}

	switch {
	case m.RuleType.IsUnknown():
	case m.RuleType.ValueString() == ruleTypePin:
		if !set(m.CustomerIDs) {
			diags.AddAttributeError(path.Root("customer_ids"), "Missing customer_ids", "A pin rule needs at least one customer ID.")
		}
		if !set(m.Providers) && !set(m.Models) {
			diags.AddAttributeError(path.Root("providers"), "Missing providers or models",
				"A pin rule needs at least one provider or model.")
		}
	default:
		if !set(m.CustomerIDs) && !set(m.Providers) && !set(m.Models) {
			diags.AddAttributeError(path.Root("customer_ids"), "Empty blocklist rule",
				"A block rule needs at least one of customer_ids, providers and models.")
		}
	}
	return diags
}

// blocklistRuleCreateRequest builds the POST /v1/blocklist-rules body from the plan.
func blocklistRuleCreateRequest(ctx context.Context, plan blocklistRuleResourceModel) (client.BlocklistRuleV1Create, diag.Diagnostics) {
	var diags diag.Diagnostics
	ruleType := client.BlocklistRuleV1CreateRuleType(plan.RuleType.ValueString())
	reason := plan.Reason.ValueString()
	return client.BlocklistRuleV1Create{
		RuleType:    &ruleType,
		CustomerIds: stringsToAPI(ctx, plan.CustomerIDs, &diags),
		Providers:   stringsToAPI(ctx, plan.Providers, &diags),
		Models:      stringsToAPI(ctx, plan.Models, &diags),
		Reason:      &reason,
	}, diags
}

// blocklistRuleUpdateRequest builds the PATCH /v1/blocklist-rules/{rule_id}
// body. It sends only the fields that differ between plan and state; a list
// sent replaces the whole list, so [] empties it.
func blocklistRuleUpdateRequest(ctx context.Context, plan, state blocklistRuleResourceModel) (client.BlocklistRuleV1Update, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.BlocklistRuleV1Update
	list := func(planned, current types.List) nullable.Nullable[[]string] {
		if planned.Equal(current) {
			return nil
		}
		return nullable.NewNullableWithValue(*stringsToAPI(ctx, planned, &diags))
	}

	if !plan.RuleType.Equal(state.RuleType) {
		body.RuleType = nullable.NewNullableWithValue(client.BlocklistRuleV1UpdateRuleType(plan.RuleType.ValueString()))
	}
	body.CustomerIds = list(plan.CustomerIDs, state.CustomerIDs)
	body.Providers = list(plan.Providers, state.Providers)
	body.Models = list(plan.Models, state.Models)
	if !plan.Reason.Equal(state.Reason) {
		body.Reason = nullable.NewNullableWithValue(plan.Reason.ValueString())
	}
	return body, diags
}

// blocklistRuleFromAPI maps an API rule to the Terraform model.
func blocklistRuleFromAPI(ctx context.Context, r *client.BlocklistRuleV1Response) (blocklistRuleResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	return blocklistRuleResourceModel{
		ID:          types.StringValue(r.Id.String()),
		RuleType:    types.StringValue(string(r.RuleType)),
		CustomerIDs: stringsFromAPI(ctx, &r.CustomerIds, &diags),
		Providers:   stringsFromAPI(ctx, &r.Providers, &diags),
		Models:      stringsFromAPI(ctx, &r.Models, &diags),
		Reason:      types.StringValue(r.Reason),
		CreatedAt:   types.StringValue(r.CreatedAt.Format(time.RFC3339)),
		UpdatedAt:   types.StringValue(r.UpdatedAt.Format(time.RFC3339)),
	}, diags
}
