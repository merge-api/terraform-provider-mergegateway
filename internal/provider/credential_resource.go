// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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
	_ resource.Resource                = &CredentialResource{}
	_ resource.ResourceWithConfigure   = &CredentialResource{}
	_ resource.ResourceWithImportState = &CredentialResource{}
	_ resource.ResourceWithModifyPlan  = &CredentialResource{}
)

func NewCredentialResource() resource.Resource {
	return &CredentialResource{}
}

// CredentialResource manages a bring-your-own-key (BYOK) vendor credential
// through /v1/credentials.
type CredentialResource struct {
	client *client.ClientWithResponses
}

// credentialResourceModel describes the resource data model. The _wo secrets
// are write-only: only the configuration holds them, and they are null in the
// plan and state.
type credentialResourceModel struct {
	ID                          types.String `tfsdk:"id"`
	Vendor                      types.String `tfsdk:"vendor"`
	Name                        types.String `tfsdk:"name"`
	APIKeyWO                    types.String `tfsdk:"api_key_wo"`
	APIKeyWOVersion             types.Int64  `tfsdk:"api_key_wo_version"`
	APIBase                     types.String `tfsdk:"api_base"`
	FallbackToManaged           types.Bool   `tfsdk:"fallback_to_managed"`
	Config                      types.Object `tfsdk:"config"`
	AWSSecretAccessKeyWO        types.String `tfsdk:"aws_secret_access_key_wo"`
	AWSSecretAccessKeyWOVersion types.Int64  `tfsdk:"aws_secret_access_key_wo_version"`
	ServiceAccountJSONWO        types.String `tfsdk:"service_account_json_wo"`
	ServiceAccountJSONWOVersion types.Int64  `tfsdk:"service_account_json_wo_version"`
	CredentialHint              types.String `tfsdk:"credential_hint"`
	CreatedAt                   types.String `tfsdk:"created_at"`
	UpdatedAt                   types.String `tfsdk:"updated_at"`
}

// credentialConfigModel holds the vendor-specific settings. Their secrets are
// the write-only aws_secret_access_key_wo and service_account_json_wo: config
// is computed, and the framework allows no write-only attribute inside a
// computed one.
type credentialConfigModel struct {
	APIVersion      types.String `tfsdk:"api_version"`
	AzureDeployment types.String `tfsdk:"azure_deployment"`
	AWSRegion       types.String `tfsdk:"aws_region"`
	AWSAccessKeyID  types.String `tfsdk:"aws_access_key_id"`
	AWSRoleARN      types.String `tfsdk:"aws_role_arn"`
	VertexProject   types.String `tfsdk:"vertex_project"`
	VertexLocation  types.String `tfsdk:"vertex_location"`
}

var credentialConfigAttrTypes = map[string]attr.Type{
	"api_version":       types.StringType,
	"azure_deployment":  types.StringType,
	"aws_region":        types.StringType,
	"aws_access_key_id": types.StringType,
	"aws_role_arn":      types.StringType,
	"vertex_project":    types.StringType,
	"vertex_location":   types.StringType,
}

// The vendors whose credentials carry their auth in config rather than api_key_wo.
const (
	vendorVertexAI = "vertexai"
	vendorBedrock  = "bedrock"
)

func (r *CredentialResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential"
}

