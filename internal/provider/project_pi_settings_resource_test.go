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

const testPIProjectID = "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11"

func TestProjectPISettingsResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewProjectPISettingsResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testPISettings is a plan with no overrides set.
func testPISettings() projectPISettingsResourceModel {
	return projectPISettingsResourceModel{
		ID:                     types.StringUnknown(),
		ProjectID:              types.StringValue(testPIProjectID),
		PiMode:                 types.StringNull(),
		PiIndirectMode:         types.StringNull(),
		PiOutputAction:         types.StringNull(),
		PiBlockThreshold:       types.Float64Null(),
		PiTier2aBlockThreshold: types.Float64Null(),
		PiTier2bBlockThreshold: types.Float64Null(),
		PiFailClosed:           types.BoolNull(),
		PiAllowlistPatterns:    types.ListNull(types.StringType),
		Effective:              types.ObjectUnknown(piEffectiveAttrTypes),
	}
}

func testPatterns(patterns ...string) types.List {
	values := []attr.Value{}
	for _, p := range patterns {
		values = append(values, types.StringValue(p))
	}
	return types.ListValueMust(types.StringType, values)
}

func TestProjectPISettingsRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *projectPISettingsResourceModel)
		want string
	}{
		"one override sends only it": {
			plan: func(m *projectPISettingsResourceModel) { m.PiMode = types.StringValue("block") },
			want: `{"pi_mode": "block"}`,
		},
		"every override": {
			plan: func(m *projectPISettingsResourceModel) {
				m.PiMode = types.StringValue("alert")
				m.PiIndirectMode = types.StringValue("block")
				m.PiOutputAction = types.StringValue("redact")
				m.PiBlockThreshold = types.Float64Value(0.5)
				m.PiTier2aBlockThreshold = types.Float64Value(0.6)
				m.PiTier2bBlockThreshold = types.Float64Value(0)
				m.PiFailClosed = types.BoolValue(false)
				m.PiAllowlistPatterns = testPatterns("b", "a")
			},
			want: `{"pi_mode": "alert", "pi_indirect_mode": "block", "pi_output_action": "redact",
				"pi_block_threshold": 0.5, "pi_tier2a_block_threshold": 0.6, "pi_tier2b_block_threshold": 0,
				"pi_fail_closed": false, "pi_allowlist_patterns": ["b", "a"]}`,
		},
		"empty allowlist is sent": {
			plan: func(m *projectPISettingsResourceModel) { m.PiAllowlistPatterns = testPatterns() },
			want: `{"pi_allowlist_patterns": []}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testPISettings()
			tt.plan(&plan)
			body, diags := projectPISettingsRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testPIResponse decodes an API PI settings response with the given override.
func testPIResponse(t *testing.T, override string) *client.ProjectPISettingsV1Response {
	t.Helper()
	body := `{
		"override": ` + override + `,
		"effective": {
			"pi_mode": "block", "pi_indirect_mode": "off", "pi_output_action": "redact",
			"pi_block_threshold": 0.57, "pi_tier2a_block_threshold": 0.6, "pi_tier2b_block_threshold": 0.45,
			"pi_fail_closed": false, "pi_allowlist_patterns": [], "pi_input_action": "block",
			"pi_log_full_text_on_block": false, "pi_pass_threshold": 0.3, "pi_safer_vendor_route": null
		},
		"inherited_fields": []
	}`
	var r client.ProjectPISettingsV1Response
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &r
}

func TestProjectPISettingsFromAPI(t *testing.T) {
	ctx := t.Context()

	t.Run("overrides", func(t *testing.T) {
		got, diags := projectPISettingsFromAPI(ctx, testPIProjectID, testPIResponse(t, `{
			"pi_mode": "block", "pi_indirect_mode": null, "pi_output_action": "route",
			"pi_block_threshold": 0.7, "pi_tier2b_block_threshold": 0,
			"pi_fail_closed": false, "pi_allowlist_patterns": ["b", "a"]}`))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		want := testPISettings()
		want.ID = types.StringValue(testPIProjectID)
		want.PiMode = types.StringValue("block")
		want.PiOutputAction = types.StringValue("route")
		want.PiBlockThreshold = types.Float64Value(0.7)
		want.PiTier2bBlockThreshold = types.Float64Value(0)
		want.PiFailClosed = types.BoolValue(false)
		want.PiAllowlistPatterns = testPatterns("b", "a")
		want.Effective = got.Effective
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
		if !got.hasOverride() {
			t.Fatal("hasOverride should be true")
		}

		// Only the overridable fields are kept.
		wantEffective := types.ObjectValueMust(piEffectiveAttrTypes, map[string]attr.Value{
			"pi_mode":                   types.StringValue("block"),
			"pi_indirect_mode":          types.StringValue("off"),
			"pi_output_action":          types.StringValue("redact"),
			"pi_block_threshold":        types.Float64Value(0.57),
			"pi_tier2a_block_threshold": types.Float64Value(0.6),
			"pi_tier2b_block_threshold": types.Float64Value(0.45),
			"pi_fail_closed":            types.BoolValue(false),
			"pi_allowlist_patterns":     testPatterns(),
		})
		if !got.Effective.Equal(wantEffective) {
			t.Fatalf("effective: got %v\nwant %v", got.Effective, wantEffective)
		}
	})

	t.Run("empty allowlist stays empty", func(t *testing.T) {
		got, _ := projectPISettingsFromAPI(ctx, testPIProjectID, testPIResponse(t, `{"pi_allowlist_patterns": []}`))
		if !got.PiAllowlistPatterns.Equal(testPatterns()) {
			t.Fatalf("pi_allowlist_patterns = %v, want []", got.PiAllowlistPatterns)
		}
	})

	t.Run("no overrides", func(t *testing.T) {
		got, diags := projectPISettingsFromAPI(ctx, testPIProjectID, testPIResponse(t, `{}`))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if got.hasOverride() {
			t.Fatalf("hasOverride should be false: %+v", got)
		}
	})
}

// TestProjectPISettingsResourceValidateConfig plans configs that must fail at
// plan time, before any API call.
func TestProjectPISettingsResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"no overrides": {
			body: ``,
			want: "Set at least one pi_\\* attribute",
		},
		"unknown mode": {
			body: `pi_mode = "shadow"`,
			want: `value must be one of`,
		},
		"unknown output action": {
			body: `pi_output_action = "drop"`,
			want: `value must be one of`,
		},
		"threshold above 1": {
			body: `pi_block_threshold = 1.5`,
			want: `must be between 0`,
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
resource "mergegateway_project_pi_settings" "test" {
  project_id = "` + testPIProjectID + `"
  ` + tt.body + `
}`,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tt.want),
				}},
			})
		})
	}
}
