// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestProjectResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewProjectResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testProject is a project as a plan or state holds it after create: no
// budget, no routing policy.
func testProject() projectResourceModel {
	return projectResourceModel{
		ID:                             types.StringValue("6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11"),
		OrganizationID:                 types.StringValue("0b6b7c1e-2d4f-4a3b-8f2a-1c9e5d7a6b22"),
		Name:                           types.StringValue("tf-acc-project"),
		Description:                    types.StringValue("A project"),
		Slug:                           types.StringValue("tf-acc-project"),
		IsActive:                       types.BoolValue(true),
		UsesOrganizationDefaultRouting: types.BoolValue(true),
		CreatedAt:                      types.StringValue("2026-10-01T12:00:00Z"),
		UpdatedAt:                      types.StringValue("2026-10-01T12:00:00Z"),
	}
}

func testBudget(thresholds []int64, emails []string) *projectBudgetConfigModel {
	thresholdValues := []attr.Value{}
	for _, t := range thresholds {
		thresholdValues = append(thresholdValues, types.Int64Value(t))
	}
	emailValues := []attr.Value{}
	for _, e := range emails {
		emailValues = append(emailValues, types.StringValue(e))
	}
	return &projectBudgetConfigModel{
		Amount:             types.Float64Value(100.5),
		Period:             types.StringValue("monthly"),
		EnforcementMode:    types.StringValue("soft_limit"),
		AlertThresholds:    types.SetValueMust(types.Int64Type, thresholdValues),
		NotificationEmails: types.SetValueMust(types.StringType, emailValues),
		NotifyOrgAdmins:    types.BoolValue(true),
	}
}

// testPriorityPolicy is a PRIORITY routing policy as configured: its computed
// fields are unknown, as in a plan.
func testPriorityPolicy(models ...string) *projectRoutingPolicyModel {
	entries := []attr.Value{}
	for i, m := range models {
		entries = append(entries, types.ObjectValueMust(priorityOrderAttrTypes, map[string]attr.Value{
			"model":    types.StringValue(m),
			"priority": types.Int64Value(int64(i + 1)),
		}))
	}
	return &projectRoutingPolicyModel{
		Strategy:         types.StringValue("PRIORITY"),
		Axis:             types.StringNull(),
		AllowedModels:    types.ListNull(types.StringType),
		PriorityOrder:    types.SetValueMust(types.ObjectType{AttrTypes: priorityOrderAttrTypes}, entries),
		AllowedProviders: types.ListUnknown(types.StringType),
		ID:               types.StringUnknown(),
		Slug:             types.StringUnknown(),
		Model:            types.StringUnknown(),
		CreatedAt:        types.StringUnknown(),
		ModifiedAt:       types.StringUnknown(),
	}
}

// assertJSON fails unless v marshals to the same JSON as want.
func assertJSON(t *testing.T, v any, want string) {
	t.Helper()
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("request body\n got: %s\nwant: %s", got, want)
	}
}

func TestProjectCreateRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *projectResourceModel)
		want string
	}{
		"name only": {
			plan: func(m *projectResourceModel) { m.Description = types.StringNull() },
			want: `{"name": "tf-acc-project"}`,
		},
		"inactive project is created active": {
			plan: func(m *projectResourceModel) { m.IsActive = types.BoolValue(false) },
			want: `{"name": "tf-acc-project", "description": "A project"}`,
		},
		"budget sends every field": {
			plan: func(m *projectResourceModel) { m.BudgetConfig = testBudget(nil, nil) },
			want: `{"name": "tf-acc-project", "description": "A project", "budget_config": {
				"amount": 100.5, "period": "monthly", "enforcement_mode": "soft_limit",
				"alert_thresholds": [], "notification_emails": [], "notify_org_admins": true}}`,
		},
		"routing policy turns off the org default": {
			plan: func(m *projectResourceModel) { m.RoutingPolicy = testPriorityPolicy("openai/gpt-4o") },
			want: `{"name": "tf-acc-project", "description": "A project",
				"routing_policy": {"strategy": "PRIORITY", "priority_order": [{"model": "openai/gpt-4o", "priority": 1}]},
				"uses_organization_default_routing": false}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testProject()
			tt.plan(&plan)
			body, diags := projectCreateRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestProjectUpdateRequest(t *testing.T) {
	tests := map[string]struct {
		state func(m *projectResourceModel)
		plan  func(m *projectResourceModel)
		want  string
	}{
		"no change sends nothing": {
			plan: func(m *projectResourceModel) {},
			want: `{}`,
		},
		"rename sends only the name": {
			plan: func(m *projectResourceModel) { m.Name = types.StringValue("tf-acc-renamed") },
			want: `{"name": "tf-acc-renamed"}`,
		},
		"removing the description clears it": {
			plan: func(m *projectResourceModel) { m.Description = types.StringNull() },
			want: `{"description": null}`,
		},
		"deactivate": {
			plan: func(m *projectResourceModel) { m.IsActive = types.BoolValue(false) },
			want: `{"is_active": false}`,
		},
		"add a budget": {
			plan: func(m *projectResourceModel) {
				m.BudgetConfig = testBudget([]int64{50, 80}, []string{"ops@example.com"})
			},
			want: `{"budget_config": {"amount": 100.5, "period": "monthly", "enforcement_mode": "soft_limit",
				"alert_thresholds": [50, 80], "notification_emails": ["ops@example.com"], "notify_org_admins": true}}`,
		},
		"unchanged budget is not sent": {
			state: func(m *projectResourceModel) { m.BudgetConfig = testBudget([]int64{50}, nil) },
			plan: func(m *projectResourceModel) {
				m.BudgetConfig = testBudget([]int64{50}, nil)
				m.Name = types.StringValue("tf-acc-renamed")
			},
			want: `{"name": "tf-acc-renamed"}`,
		},
		"removing the budget clears it": {
			state: func(m *projectResourceModel) { m.BudgetConfig = testBudget(nil, nil) },
			plan:  func(m *projectResourceModel) {},
			want:  `{"budget_config": null}`,
		},
		"set a routing policy": {
			plan: func(m *projectResourceModel) { m.RoutingPolicy = testPriorityPolicy("openai/gpt-4o") },
			want: `{"routing_policy": {"strategy": "PRIORITY", "priority_order": [{"model": "openai/gpt-4o", "priority": 1}]},
				"uses_organization_default_routing": false}`,
		},
		"unchanged routing policy is not sent, though its computed fields are unknown": {
			state: func(m *projectResourceModel) {
				m.RoutingPolicy = testPriorityPolicy("openai/gpt-4o")
				m.RoutingPolicy.ID = types.StringValue("policy-id")
			},
			plan: func(m *projectResourceModel) { m.RoutingPolicy = testPriorityPolicy("openai/gpt-4o") },
			want: `{}`,
		},
		"removing the routing policy returns to the org default": {
			state: func(m *projectResourceModel) { m.RoutingPolicy = testPriorityPolicy("openai/gpt-4o") },
			plan:  func(m *projectResourceModel) {},
			want:  `{"uses_organization_default_routing": true}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testProject(), testProject()
			if tt.state != nil {
				tt.state(&state)
			}
			tt.plan(&plan)
			body, diags := projectUpdateRequest(t.Context(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testResponse decodes an API project response with the given extra fields.
func testResponse(t *testing.T, extra string) *client.ProjectV1Response {
	t.Helper()
	body := `{
		"id": "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11",
		"organization_id": "0b6b7c1e-2d4f-4a3b-8f2a-1c9e5d7a6b22",
		"name": "tf-acc-project",
		"slug": "tf-acc-project",
		"description": null,
		"is_active": true,
		"created_at": "2026-10-01T12:00:00Z",
		"updated_at": "2026-10-02T12:00:00.123456Z",
		"uses_organization_default_routing": true,
		"budget_config": null,
		"routing_policy": null` + extra + `}`
	var p client.ProjectV1Response
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &p
}

func TestProjectFromAPI(t *testing.T) {
	ctx := t.Context()

	t.Run("project", func(t *testing.T) {
		got, diags := projectFromAPI(ctx, testResponse(t, ``), projectResourceModel{})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		want := testProject()
		want.Description = types.StringNull()
		want.UpdatedAt = types.StringValue("2026-10-02T12:00:00Z")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("budget", func(t *testing.T) {
		prior := testProject()
		prior.BudgetConfig = testBudget([]int64{90, 50}, []string{"Ops@Example.COM", "b@example.com"})
		got, diags := projectFromAPI(ctx, testResponse(t, `, "budget_config": {
			"amount": 100.5, "period": "monthly", "enforcement_mode": "soft_limit",
			"alert_thresholds": [50, 90], "notification_emails": ["Ops@example.com", "b@example.com"]}`), prior)
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		// Sets ignore the order, the configured spelling of an address is
		// kept, and a missing notify_org_admins is true.
		if !got.BudgetConfig.equal(prior.BudgetConfig) {
			t.Fatalf("got %+v\nwant %+v", got.BudgetConfig, prior.BudgetConfig)
		}
	})

	t.Run("budget with lists left out", func(t *testing.T) {
		got, diags := projectFromAPI(ctx, testResponse(t, `, "budget_config": {
			"amount": 100.5, "period": "monthly", "enforcement_mode": "soft_limit"}`), projectResourceModel{})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if want := testBudget(nil, nil); !got.BudgetConfig.equal(want) {
			t.Fatalf("got %+v\nwant %+v", got.BudgetConfig, want)
		}
	})

	t.Run("priority policy", func(t *testing.T) {
		got, diags := projectFromAPI(ctx, testResponse(t, `, "uses_organization_default_routing": false, "routing_policy": {
			"id": "policy-id", "name": "tf-acc-project routing policy", "description": null, "strategy": "PRIORITY",
			"allowed_providers": null, "allowed_models": [], "axis": null,
			"priority_order": [{"model": "openai/gpt-4o", "priority": 1}, {"model": "openai/gpt-4o-mini", "priority": 2}],
			"slug": null, "model": null, "created_at": "2026-10-01T12:00:00Z", "modified_at": "2026-10-01T12:00:00Z"}`), projectResourceModel{})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		want := testPriorityPolicy("openai/gpt-4o", "openai/gpt-4o-mini")
		if !got.RoutingPolicy.equal(want) {
			t.Fatalf("got %+v\nwant %+v", got.RoutingPolicy, want)
		}
		if got.RoutingPolicy.ID.ValueString() != "policy-id" || !got.RoutingPolicy.Slug.IsNull() || !got.RoutingPolicy.AllowedProviders.IsNull() {
			t.Fatalf("computed fields: %+v", got.RoutingPolicy)
		}
		if got.UsesOrganizationDefaultRouting.ValueBool() {
			t.Fatal("uses_organization_default_routing should be false")
		}
	})

	intelligent := `, "uses_organization_default_routing": false, "routing_policy": {
		"id": "policy-id", "name": "p", "description": null, "strategy": "INTELLIGENT",
		"allowed_providers": ["openai"], "allowed_models": ["openai/gpt-4o"], "axis": "COST", "priority_order": null,
		"slug": "p", "model": "@alias/p", "created_at": "2026-10-01T12:00:00Z", "modified_at": "2026-10-01T12:00:00Z"}`

	t.Run("default axis stays unset", func(t *testing.T) {
		prior := testProject()
		prior.RoutingPolicy = &projectRoutingPolicyModel{Axis: types.StringNull()}
		got, _ := projectFromAPI(ctx, testResponse(t, intelligent), prior)
		if !got.RoutingPolicy.Axis.IsNull() {
			t.Fatalf("axis = %v, want null", got.RoutingPolicy.Axis)
		}
	})

	t.Run("configured axis is kept", func(t *testing.T) {
		prior := testProject()
		prior.RoutingPolicy = &projectRoutingPolicyModel{Axis: types.StringValue("COST")}
		got, _ := projectFromAPI(ctx, testResponse(t, intelligent), prior)
		if got.RoutingPolicy.Axis.ValueString() != "COST" {
			t.Fatalf("axis = %v, want COST", got.RoutingPolicy.Axis)
		}
		if got.RoutingPolicy.Model.ValueString() != "@alias/p" || len(got.RoutingPolicy.AllowedProviders.Elements()) != 1 {
			t.Fatalf("computed fields: %+v", got.RoutingPolicy)
		}
	})
}

func TestCheckAllowedModels(t *testing.T) {
	models := func(m ...string) *projectRoutingPolicyModel {
		values := []attr.Value{}
		for _, s := range m {
			values = append(values, types.StringValue(s))
		}
		return &projectRoutingPolicyModel{AllowedModels: types.ListValueMust(types.StringType, values)}
	}
	plan, state := testProject(), testProject()

	plan.RoutingPolicy, state.RoutingPolicy = models("a", "b"), models("a", "b")
	if diags := checkAllowedModels(plan, state); diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}

	state.RoutingPolicy = models("a")
	diags := checkAllowedModels(plan, state)
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), ": b.") {
		t.Fatalf("expected an error naming b, got %v", diags)
	}
}

func TestAPIError(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
		want   string
	}{
		"string detail": {404, `{"detail": "Project not found"}`, "HTTP 404 Not Found: Project not found"},
		"structured detail": {
			409, `{"detail": {"code": "project_has_keys", "message": "Delete the project's keys first", "count": 2}}`,
			"HTTP 409 Conflict: Delete the project's keys first (project_has_keys)",
		},
		"validation errors": {
			422, `{"detail": [{"loc": ["body", "budget_config", "amount"], "msg": "must be greater than 0", "type": "x"}]}`,
			"HTTP 422 Unprocessable Entity:\nbody.budget_config.amount: must be greater than 0",
		},
		"not JSON": {502, `<html>Bad gateway</html>`, "HTTP 502 Bad Gateway"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := apiError(tt.status, []byte(tt.body)); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestProjectResourceValidateConfig plans configs the API would reject: they
// must fail at plan time, before any API call.
func TestProjectResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"zero budget": {
			body: `budget_config = { amount = 0, period = "monthly", enforcement_mode = "soft_limit" }`,
			want: "amount must be greater than 0",
		},
		"emails differing only in case": {
			body: `budget_config = {
  amount = 10, period = "monthly", enforcement_mode = "soft_limit"
  notification_emails = ["Ops@example.com", "ops@example.com"]
}`,
			want: "Duplicate notification email",
		},
		"PRIORITY with allowed_models": {
			body: `routing_policy = {
  strategy       = "PRIORITY"
  priority_order = [{ model = "openai/gpt-4o", priority = 1 }]
  allowed_models = ["openai/gpt-4o"]
}`,
			want: "allowed_models applies only to INTELLIGENT",
		},
		"PRIORITY without priority_order": {
			body: `routing_policy = { strategy = "PRIORITY" }`,
			want: "needs priority_order",
		},
		"INTELLIGENT without allowed_models": {
			body: `routing_policy = { strategy = "INTELLIGENT" }`,
			want: "needs allowed_models",
		},
		"INTELLIGENT with priority_order": {
			body: `routing_policy = {
  strategy       = "INTELLIGENT"
  priority_order = [{ model = "openai/gpt-4o", priority = 1 }]
}`,
			want: "priority_order applies only to PRIORITY",
		},
		"model listed twice in priority_order": {
			body: `routing_policy = {
  strategy = "PRIORITY"
  priority_order = [
    { model = "openai/gpt-4o", priority = 1 },
    { model = "openai/gpt-4o", priority = 2 },
  ]
}`,
			want: "Duplicate model in priority_order",
		},
		"model listed twice in allowed_models": {
			body: `routing_policy = {
  strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o", "openai/gpt-4o"]
}`,
			want: "duplicate values",
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
resource "mergegateway_project" "test" {
  name = "tf-acc-validate"
  ` + tt.body + `
}`,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tt.want),
				}},
			})
		})
	}
}