func (r *CredentialResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	const keep = " Removing it keeps the current value: the API can't clear it, so Terraform stops managing it."
	configString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description + keep,
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		}
	}
	// writeOnly is a write-only secret, sent again when its _version changes.
	writeOnly := func(name, description string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description + " Write-only (Terraform 1.11+): Terraform sends it to the API but never " +
				"stores it in the plan or state. It is sent on create, and on update only when `" + name + "_version` " +
				"changes: to rotate it, set the new value and change the version. The API never returns it, so a change " +
				"made outside Terraform isn't detected. Removing it keeps the stored value.",
			Optional:   true,
			Sensitive:  true,
			WriteOnly:  true,
			Validators: []validator.String{stringvalidator.LengthAtLeast(1), stringvalidator.AlsoRequires(path.MatchRoot(name + "_version"))},
		}
	}
	version := func(name string) schema.Int64Attribute {
		return schema.Int64Attribute{
			MarkdownDescription: "Set with `" + name + "`. Change it, to any other number, to send `" + name + "` again, " +
				"such as to rotate it.",
			Optional:   true,
			Validators: []validator.Int64{int64validator.AlsoRequires(path.MatchRoot(name))},
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a bring-your-own-key (BYOK) credential: the organization's own key for a vendor, " +
			"which the Gateway uses instead of Merge-managed access. An organization has at most one credential per vendor, " +
			"and BYOK needs a Pro or Enterprise plan: on the Free plan the API refuses it with 403 \"Bring-your-own-key " +
			"credentials require a Pro or Enterprise plan\". Requires Terraform 1.11 or later: the secrets are write-only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The credential's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"vendor": schema.StringAttribute{
				MarkdownDescription: "The vendor's slug, such as `openai`; see the `mergegateway_vendors` data source for those that allow BYOK. " +
					"It can't be changed after creation: changing it replaces the credential.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The credential's name. Leave it out for none." + keep,
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"api_key_wo": writeOnly("api_key_wo", "The vendor API key. Required to create a credential, except for `vertexai` "+
				"(which uses `service_account_json_wo`) and `bedrock` (which can use AWS keys or a role instead). "+
				"`credential_hint` shows the stored key's last 4 characters."),
			"api_key_wo_version": version("api_key_wo"),
			"api_base": schema.StringAttribute{
				MarkdownDescription: "A custom base URL for the vendor's API." + keep,
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"fallback_to_managed": schema.BoolAttribute{
				MarkdownDescription: "When a request with this key fails, retry it once on Merge-managed access for the same vendor, " +
					"if the organization has it. Defaults to `true`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"config": schema.SingleNestedAttribute{
				MarkdownDescription: "Vendor-specific settings. Each applies only to the vendors it names. " +
					"Removing the block keeps every current setting.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"api_version":       configString("Azure OpenAI API version."),
					"azure_deployment":  configString("Azure OpenAI deployment name."),
					"aws_region":        configString("Bedrock region, such as `us-east-1`. Required to create a `bedrock` credential."),
					"aws_access_key_id": configString("Bedrock access key ID, set with `aws_secret_access_key_wo`."),
					"aws_role_arn":      configString("Bedrock IAM role to assume, such as `arn:aws:iam::123456789012:role/gateway`."),
					"vertex_project":    configString("Vertex AI project ID. Required to create a `vertexai` credential."),
					"vertex_location":   configString("Vertex AI location. Required to create a `vertexai` credential."),
				},
			},
			"aws_secret_access_key_wo": writeOnly("aws_secret_access_key_wo",
				"Bedrock secret access key, set with `config.aws_access_key_id`."),
			"aws_secret_access_key_wo_version": version("aws_secret_access_key_wo"),
			"service_account_json_wo": writeOnly("service_account_json_wo",
				"Vertex AI service account key file, as a JSON string. Required to create a `vertexai` credential."),
			"service_account_json_wo_version": version("service_account_json_wo"),
			"credential_hint": schema.StringAttribute{
				MarkdownDescription: "The last 4 characters of the stored API key, to tell keys apart. " +
					"Null when there is no API key or it is shorter than 12 characters.",
				Computed: true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the credential was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the credential was last changed (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (r *CredentialResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan catches, when creating a credential, the secrets and settings its
// vendor needs that the API would reject at apply. Only a create needs them:
// removing one later keeps the current value.
func (r *CredentialResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Updating or destroying the credential. A new vendor plans a create.
	if !req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	// The write-only secrets are only in the configuration.
	var config credentialResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(credentialConfigErrors(ctx, config)...)
}

func (r *CredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config credentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := credentialCreateRequest(ctx, plan, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateCredentialWithResponse(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create credential", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create credential", apiError(created.StatusCode(), created.Body))
		return
	}
	tflog.Trace(ctx, "created a credential", map[string]any{"id": created.JSON201.Id.String()})

	state, diags := credentialFromAPI(ctx, created.JSON201, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state credentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "credential")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetCredentialWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read credential", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "credential not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read credential", apiError(got.StatusCode(), got.Body))
		return
	}

	state, diags = credentialFromAPI(ctx, got.JSON200, state)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config credentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "credential")
	resp.Diagnostics.Append(diags...)
	body, diags := credentialUpdateRequest(ctx, plan, state, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateCredentialWithResponse(ctx, id, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update credential", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update credential", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state, diags = credentialFromAPI(ctx, updated.JSON200, plan)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state credentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "credential")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteCredentialWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete credential", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete credential", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports by credential ID. The secrets can't be imported: the
// API never returns them, and an import leaves their _wo_version null, so the
// first apply after an import sends the configured secrets again.
func (r *CredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// settings reads config into its model. A null or unknown config, or
// setting, reads as null.
func (m credentialResourceModel) settings(ctx context.Context) (credentialConfigModel, diag.Diagnostics) {
	var c credentialConfigModel
	diags := m.Config.As(ctx, &c, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})
	return c, diags
}

// credentialConfigErrors reports the secrets and settings each kind of vendor
// needs to create a credential. m is the configuration. Values not known yet
// are skipped.
func credentialConfigErrors(ctx context.Context, m credentialResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if m.Vendor.IsUnknown() {
		return diags
	}
	config, d := m.settings(ctx)
	diags.Append(d...)
	missing := func(attrPath path.Path, v types.String, detail string) {
		if v.IsNull() {
			diags.AddAttributeError(attrPath, "Missing "+attrPath.String(), detail)
		}
	}
	// A config not known yet can't be checked.
	configPath := path.Root("config")
	missingSetting := func(name string, v types.String, detail string) {
		if !m.Config.IsUnknown() {
			missing(configPath.AtName(name), v, detail)
		}
	}

	switch m.Vendor.ValueString() {
	case vendorVertexAI:
		const detail = "Vendor \"vertexai\" needs service_account_json_wo, config.vertex_project and config.vertex_location."
		missing(path.Root("service_account_json_wo"), m.ServiceAccountJSONWO, detail)
		missingSetting("vertex_project", config.VertexProject, detail)
		missingSetting("vertex_location", config.VertexLocation, detail)
	case vendorBedrock:
		missingSetting("aws_region", config.AWSRegion, "Vendor \"bedrock\" needs config.aws_region.")
		// Access keys go in pairs.
		const pair = "config.aws_access_key_id and aws_secret_access_key_wo are set together."
		if !m.AWSSecretAccessKeyWO.IsNull() {
			missingSetting("aws_access_key_id", config.AWSAccessKeyID, pair)
		}
		if !config.AWSAccessKeyID.IsNull() {
			missing(path.Root("aws_secret_access_key_wo"), m.AWSSecretAccessKeyWO, pair)
		}
	default:
		missing(path.Root("api_key_wo"), m.APIKeyWO, fmt.Sprintf("Vendor %q needs api_key_wo.", m.Vendor.ValueString()))
	}
	return diags
}

// rotated is the secret to send on update: the configured one when its
// version changes, else null.
func rotated(secret types.String, planned, current types.Int64) types.String {
	if planned.Equal(current) {
		return types.StringNull()
	}
	return secret
}

// credentialCreateRequest builds the POST /v1/credentials body from the plan,
// and the secrets from the configuration.
func credentialCreateRequest(ctx context.Context, plan, config credentialResourceModel) (client.CredentialV1Create, diag.Diagnostics) {
	settings, diags := plan.settings(ctx)
	fallback := plan.FallbackToManaged.ValueBool()
	body := client.CredentialV1Create{
		Vendor:            plan.Vendor.ValueString(),
		Name:              optionalString(plan.Name),
		ApiKey:            optionalString(config.APIKeyWO),
		ApiBase:           optionalString(plan.APIBase),
		FallbackToManaged: &fallback,
	}
	if c, ok := credentialConfigToAPI(settings, credentialConfigModel{}, config.AWSSecretAccessKeyWO, config.ServiceAccountJSONWO); ok {
		body.Config = nullable.NewNullableWithValue(c)
	}
	return body, diags
}

// credentialUpdateRequest builds the PATCH /v1/credentials/{id} body. It
// sends the settings that differ between plan and state, and a secret from
// the configuration only when its _wo_version changes. A setting removed from
// the configuration keeps its state value in the plan, so nothing is ever
// sent as null (which the API treats as "keep"). vendor is never sent:
// changing it replaces the credential.
func credentialUpdateRequest(ctx context.Context, plan, state, config credentialResourceModel) (client.CredentialV1Update, diag.Diagnostics) {
	var body client.CredentialV1Update
	changed := func(planned, current types.String) nullable.Nullable[string] {
		if planned.Equal(current) {
			return nil
		}
		return optionalString(planned)
	}

	body.Name = changed(plan.Name, state.Name)
	body.ApiKey = optionalString(rotated(config.APIKeyWO, plan.APIKeyWOVersion, state.APIKeyWOVersion))
	body.ApiBase = changed(plan.APIBase, state.APIBase)
	if !plan.FallbackToManaged.Equal(state.FallbackToManaged) {
		body.FallbackToManaged = nullable.NewNullableWithValue(plan.FallbackToManaged.ValueBool())
	}

	planned, diags := plan.settings(ctx)
	current, d := state.settings(ctx)
	diags.Append(d...)
	if c, ok := credentialConfigToAPI(planned, current,
		rotated(config.AWSSecretAccessKeyWO, plan.AWSSecretAccessKeyWOVersion, state.AWSSecretAccessKeyWOVersion),
		rotated(config.ServiceAccountJSONWO, plan.ServiceAccountJSONWOVersion, state.ServiceAccountJSONWOVersion),
	); ok {
		body.Config = nullable.NewNullableWithValue(c)
	}
	return body, diags
}

// credentialConfigToAPI builds the config to send: every setting that is set
// and differs from prior (empty on create), and the secrets that are set. ok
// is false when there is nothing to send.
func credentialConfigToAPI(config, prior credentialConfigModel, awsSecretAccessKey, serviceAccountJSON types.String) (c client.CredentialConfigV1Request, ok bool) {
	set := func(v types.String) nullable.Nullable[string] {
		if v.IsNull() || v.IsUnknown() {
			return nil
		}
		ok = true
		return nullable.NewNullableWithValue(v.ValueString())
	}
	changed := func(planned, current types.String) nullable.Nullable[string] {
		if planned.Equal(current) {
			return nil
		}
		return set(planned)
	}
	c = client.CredentialConfigV1Request{
		ApiVersion:         changed(config.APIVersion, prior.APIVersion),
		AzureDeployment:    changed(config.AzureDeployment, prior.AzureDeployment),
		AwsRegion:          changed(config.AWSRegion, prior.AWSRegion),
		AwsAccessKeyId:     changed(config.AWSAccessKeyID, prior.AWSAccessKeyID),
		AwsSecretAccessKey: set(awsSecretAccessKey),
		AwsRoleArn:         changed(config.AWSRoleARN, prior.AWSRoleARN),
		VertexProject:      changed(config.VertexProject, prior.VertexProject),
		VertexLocation:     changed(config.VertexLocation, prior.VertexLocation),
		ServiceAccountJson: set(serviceAccountJSON),
	}
	return c, ok
}

// credentialFromAPI maps an API credential to the Terraform model. prior is
// the plan (after create or update) or the state (after read): the
// _wo_versions come from it. The secrets stay null: the API never returns
// them, and Terraform never stores them.
func credentialFromAPI(ctx context.Context, c *client.CredentialV1Response, prior credentialResourceModel) (credentialResourceModel, diag.Diagnostics) {
	m := credentialResourceModel{
		ID:                          types.StringValue(c.Id.String()),
		Vendor:                      types.StringValue(c.Vendor),
		Name:                        nullableString(c.Name),
		APIKeyWO:                    types.StringNull(),
		APIKeyWOVersion:             prior.APIKeyWOVersion,
		APIBase:                     nullableString(c.ApiBase),
		FallbackToManaged:           types.BoolValue(c.FallbackToManaged),
		AWSSecretAccessKeyWO:        types.StringNull(),
		AWSSecretAccessKeyWOVersion: prior.AWSSecretAccessKeyWOVersion,
		ServiceAccountJSONWO:        types.StringNull(),
		ServiceAccountJSONWOVersion: prior.ServiceAccountJSONWOVersion,
		CredentialHint:              nullableString(c.CredentialHint),
		CreatedAt:                   nullableTime(c.CreatedAt),
		UpdatedAt:                   nullableTime(c.UpdatedAt),
	}

	var diags diag.Diagnostics
	m.Config, diags = types.ObjectValueFrom(ctx, credentialConfigAttrTypes, credentialConfigModel{
		APIVersion:      nullableString(c.Config.ApiVersion),
		AzureDeployment: nullableString(c.Config.AzureDeployment),
		AWSRegion:       nullableString(c.Config.AwsRegion),
		AWSAccessKeyID:  nullableString(c.Config.AwsAccessKeyId),
		AWSRoleARN:      nullableString(c.Config.AwsRoleArn),
		VertexProject:   nullableString(c.Config.VertexProject),
		VertexLocation:  nullableString(c.Config.VertexLocation),
	})
	return m, diags
}

// optionalString is the API value of an optional string: unset when null.
func optionalString(v types.String) nullable.Nullable[string] {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return nullable.NewNullableWithValue(v.ValueString())
}
