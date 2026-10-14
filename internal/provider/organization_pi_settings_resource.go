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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
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
	_ resource.Resource                = &OrganizationPISettingsResource{}
	_ resource.ResourceWithConfigure   = &OrganizationPISettingsResource{}
	_ resource.ResourceWithImportState = &OrganizationPISettingsResource{}
)

func NewOrganizationPISettingsResource() resource.Resource {
	return &OrganizationPISettingsResource{}
}

// OrganizationPISettingsResource manages the organization's prompt-injection
// policy through /v1/organization/pi-settings. There is one per organization:
// create and update replace it with PUT, and delete resets it to the defaults
// with DELETE.
type OrganizationPISettingsResource struct {
	client *client.ClientWithResponses
}

// organizationPISettingsResourceModel describes the resource data model.
// Every setting defaults to the API's default, since a PUT resets each one
// left out to it.
type organizationPISettingsResourceModel struct {
	ID                     types.String  `tfsdk:"id"`
	PiMode                 types.String  `tfsdk:"pi_mode"`
	PiIndirectMode         types.String  `tfsdk:"pi_indirect_mode"`
	PiInputAction          types.String  `tfsdk:"pi_input_action"`
	PiOutputAction         types.String  `tfsdk:"pi_output_action"`
	PiBlockThreshold       types.Float64 `tfsdk:"pi_block_threshold"`
	PiPassThreshold        types.Float64 `tfsdk:"pi_pass_threshold"`
	PiTier2aBlockThreshold types.Float64 `tfsdk:"pi_tier2a_block_threshold"`
	PiTier2bBlockThreshold types.Float64 `tfsdk:"pi_tier2b_block_threshold"`
	PiFailClosed           types.Bool    `tfsdk:"pi_fail_closed"`
	PiLogFullTextOnBlock   types.Bool    `tfsdk:"pi_log_full_text_on_block"`
	PiAllowlistPatterns    types.List    `tfsdk:"pi_allowlist_patterns"`
	PiSaferVendorRoute     types.String  `tfsdk:"pi_safer_vendor_route"`
}

// organizationPISettingsID is the ID of the one PI policy an organization has.
const organizationPISettingsID = "organization"

func (r *OrganizationPISettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_pi_settings"
}

func (r *OrganizationPISettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	modes := []string{
		string(client.OrganizationPISettingsV1PiModeOff),
		string(client.OrganizationPISettingsV1PiModeAlert),
		string(client.OrganizationPISettingsV1PiModeBlock),
	}
	actions := []string{
		string(client.OrganizationPISettingsV1PiOutputActionObserve),
		string(client.OrganizationPISettingsV1PiOutputActionRedact),
		string(client.OrganizationPISettingsV1PiOutputActionRoute),
		string(client.OrganizationPISettingsV1PiOutputActionBlock),
		string(client.OrganizationPISettingsV1PiOutputActionEscalate),
	}
	mode := func(description, def string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description + fmt.Sprintf(" Defaults to `%s`.", def),
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(def),
			Validators:          []validator.String{stringvalidator.OneOf(modes...)},
		}
	}
	action := func(description, def string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description + ": `observe`, `redact`, `route`, `block` or `escalate`." +
				fmt.Sprintf(" Defaults to `%s`.", def),
			Optional:   true,
			Computed:   true,
			Default:    stringdefault.StaticString(def),
			Validators: []validator.String{stringvalidator.OneOf(actions...)},
		}
	}
	threshold := func(description string, def float64) schema.Float64Attribute {
		return schema.Float64Attribute{
			MarkdownDescription: description + fmt.Sprintf(" Between 0 and 1. Defaults to `%g`.", def),
			Optional:            true,
			Computed:            true,
			Default:             float64default.StaticFloat64(def),
			Validators:          []validator.Float64{float64validator.Between(0, 1)},
		}
	}
	flag := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{
			MarkdownDescription: description + " Defaults to `false`.",
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the organization's prompt-injection (PI) protection policy, which projects inherit " +
			"unless they override it with `mergegateway_project_pi_settings`. There is one per organization: creating it " +
			"replaces the current policy, each setting left out takes its default, and destroying it resets the policy " +
			"to the defaults (PI off).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Always `organization`: there is one policy per organization.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"pi_mode": mode("Prompt-injection detection on the request: `off`, `alert` (score but never block) or "+
				"`block` (block when the score reaches `pi_block_threshold`).", string(client.OrganizationPISettingsV1PiModeOff)),
			"pi_indirect_mode": mode("Detection of indirect prompt injection in tool results and documents: `off`, `alert` or `block`.",
				string(client.OrganizationPISettingsV1PiIndirectModeOff)),
			"pi_input_action":           action("What to do when a request is flagged", string(client.OrganizationPISettingsV1PiInputActionBlock)),
			"pi_output_action":          action("What to do when a response is flagged", string(client.OrganizationPISettingsV1PiOutputActionRedact)),
			"pi_block_threshold":        threshold("The Tier 1 score at or above which `block` mode blocks a request.", 0.57),
			"pi_pass_threshold":         threshold("The Tier 1 score below which a request passes without further checks.", 0.3),
			"pi_tier2a_block_threshold": threshold("The Tier 2A block threshold.", 0.6),
			"pi_tier2b_block_threshold": threshold("The Tier 2B block threshold.", 0.45),
			"pi_fail_closed":            flag("Block requests when PI detection fails, instead of letting them through."),
			"pi_log_full_text_on_block": flag("Log the full text of blocked requests."),
			"pi_allowlist_patterns": schema.ListAttribute{
				MarkdownDescription: "Patterns for text that is never flagged, kept in the order given. Defaults to none.",
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
			},
			"pi_safer_vendor_route": schema.StringAttribute{
				MarkdownDescription: "The vendor flagged requests are routed to when an action is `route`. Leave it out for none.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
		},
	}
}

