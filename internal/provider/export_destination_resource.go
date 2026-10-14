package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/oapi-codegen/nullable"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &ExportDestinationResource{}
	_ resource.ResourceWithConfigure      = &ExportDestinationResource{}
	_ resource.ResourceWithImportState    = &ExportDestinationResource{}
	_ resource.ResourceWithValidateConfig = &ExportDestinationResource{}
)

func NewExportDestinationResource() resource.Resource {
	return &ExportDestinationResource{}
}

// ExportDestinationResource manages a telemetry export destination through
// /v1/export-destinations.
type ExportDestinationResource struct {
	client *client.ClientWithResponses
}

// exportDestinationResourceModel describes the resource data model.
// credential_secret_wo is write-only: only the configuration holds it, and it
// is null in the plan and state.
type exportDestinationResourceModel struct {
	ID                        types.String  `tfsdk:"id"`
	OrganizationID            types.String  `tfsdk:"organization_id"`
	Name                      apiString     `tfsdk:"name"`
	Kind                      types.String  `tfsdk:"kind"`
	OTLPPreset                types.String  `tfsdk:"otlp_preset"`
	Site                      apiString     `tfsdk:"site"`
	OTLPEndpoint              apiString     `tfsdk:"otlp_endpoint"`
	MLApp                     apiString     `tfsdk:"ml_app"`
	CredentialSecretWO        types.String  `tfsdk:"credential_secret_wo"`
	CredentialSecretWOVersion types.Int64   `tfsdk:"credential_secret_wo_version"`
	CredentialUsername        apiString     `tfsdk:"credential_username"`
	CredentialHint            types.String  `tfsdk:"credential_hint"`
	Enabled                   types.Bool    `tfsdk:"enabled"`
	PrivacyMode               types.Bool    `tfsdk:"privacy_mode"`
	MetricsEnabled            types.Bool    `tfsdk:"metrics_enabled"`
	SampleRate                types.Float64 `tfsdk:"sample_rate"`
	ConsecutiveFailures       types.Int64   `tfsdk:"consecutive_failures"`
	DeliverySuspendedUntil    types.String  `tfsdk:"delivery_suspended_until"`
	LastSuccessAt             types.String  `tfsdk:"last_success_at"`
	LastErrorAt               types.String  `tfsdk:"last_error_at"`
	LastErrorMessage          types.String  `tfsdk:"last_error_message"`
	CreatedAt                 types.String  `tfsdk:"created_at"`
	UpdatedAt                 types.String  `tfsdk:"updated_at"`
}

// The presets an export destination can be created with.
const (
	presetDatadog = string(client.ExportDestinationV1CreateOtlpPresetDatadog)
	presetGrafana = string(client.ExportDestinationV1CreateOtlpPresetGrafana)
	presetGeneric = string(client.ExportDestinationV1CreateOtlpPresetGeneric)
)

func (r *ExportDestinationResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_export_destination"
}

