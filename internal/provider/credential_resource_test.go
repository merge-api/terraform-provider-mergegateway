package provider

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

const testCredentialID = "2f9c3a7e-5b1d-4c8e-9f60-1a2b3c4d5e6f"

func TestCredentialResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewCredentialResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	for _, name := range []string{"api_key_wo", "aws_secret_access_key_wo", "service_account_json_wo"} {
		if a := resp.Schema.Attributes[name]; !a.IsSensitive() || !a.IsWriteOnly() {
			t.Fatalf("%s must be sensitive and write-only", name)
		}
	}
}

// testCredentialConfig is config as an object, with the settings not set in c null.
func testCredentialConfig(t *testing.T, c credentialConfigModel) types.Object {
	t.Helper()
	v, diags := types.ObjectValueFrom(t.Context(), credentialConfigAttrTypes, c)
	if diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	return v
}

// testCredential is an openai credential as a plan or state holds it after
// create: a name and key version, no api_base, no config settings.
func testCredential(t *testing.T) credentialResourceModel {
	return credentialResourceModel{
		ID:                          types.StringValue(testCredentialID),
		Vendor:                      types.StringValue("openai"),
		Name:                        types.StringValue("tf-acc-openai"),
		APIKeyWO:                    types.StringNull(),
		APIKeyWOVersion:             types.Int64Value(1),
		APIBase:                     types.StringNull(),
		FallbackToManaged:           types.BoolValue(true),
		Config:                      testCredentialConfig(t, credentialConfigModel{}),
		AWSSecretAccessKeyWO:        types.StringNull(),
		AWSSecretAccessKeyWOVersion: types.Int64Null(),
		ServiceAccountJSONWO:        types.StringNull(),
		ServiceAccountJSONWOVersion: types.Int64Null(),
		CredentialHint:              types.StringValue("1234"),
		CreatedAt:                   types.StringValue("2026-10-01T12:00:00Z"),
		UpdatedAt:                   types.StringValue("2026-10-01T12:00:00Z"),
	}
}

// testCredentialSecrets is the configuration's write-only secrets: only the
// configuration holds them.
func testCredentialSecrets() credentialResourceModel {
	return credentialResourceModel{
		APIKeyWO:             types.StringValue("sk-test-key-000000001234"),
		AWSSecretAccessKeyWO: types.StringNull(),
		ServiceAccountJSONWO: types.StringNull(),
	}
}

// testAzureConfig has the Azure settings set.
func testAzureConfig() credentialConfigModel {
	return credentialConfigModel{
		APIVersion:      types.StringValue("2024-02-15-preview"),
		AzureDeployment: types.StringValue("gpt-4o"),
	}
}

