package provider

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestGuardrailsDataSourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &datasource.SchemaResponse{}
	NewGuardrailsDataSource().Schema(ctx, datasource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

func TestGuardrailFromAPI(t *testing.T) {
	var g client.GuardrailV1Response
	if err := json.Unmarshal([]byte(`{
		"project_id": "`+testPIProjectID+`", "project_name": "Support bot",
		"controls": {"scope": "project", "scope_id": "`+testPIProjectID+`", "override": {}, "sources": {},
			"effective": {"allowed_regions": [], "ignored_regions": ["cn"], "allowed_vendors": ["openai", "anthropic"],
				"ignored_vendors": [], "vendor_routing_preference": ["anthropic"], "zero_data_retention": true}},
		"pi_settings": {"override": {}, "inherited_fields": [],
			"effective": {"pi_mode": "block", "pi_indirect_mode": "off", "pi_output_action": "redact",
				"pi_block_threshold": 0.57, "pi_tier2a_block_threshold": 0.6, "pi_tier2b_block_threshold": 0.45,
				"pi_fail_closed": false, "pi_allowlist_patterns": []}},
		"dlp_settings": {"override": {}, "entities": [
			{"entity_type": "EMAIL_ADDRESS", "category": "global", "is_seeded": true, "overridden": true, "effective_action": "redact"},
			{"entity_type": "tf-acc-rule", "category": "custom", "is_seeded": false, "overridden": false, "effective_action": null}]}
	}`), &g); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	got, diags := guardrailFromAPI(t.Context(), g)
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if got.ProjectID.ValueString() != testPIProjectID || got.ProjectName.ValueString() != "Support bot" {
		t.Fatalf("project = %v %v", got.ProjectID, got.ProjectName)
	}
	wantControls := guardrailControlsModel{
		AllowedRegions:          testPatterns(),
		IgnoredRegions:          testPatterns("cn"),
		AllowedVendors:          testPatterns("openai", "anthropic"),
		IgnoredVendors:          testPatterns(),
		VendorRoutingPreference: testPatterns("anthropic"),
		ZeroDataRetention:       types.BoolValue(true),
	}
	if !reflect.DeepEqual(got.Controls, wantControls) {
		t.Fatalf("controls = %+v\nwant %+v", got.Controls, wantControls)
	}
	if mode := got.PISettings.Attributes()["pi_mode"]; !mode.Equal(types.StringValue("block")) {
		t.Fatalf("pi_settings.pi_mode = %v, want block", mode)
	}
	wantDLP := []guardrailDLPEntityModel{
		{EntityType: types.StringValue("EMAIL_ADDRESS"), Category: types.StringValue("global"),
			EffectiveAction: types.StringValue("redact"), Overridden: types.BoolValue(true)},
		{EntityType: types.StringValue("tf-acc-rule"), Category: types.StringValue("custom"),
			EffectiveAction: types.StringNull(), Overridden: types.BoolValue(false)},
	}
	if len(got.DLPEntities) != 2 || got.DLPEntities[0] != wantDLP[0] || got.DLPEntities[1] != wantDLP[1] {
		t.Fatalf("dlp_entities = %+v\nwant %+v", got.DLPEntities, wantDLP)
	}
}
