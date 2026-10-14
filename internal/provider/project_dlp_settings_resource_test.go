package provider

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

const testDLPProjectID = "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11"

func TestProjectDLPSettingsResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewProjectDLPSettingsResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

func TestProjectDLPSettingsRequest(t *testing.T) {
	tests := map[string]struct {
		override map[string]projectDLPEntityOverrideModel
		want     string
	}{
		"both fields": {
			override: map[string]projectDLPEntityOverrideModel{
				"EMAIL_ADDRESS": {Enabled: types.BoolValue(true), Action: types.StringValue("redact")},
			},
			want: `{"override": {"EMAIL_ADDRESS": {"enabled": true, "action": "redact"}}}`,
		},
		"unset fields are omitted": {
			override: map[string]projectDLPEntityOverrideModel{
				"EMAIL_ADDRESS": {Enabled: types.BoolValue(false), Action: types.StringNull()},
				"PHONE_NUMBER":  {Enabled: types.BoolNull(), Action: types.StringValue("block")},
			},
			want: `{"override": {"EMAIL_ADDRESS": {"enabled": false}, "PHONE_NUMBER": {"action": "block"}}}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := projectDLPSettingsResourceModel{
				ID:        types.StringUnknown(),
				ProjectID: types.StringValue(testDLPProjectID),
				Override:  tt.override,
			}
			assertJSON(t, projectDLPSettingsRequest(plan), tt.want)
		})
	}
}

func TestProjectDLPSettingsFromAPI(t *testing.T) {
	body := `{
		"override": {
			"EMAIL_ADDRESS": {"enabled": true, "action": "redact"},
			"PHONE_NUMBER": {"enabled": null, "action": "block"},
			"US_SSN": {"enabled": false}
		},
		"entities": [{"entity_type": "EMAIL_ADDRESS", "category": "pii", "is_seeded": true, "overridden": true,
			"org_action": "log", "effective_action": "redact"}]
	}`
	var s client.ProjectDLPSettingsV1Response
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	got := projectDLPSettingsFromAPI(types.StringValue(testDLPProjectID), &s)
	want := projectDLPSettingsResourceModel{
		ID:        types.StringValue(testDLPProjectID),
		ProjectID: types.StringValue(testDLPProjectID),
		Override: map[string]projectDLPEntityOverrideModel{
			"EMAIL_ADDRESS": {Enabled: types.BoolValue(true), Action: types.StringValue("redact")},
			"PHONE_NUMBER":  {Enabled: types.BoolNull(), Action: types.StringValue("block")},
			"US_SSN":        {Enabled: types.BoolValue(false), Action: types.StringNull()},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestProjectDLPSettingsResourceValidateConfig plans configs the API would
// reject: they must fail at plan time, before any API call.
func TestProjectDLPSettingsResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"project_id is not a UUID": {
			body: `project_id = "not-a-uuid"
  override = { EMAIL_ADDRESS = { action = "redact" } }`,
			want: "Expected a project UUID",
		},
		"empty override": {
			body: `project_id = "` + testDLPProjectID + `"
  override = {}`,
			want: "at least 1",
		},
		"entity with neither field": {
			body: `project_id = "` + testDLPProjectID + `"
  override = { EMAIL_ADDRESS = {} }`,
			want: "Set enabled, action or both",
		},
		"unknown action": {
			body: `project_id = "` + testDLPProjectID + `"
  override = { EMAIL_ADDRESS = { action = "flag" } }`,
			want: "value must be one of",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
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
resource "mergegateway_project_dlp_settings" "test" {
  ` + tt.body + `
}`,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tt.want),
				}},
			})
		})
	}
}

// TestProjectDLPSettingsResourceValidateConfigUnknown plans an override whose
// entity isn't known until apply, and a project ID in upper case: both are
// valid, so the plan must succeed.
func TestProjectDLPSettingsResourceValidateConfigUnknown(t *testing.T) {
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
resource "terraform_data" "entity" {
  input = { action = "redact" }
}
resource "mergegateway_project_dlp_settings" "test" {
  project_id = "` + strings.ToUpper(testDLPProjectID) + `"
  override   = { EMAIL_ADDRESS = terraform_data.entity.output }
}`,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		}},
	})
}
