package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

const testExportDestinationID = "7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f"

func TestExportDestinationResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewExportDestinationResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	if a := resp.Schema.Attributes["credential_secret_wo"]; !a.IsSensitive() || !a.IsWriteOnly() {
		t.Fatal("credential_secret_wo must be sensitive and write-only")
	}
}

// testExportDestination is a Datadog destination with the defaults, as a plan
// or state holds it after create.
func testExportDestination() exportDestinationResourceModel {
	return exportDestinationResourceModel{
		ID:                        types.StringValue(testExportDestinationID),
		OrganizationID:            types.StringValue(testPIProjectID),
		Name:                      trimmedForm.value(types.StringValue("tf-acc-datadog")),
		Kind:                      types.StringValue("otlp"),
		OTLPPreset:                types.StringValue("datadog"),
		Site:                      lowercaseForm.value(types.StringValue("datadoghq.com")),
		OTLPEndpoint:              endpointForm.value(types.StringNull()),
		MLApp:                     trimmedForm.value(types.StringNull()),
		CredentialSecretWO:        types.StringNull(),
		CredentialSecretWOVersion: types.Int64Value(1),
		CredentialUsername:        trimmedForm.value(types.StringNull()),
		CredentialHint:            types.StringValue("AB12"),
		Enabled:                   types.BoolValue(true),
		PrivacyMode:               types.BoolValue(true),
		MetricsEnabled:            types.BoolValue(false),
		SampleRate:                types.Float64Value(1),
		ConsecutiveFailures:       types.Int64Value(0),
		DeliverySuspendedUntil:    types.StringNull(),
		LastSuccessAt:             types.StringNull(),
		LastErrorAt:               types.StringNull(),
		LastErrorMessage:          types.StringNull(),
		CreatedAt:                 types.StringValue("2026-10-01T12:00:00Z"),
		UpdatedAt:                 types.StringValue("2026-10-01T12:00:00Z"),
	}
}

// testExportDestinationSecret is the configuration's write-only secret: only
// the configuration holds it.
func testExportDestinationSecret(secret string) exportDestinationResourceModel {
	return exportDestinationResourceModel{CredentialSecretWO: types.StringValue(secret)}
}

func TestExportDestinationCreateRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *exportDestinationResourceModel)
		want string
	}{
		"defaults": {
			plan: func(m *exportDestinationResourceModel) {},
			want: `{"name": "tf-acc-datadog", "kind": "otlp", "otlp_preset": "datadog", "site": "datadoghq.com",
				"credential_secret": "dd-secret-AB12", "enabled": true, "privacy_mode": true, "metrics_enabled": false, "sample_rate": 1}`,
		},
		"every field": {
			plan: func(m *exportDestinationResourceModel) {
				m.OTLPPreset = types.StringValue("grafana")
				m.Site = lowercaseForm.value(types.StringValue("prod-us-east-0"))
				m.MLApp = trimmedForm.value(types.StringValue("support-bot"))
				m.CredentialUsername = trimmedForm.value(types.StringValue("123456"))
				m.Enabled = types.BoolValue(false)
				m.PrivacyMode = types.BoolValue(false)
				m.MetricsEnabled = types.BoolValue(true)
				m.SampleRate = types.Float64Value(0.25)
			},
			want: `{"name": "tf-acc-datadog", "kind": "otlp", "otlp_preset": "grafana", "site": "prod-us-east-0",
				"ml_app": "support-bot", "credential_secret": "dd-secret-AB12", "credential_username": "123456",
				"enabled": false, "privacy_mode": false, "metrics_enabled": true, "sample_rate": 0.25}`,
		},
		"generic": {
			plan: func(m *exportDestinationResourceModel) {
				m.OTLPPreset = types.StringValue("generic")
				m.Site = lowercaseForm.value(types.StringNull())
				m.OTLPEndpoint = endpointForm.value(types.StringValue("https://otel.example.com"))
			},
			want: `{"name": "tf-acc-datadog", "kind": "otlp", "otlp_preset": "generic", "otlp_endpoint": "https://otel.example.com",
				"credential_secret": "dd-secret-AB12", "enabled": true, "privacy_mode": true, "metrics_enabled": false, "sample_rate": 1}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testExportDestination()
			tt.plan(&plan)
			assertJSON(t, exportDestinationCreateRequest(plan, testExportDestinationSecret("dd-secret-AB12")), tt.want)
		})
	}
}

func TestExportDestinationUpdateRequest(t *testing.T) {
	withOptional := func(m *exportDestinationResourceModel) {
		m.MLApp = trimmedForm.value(types.StringValue("support-bot"))
		m.CredentialUsername = trimmedForm.value(types.StringValue("123456"))
	}
	tests := map[string]struct {
		state  func(m *exportDestinationResourceModel)
		plan   func(m *exportDestinationResourceModel)
		secret string
		want   string
	}{
		"no change sends nothing, not even the secret": {
			plan: func(m *exportDestinationResourceModel) {},
			want: `{}`,
		},
		"a new secret without a version change isn't sent": {
			plan:   func(m *exportDestinationResourceModel) {},
			secret: "dd-secret-CD34",
			want:   `{}`,
		},
		"a version change sends the secret": {
			plan:   func(m *exportDestinationResourceModel) { m.CredentialSecretWOVersion = types.Int64Value(2) },
			secret: "dd-secret-CD34",
			want:   `{"credential_secret": "dd-secret-CD34"}`,
		},
		"a version set after an import sends the secret": {
			state: func(m *exportDestinationResourceModel) { m.CredentialSecretWOVersion = types.Int64Null() },
			plan:  func(m *exportDestinationResourceModel) {},
			want:  `{"credential_secret": "dd-secret-AB12"}`,
		},
		"change every field": {
			plan: func(m *exportDestinationResourceModel) {
				withOptional(m)
				m.Name = trimmedForm.value(types.StringValue("tf-acc-renamed"))
				m.Site = lowercaseForm.value(types.StringValue("datadoghq.eu"))
				m.CredentialSecretWOVersion = types.Int64Value(2)
				m.Enabled = types.BoolValue(false)
				m.PrivacyMode = types.BoolValue(false)
				m.MetricsEnabled = types.BoolValue(true)
				m.SampleRate = types.Float64Value(0.5)
			},
			secret: "dd-secret-CD34",
			want: `{"name": "tf-acc-renamed", "site": "datadoghq.eu", "ml_app": "support-bot", "credential_secret": "dd-secret-CD34",
				"credential_username": "123456", "enabled": false, "privacy_mode": false, "metrics_enabled": true, "sample_rate": 0.5}`,
		},
		"removing ml_app and credential_username clears them": {
			state: withOptional,
			plan:  func(m *exportDestinationResourceModel) {},
			want:  `{"ml_app": null, "credential_username": null}`,
		},
		"site is never sent as null": {
			plan: func(m *exportDestinationResourceModel) { m.Site = lowercaseForm.value(types.StringNull()) },
			want: `{}`,
		},
		"kind and otlp_preset are never sent": {
			plan: func(m *exportDestinationResourceModel) { m.OTLPPreset = types.StringValue("grafana") },
			want: `{}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testExportDestination(), testExportDestination()
			if tt.state != nil {
				tt.state(&state)
			}
			tt.plan(&plan)
			secret := tt.secret
			if secret == "" {
				secret = "dd-secret-AB12"
			}
			assertJSON(t, exportDestinationUpdateRequest(plan, state, testExportDestinationSecret(secret)), tt.want)
		})
	}
}

