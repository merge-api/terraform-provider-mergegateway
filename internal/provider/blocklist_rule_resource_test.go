package provider

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

const testBlocklistRuleID = "4b5c6d7e-8f9a-4b0c-8d1e-2f3a4b5c6d7e"

func TestBlocklistRuleResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewBlocklistRuleResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testBlocklistRule is a block rule on one provider, as state holds it after create.
func testBlocklistRule() blocklistRuleResourceModel {
	return blocklistRuleResourceModel{
		ID:          types.StringValue(testBlocklistRuleID),
		RuleType:    types.StringValue("block"),
		CustomerIDs: testPatterns(),
		Providers:   testPatterns("deepseek"),
		Models:      testPatterns(),
		Reason:      types.StringValue(""),
		CreatedAt:   types.StringValue("2026-10-01T12:00:00Z"),
		UpdatedAt:   types.StringValue("2026-10-01T12:00:00Z"),
	}
}

func TestBlocklistRuleCreateRequest(t *testing.T) {
	plan := testBlocklistRule()
	body, diags := blocklistRuleCreateRequest(t.Context(), plan)
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	assertJSON(t, body, `{"rule_type": "block", "customer_ids": [], "providers": ["deepseek"], "models": [], "reason": ""}`)

	plan.RuleType = types.StringValue("pin")
	plan.CustomerIDs = testPatterns("cust-2", "cust-1")
	plan.Models = testPatterns("openai/gpt-4o")
	plan.Reason = types.StringValue("Contract")
	body, _ = blocklistRuleCreateRequest(t.Context(), plan)
	assertJSON(t, body, `{"rule_type": "pin", "customer_ids": ["cust-2", "cust-1"], "providers": ["deepseek"],
		"models": ["openai/gpt-4o"], "reason": "Contract"}`)
}

func TestBlocklistRuleUpdateRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *blocklistRuleResourceModel)
		want string
	}{
		"no change sends nothing": {
			plan: func(m *blocklistRuleResourceModel) {},
			want: `{}`,
		},
		"change every field": {
			plan: func(m *blocklistRuleResourceModel) {
				m.RuleType = types.StringValue("pin")
				m.CustomerIDs = testPatterns("cust-1")
				m.Models = testPatterns("openai/gpt-4o")
				m.Reason = types.StringValue("Contract")
			},
			want: `{"rule_type": "pin", "customer_ids": ["cust-1"], "models": ["openai/gpt-4o"], "reason": "Contract"}`,
		},
		"emptying a list sends []": {
			plan: func(m *blocklistRuleResourceModel) {
				m.Providers = testPatterns()
				m.Models = testPatterns("openai/gpt-4o")
			},
			want: `{"providers": [], "models": ["openai/gpt-4o"]}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testBlocklistRule(), testBlocklistRule()
			tt.plan(&plan)
			body, diags := blocklistRuleUpdateRequest(t.Context(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestBlocklistRuleFromAPI(t *testing.T) {
	var r client.BlocklistRuleV1Response
	if err := json.Unmarshal([]byte(`{"id": "`+testBlocklistRuleID+`", "rule_type": "block", "customer_ids": [],
		"providers": ["deepseek"], "models": [], "reason": "", "created_at": "2026-10-01T12:00:00.5Z",
		"updated_at": "2026-10-01T12:00:00Z"}`), &r); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got, diags := blocklistRuleFromAPI(t.Context(), &r)
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if want := testBlocklistRule(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestBlocklistRuleResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"empty block rule":        {body: `reason = "x"`, want: "needs at least one of customer_ids, providers and models"},
		"pin without customers":   {body: "rule_type = \"pin\"\nproviders = [\"openai\"]", want: "needs at least one customer ID"},
		"pin without providers":   {body: "rule_type = \"pin\"\ncustomer_ids = [\"c\"]", want: "needs at least one provider or model"},
		"blank entry":             {body: `providers = [" "]`, want: "must not be blank"},
		"unknown rule_type":       {body: "rule_type = \"allow\"\nproviders = [\"openai\"]", want: "value must be one of"},
		"empty lists still empty": {body: "providers = []\nmodels = []", want: "Empty blocklist rule"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			expectPlanError(t, "resource \"mergegateway_blocklist_rule\" \"test\" {\n"+tt.body+"\n}", tt.want)
		})
	}
}
