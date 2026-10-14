package provider

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestOrganizationPISettingsResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewOrganizationPISettingsResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testOrganizationPISettings is the default policy, as a plan with every
// setting left out holds it.
func testOrganizationPISettings() organizationPISettingsResourceModel {
	return organizationPISettingsResourceModel{
		ID:                     types.StringValue("organization"),
		PiMode:                 types.StringValue("off"),
		PiIndirectMode:         types.StringValue("off"),
		PiInputAction:          types.StringValue("block"),
		PiOutputAction:         types.StringValue("redact"),
		PiBlockThreshold:       types.Float64Value(0.57),
		PiPassThreshold:        types.Float64Value(0.3),
		PiTier2aBlockThreshold: types.Float64Value(0.6),
		PiTier2bBlockThreshold: types.Float64Value(0.45),
		PiFailClosed:           types.BoolValue(false),
		PiLogFullTextOnBlock:   types.BoolValue(false),
		PiAllowlistPatterns:    testPatterns(),
		PiSaferVendorRoute:     types.StringNull(),
	}
}

func TestOrganizationPISettingsRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *organizationPISettingsResourceModel)
		want string
	}{
		// PUT replaces the whole policy, so every setting is sent.
		"defaults": {
			plan: func(m *organizationPISettingsResourceModel) {},
			want: `{"pi_mode": "off", "pi_indirect_mode": "off", "pi_input_action": "block", "pi_output_action": "redact",
				"pi_block_threshold": 0.57, "pi_pass_threshold": 0.3, "pi_tier2a_block_threshold": 0.6,
				"pi_tier2b_block_threshold": 0.45, "pi_fail_closed": false, "pi_log_full_text_on_block": false,
				"pi_allowlist_patterns": [], "pi_safer_vendor_route": null}`,
		},
		"every field": {
			plan: func(m *organizationPISettingsResourceModel) {
				m.PiMode = types.StringValue("block")
				m.PiIndirectMode = types.StringValue("alert")
				m.PiInputAction = types.StringValue("route")
				m.PiOutputAction = types.StringValue("block")
				m.PiBlockThreshold = types.Float64Value(0.7)
				m.PiPassThreshold = types.Float64Value(0.2)
				m.PiTier2aBlockThreshold = types.Float64Value(0.65)
				m.PiTier2bBlockThreshold = types.Float64Value(0.35)
				m.PiFailClosed = types.BoolValue(true)
				m.PiLogFullTextOnBlock = types.BoolValue(true)
				m.PiAllowlistPatterns = testPatterns("b", "a")
				m.PiSaferVendorRoute = types.StringValue("anthropic")
			},
			want: `{"pi_mode": "block", "pi_indirect_mode": "alert", "pi_input_action": "route", "pi_output_action": "block",
				"pi_block_threshold": 0.7, "pi_pass_threshold": 0.2, "pi_tier2a_block_threshold": 0.65,
				"pi_tier2b_block_threshold": 0.35, "pi_fail_closed": true, "pi_log_full_text_on_block": true,
				"pi_allowlist_patterns": ["b", "a"], "pi_safer_vendor_route": "anthropic"}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testOrganizationPISettings()
			tt.plan(&plan)
			body, diags := organizationPISettingsRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestOrganizationPISettingsFromAPI(t *testing.T) {
	decode := func(t *testing.T, body string) *client.OrganizationPISettingsV1Response {
		t.Helper()
		var p client.OrganizationPISettingsV1Response
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return &p
	}

	t.Run("defaults", func(t *testing.T) {
		got, diags := organizationPISettingsFromAPI(t.Context(), decode(t, `{"pi_mode": "off", "pi_indirect_mode": "off",
			"pi_input_action": "block", "pi_output_action": "redact", "pi_block_threshold": 0.57, "pi_pass_threshold": 0.3,
			"pi_tier2a_block_threshold": 0.6, "pi_tier2b_block_threshold": 0.45, "pi_fail_closed": false,
			"pi_log_full_text_on_block": false, "pi_allowlist_patterns": [], "pi_safer_vendor_route": null}`))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if want := testOrganizationPISettings(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("safer vendor route and patterns", func(t *testing.T) {
		got, _ := organizationPISettingsFromAPI(t.Context(), decode(t, `{"pi_mode": "block", "pi_indirect_mode": "off",
			"pi_input_action": "route", "pi_output_action": "redact", "pi_block_threshold": 0.57, "pi_pass_threshold": 0.3,
			"pi_tier2a_block_threshold": 0.6, "pi_tier2b_block_threshold": 0.45, "pi_fail_closed": false,
			"pi_log_full_text_on_block": false, "pi_allowlist_patterns": ["b", "a"], "pi_safer_vendor_route": "anthropic"}`))
		if !got.PiAllowlistPatterns.Equal(testPatterns("b", "a")) || got.PiSaferVendorRoute.ValueString() != "anthropic" {
			t.Fatalf("got patterns %v, route %v", got.PiAllowlistPatterns, got.PiSaferVendorRoute)
		}
	})
}

func TestOrganizationPISettingsResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"unknown mode":         {body: `pi_mode = "shadow"`, want: "value must be one of"},
		"unknown action":       {body: `pi_input_action = "drop"`, want: "value must be one of"},
		"threshold above 1":    {body: `pi_block_threshold = 1.5`, want: "between 0.000000 and 1.000000"},
		"empty vendor route":   {body: `pi_safer_vendor_route = ""`, want: "at least 1"},
		"negative pass thresh": {body: `pi_pass_threshold = -0.1`, want: "between"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			expectPlanError(t, "resource \"mergegateway_organization_pi_settings\" \"test\" {\n"+tt.body+"\n}", tt.want)
		})
	}
}