// testExportDestinationResponse decodes an API destination with the given extra fields.
func testExportDestinationResponse(t *testing.T, extra string) *client.ExportDestinationV1Response {
	t.Helper()
	body := `{
		"id": "` + testExportDestinationID + `", "organization_id": "` + testPIProjectID + `", "name": "tf-acc-datadog",
		"kind": "otlp", "otlp_preset": "datadog", "site": "datadoghq.com", "otlp_endpoint": null, "ml_app": null,
		"credential_hint": "AB12", "credential_username": null, "enabled": true, "privacy_mode": true,
		"metrics_enabled": false, "sample_rate": 1.0, "consecutive_failures": 0, "delivery_suspended_until": null,
		"last_success_at": null, "last_error_at": null, "last_error_message": null,
		"created_at": "2026-10-01T12:00:00.123456Z", "updated_at": "2026-10-01T12:00:00Z"` + extra + `}`
	var d client.ExportDestinationV1Response
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &d
}

func TestExportDestinationFromAPI(t *testing.T) {
	t.Run("destination", func(t *testing.T) {
		// The secret version comes from prior; the secret is never stored.
		prior := testExportDestination()
		prior.CredentialSecretWO = types.StringValue("dd-secret-AB12")
		got := exportDestinationFromAPI(testExportDestinationResponse(t, ``), prior)
		if want := testExportDestination(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("import has no secret version", func(t *testing.T) {
		got := exportDestinationFromAPI(testExportDestinationResponse(t, ``), exportDestinationResourceModel{})
		if !got.CredentialSecretWO.IsNull() || !got.CredentialSecretWOVersion.IsNull() {
			t.Fatalf("credential_secret_wo = %v, version %v; want null", got.CredentialSecretWO, got.CredentialSecretWOVersion)
		}
	})

	t.Run("every field", func(t *testing.T) {
		got := exportDestinationFromAPI(testExportDestinationResponse(t, `, "otlp_preset": "grafana", "site": "prod-us-east-0",
			"ml_app": "support-bot", "credential_username": "123456", "enabled": false, "privacy_mode": false,
			"metrics_enabled": true, "sample_rate": 0.25, "consecutive_failures": 3,
			"delivery_suspended_until": "2026-10-02T00:00:00Z", "last_success_at": "2026-10-01T10:00:00Z",
			"last_error_at": "2026-10-01T11:00:00Z", "last_error_message": "HTTP 403"`), testExportDestination())
		want := testExportDestination()
		want.OTLPPreset = types.StringValue("grafana")
		want.Site = lowercaseForm.value(types.StringValue("prod-us-east-0"))
		want.MLApp = trimmedForm.value(types.StringValue("support-bot"))
		want.CredentialUsername = trimmedForm.value(types.StringValue("123456"))
		want.Enabled = types.BoolValue(false)
		want.PrivacyMode = types.BoolValue(false)
		want.MetricsEnabled = types.BoolValue(true)
		want.SampleRate = types.Float64Value(0.25)
		want.ConsecutiveFailures = types.Int64Value(3)
		want.DeliverySuspendedUntil = types.StringValue("2026-10-02T00:00:00Z")
		want.LastSuccessAt = types.StringValue("2026-10-01T10:00:00Z")
		want.LastErrorAt = types.StringValue("2026-10-01T11:00:00Z")
		want.LastErrorMessage = types.StringValue("HTTP 403")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
}

func TestCheckOTLPEndpoint(t *testing.T) {
	// The API rewrites these, but accepts them.
	for _, ok := range []string{
		"https://otel.example.com", "https://otel.example.com:4318/v1", "https://10.0.0.1",
		"https://Otel.Example.com:443/v1/", " https://otel.example.com\n",
	} {
		if err := checkOTLPEndpoint(ok); err != nil {
			t.Errorf("%s: unexpected error: %v", ok, err)
		}
	}
	for endpoint, want := range map[string]string{
		"http://otel.example.com":       "must be an https URL",
		"otel.example.com":              "must be an https URL",
		"https://otel.example.com/?a=b": "query string",
		"https://otel.example.com/#top": "fragment",
	} {
		err := checkOTLPEndpoint(endpoint)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want one mentioning %q", endpoint, err, want)
		}
	}
}

func TestAPIStringSemanticEquals(t *testing.T) {
	tests := []struct {
		form       apiStringForm
		prior, new string
		want       bool
	}{
		{trimmedForm, "tf-acc", "tf-acc", true},
		{trimmedForm, "tf-acc", " tf-acc\n", true},
		{trimmedForm, "tf-acc", "TF-acc", false},
		{trimmedForm, "tf-acc", "tf-acc-2", false},
		{lowercaseForm, "datadoghq.com", "DatadogHQ.com", true},
		{lowercaseForm, "datadoghq.com", "datadoghq.eu", false},
		{endpointForm, "https://otel.example.com/v1", "https://otel.example.com/v1", true},
		{endpointForm, "https://otel.example.com/v1", " https://Otel.Example.COM:443/v1/ ", true},
		{endpointForm, "https://otel.example.com", "https://otel.example.com/", true},
		{endpointForm, "https://otel.example.com/v1", "https://otel.example.com/V1", false},
		{endpointForm, "https://otel.example.com", "https://otel.example.com:4318", false},
		{endpointForm, "https://otel.example.com", "https://otel.example.org", false},
		{endpointForm, "https://a@otel.example.com", "https://b@otel.example.com", false},
	}
	for _, tt := range tests {
		prior, value := tt.form.value(types.StringValue(tt.prior)), tt.form.value(types.StringValue(tt.new))
		got, diags := prior.StringSemanticEquals(t.Context(), value)
		if diags.HasError() {
			t.Fatalf("%q, %q: %v", tt.prior, tt.new, diags)
		}
		if got != tt.want {
			t.Errorf("%q, %q: semantically equal = %v, want %v", tt.prior, tt.new, got, tt.want)
		}
	}
}

// TestExportDestinationResourceValidateConfig plans configs the API would
// reject: they must fail at plan time.
func TestExportDestinationResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"datadog without site":     {body: ``, want: "A datadog destination needs site"},
		"datadog with username":    {body: "site = \"datadoghq.com\"\ncredential_username = \"1\"", want: "takes no credential_username"},
		"datadog with endpoint":    {body: "site = \"datadoghq.com\"\notlp_endpoint = \"https://otel.example.com\"", want: "takes no otlp_endpoint"},
		"grafana without username": {body: "otlp_preset = \"grafana\"\nsite = \"prod-us-east-0\"", want: "needs credential_username"},
		"grafana with ml_app":      {body: "otlp_preset = \"grafana\"\nsite = \"prod-us-east-0\"\ncredential_username = \"1\"\nml_app = \"app\"", want: "takes no ml_app"},
		"generic without endpoint": {body: `otlp_preset = "generic"`, want: "needs otlp_endpoint"},
		"generic with site":        {body: "otlp_preset = \"generic\"\notlp_endpoint = \"https://otel.example.com\"\nsite = \"x\"", want: "takes no site"},
		"site with a space":        {body: `site = "datadoghq .com"`, want: "must be letters, digits"},
		"http endpoint":            {body: "otlp_preset = \"generic\"\notlp_endpoint = \"http://otel.example.com\"", want: "must be an https URL"},
		"blank name":               {body: "site = \"datadoghq.com\"\nname = \" \"", want: "must not be blank"},
		"sample_rate above 1":      {body: "site = \"datadoghq.com\"\nsample_rate = 2", want: "between"},
		"unknown preset":           {body: `otlp_preset = "s3"`, want: "value must be one of"},
		"empty secret":             {body: "site = \"datadoghq.com\"\ncredential_secret_wo = \"\"\ncredential_secret_wo_version = 1", want: "at least 1"},
		"no secret version":        {body: "site = \"datadoghq.com\"\ncredential_secret_wo = \"secret\"", want: "credential_secret_wo_version"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			body := tt.body
			if !strings.Contains("\n"+body, "\nname =") {
				body += "\nname = \"tf-acc\""
			}
			if !strings.Contains(body, "credential_secret") {
				body += "\ncredential_secret_wo = \"secret\"\ncredential_secret_wo_version = 1"
			}
			expectPlanError(t, "resource \"mergegateway_export_destination\" \"test\" {\n"+body+"\n}", tt.want)
		})
	}
}