func (r *ExportDestinationResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	timestamp := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description + " (RFC 3339), as of the last refresh.", Computed: true}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a telemetry export destination: where the Gateway sends request traces (OTLP spans, " +
			"and optionally metrics), such as Datadog, Grafana Cloud or your own OTLP collector. " +
			"Needs the telemetry export feature enabled for the organization. Requires Terraform 1.11 or later: the secret is write-only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The export destination's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the organization that owns the destination.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The destination's name, unique in the organization. The API trims surrounding whitespace.",
				Required:            true,
				CustomType:          apiStringType{form: trimmedForm},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					// Trimmed, a blank name would be empty.
					stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be blank"),
				},
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "The export protocol. Only `otlp` exists today, and it is the default. " +
					"Changing it replaces the destination.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(string(client.ExportDestinationV1CreateKindOtlp)),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf(string(client.ExportDestinationV1CreateKindOtlp))},
			},
			"otlp_preset": schema.StringAttribute{
				MarkdownDescription: "Where spans go: `datadog` (the default), `grafana` (Grafana Cloud) or `generic` " +
					"(any OTLP collector; needs an enterprise plan). Changing it replaces the destination.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(presetDatadog),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf(presetDatadog, presetGrafana, presetGeneric)},
			},
			"site": schema.StringAttribute{
				MarkdownDescription: "`datadog`: the Datadog site, such as `datadoghq.com`. `grafana`: the Grafana Cloud zone, " +
					"such as `prod-us-east-0`. Required for both; not allowed for `generic`. The API stores it in lowercase; " +
					"either case works, and the state keeps the one written.",
				Optional:   true,
				CustomType: apiStringType{form: lowercaseForm},
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9.-]+$`),
					"must be letters, digits, dots and hyphens")},
			},
			"otlp_endpoint": schema.StringAttribute{
				MarkdownDescription: "`generic` only, and required for it: the https URL of your OTLP collector, such as " +
					"`https://otel.example.com/v1`. The API stores it with a lowercase host, no `:443` port, no trailing slash " +
					"and no surrounding whitespace; either form works, and the state keeps the one written.",
				Optional:   true,
				CustomType: apiStringType{form: endpointForm},
				Validators: []validator.String{stringvalidator.LengthAtMost(255)},
			},
			"ml_app": schema.StringAttribute{
				MarkdownDescription: "`datadog` only: the `ml_app` tag on exported spans. Leave it out for none. " +
					"The API trims surrounding whitespace.",
				Optional:   true,
				CustomType: apiStringType{form: trimmedForm},
				Validators: []validator.String{stringvalidator.LengthBetween(1, 193)},
			},
			"credential_secret_wo": schema.StringAttribute{
				MarkdownDescription: "The API key or token the destination authenticates with. Write-only (Terraform 1.11+): " +
					"Terraform sends it to the API but never stores it in the plan or state. It is sent on create, and on update " +
					"only when `credential_secret_wo_version` changes: to rotate it, set the new secret and change the version. " +
					"The API never returns it, so a secret changed outside Terraform isn't detected; `credential_hint` shows the " +
					"stored secret's last 4 characters.",
				Required:   true,
				Sensitive:  true,
				WriteOnly:  true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"credential_secret_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Set with `credential_secret_wo`. Change it, to any other number, to send " +
					"`credential_secret_wo` again, such as to rotate it.",
				Required: true,
			},
			"credential_username": schema.StringAttribute{
				MarkdownDescription: "`grafana` only, and required for it: the Grafana Cloud instance ID. Not a secret. " +
					"The API trims surrounding whitespace.",
				Optional:   true,
				CustomType: apiStringType{form: trimmedForm},
				Validators: []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"credential_hint": schema.StringAttribute{
				MarkdownDescription: "The last 4 characters of the stored secret.",
				Computed:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the destination receives telemetry. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"privacy_mode": schema.BoolAttribute{
				MarkdownDescription: "Leave prompts and completions out of exported spans. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"metrics_enabled": schema.BoolAttribute{
				MarkdownDescription: "Also export metrics. Datadog and Grafana Cloud bill these separately. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"sample_rate": schema.Float64Attribute{
				MarkdownDescription: "The fraction of requests exported, from 0.0001 to 1. Defaults to `1`.",
				Optional:            true,
				Computed:            true,
				Default:             float64default.StaticFloat64(1),
				Validators:          []validator.Float64{float64validator.Between(0.0001, 1)},
			},
			"consecutive_failures": schema.Int64Attribute{
				MarkdownDescription: "Failed deliveries in a row, as of the last refresh.",
				Computed:            true,
			},
			"delivery_suspended_until": timestamp("Set while delivery is paused after repeated failures"),
			"last_success_at":          timestamp("The last successful delivery"),
			"last_error_at":            timestamp("The last failed delivery"),
			"last_error_message": schema.StringAttribute{
				MarkdownDescription: "Why the last delivery failed, as of the last refresh.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the destination was created (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the destination was last changed (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (r *ExportDestinationResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *ExportDestinationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config exportDestinationResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(exportDestinationConfigErrors(config)...)
}

func (r *ExportDestinationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// The write-only secret is only in the configuration.
	var plan, config exportDestinationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateExportDestinationWithResponse(ctx, exportDestinationCreateRequest(plan, config))
	if err != nil {
		resp.Diagnostics.AddError("Unable to create export destination", err.Error())
		return
	}
	if created.JSON201 == nil {
		resp.Diagnostics.AddError("Unable to create export destination", apiError(created.StatusCode(), created.Body))
		return
	}
	tflog.Trace(ctx, "created an export destination", map[string]any{"id": created.JSON201.Id.String()})

	state := exportDestinationFromAPI(created.JSON201, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ExportDestinationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state exportDestinationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "export destination")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.client.GetExportDestinationWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read export destination", err.Error())
		return
	}
	// Deleted outside Terraform: drop it from state so the next plan recreates it.
	if got.StatusCode() == http.StatusNotFound {
		tflog.Warn(ctx, "export destination not found, removing it from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if got.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to read export destination", apiError(got.StatusCode(), got.Body))
		return
	}

	state = exportDestinationFromAPI(got.JSON200, state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ExportDestinationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config exportDestinationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "export destination")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.client.UpdateExportDestinationWithResponse(ctx, id, exportDestinationUpdateRequest(plan, state, config))
	if err != nil {
		resp.Diagnostics.AddError("Unable to update export destination", err.Error())
		return
	}
	if updated.JSON200 == nil {
		resp.Diagnostics.AddError("Unable to update export destination", apiError(updated.StatusCode(), updated.Body))
		return
	}

	state = exportDestinationFromAPI(updated.JSON200, plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ExportDestinationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state exportDestinationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parseUUID(state.ID, "export destination")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleted, err := r.client.DeleteExportDestinationWithResponse(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete export destination", err.Error())
		return
	}
	// Already gone is fine.
	if deleted.StatusCode() != http.StatusNoContent && deleted.StatusCode() != http.StatusNotFound {
		resp.Diagnostics.AddError("Unable to delete export destination", apiError(deleted.StatusCode(), deleted.Body))
	}
}

// ImportState imports by destination ID. The secret can't be imported: the
// API never returns it, and an import leaves credential_secret_wo_version
// null, so the first apply after an import sends the configured secret again.
func (r *ExportDestinationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// exportDestinationConfigErrors reports the settings each preset needs or
// refuses, and an otlp_endpoint the API rejects. Values not known yet are
// skipped.
func exportDestinationConfigErrors(m exportDestinationResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	need := func(attr string, v attr.Value, preset string) {
		if v.IsNull() {
			diags.AddAttributeError(path.Root(attr), "Missing "+attr, fmt.Sprintf("A %s destination needs %s.", preset, attr))
		}
	}
	refuse := func(attr string, v attr.Value, preset string) {
		if !v.IsNull() && !v.IsUnknown() {
			diags.AddAttributeError(path.Root(attr), "Invalid "+attr, fmt.Sprintf("A %s destination takes no %s.", preset, attr))
		}
	}

	// otlp_preset is null in a config that leaves it to its default.
	preset := m.OTLPPreset.ValueString()
	if m.OTLPPreset.IsNull() {
		preset = presetDatadog
	}
	switch {
	case m.OTLPPreset.IsUnknown():
	case preset == presetGeneric:
		need("otlp_endpoint", m.OTLPEndpoint, preset)
		refuse("site", m.Site, preset)
		refuse("credential_username", m.CredentialUsername, preset)
		refuse("ml_app", m.MLApp, preset)
	case preset == presetGrafana:
		need("site", m.Site, preset)
		need("credential_username", m.CredentialUsername, preset)
		refuse("otlp_endpoint", m.OTLPEndpoint, preset)
		refuse("ml_app", m.MLApp, preset)
	default:
		need("site", m.Site, preset)
		refuse("otlp_endpoint", m.OTLPEndpoint, preset)
		refuse("credential_username", m.CredentialUsername, preset)
	}

	if !m.OTLPEndpoint.IsNull() && !m.OTLPEndpoint.IsUnknown() {
		if err := checkOTLPEndpoint(m.OTLPEndpoint.ValueString()); err != nil {
			diags.AddAttributeError(path.Root("otlp_endpoint"), "Invalid otlp_endpoint", err.Error())
		}
	}
	return diags
}

// checkOTLPEndpoint reports an endpoint the API rejects: one that isn't an
// https URL, or has a query string or fragment. The API trims surrounding
// whitespace first.
func checkOTLPEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	u, err := url.Parse(endpoint)
	switch {
	case err != nil:
		return fmt.Errorf("%q is not a URL: %w", endpoint, err)
	case !strings.HasPrefix(endpoint, "https://") || u.Host == "":
		return fmt.Errorf("%q must be an https URL, such as https://otel.example.com", endpoint)
	case u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(endpoint, "?#"):
		return fmt.Errorf("%q must not have a query string or fragment", endpoint)
	}
	return nil
}

// exportDestinationCreateRequest builds the POST /v1/export-destinations body
// from the plan, and the secret from the configuration.
func exportDestinationCreateRequest(plan, config exportDestinationResourceModel) client.ExportDestinationV1Create {
	kind := client.ExportDestinationV1CreateKind(plan.Kind.ValueString())
	preset := client.ExportDestinationV1CreateOtlpPreset(plan.OTLPPreset.ValueString())
	enabled := plan.Enabled.ValueBool()
	privacyMode := plan.PrivacyMode.ValueBool()
	metricsEnabled := plan.MetricsEnabled.ValueBool()
	sampleRate := plan.SampleRate.ValueFloat64()
	secret := config.CredentialSecretWO.ValueString()
	return client.ExportDestinationV1Create{
		Name:               plan.Name.ValueString(),
		Kind:               &kind,
		OtlpPreset:         &preset,
		Site:               optionalString(plan.Site.StringValue),
		OtlpEndpoint:       optionalString(plan.OTLPEndpoint.StringValue),
		MlApp:              optionalString(plan.MLApp.StringValue),
		CredentialSecret:   &secret,
		CredentialUsername: optionalString(plan.CredentialUsername.StringValue),
		Enabled:            &enabled,
		PrivacyMode:        &privacyMode,
		MetricsEnabled:     &metricsEnabled,
		SampleRate:         &sampleRate,
	}
}

// exportDestinationUpdateRequest builds the PATCH body. It sends only the
// fields that differ between plan and state, and the secret from the
// configuration only when credential_secret_wo_version changes. A removed
// ml_app or credential_username is sent as null, which clears it. site and
// otlp_endpoint are never sent as null (the API rejects it; the preset that
// takes one requires it), and kind and otlp_preset are never sent: changing
// them replaces the destination.
func exportDestinationUpdateRequest(plan, state, config exportDestinationResourceModel) client.ExportDestinationV1Update {
	var body client.ExportDestinationV1Update
	changed := func(planned, current types.String) *string {
		if planned.Equal(current) || planned.IsNull() {
			return nil
		}
		v := planned.ValueString()
		return &v
	}
	clearable := func(planned, current types.String) nullable.Nullable[string] {
		if planned.Equal(current) {
			return nil
		}
		if planned.IsNull() {
			return nullable.NewNullNullable[string]()
		}
		return nullable.NewNullableWithValue(planned.ValueString())
	}
	changedBool := func(planned, current types.Bool) *bool {
		if planned.Equal(current) {
			return nil
		}
		v := planned.ValueBool()
		return &v
	}

	body.Name = changed(plan.Name.StringValue, state.Name.StringValue)
	body.Site = changed(plan.Site.StringValue, state.Site.StringValue)
	body.OtlpEndpoint = changed(plan.OTLPEndpoint.StringValue, state.OTLPEndpoint.StringValue)
	if secret := rotated(config.CredentialSecretWO, plan.CredentialSecretWOVersion, state.CredentialSecretWOVersion); !secret.IsNull() {
		v := secret.ValueString()
		body.CredentialSecret = &v
	}
	body.MlApp = clearable(plan.MLApp.StringValue, state.MLApp.StringValue)
	body.CredentialUsername = clearable(plan.CredentialUsername.StringValue, state.CredentialUsername.StringValue)
	body.Enabled = changedBool(plan.Enabled, state.Enabled)
	body.PrivacyMode = changedBool(plan.PrivacyMode, state.PrivacyMode)
	body.MetricsEnabled = changedBool(plan.MetricsEnabled, state.MetricsEnabled)
	if !plan.SampleRate.Equal(state.SampleRate) {
		sampleRate := plan.SampleRate.ValueFloat64()
		body.SampleRate = &sampleRate
	}
	return body
}

// exportDestinationFromAPI maps an API destination to the Terraform model.
// prior is the plan (after create or update) or the state (after read):
// credential_secret_wo_version comes from it. The secret stays null: the API
// never returns it, and Terraform never stores it.
func exportDestinationFromAPI(d *client.ExportDestinationV1Response, prior exportDestinationResourceModel) exportDestinationResourceModel {
	return exportDestinationResourceModel{
		ID:                        types.StringValue(d.Id.String()),
		OrganizationID:            types.StringValue(d.OrganizationId.String()),
		Name:                      trimmedForm.value(types.StringValue(d.Name)),
		Kind:                      types.StringValue(string(d.Kind)),
		OTLPPreset:                types.StringValue(string(d.OtlpPreset)),
		Site:                      lowercaseForm.value(nullableString(d.Site)),
		OTLPEndpoint:              endpointForm.value(nullableString(d.OtlpEndpoint)),
		MLApp:                     trimmedForm.value(nullableString(d.MlApp)),
		CredentialSecretWO:        types.StringNull(),
		CredentialSecretWOVersion: prior.CredentialSecretWOVersion,
		CredentialUsername:        trimmedForm.value(nullableString(d.CredentialUsername)),
		CredentialHint:            nullableString(d.CredentialHint),
		Enabled:                   types.BoolValue(d.Enabled),
		PrivacyMode:               types.BoolValue(d.PrivacyMode),
		MetricsEnabled:            types.BoolValue(d.MetricsEnabled),
		SampleRate:                types.Float64Value(d.SampleRate),
		ConsecutiveFailures:       types.Int64Value(int64(d.ConsecutiveFailures)),
		DeliverySuspendedUntil:    nullableTime(d.DeliverySuspendedUntil),
		LastSuccessAt:             nullableTime(d.LastSuccessAt),
		LastErrorAt:               nullableTime(d.LastErrorAt),
		LastErrorMessage:          nullableString(d.LastErrorMessage),
		CreatedAt:                 types.StringValue(d.CreatedAt.Format(time.RFC3339)),
		UpdatedAt:                 types.StringValue(d.UpdatedAt.Format(time.RFC3339)),
	}
}

// apiStringForm is how the API rewrites a string when it saves it.
type apiStringForm int

const (
	trimmedForm   apiStringForm = iota // surrounding whitespace removed
	lowercaseForm                      // also lowercased
	endpointForm                       // also a URL's host lowercased, and no :443 port or trailing slash
)

// normalize returns s as the API stores it.
func (f apiStringForm) normalize(s string) string {
	s = strings.TrimSpace(s)
	switch f {
	case lowercaseForm:
		return strings.ToLower(s)
	case endpointForm:
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return s
		}
		u.Host = strings.TrimSuffix(strings.ToLower(u.Host), ":443")
		u.Path = strings.TrimRight(u.Path, "/")
		u.RawPath = strings.TrimRight(u.RawPath, "/")
		return u.String()
	}
	return s
}

// value is v as a value of this form.
func (f apiStringForm) value(v types.String) apiString {
	return apiString{StringValue: v, form: f}
}

// apiStringType is a string the API rewrites when it saves it. Values the API
// stores alike are semantically equal, so the framework keeps the configured
// form in state rather than reporting the stored one as a change.
type apiStringType struct {
	basetypes.StringType
	form apiStringForm
}

var _ basetypes.StringTypable = apiStringType{}

func (t apiStringType) Equal(o attr.Type) bool {
	other, ok := o.(apiStringType)
	return ok && t.form == other.form
}

func (t apiStringType) String() string {
	return "apiStringType"
}

func (t apiStringType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return t.form.value(in), nil
}

func (t apiStringType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	v, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	s, ok := v.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", v)
	}
	return t.form.value(s), nil
}

func (t apiStringType) ValueType(ctx context.Context) attr.Value {
	return apiString{form: t.form}
}

// apiString is a value of apiStringType.
type apiString struct {
	basetypes.StringValue
	form apiStringForm
}

var _ basetypes.StringValuableWithSemanticEquals = apiString{}

func (v apiString) Equal(o attr.Value) bool {
	other, ok := o.(apiString)
	return ok && v.form == other.form && v.StringValue.Equal(other.StringValue)
}

func (v apiString) Type(ctx context.Context) attr.Type {
	return apiStringType{form: v.form}
}

// StringSemanticEquals reports whether the API stores both values alike.
func (v apiString) StringSemanticEquals(ctx context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(apiString)
	if !ok {
		diags.AddError("Semantic Equality Check Error", fmt.Sprintf("Expected an apiString, got: %T. Please report this issue to the provider developers.", newValuable))
		return false, diags
	}
	return v.form.normalize(v.ValueString()) == v.form.normalize(newValue.ValueString()), diags
}
