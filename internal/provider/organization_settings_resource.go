package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
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
	_ resource.Resource                = &OrganizationSettingsResource{}
	_ resource.ResourceWithConfigure   = &OrganizationSettingsResource{}
	_ resource.ResourceWithImportState = &OrganizationSettingsResource{}
)

func NewOrganizationSettingsResource() resource.Resource {
	return &OrganizationSettingsResource{}
}

// OrganizationSettingsResource manages the settings of the management key's
// organization through /v1/organization. The organization always exists, so
// create and update are the same PATCH, and delete only forgets it.
type OrganizationSettingsResource struct {
	client *client.ClientWithResponses
}

// organizationSettingsResourceModel describes the resource data model. Every
// setting is optional and computed: one left out of the config keeps the
// organization's current value. tag_definitions and vendor_restrictions are
// framework values, since they can be unknown in a plan.
type organizationSettingsResourceModel struct {
	ID                          types.String `tfsdk:"id"`
	Name                        apiString    `tfsdk:"name"`
	Slug                        types.String `tfsdk:"slug"`
	TagDefinitions              types.List   `tfsdk:"tag_definitions"`
	LogPayloads                 types.Bool   `tfsdk:"log_payloads"`
	ZeroDataRetention           types.Bool   `tfsdk:"zero_data_retention"`
	WebSearchMode               types.String `tfsdk:"web_search_mode"`
	AutoHandleModelDeprecations types.Bool   `tfsdk:"auto_handle_model_deprecations"`
	VendorRestrictions          types.Object `tfsdk:"vendor_restrictions"`
	CreatedAt                   types.String `tfsdk:"created_at"`
}

// tagDefinitionModel is one tag_definitions entry.
type tagDefinitionModel struct {
	Key    types.String `tfsdk:"key"`
	Values types.List   `tfsdk:"values"`
}

var tagDefinitionAttrTypes = map[string]attr.Type{
	"key":    types.StringType,
	"values": types.ListType{ElemType: types.StringType},
}

// vendorRestrictionsModel is the vendor_restrictions object.
type vendorRestrictionsModel struct {
	AllowedVendors          types.List   `tfsdk:"allowed_vendors"`
	IgnoredVendors          types.List   `tfsdk:"ignored_vendors"`
	VendorRoutingPreference types.List   `tfsdk:"vendor_routing_preference"`
	AllowedRegions          types.List   `tfsdk:"allowed_regions"`
	IgnoredRegions          types.List   `tfsdk:"ignored_regions"`
	PreferBYOK              types.Bool   `tfsdk:"prefer_byok"`
	BYOKOnly                types.Bool   `tfsdk:"byok_only"`
	CustomerKeyUsageDefault types.String `tfsdk:"customer_key_usage_default"`
}

var vendorRestrictionsAttrTypes = map[string]attr.Type{
	"allowed_vendors":            types.ListType{ElemType: types.StringType},
	"ignored_vendors":            types.ListType{ElemType: types.StringType},
	"vendor_routing_preference":  types.ListType{ElemType: types.StringType},
	"allowed_regions":            types.ListType{ElemType: types.StringType},
	"ignored_regions":            types.ListType{ElemType: types.StringType},
	"prefer_byok":                types.BoolType,
	"byok_only":                  types.BoolType,
	"customer_key_usage_default": types.StringType,
}

func (r *OrganizationSettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_settings"
}

