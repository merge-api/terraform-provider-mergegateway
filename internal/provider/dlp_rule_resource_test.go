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

const testDLPRuleID = "3a4b5c6d-7e8f-4a0b-9c1d-2e3f4a5b6c7d"

func TestDLPRuleResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewDLPRuleResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testDLPRule is a rule with a pattern and the defaults, as state holds it after create.
func testDLPRule() dlpRuleResourceModel {
	return dlpRuleResourceModel{
		ID:          types.StringValue(testDLPRuleID),
		EntityType:  types.StringValue("tf-acc-employee-id"),
		Description: types.StringValue(""),
		Pattern:     types.StringValue(`EMP-\d{6}`),
		Keywords:    types.ListNull(types.StringType),
		Action:      types.StringValue("log"),
		Enabled:     types.BoolValue(true),
		CreatedAt:   types.StringValue("2026-10-01T12:00:00Z"),
		UpdatedAt:   types.StringValue("2026-10-01T12:00:00Z"),
	}
}

func TestDLPRuleCreateRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *dlpRuleResourceModel)
		want string
	}{
		"defaults": {
			plan: func(m *dlpRuleResourceModel) {},
			want: `{"entity_type": "tf-acc-employee-id", "description": "", "pattern": "EMP-\\d{6}", "action": "log", "enabled": true}`,
		},
		"every field": {
			plan: func(m *dlpRuleResourceModel) {
				m.Description = types.StringValue("Employee IDs")
				m.Keywords = testPatterns("payroll", "badge")
				m.Action = types.StringValue("redact")
				m.Enabled = types.BoolValue(false)
			},
			want: `{"entity_type": "tf-acc-employee-id", "description": "Employee IDs", "pattern": "EMP-\\d{6}",
				"keywords": ["payroll", "badge"], "action": "redact", "enabled": false}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testDLPRule()
			tt.plan(&plan)
			body, diags := dlpRuleCreateRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestDLPRuleUpdateRequest(t *testing.T) {
	tests := map[string]struct {
		state func(m *dlpRuleResourceModel)
		plan  func(m *dlpRuleResourceModel)
		want  string
	}{
		"no change sends nothing": {
			plan: func(m *dlpRuleResourceModel) {},
			want: `{}`,
		},
		"change every field": {
			plan: func(m *dlpRuleResourceModel) {
				m.EntityType = types.StringValue("tf-acc-renamed")
				m.Description = types.StringValue("Employee IDs")
				m.Pattern = types.StringValue(`EMP-\d{8}`)
				m.Keywords = testPatterns("payroll")
				m.Action = types.StringValue("block")
				m.Enabled = types.BoolValue(false)
			},
			want: `{"entity_type": "tf-acc-renamed", "description": "Employee IDs", "pattern": "EMP-\\d{8}",
				"keywords": ["payroll"], "action": "block", "enabled": false}`,
		},
		// null means "keep" to the API.
		"removing pattern and keywords clears them": {
			state: func(m *dlpRuleResourceModel) { m.Keywords = testPatterns("payroll") },
			plan: func(m *dlpRuleResourceModel) {
				m.Pattern = types.StringNull()
				m.Keywords = types.ListNull(types.StringType)
			},
			want: `{"pattern": "", "keywords": []}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testDLPRule(), testDLPRule()
			if tt.state != nil {
				tt.state(&state)
			}
			tt.plan(&plan)
			body, diags := dlpRuleUpdateRequest(t.Context(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testDLPRuleResponse decodes an API rule with the given extra fields.
func testDLPRuleResponse(t *testing.T, extra string) *client.DLPRuleV1Response {
	t.Helper()
	body := `{
		"id": "` + testDLPRuleID + `", "category": "custom", "entity_type": "tf-acc-employee-id",
		"display_name": "tf-acc-employee-id", "description": "", "detection_method": "Custom (regex + keywords)",
		"action": "log", "enabled": true, "pattern": "EMP-\\d{6}", "keywords": null, "is_seeded": false,
		"created_at": "2026-10-01T12:00:00.123456Z", "updated_at": "2026-10-01T12:00:00Z"` + extra + `}`
	var r client.DLPRuleV1Response
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &r
}

func TestDLPRuleFromAPI(t *testing.T) {
	ctx := t.Context()

	t.Run("rule", func(t *testing.T) {
		got, diags := dlpRuleFromAPI(ctx, testDLPRuleResponse(t, ``))
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if want := testDLPRule(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("cleared pattern and keywords read as null", func(t *testing.T) {
		got, _ := dlpRuleFromAPI(ctx, testDLPRuleResponse(t, `, "pattern": "", "keywords": []`))
		if !got.Pattern.IsNull() || !got.Keywords.IsNull() {
			t.Fatalf("pattern = %v, keywords = %v, want both null", got.Pattern, got.Keywords)
		}
	})

	t.Run("keywords keep their order", func(t *testing.T) {
		got, _ := dlpRuleFromAPI(ctx, testDLPRuleResponse(t, `, "keywords": ["payroll", "badge"], "action": "block"`))
		if !got.Keywords.Equal(testPatterns("payroll", "badge")) || got.Action.ValueString() != "block" {
			t.Fatalf("keywords = %v, action = %v", got.Keywords, got.Action)
		}
	})
}

func TestDLPRuleResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"no pattern or keywords": {body: `entity_type = "x"`, want: "needs a pattern, keywords or both"},
		"empty keywords":         {body: "entity_type = \"x\"\nkeywords = []", want: "at least 1"},
		"empty pattern":          {body: "entity_type = \"x\"\npattern = \"\"", want: "between 1 and 500"},
		"long entity_type":       {body: "entity_type = \"" + strings.Repeat("a", 81) + "\"\npattern = \"x\"", want: "between 1 and 80"},
		"unknown action":         {body: "entity_type = \"x\"\npattern = \"x\"\naction = \"alert\"", want: "value must be one of"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			expectPlanError(t, "resource \"mergegateway_dlp_rule\" \"test\" {\n"+tt.body+"\n}", tt.want)
		})
	}
}
