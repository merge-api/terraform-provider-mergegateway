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

func TestAPIKeyResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewAPIKeyResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	if key := resp.Schema.Attributes["key"]; !key.IsSensitive() {
		t.Fatal("key must be sensitive")
	}
}

// testAPIKey is a key as a plan or state holds it after create: no project,
// no limit, no allowed models, no routing policy.
func testAPIKey() apiKeyResourceModel {
	return apiKeyResourceModel{
		ID:              types.StringValue("3b4c5d"),
		Key:             types.StringValue("sk-test-raw"),
		Name:            types.StringValue("tf-acc-key"),
		ProjectID:       types.StringNull(),
		Limit:           types.Float64Null(),
		LimitReset:      types.StringValue("monthly"),
		AllowedModels:   types.SetNull(types.StringType),
		Disabled:        types.BoolValue(false),
		RoutingPolicyID: types.StringNull(),
		BudgetGroupID:   types.StringNull(),
		Label:           types.StringValue("sk-test"),
		Usage:           types.Float64Value(0),
		LimitRemaining:  types.Float64Null(),
		CreatedAt:       types.StringValue("2026-10-01T12:00:00Z"),
		CreatorUserID:   types.StringNull(),
		ExpiresAt:       types.StringNull(),
	}
}

func testModels(models ...string) types.Set {
	values := []attr.Value{}
	for _, m := range models {
		values = append(values, types.StringValue(m))
	}
	return types.SetValueMust(types.StringType, values)
}

func TestAPIKeyCreateRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *apiKeyResourceModel)
		want string
	}{
		"defaults": {
			plan: func(m *apiKeyResourceModel) { m.Name = types.StringNull() },
			want: `{"limit_reset": "monthly", "disabled": false}`,
		},
		"every field": {
			plan: func(m *apiKeyResourceModel) {
				m.ProjectID = types.StringValue("6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11")
				m.Limit = types.Float64Value(12.5)
				m.LimitReset = types.StringValue("weekly")
				m.AllowedModels = testModels("openai/gpt-4o")
				m.Disabled = types.BoolValue(true)
				m.RoutingPolicyID = types.StringValue("policy-id")
			},
			want: `{"name": "tf-acc-key", "project_id": "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11", "limit": 12.5,
				"limit_reset": "weekly", "allowed_models": ["openai/gpt-4o"], "disabled": true, "routing_policy_id": "policy-id"}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testAPIKey()
			tt.plan(&plan)
			body, diags := apiKeyCreateRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestAPIKeyUpdateRequest(t *testing.T) {
	full := func(m *apiKeyResourceModel) {
		m.Limit = types.Float64Value(10)
		m.AllowedModels = testModels("openai/gpt-4o")
		m.RoutingPolicyID = types.StringValue("policy-id")
	}
	tests := map[string]struct {
		state func(m *apiKeyResourceModel)
		plan  func(m *apiKeyResourceModel)
		want  string
	}{
		"no change sends nothing": {
			plan: func(m *apiKeyResourceModel) {},
			want: `{}`,
		},
		"change every field": {
			plan: func(m *apiKeyResourceModel) {
				full(m)
				m.Name = types.StringValue("tf-acc-renamed")
				m.LimitReset = types.StringValue("daily")
				m.Disabled = types.BoolValue(true)
			},
			want: `{"name": "tf-acc-renamed", "limit": 10, "limit_reset": "daily", "allowed_models": ["openai/gpt-4o"],
				"disabled": true, "routing_policy_id": "policy-id"}`,
		},
		"unchanged fields are not sent": {
			state: full,
			plan: func(m *apiKeyResourceModel) {
				full(m)
				m.Limit = types.Float64Value(20)
			},
			want: `{"limit": 20}`,
		},
		"removing allowed_models and routing_policy_id clears them": {
			state: full,
			plan: func(m *apiKeyResourceModel) {
				m.Limit = types.Float64Value(10)
			},
			want: `{"allowed_models": null, "routing_policy_id": null}`,
		},
		"name and limit are never sent as null": {
			state: full,
			plan: func(m *apiKeyResourceModel) {
				full(m)
				m.Name = types.StringNull()
				m.Limit = types.Float64Null()
			},
			want: `{}`,
		},
		"project_id is never sent": {
			plan: func(m *apiKeyResourceModel) { m.ProjectID = types.StringValue("6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11") },
			want: `{}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testAPIKey(), testAPIKey()
			if tt.state != nil {
				tt.state(&state)
			}
			tt.plan(&plan)
			body, diags := apiKeyUpdateRequest(t.Context(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestAPIKeyClearErrors(t *testing.T) {
	state := testAPIKey()
	state.Limit = types.Float64Value(10)

	changed := state
	changed.Name = types.StringValue("tf-acc-renamed")
	changed.Limit = types.Float64Value(20)
	if diags := apiKeyClearErrors(changed, state); diags.HasError() {
		t.Fatalf("changing name and limit: unexpected error: %v", diags)
	}

	removed := state
	removed.Name = types.StringNull()
	removed.Limit = types.Float64Null()
	diags := apiKeyClearErrors(removed, state)
	if len(diags) != 2 || diags[0].Summary() != "Can't remove name" || diags[1].Summary() != "Can't remove limit" {
		t.Fatalf("expected errors for name and limit, got %v", diags)
	}

	// A new project replaces the key, so it may drop them.
	removed.ProjectID = types.StringValue("6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11")
	if diags := apiKeyClearErrors(removed, state); diags.HasError() {
		t.Fatalf("replacing: unexpected error: %v", diags)
	}
}

// testKeyResponse decodes an API key object with the given extra fields.
func testKeyResponse(t *testing.T, extra string) *client.KeyObject {
	t.Helper()
	body := `{
		"hash": "3b4c5d", "label": "sk-test", "name": "tf-acc-key", "disabled": false,
		"limit": null, "limit_remaining": null, "limit_reset": null, "allowed_models": null,
		"project_id": null, "routing_policy_id": null, "budget_group_id": null,
		"usage": 0, "usage_daily": 0, "usage_weekly": 0, "usage_monthly": 0,
		"byok_usage": 0, "byok_usage_daily": 0, "byok_usage_weekly": 0, "byok_usage_monthly": 0,
		"creator_user_id": null, "expires_at": null, "created_at": "2026-10-01T12:00:00.123456Z"` + extra + `}`
	var k client.KeyObject
	if err := json.Unmarshal([]byte(body), &k); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &k
}

func TestAPIKeyFromAPI(t *testing.T) {
	ctx := t.Context()

	t.Run("key", func(t *testing.T) {
		got, diags := apiKeyFromAPI(ctx, testKeyResponse(t, ``), testAPIKey())
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		// A null limit_reset is monthly, and the raw key comes from prior.
		if want := testAPIKey(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("import has no raw key", func(t *testing.T) {
		prior := apiKeyResourceModel{Key: types.StringNull()}
		got, _ := apiKeyFromAPI(ctx, testKeyResponse(t, ``), prior)
		if !got.Key.IsNull() {
			t.Fatalf("key = %v, want null", got.Key)
		}
	})

	t.Run("every field", func(t *testing.T) {
		got, diags := apiKeyFromAPI(ctx, testKeyResponse(t, `, "limit": 10, "limit_remaining": 7.5, "limit_reset": "weekly",
			"allowed_models": ["openai/gpt-4o-mini", "openai/gpt-4o"], "disabled": true, "usage": 2.5,
			"project_id": "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11", "routing_policy_id": "policy-id", "budget_group_id": "bg",
			"creator_user_id": "0b6b7c1e-2d4f-4a3b-8f2a-1c9e5d7a6b22", "expires_at": "2027-01-01T00:00:00Z"`), testAPIKey())
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		want := testAPIKey()
		want.ProjectID = types.StringValue("6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11")
		want.Limit = types.Float64Value(10)
		want.LimitRemaining = types.Float64Value(7.5)
		want.LimitReset = types.StringValue("weekly")
		want.AllowedModels = testModels("openai/gpt-4o", "openai/gpt-4o-mini")
		want.Disabled = types.BoolValue(true)
		want.Usage = types.Float64Value(2.5)
		want.RoutingPolicyID = types.StringValue("policy-id")
		want.BudgetGroupID = types.StringValue("bg")
		want.CreatorUserID = types.StringValue("0b6b7c1e-2d4f-4a3b-8f2a-1c9e5d7a6b22")
		want.ExpiresAt = types.StringValue("2027-01-01T00:00:00Z")
		// Sets ignore the order, but DeepEqual doesn't.
		if !got.AllowedModels.Equal(want.AllowedModels) {
			t.Fatalf("allowed_models = %v, want %v", got.AllowedModels, want.AllowedModels)
		}
		got.AllowedModels = want.AllowedModels
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	// [] allows no model, unlike null: it must show as a diff, not as null.
	t.Run("empty allowed_models stays empty", func(t *testing.T) {
		got, _ := apiKeyFromAPI(ctx, testKeyResponse(t, `, "allowed_models": []`), testAPIKey())
		if want := testModels(); !got.AllowedModels.Equal(want) {
			t.Fatalf("allowed_models = %v, want %v", got.AllowedModels, want)
		}
	})
}

// TestAPIKeyResourceValidateConfig plans configs the API would reject: they
// must fail at plan time, before any API call.
func TestAPIKeyResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"zero limit":           {body: `limit = 0`, want: "limit must be greater than 0"},
		"unknown limit_reset":  {body: `limit_reset = "yearly"`, want: "value must be one of"},
		"empty allowed_models": {body: `allowed_models = []`, want: "at least 1"},
		"empty name":           {body: `name = ""`, want: "at least 1"},
		"model with surrounding whitespace": {
			body: `allowed_models = [" openai/gpt-4o"]`,
			want: `must not start or end with\s+whitespace`,
		},
		"model without a provider": {
			body: `allowed_models = ["gpt-4o"]`,
			want: "must be written as provider/model",
		},
		"model alias": {
			body: `allowed_models = ["@alias/support"]`,
			want: "is a model alias",
		},
		"wildcard not at the end": {
			body: `allowed_models = ["openai/*-mini"]`,
			want: "allowed only once, as the last character",
		},
		"two wildcards": {
			body: `allowed_models = ["openai/gpt-**"]`,
			want: "allowed only once, as the last character",
		},
		"bare wildcard": {
			body: `allowed_models = ["*"]`,
			want: "must be written as provider/model",
		},
		"project_id not a UUID": {
			body: `project_id = "production"`,
			want: "Expected a project UUID",
		},
		"project_id in upper case": {
			body: `project_id = "6F1C1F5E-8A8B-4C43-9A0E-3F0F6D0C8B11"`,
			want: "lowercase with hyphens",
		},
		"project_id without hyphens": {
			body: `project_id = "6f1c1f5e8a8b4c439a0e3f0f6d0c8b11"`,
			want: "lowercase with hyphens",
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
resource "mergegateway_api_key" "test" {
  ` + tt.body + `
}`,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tt.want),
				}},
			})
		})
	}
}

// TestAPIKeyResourceValidateConfigAllowsWildcards plans models with a trailing
// "*", which the API accepts.
func TestAPIKeyResourceValidateConfigAllowsWildcards(t *testing.T) {
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
resource "mergegateway_api_key" "test" {
  allowed_models = ["openai/*", "anthropic/claude-opus-*", "openai/gpt-4o"]
}`,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		}},
	})
}