func (r *OrganizationSettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	const keep = " Leave it out to keep the organization's current value."
	setting := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{
			MarkdownDescription: description + keep,
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		}
	}
	vendorList := func(description string) schema.ListAttribute {
		return schema.ListAttribute{
			MarkdownDescription: description + " Defaults to none.",
			ElementType:         types.StringType,
			Optional:            true,
			Computed:            true,
			Default:             listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
			Validators:          []validator.List{listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the settings of the organization the management key belongs to. " +
			"The organization always exists, so this resource never creates or deletes anything: creating it updates the " +
			"settings in its config, settings left out keep their current values, and destroying it only removes it from " +
			"the Terraform state, leaving the settings as they are. Declare it at most once.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The organization's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The organization's name. The API trims surrounding whitespace." + keep,
				Optional:            true,
				Computed:            true,
				CustomType:          apiStringType{form: trimmedForm},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					// Trimmed, a blank name would be empty.
					stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be blank"),
				},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Set when the organization is created. It does not change.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"tag_definitions": schema.ListNestedAttribute{
				MarkdownDescription: "The request tag keys and the values each allows, kept in the order given. " +
					"`[]` removes them all." + keep,
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							MarkdownDescription: "The tag key.",
							Required:            true,
						},
						"values": schema.ListAttribute{
							MarkdownDescription: "The values the key allows.",
							ElementType:         types.StringType,
							Required:            true,
						},
					},
				},
			},
			"log_payloads":        setting("Whether to store request and response bodies."),
			"zero_data_retention": setting("Whether to route only to vendors that retain no data."),
			"web_search_mode": schema.StringAttribute{
				MarkdownDescription: "How web search runs: `auto`, `native` or `merge`." + keep,
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.OneOf(
					string(client.OrganizationSettingsV1UpdateWebSearchModeAuto),
					string(client.OrganizationSettingsV1UpdateWebSearchModeNative),
					string(client.OrganizationSettingsV1UpdateWebSearchModeMerge),
				)},
			},
			"auto_handle_model_deprecations": setting("Whether to move routing policies off deprecated models automatically."),
			"vendor_restrictions": schema.SingleNestedAttribute{
				MarkdownDescription: "Which vendors and regions the organization's traffic may use. It is set as a whole: " +
					"attributes left out of the block take their defaults." + keep,
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"allowed_vendors":           vendorList("The only vendors requests may be served by."),
					"ignored_vendors":           vendorList("The vendors requests are never served by."),
					"vendor_routing_preference": vendorList("The vendors to prefer, most preferred first."),
					"allowed_regions":           vendorList("The only regions requests may be served from."),
					"ignored_regions":           vendorList("The regions requests are never served from."),
					"prefer_byok": schema.BoolAttribute{
						MarkdownDescription: "Prefer the organization's own credentials over Merge-managed access. Defaults to `false`.",
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
					},
					"byok_only": schema.BoolAttribute{
						MarkdownDescription: "Use only the organization's own credentials. Defaults to `false`.",
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
					},
					"customer_key_usage_default": schema.StringAttribute{
						MarkdownDescription: "The default key usage for end customers without their own setting: `use_any`, " +
							"`prefer_byok`, `byok_only` or `customer_only`. Leave it out to have customers follow " +
							"`prefer_byok` and `byok_only`.",
						Optional: true,
						Validators: []validator.String{stringvalidator.OneOf(
							string(client.UseAny),
							string(client.PreferByok),
							string(client.ByokOnly),
							string(client.CustomerOnly),
						)},
					},
				},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the organization was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *OrganizationSettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create sets the configured settings on the organization, which already exists.
