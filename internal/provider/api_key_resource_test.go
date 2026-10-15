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
		UpdatedAt:       types.StringValue("2026-10-02T08:30:00Z"),
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
			plan: func(m *apiKeyResourceModel) { m.Name = types.StringUnknown() },
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
		"expires_at is sent": {
			plan: func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-01-01T01:00:00+01:00") },
			want: `{"name": "tf-acc-key", "limit_reset": "monthly", "disabled": false, "expires_at": "2027-01-01T01:00:00+01:00"}`,
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
		"removing limit clears it": {
			state: full,
			plan: func(m *apiKeyResourceModel) {
				full(m)
				m.Limit = types.Float64Null()
			},
			want: `{"limit": null}`,
		},
		"setting a limit again sends its window": {
			plan: func(m *apiKeyResourceModel) { m.Limit = types.Float64Value(10) },
			want: `{"limit": 10, "limit_reset": "monthly"}`,
		},
		"name left out keeps the state's": {
			plan: func(m *apiKeyResourceModel) { m.Name = types.StringUnknown() },
			want: `{}`,
		},
		"name is never sent as null": {
			plan: func(m *apiKeyResourceModel) { m.Name = types.StringNull() },
			want: `{}`,
		},
		"setting expires_at sends it": {
			plan: func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-01-01T00:00:00Z") },
			want: `{"expires_at": "2027-01-01T00:00:00Z"}`,
		},
		"changing expires_at sends it": {
			state: func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-01-01T00:00:00Z") },
			plan:  func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-06-30T12:00:00Z") },
			want:  `{"expires_at": "2027-06-30T12:00:00Z"}`,
		},
		"removing expires_at sends null": {
			state: func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-01-01T00:00:00Z") },
			plan:  func(m *apiKeyResourceModel) {},
			want:  `{"expires_at": null}`,
		},
		// The API refuses a past expiry, even an unchanged one.
		"an unchanged, past expires_at is not sent": {
			state: func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2026-01-01T00:00:00Z") },
			plan: func(m *apiKeyResourceModel) {
				m.ExpiresAt = types.StringValue("2026-01-01T00:00:00Z")
				m.Disabled = types.BoolValue(true)
			},
			want: `{"disabled": true}`,
		},
		"the same expires_at in another offset is not sent": {
			state: func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-01-01T00:00:00Z") },
			plan:  func(m *apiKeyResourceModel) { m.ExpiresAt = types.StringValue("2027-01-01T01:00:00+01:00") },
			want:  `{}`,
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

// testKeyResponse decodes an API key object with the given extra fields.
func testKeyResponse(t *testing.T, extra string) *client.KeyObject {
	t.Helper()
	body := `{
		"hash": "3b4c5d", "label": "sk-test", "name": "tf-acc-key", "disabled": false,
		"limit": null, "limit_remaining": null, "limit_reset": null, "allowed_models": null,
		"project_id": null, "routing_policy_id": null, "budget_group_id": null,
		"usage": 0, "usage_daily": 0, "usage_weekly": 0, "usage_monthly": 0,
		"byok_usage": 0, "byok_usage_daily": 0, "byok_usage_weekly": 0, "byok_usage_monthly": 0,
		"creator_user_id": null, "expires_at": null, "created_at": "2026-10-01T12:00:00.123456Z",
		"updated_at": "2026-10-02T08:30:00.654321Z"` + extra + `}`
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

	// Removing a limit clears its window: the prior window stays, so a config
	// that keeps limit_reset converges.
	t.Run("no limit keeps the prior limit_reset", func(t *testing.T) {
		prior := testAPIKey()
		prior.LimitReset = types.StringValue("daily")
		got, _ := apiKeyFromAPI(ctx, testKeyResponse(t, ``), prior)
		if want := types.StringValue("daily"); !got.LimitReset.Equal(want) {
			t.Fatalf("limit_reset = %v, want %v", got.LimitReset, want)
		}
	})

	// The API returns the expiry in UTC: the configured text stays when it's
	// the same time, so the next plan is empty.
	t.Run("same expires_at in another offset keeps the prior text", func(t *testing.T) {
		prior := testAPIKey()
		prior.ExpiresAt = types.StringValue("2027-01-01T01:00:00+01:00")
		got, _ := apiKeyFromAPI(ctx, testKeyResponse(t, `, "expires_at": "2027-01-01T00:00:00+00:00"`), prior)
		if !got.ExpiresAt.Equal(prior.ExpiresAt) {
			t.Fatalf("expires_at = %v, want %v", got.ExpiresAt, prior.ExpiresAt)
		}
	})

	t.Run("expires_at keeps fractions of a second", func(t *testing.T) {
		prior := testAPIKey()
		prior.ExpiresAt = types.StringValue("2027-01-01T01:00:00.5+01:00")
		got, _ := apiKeyFromAPI(ctx, testKeyResponse(t, `, "expires_at": "2027-01-01T00:00:00.500000+00:00"`), prior)
		if !got.ExpiresAt.Equal(prior.ExpiresAt) {
			t.Fatalf("expires_at = %v, want %v", got.ExpiresAt, prior.ExpiresAt)
		}
	})

	t.Run("changed expires_at reads the API's", func(t *testing.T) {
		prior := testAPIKey()
		prior.ExpiresAt = types.StringValue("2027-01-01T00:00:00Z")
		got, _ := apiKeyFromAPI(ctx, testKeyResponse(t, `, "expires_at": "2027-06-30T12:00:00+00:00"`), prior)
		if want := types.StringValue("2027-06-30T12:00:00Z"); !got.ExpiresAt.Equal(want) {
			t.Fatalf("expires_at = %v, want %v", got.ExpiresAt, want)
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
		"expires_at without a time": {
			body: `expires_at = "2027-01-01"`,
			want: "Expected an RFC 3339 timestamp",
		},
		"expires_at without an offset": {
			body: `expires_at = "2027-01-01T00:00:00"`,
			want: "Expected an RFC 3339 timestamp",
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
