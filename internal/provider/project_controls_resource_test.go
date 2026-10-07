// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

const testControlsProjectID = "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11"

func TestProjectControlsResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewProjectControlsResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testControls is project controls with nothing overridden.
func testControls() projectControlsResourceModel {
	return projectControlsResourceModel{
		ID:                      types.StringValue(testControlsProjectID),
		ProjectID:               types.StringValue(testControlsProjectID),
		AllowedRegions:          types.ListNull(types.StringType),
		IgnoredRegions:          types.ListNull(types.StringType),
		AllowedVendors:          types.ListNull(types.StringType),
		IgnoredVendors:          types.ListNull(types.StringType),
		VendorRoutingPreference: types.ListNull(types.StringType),
		ZeroDataRetention:       types.BoolNull(),
	}
}

func testList(values ...string) types.List {
	elems := []attr.Value{}
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}
	return types.ListValueMust(types.StringType, elems)
}

func TestProjectControlsRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *projectControlsResourceModel)
		want string
	}{
		"nothing overridden sends nothing": {
			plan: func(m *projectControlsResourceModel) {},
			want: `{}`,
		},
		"every field": {
			plan: func(m *projectControlsResourceModel) {
				m.AllowedRegions = testList("US")
				m.IgnoredRegions = testList("CA")
				m.AllowedVendors = testList("openai", "anthropic")
				m.IgnoredVendors = testList("google")
				m.VendorRoutingPreference = testList("anthropic", "openai", "anthropic")
				m.ZeroDataRetention = types.BoolValue(true)
			},
			want: `{"allowed_regions": ["US"], "ignored_regions": ["CA"],
				"allowed_vendors": ["openai", "anthropic"], "ignored_vendors": ["google"],
				"vendor_routing_preference": ["anthropic", "openai", "anthropic"],
				"zero_data_retention": true}`,
		},
		"an empty list is sent as []": {
			plan: func(m *projectControlsResourceModel) { m.AllowedVendors = testList() },
			want: `{"allowed_vendors": []}`,
		},
		"zero_data_retention false is sent": {
			plan: func(m *projectControlsResourceModel) { m.ZeroDataRetention = types.BoolValue(false) },
			want: `{"zero_data_retention": false}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testControls()
			tt.plan(&plan)
			body, diags := projectControlsRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testControlsResponse decodes an API controls response with the given override.
func testControlsResponse(t *testing.T, override string) *client.ProjectControlsV1Response {
	t.Helper()
	body := `{
		"scope": "project",
		"scope_id": "` + testControlsProjectID + `",
		"override": ` + override + `,
		"effective": {"zero_data_retention": false, "ignored_vendors": [], "allowed_vendors": [],
			"vendor_routing_preference": [], "ignored_regions": [], "allowed_regions": []},
		"sources": {"zero_data_retention": "org", "vendors": "org", "regions": "org"}
	}`
	var c client.ProjectControlsV1Response
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &c
}

func TestProjectControlsFromAPI(t *testing.T) {
	tests := map[string]struct {
		prior    func(m *projectControlsResourceModel)
		override string
		want     func(m *projectControlsResourceModel)
	}{
		"nothing overridden": {
			override: `{}`,
		},
		"every field": {
			override: `{"regions": {"allowed_regions": ["US"], "ignored_regions": ["CA"]},
				"vendors": {"allowed_vendors": ["openai"], "ignored_vendors": ["google"], "vendor_routing_preference": ["openai", "openai"]},
				"zero_data_retention": false}`,
			want: func(m *projectControlsResourceModel) {
				m.AllowedRegions = testList("US")
				m.IgnoredRegions = testList("CA")
				m.AllowedVendors = testList("openai")
				m.IgnoredVendors = testList("google")
				m.VendorRoutingPreference = testList("openai", "openai")
				m.ZeroDataRetention = types.BoolValue(false)
			},
		},
		"fields of a unit that weren't set stay null": {
			prior:    func(m *projectControlsResourceModel) { m.AllowedVendors = testList("openai") },
			override: `{"vendors": {"allowed_vendors": ["openai"], "ignored_vendors": [], "vendor_routing_preference": []}}`,
			want:     func(m *projectControlsResourceModel) { m.AllowedVendors = testList("openai") },
		},
		"a field set to [] stays []": {
			prior:    func(m *projectControlsResourceModel) { m.IgnoredRegions = testList() },
			override: `{"regions": {"allowed_regions": [], "ignored_regions": []}}`,
			want:     func(m *projectControlsResourceModel) { m.IgnoredRegions = testList() },
		},
		"a field set outside Terraform shows up": {
			prior:    func(m *projectControlsResourceModel) { m.AllowedVendors = testList("openai") },
			override: `{"vendors": {"allowed_vendors": ["openai"], "ignored_vendors": ["google"], "vendor_routing_preference": []}}`,
			want: func(m *projectControlsResourceModel) {
				m.AllowedVendors = testList("openai")
				m.IgnoredVendors = testList("google")
			},
		},
		// Someone overrode vendors with nothing outside Terraform, dropping
		// the organization's vendor controls: that must show as a diff.
		"an empty unit set outside Terraform shows up": {
			override: `{"vendors": {"allowed_vendors": [], "ignored_vendors": [], "vendor_routing_preference": []}}`,
			want:     func(m *projectControlsResourceModel) { m.AllowedVendors = testList() },
		},
		"project_id is kept as configured": {
			prior: func(m *projectControlsResourceModel) {
				m.ProjectID = types.StringValue("6F1C1F5E-8A8B-4C43-9A0E-3F0F6D0C8B11")
			},
			override: `{}`,
			want: func(m *projectControlsResourceModel) {
				m.ProjectID = types.StringValue("6F1C1F5E-8A8B-4C43-9A0E-3F0F6D0C8B11")
			},
		},
		// The overrides were cleared outside Terraform: reading them as null
		// makes the next plan set them again.
		"an empty override clears every field": {
			prior: func(m *projectControlsResourceModel) {
				m.AllowedRegions = testList("US")
				m.IgnoredRegions = testList()
				m.VendorRoutingPreference = testList("openai")
				m.ZeroDataRetention = types.BoolValue(true)
			},
			override: `{}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			prior, want := testControls(), testControls()
			if tt.prior != nil {
				tt.prior(&prior)
			}
			if tt.want != nil {
				tt.want(&want)
			}
			got, diags := projectControlsFromAPI(t.Context(), testControlsResponse(t, tt.override), prior)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v\nwant %+v", got, want)
			}
		})
	}

	t.Run("import fills in project_id", func(t *testing.T) {
		prior := projectControlsResourceModel{ID: types.StringValue(testControlsProjectID)}
		got, diags := projectControlsFromAPI(t.Context(), testControlsResponse(t, `{"zero_data_retention": true}`), prior)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		want := testControls()
		want.ZeroDataRetention = types.BoolValue(true)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
}

// TestProjectControlsResourceValidateConfig plans configs the API would
// reject: they must fail at plan time, before any API call.
func TestProjectControlsResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"missing project_id": {
			body: `zero_data_retention = true`,
			want: `"project_id" is required`,
		},
		"empty vendor": {
			body: `project_id = "` + testControlsProjectID + `"
  allowed_vendors = ["openai", ""]`,
			want: "string length must be at least 1",
		},
		"preferred vendor not allowed": {
			body: `project_id = "` + testControlsProjectID + `"
  allowed_vendors           = ["anthropic"]
  vendor_routing_preference = ["anthropic", "openai"]`,
			want: `"openai" is not in allowed_vendors`,
		},
		"null vendor": {
			body: `project_id = "` + testControlsProjectID + `"
  allowed_vendors = ["openai", null]`,
			want: "contains a null value",
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
resource "mergegateway_project_controls" "test" {
  ` + tt.body + `
}`,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tt.want),
				}},
			})
		})
	}
}