func (r *OrganizationPISettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OrganizationPISettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationPISettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := organizationPISettingsRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateOrganizationPiSettingsWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to set organization PI settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to set organization PI settings", apiError(updated.StatusCode(), updated.Body))
		return
	}
	tflog.Trace(ctx, "set organization PI settings")

	state, diags := organizationPISettingsFromAPI(ctx, updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrganizationPISettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	got, err := r.client.GetOrganizationPiSettingsWithResponse(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization PI settings", err.Error())
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read organization PI settings", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags := organizationPISettingsFromAPI(ctx, got.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrganizationPISettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan organizationPISettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := organizationPISettingsRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateOrganizationPiSettingsWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update organization PI settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update organization PI settings", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags := organizationPISettingsFromAPI(ctx, updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete resets the organization's policy to the defaults.
func (r *OrganizationPISettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	deleted, err := r.client.ResetOrganizationPiSettingsWithResponse(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to reset organization PI settings", err.Error())
		return
	}
	if deleted.StatusCode() != http.StatusOK {
		resp.Diagnostics.AddError("Unable to reset organization PI settings", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports the organization's policy. There is one per
// organization, so any ID works; the read sets it to "organization".
func (r *OrganizationPISettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// organizationPISettingsRequest builds the PUT body from the plan. PUT
// replaces the whole policy, so it sends every setting.
func organizationPISettingsRequest(ctx context.Context, plan organizationPISettingsResourceModel) (client.OrganizationPISettingsV1, diag.Diagnostics) {
	var diags diag.Diagnostics
	patterns := []string{}
	diags.Append(plan.PiAllowlistPatterns.ElementsAs(ctx, &patterns, false)...)

	piMode := client.OrganizationPISettingsV1PiMode(plan.PiMode.ValueString())
	piIndirectMode := client.OrganizationPISettingsV1PiIndirectMode(plan.PiIndirectMode.ValueString())
	piInputAction := client.OrganizationPISettingsV1PiInputAction(plan.PiInputAction.ValueString())
	piOutputAction := client.OrganizationPISettingsV1PiOutputAction(plan.PiOutputAction.ValueString())
	body := client.OrganizationPISettingsV1{
		PiMode:                 &piMode,
		PiIndirectMode:         &piIndirectMode,
		PiInputAction:          &piInputAction,
		PiOutputAction:         &piOutputAction,
		PiBlockThreshold:       plan.PiBlockThreshold.ValueFloat64Pointer(),
		PiPassThreshold:        plan.PiPassThreshold.ValueFloat64Pointer(),
		PiTier2aBlockThreshold: plan.PiTier2aBlockThreshold.ValueFloat64Pointer(),
		PiTier2bBlockThreshold: plan.PiTier2bBlockThreshold.ValueFloat64Pointer(),
		PiFailClosed:           plan.PiFailClosed.ValueBoolPointer(),
		PiLogFullTextOnBlock:   plan.PiLogFullTextOnBlock.ValueBoolPointer(),
		PiAllowlistPatterns:    &patterns,
		PiSaferVendorRoute:     nullable.NewNullNullable[string](),
	}
	if !plan.PiSaferVendorRoute.IsNull() {
		body.PiSaferVendorRoute = nullable.NewNullableWithValue(plan.PiSaferVendorRoute.ValueString())
	}
	return body, diags
}

// organizationPISettingsFromAPI maps the API's policy to the Terraform model.
func organizationPISettingsFromAPI(ctx context.Context, p *client.OrganizationPISettingsV1Response) (organizationPISettingsResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	patterns := p.PiAllowlistPatterns
	if patterns == nil {
		patterns = []string{}
	}
	allowlist, d := types.ListValueFrom(ctx, types.StringType, patterns)
	diags.Append(d...)

	return organizationPISettingsResourceModel{
		ID:                     types.StringValue(organizationPISettingsID),
		PiMode:                 types.StringValue(string(p.PiMode)),
		PiIndirectMode:         types.StringValue(string(p.PiIndirectMode)),
		PiInputAction:          types.StringValue(string(p.PiInputAction)),
		PiOutputAction:         types.StringValue(string(p.PiOutputAction)),
		PiBlockThreshold:       types.Float64Value(p.PiBlockThreshold),
		PiPassThreshold:        types.Float64Value(p.PiPassThreshold),
		PiTier2aBlockThreshold: types.Float64Value(p.PiTier2aBlockThreshold),
		PiTier2bBlockThreshold: types.Float64Value(p.PiTier2bBlockThreshold),
		PiFailClosed:           types.BoolValue(p.PiFailClosed),
		PiLogFullTextOnBlock:   types.BoolValue(p.PiLogFullTextOnBlock),
		PiAllowlistPatterns:    allowlist,
		PiSaferVendorRoute:     nullableString(p.PiSaferVendorRoute),
	}, diags
}