func (r *OrganizationSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Compared with an empty state, every known setting is sent.
	body, diags := organizationSettingsRequest(ctx, plan, organizationSettingsResourceModel{})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateOrganizationSettingsWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to set organization settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to set organization settings", apiError(updated.StatusCode(), updated.Body))
		return
	}
	tflog.Trace(ctx, "set organization settings", map[string]any{"id": updated.JSON200.Id.String()})

	state, diags := organizationSettingsFromAPI(ctx, updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrganizationSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	got, err := r.client.GetOrganizationSettingsWithResponse(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization settings", err.Error())
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read organization settings", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags := organizationSettingsFromAPI(ctx, got.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *OrganizationSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state organizationSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := organizationSettingsRequest(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateOrganizationSettingsWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update organization settings", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update organization settings", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = organizationSettingsFromAPI(ctx, updated.JSON200)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete only removes the settings from state: the organization can't be
// deleted, and its settings stay as they are.
func (r *OrganizationSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "removing organization settings from state; the organization keeps them")
}

// ImportState imports the organization's settings. There is only one
// organization per management key, so any ID works, such as "organization".
func (r *OrganizationSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// organizationSettingsRequest builds the PATCH /v1/organization body. It sends
// only the settings that are known in the plan and differ from state; the API
// keeps the rest. Settings are optional and computed, so they are never null
// in a plan.
func organizationSettingsRequest(ctx context.Context, plan, state organizationSettingsResourceModel) (client.OrganizationSettingsV1Update, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body client.OrganizationSettingsV1Update
	send := func(planned, current attr.Value) bool {
		return !planned.IsUnknown() && !planned.IsNull() && !planned.Equal(current)
	}

	if send(plan.Name, state.Name) {
		body.Name = nullable.NewNullableWithValue(plan.Name.ValueString())
	}
	if send(plan.TagDefinitions, state.TagDefinitions) {
		var entries []tagDefinitionModel
		diags.Append(plan.TagDefinitions.ElementsAs(ctx, &entries, false)...)
		// Send [] rather than omitting it, so removing every tag clears them.
		tags := make([]client.TagDefinitionV1, 0, len(entries))
		for _, e := range entries {
			values := []string{}
			diags.Append(e.Values.ElementsAs(ctx, &values, false)...)
			tags = append(tags, client.TagDefinitionV1{Key: e.Key.ValueString(), Values: values})
		}
		body.TagDefinitions = nullable.NewNullableWithValue(tags)
	}
	if send(plan.LogPayloads, state.LogPayloads) {
		body.LogPayloads = nullable.NewNullableWithValue(plan.LogPayloads.ValueBool())
	}
	if send(plan.ZeroDataRetention, state.ZeroDataRetention) {
		body.ZeroDataRetention = nullable.NewNullableWithValue(plan.ZeroDataRetention.ValueBool())
	}
	if send(plan.WebSearchMode, state.WebSearchMode) {
		body.WebSearchMode = nullable.NewNullableWithValue(client.OrganizationSettingsV1UpdateWebSearchMode(plan.WebSearchMode.ValueString()))
	}
	if send(plan.AutoHandleModelDeprecations, state.AutoHandleModelDeprecations) {
		body.AutoHandleModelDeprecations = nullable.NewNullableWithValue(plan.AutoHandleModelDeprecations.ValueBool())
	}
	if send(plan.VendorRestrictions, state.VendorRestrictions) {
		var v vendorRestrictionsModel
		diags.Append(plan.VendorRestrictions.As(ctx, &v, basetypes.ObjectAsOptions{})...)
		restrictions := client.VendorRestrictionsV1{
			AllowedVendors:          stringsToAPI(ctx, v.AllowedVendors, &diags),
			IgnoredVendors:          stringsToAPI(ctx, v.IgnoredVendors, &diags),
			VendorRoutingPreference: stringsToAPI(ctx, v.VendorRoutingPreference, &diags),
			AllowedRegions:          stringsToAPI(ctx, v.AllowedRegions, &diags),
			IgnoredRegions:          stringsToAPI(ctx, v.IgnoredRegions, &diags),
			PreferByok:              v.PreferBYOK.ValueBoolPointer(),
			ByokOnly:                v.BYOKOnly.ValueBoolPointer(),
		}
		// Sent as null when left out: the API replaces the whole object.
		restrictions.CustomerKeyUsageDefault = nullable.NewNullNullable[client.VendorRestrictionsV1CustomerKeyUsageDefault]()
		if !v.CustomerKeyUsageDefault.IsNull() {
			restrictions.CustomerKeyUsageDefault = nullable.NewNullableWithValue(
				client.VendorRestrictionsV1CustomerKeyUsageDefault(v.CustomerKeyUsageDefault.ValueString()))
		}
		body.VendorRestrictions = nullable.NewNullableWithValue(restrictions)
	}
	return body, diags
}

// stringsToAPI converts a list of strings for a request body, [] for an empty list.
func stringsToAPI(ctx context.Context, l types.List, diags *diag.Diagnostics) *[]string {
	values := []string{}
	diags.Append(l.ElementsAs(ctx, &values, false)...)
	return &values
}

// stringsFromAPI converts a list of strings from a response, [] for a missing one.
func stringsFromAPI(ctx context.Context, values *[]string, diags *diag.Diagnostics) types.List {
	l := []string{}
	if values != nil && *values != nil {
		l = *values
	}
	list, d := types.ListValueFrom(ctx, types.StringType, l)
	diags.Append(d...)
	return list
}

// organizationSettingsFromAPI maps the API's settings to the Terraform model.
func organizationSettingsFromAPI(ctx context.Context, o *client.OrganizationSettingsV1Response) (organizationSettingsResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := organizationSettingsResourceModel{
		ID:                          types.StringValue(o.Id.String()),
		Name:                        trimmedForm.value(types.StringValue(o.Name)),
		Slug:                        types.StringValue(o.Slug),
		LogPayloads:                 types.BoolValue(o.LogPayloads),
		ZeroDataRetention:           types.BoolValue(o.ZeroDataRetention),
		WebSearchMode:               types.StringValue(string(o.WebSearchMode)),
		AutoHandleModelDeprecations: types.BoolValue(o.AutoHandleModelDeprecations),
		CreatedAt:                   types.StringValue(o.CreatedAt.Format(time.RFC3339)),
	}

	tags := make([]tagDefinitionModel, 0, len(o.TagDefinitions))
	for _, td := range o.TagDefinitions {
		values := td.Values
		tags = append(tags, tagDefinitionModel{Key: types.StringValue(td.Key), Values: stringsFromAPI(ctx, &values, &diags)})
	}
	var d diag.Diagnostics
	m.TagDefinitions, d = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: tagDefinitionAttrTypes}, tags)
	diags.Append(d...)

	v := o.VendorRestrictions
	restrictions := vendorRestrictionsModel{
		AllowedVendors:          stringsFromAPI(ctx, v.AllowedVendors, &diags),
		IgnoredVendors:          stringsFromAPI(ctx, v.IgnoredVendors, &diags),
		VendorRoutingPreference: stringsFromAPI(ctx, v.VendorRoutingPreference, &diags),
		AllowedRegions:          stringsFromAPI(ctx, v.AllowedRegions, &diags),
		IgnoredRegions:          stringsFromAPI(ctx, v.IgnoredRegions, &diags),
		PreferBYOK:              types.BoolValue(v.PreferByok != nil && *v.PreferByok),
		BYOKOnly:                types.BoolValue(v.ByokOnly != nil && *v.ByokOnly),
		CustomerKeyUsageDefault: types.StringNull(),
	}
	if mode, err := v.CustomerKeyUsageDefault.Get(); err == nil {
		restrictions.CustomerKeyUsageDefault = types.StringValue(string(mode))
	}
	m.VendorRestrictions, d = types.ObjectValueFrom(ctx, vendorRestrictionsAttrTypes, restrictions)
	diags.Append(d...)

	return m, diags
}
