package provider

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestOrganizationSettingsResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewOrganizationSettingsResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// TestOrganizationSettingsResourceValidateConfig plans configs the API would
// reject: they must fail at plan time.
func TestOrganizationSettingsResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"blank name": {body: `name = " \n"`, want: "must not be blank"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			expectPlanError(t, "resource \"mergegateway_organization_settings\" \"test\" {\n"+tt.body+"\n}", tt.want)
		})
	}
}

// testVendorRestrictions returns vendor_restrictions with the given allowed
// vendors and the defaults for the rest.
func testVendorRestrictions(allowed ...string) types.Object {
	return types.ObjectValueMust(vendorRestrictionsAttrTypes, map[string]attr.Value{
		"allowed_vendors":            testPatterns(allowed...),
		"ignored_vendors":            testPatterns(),
		"vendor_routing_preference":  testPatterns(),
		"allowed_regions":            testPatterns(),
		"ignored_regions":            testPatterns(),
		"prefer_byok":                types.BoolValue(false),
		"byok_only":                  types.BoolValue(false),
		"customer_key_usage_default": types.StringNull(),
	})
}

// testTags returns tag_definitions with one key per entry of keyValues.
func testTags(keyValues map[string][]string) types.List {
	values := []attr.Value{}
	for key, v := range keyValues {
		values = append(values, types.ObjectValueMust(tagDefinitionAttrTypes, map[string]attr.Value{
			"key": types.StringValue(key), "values": testPatterns(v...),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: tagDefinitionAttrTypes}, values)
}

// testOrganizationSettings is the settings as state holds them.
func testOrganizationSettings() organizationSettingsResourceModel {
	return organizationSettingsResourceModel{
		ID:                          types.StringValue(testPIProjectID),
		Name:                        trimmedForm.value(types.StringValue("Acme")),
		Slug:                        types.StringValue("acme"),
		TagDefinitions:              testTags(nil),
		LogPayloads:                 types.BoolValue(true),
		ZeroDataRetention:           types.BoolValue(false),
		WebSearchMode:               types.StringValue("auto"),
		AutoHandleModelDeprecations: types.BoolValue(false),
		VendorRestrictions:          testVendorRestrictions(),
		CreatedAt:                   types.StringValue("2026-01-01T00:00:00Z"),
	}
}

func TestOrganizationSettingsRequest(t *testing.T) {
	unknown := func(m *organizationSettingsResourceModel) {
		m.Name = trimmedForm.value(types.StringUnknown())
		m.TagDefinitions = types.ListUnknown(types.ObjectType{AttrTypes: tagDefinitionAttrTypes})
		m.LogPayloads = types.BoolUnknown()
		m.ZeroDataRetention = types.BoolUnknown()
		m.WebSearchMode = types.StringUnknown()
		m.AutoHandleModelDeprecations = types.BoolUnknown()
		m.VendorRestrictions = types.ObjectUnknown(vendorRestrictionsAttrTypes)
	}
	tests := map[string]struct {
		state *organizationSettingsResourceModel
		plan  func(m *organizationSettingsResourceModel)
		want  string
	}{
		"create sends only the configured settings": {
			state: &organizationSettingsResourceModel{},
			plan: func(m *organizationSettingsResourceModel) {
				unknown(m)
				m.ZeroDataRetention = types.BoolValue(true)
			},
			want: `{"zero_data_retention": true}`,
		},
		"create with every setting": {
			state: &organizationSettingsResourceModel{},
			plan: func(m *organizationSettingsResourceModel) {
				m.TagDefinitions = testTags(map[string][]string{"team": {"support", "sales"}})
				m.VendorRestrictions = testVendorRestrictions("openai")
			},
			want: `{"name": "Acme", "tag_definitions": [{"key": "team", "values": ["support", "sales"]}], "log_payloads": true,
				"zero_data_retention": false, "web_search_mode": "auto", "auto_handle_model_deprecations": false,
				"vendor_restrictions": {"allowed_vendors": ["openai"], "ignored_vendors": [], "vendor_routing_preference": [],
					"allowed_regions": [], "ignored_regions": [], "prefer_byok": false, "byok_only": false,
					"customer_key_usage_default": null}}`,
		},
		"no change sends nothing": {
			plan: func(m *organizationSettingsResourceModel) {},
			want: `{}`,
		},
		"update sends what changed": {
			plan: func(m *organizationSettingsResourceModel) {
				m.Name = trimmedForm.value(types.StringValue("Acme Inc"))
				m.WebSearchMode = types.StringValue("merge")
			},
			want: `{"name": "Acme Inc", "web_search_mode": "merge"}`,
		},
		"removing every tag sends []": {
			state: func() *organizationSettingsResourceModel {
				m := testOrganizationSettings()
				m.TagDefinitions = testTags(map[string][]string{"team": {"support"}})
				return &m
			}(),
			plan: func(m *organizationSettingsResourceModel) {},
			want: `{"tag_definitions": []}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testOrganizationSettings(), testOrganizationSettings()
			if tt.state != nil {
				state = *tt.state
			}
			tt.plan(&plan)
			body, diags := organizationSettingsRequest(t.Context(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testOrganizationSettingsResponse decodes the API's settings with the given extra fields.
func testOrganizationSettingsResponse(t *testing.T, extra string) *client.OrganizationSettingsV1Response {
	t.Helper()
	body := `{
		"id": "` + testPIProjectID + `", "name": "Acme", "slug": "acme", "tag_definitions": [], "log_payloads": true,
		"zero_data_retention": false, "web_search_mode": "auto", "auto_handle_model_deprecations": false,
		"vendor_restrictions": {}, "created_at": "2026-01-01T00:00:00.123Z"` + extra + `}`
	var o client.OrganizationSettingsV1Response
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &o
}

func TestOrganizationSettingsFromAPI(t *testing.T) {
	ctx := t.Context()

	t.Run("defaults", func(t *testing.T) {
		// Vendor restrictions left out read as their defaults.
		got, diags := organizationSettingsFromAPI(ctx, testOrganizationSettingsResponse(t, ``))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if want := testOrganizationSettings(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("name with whitespace", func(t *testing.T) {
		// The API strips the configured name's surrounding whitespace: what it
		// stores reads as no change.
		got, diags := organizationSettingsFromAPI(ctx, testOrganizationSettingsResponse(t, ``))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		configured := trimmedForm.value(types.StringValue(" Acme\n"))
		equal, diags := configured.StringSemanticEquals(ctx, got.Name)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if !equal {
			t.Fatalf("name %v is not semantically equal to the configured %v", got.Name, configured)
		}
	})

	t.Run("every field", func(t *testing.T) {
		got, diags := organizationSettingsFromAPI(ctx, testOrganizationSettingsResponse(t, `,
			"tag_definitions": [{"key": "team", "values": ["support", "sales"]}],
			"vendor_restrictions": {"allowed_vendors": ["openai", "anthropic"], "ignored_vendors": ["groq"],
				"vendor_routing_preference": ["anthropic"], "allowed_regions": ["us"], "ignored_regions": ["cn"],
				"prefer_byok": true, "byok_only": true, "customer_key_usage_default": "prefer_byok"}`))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		wantTags := testTags(map[string][]string{"team": {"support", "sales"}})
		wantRestrictions := types.ObjectValueMust(vendorRestrictionsAttrTypes, map[string]attr.Value{
			"allowed_vendors":            testPatterns("openai", "anthropic"),
			"ignored_vendors":            testPatterns("groq"),
			"vendor_routing_preference":  testPatterns("anthropic"),
			"allowed_regions":            testPatterns("us"),
			"ignored_regions":            testPatterns("cn"),
			"prefer_byok":                types.BoolValue(true),
			"byok_only":                  types.BoolValue(true),
			"customer_key_usage_default": types.StringValue("prefer_byok"),
		})
		if !got.TagDefinitions.Equal(wantTags) {
			t.Fatalf("tag_definitions = %v, want %v", got.TagDefinitions, wantTags)
		}
		if !got.VendorRestrictions.Equal(wantRestrictions) {
			t.Fatalf("vendor_restrictions = %v, want %v", got.VendorRestrictions, wantRestrictions)
		}
	})
}