func TestCredentialCreateRequest(t *testing.T) {
	tests := map[string]struct {
		plan    func(m *credentialResourceModel)
		secrets func(m *credentialResourceModel)
		want    string
	}{
		"defaults": {
			plan: func(m *credentialResourceModel) { m.Name = types.StringNull() },
			want: `{"vendor": "openai", "api_key": "sk-test-key-000000001234", "fallback_to_managed": true}`,
		},
		"every field": {
			plan: func(m *credentialResourceModel) {
				m.Vendor = types.StringValue("azure")
				m.APIBase = types.StringValue("https://example.openai.azure.com")
				m.FallbackToManaged = types.BoolValue(false)
				m.Config = testCredentialConfig(t, testAzureConfig())
			},
			want: `{"vendor": "azure", "name": "tf-acc-openai", "api_key": "sk-test-key-000000001234",
				"api_base": "https://example.openai.azure.com", "fallback_to_managed": false,
				"config": {"api_version": "2024-02-15-preview", "azure_deployment": "gpt-4o"}}`,
		},
		"settings not known yet are left out": {
			plan: func(m *credentialResourceModel) {
				m.Name = types.StringUnknown()
				m.Config = types.ObjectUnknown(credentialConfigAttrTypes)
			},
			want: `{"vendor": "openai", "api_key": "sk-test-key-000000001234", "fallback_to_managed": true}`,
		},
		"secrets in config": {
			plan: func(m *credentialResourceModel) {
				m.Vendor = types.StringValue("bedrock")
				m.Config = testCredentialConfig(t, credentialConfigModel{
					AWSRegion:      types.StringValue("us-east-1"),
					AWSAccessKeyID: types.StringValue("AKIATEST"),
				})
			},
			secrets: func(m *credentialResourceModel) {
				m.APIKeyWO = types.StringNull()
				m.AWSSecretAccessKeyWO = types.StringValue("secret")
			},
			want: `{"vendor": "bedrock", "name": "tf-acc-openai", "fallback_to_managed": true,
				"config": {"aws_region": "us-east-1", "aws_access_key_id": "AKIATEST", "aws_secret_access_key": "secret"}}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan, secrets := testCredential(t), testCredentialSecrets()
			tt.plan(&plan)
			if tt.secrets != nil {
				tt.secrets(&secrets)
			}
			body, diags := credentialCreateRequest(t.Context(), plan, secrets)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestCredentialUpdateRequest(t *testing.T) {
	azure := func(m *credentialResourceModel) {
		m.APIBase = types.StringValue("https://example.openai.azure.com")
		m.Config = testCredentialConfig(t, testAzureConfig())
	}
	bedrock := func(m *credentialResourceModel) {
		m.Vendor = types.StringValue("bedrock")
		m.APIKeyWOVersion = types.Int64Null()
		m.AWSSecretAccessKeyWOVersion = types.Int64Value(1)
		m.Config = testCredentialConfig(t, credentialConfigModel{
			AWSRegion:      types.StringValue("us-east-1"),
			AWSAccessKeyID: types.StringValue("AKIATEST"),
		})
	}
	tests := map[string]struct {
		state   func(m *credentialResourceModel)
		plan    func(m *credentialResourceModel)
		secrets func(m *credentialResourceModel)
		want    string
	}{
		"no change sends nothing, not even the key": {
			state: azure,
			plan:  azure,
			want:  `{}`,
		},
		"a new key without a version change isn't sent": {
			plan:    func(m *credentialResourceModel) {},
			secrets: func(m *credentialResourceModel) { m.APIKeyWO = types.StringValue("sk-test-key-000000005678") },
			want:    `{}`,
		},
		"a version change sends the key": {
			plan:    func(m *credentialResourceModel) { m.APIKeyWOVersion = types.Int64Value(2) },
			secrets: func(m *credentialResourceModel) { m.APIKeyWO = types.StringValue("sk-test-key-000000005678") },
			want:    `{"api_key": "sk-test-key-000000005678"}`,
		},
		"a version set after an import sends the key": {
			state: func(m *credentialResourceModel) { m.APIKeyWOVersion = types.Int64Null() },
			plan:  func(m *credentialResourceModel) {},
			want:  `{"api_key": "sk-test-key-000000001234"}`,
		},
		"removing the key and its version sends nothing": {
			plan:    func(m *credentialResourceModel) { m.APIKeyWOVersion = types.Int64Null() },
			secrets: func(m *credentialResourceModel) { m.APIKeyWO = types.StringNull() },
			want:    `{}`,
		},
		"change every field": {
			plan: func(m *credentialResourceModel) {
				azure(m)
				m.Name = types.StringValue("tf-acc-renamed")
				m.APIKeyWOVersion = types.Int64Value(2)
				m.FallbackToManaged = types.BoolValue(false)
			},
			want: `{"name": "tf-acc-renamed", "api_key": "sk-test-key-000000001234", "api_base": "https://example.openai.azure.com",
				"fallback_to_managed": false, "config": {"api_version": "2024-02-15-preview", "azure_deployment": "gpt-4o"}}`,
		},
		"only changed config settings are sent": {
			state: azure,
			plan: func(m *credentialResourceModel) {
				m.APIBase = types.StringValue("https://example.openai.azure.com")
				c := testAzureConfig()
				c.AzureDeployment = types.StringValue("gpt-4o-mini")
				m.Config = testCredentialConfig(t, c)
			},
			want: `{"config": {"azure_deployment": "gpt-4o-mini"}}`,
		},
		"a config secret is sent only on a version change": {
			state: bedrock,
			plan: func(m *credentialResourceModel) {
				bedrock(m)
				m.AWSSecretAccessKeyWOVersion = types.Int64Value(2)
			},
			secrets: func(m *credentialResourceModel) {
				m.APIKeyWO = types.StringNull()
				m.AWSSecretAccessKeyWO = types.StringValue("rotated-secret")
			},
			want: `{"config": {"aws_secret_access_key": "rotated-secret"}}`,
		},
		"a config secret without a version change isn't sent": {
			state: bedrock,
			plan:  bedrock,
			secrets: func(m *credentialResourceModel) {
				m.APIKeyWO = types.StringNull()
				m.AWSSecretAccessKeyWO = types.StringValue("rotated-secret")
			},
			want: `{}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan, secrets := testCredential(t), testCredential(t), testCredentialSecrets()
			if tt.state != nil {
				tt.state(&state)
			}
			tt.plan(&plan)
			if tt.secrets != nil {
				tt.secrets(&secrets)
			}
			body, diags := credentialUpdateRequest(t.Context(), plan, state, secrets)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testCredentialResponse decodes an API credential with the given extra fields.
func testCredentialResponse(t *testing.T, extra string) *client.CredentialV1Response {
	t.Helper()
	body := `{
		"id": "` + testCredentialID + `", "vendor": "openai", "name": "tf-acc-openai", "api_base": null,
		"fallback_to_managed": true, "credential_hint": "1234",
		"config": {"api_version": null, "azure_deployment": null, "aws_region": null, "aws_access_key_id": null,
			"aws_role_arn": null, "vertex_project": null, "vertex_location": null},
		"created_at": "2026-10-01T12:00:00.123456Z", "updated_at": "2026-10-01T12:00:00Z"` + extra + `}`
	var c client.CredentialV1Response
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &c
}

func TestCredentialFromAPI(t *testing.T) {
	fromAPI := func(t *testing.T, extra string, prior credentialResourceModel) credentialResourceModel {
		t.Helper()
		got, diags := credentialFromAPI(t.Context(), testCredentialResponse(t, extra), prior)
		if diags.HasError() {
			t.Fatalf("diagnostics: %v", diags)
		}
		return got
	}

	t.Run("credential", func(t *testing.T) {
		// The key version comes from prior; the secrets are never stored.
		prior := testCredential(t)
		prior.APIKeyWO = types.StringValue("sk-test-key-000000001234")
		got := fromAPI(t, ``, prior)
		if want := testCredential(t); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("import has no secret versions", func(t *testing.T) {
		got := fromAPI(t, ``, credentialResourceModel{})
		if !got.APIKeyWO.IsNull() || !got.APIKeyWOVersion.IsNull() || !got.ServiceAccountJSONWOVersion.IsNull() {
			t.Fatalf("got api_key_wo %v, api_key_wo_version %v; want null", got.APIKeyWO, got.APIKeyWOVersion)
		}
	})

	t.Run("every field", func(t *testing.T) {
		prior := testCredential(t)
		prior.AWSSecretAccessKeyWOVersion = types.Int64Value(3)
		got := fromAPI(t, `, "api_base": "https://example.openai.azure.com", "fallback_to_managed": false,
			"credential_hint": null, "config": {"api_version": "2024-02-15-preview", "azure_deployment": "gpt-4o",
			"aws_region": "us-east-1", "aws_access_key_id": "AKIATEST", "aws_role_arn": "arn:aws:iam::123456789012:role/gw",
			"vertex_project": "p", "vertex_location": "l"}`, prior)
		want := testCredential(t)
		want.APIBase = types.StringValue("https://example.openai.azure.com")
		want.FallbackToManaged = types.BoolValue(false)
		want.AWSSecretAccessKeyWOVersion = types.Int64Value(3)
		want.CredentialHint = types.StringNull()
		want.Config = testCredentialConfig(t, credentialConfigModel{
			APIVersion:      types.StringValue("2024-02-15-preview"),
			AzureDeployment: types.StringValue("gpt-4o"),
			AWSRegion:       types.StringValue("us-east-1"),
			AWSAccessKeyID:  types.StringValue("AKIATEST"),
			AWSRoleARN:      types.StringValue("arn:aws:iam::123456789012:role/gw"),
			VertexProject:   types.StringValue("p"),
			VertexLocation:  types.StringValue("l"),
		})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
}

// expectPlanError plans the resource config with a provider that never
// reaches an API, and expects an error matching want.
func expectPlanError(t *testing.T, config, want string) {
	t.Helper()
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"mergegateway": providerserver.NewProtocol6WithError(New("test")()),
		},
		Steps: []tfresource.TestStep{{
			Config: `
provider "mergegateway" {
  api_key  = "test"
  base_url = "http://localhost:1"
}
` + config,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(want),
		}},
	})
}

// TestCredentialResourcePlanErrors plans configs the API would reject: they
// must fail at plan time, before any API call.
func TestCredentialResourcePlanErrors(t *testing.T) {
	const key = "\napi_key_wo = \"k\"\napi_key_wo_version = 1"
	tests := map[string]struct {
		body string
		want string
	}{
		"missing api_key_wo":       {body: `vendor = "openai"`, want: `Vendor "openai" needs api_key_wo`},
		"empty api_key_wo":         {body: "vendor = \"openai\"\napi_key_wo = \"\"\napi_key_wo_version = 1", want: "at least 1"},
		"api_key_wo, no version":   {body: "vendor = \"openai\"\napi_key_wo = \"k\"", want: `"api_key_wo_version" must be specified`},
		"a version, no api_key_wo": {body: "vendor = \"openai\"\napi_key_wo_version = 1", want: `"api_key_wo" must be specified`},
		"empty name":               {body: "vendor = \"openai\"\nname = \"\"" + key, want: "between 1 and 255"},
		"bedrock without region":   {body: "vendor = \"bedrock\"" + key, want: "needs config.aws_region"},
		"vertexai without service account": {
			body: "vendor = \"vertexai\"\nconfig = { vertex_project = \"p\", vertex_location = \"l\" }",
			want: "Missing service_account_json_wo",
		},
		"bedrock key without secret": {
			body: "vendor = \"bedrock\"\nconfig = { aws_region = \"us-east-1\", aws_access_key_id = \"AKIA\" }",
			want: "Missing aws_secret_access_key_wo",
		},
		"openai without api_key_wo, config not known yet": {
			body: "vendor = \"openai\"\nconfig = { api_version = uuid() }",
			want: `Vendor "openai" needs api_key_wo`,
		},
		"empty config setting": {
			body: "vendor = \"azure\"\nconfig = { api_version = \"\" }" + key,
			want: "at least 1",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			expectPlanError(t, "resource \"mergegateway_credential\" \"test\" {\n"+tt.body+"\n}", tt.want)
		})
	}
}
