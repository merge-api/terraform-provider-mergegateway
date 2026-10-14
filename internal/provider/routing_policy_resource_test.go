package provider

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestRoutingPolicyResourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &resource.SchemaResponse{}
	NewRoutingPolicyResource().Schema(ctx, resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

// testRoutingPolicy is a PRIORITY routing policy as state holds it after create.
func testRoutingPolicy(models ...string) routingPolicyResourceModel {
	return routingPolicyResourceModel{
		ID:               types.StringValue("3d7c2a10-5b4e-4f6a-9c1d-2e8f7a6b5c44"),
		Name:             types.StringValue("tf-acc-policy"),
		Description:      types.StringValue("A policy"),
		Slug:             types.StringValue("tf-acc-policy"),
		Strategy:         types.StringValue("PRIORITY"),
		Axis:             types.StringNull(),
		AllowedModels:    types.ListNull(types.StringType),
		PriorityOrder:    testPriorityOrder(models...),
		AllowedProviders: types.ListNull(types.StringType),
		Model:            types.StringValue("@alias/tf-acc-policy"),
		IsDefault:        types.BoolValue(false),
		CreatedAt:        types.StringValue("2026-10-01T12:00:00Z"),
		ModifiedAt:       types.StringValue("2026-10-01T12:00:00Z"),
	}
}

// testPriorityOrder lists the models with priorities 1, 2, ...
func testPriorityOrder(models ...string) types.Set {
	entries := []attr.Value{}
	for i, m := range models {
		entries = append(entries, types.ObjectValueMust(priorityOrderAttrTypes, map[string]attr.Value{
			"model":    types.StringValue(m),
			"priority": types.Int64Value(int64(i + 1)),
		}))
	}
	return types.SetValueMust(types.ObjectType{AttrTypes: priorityOrderAttrTypes}, entries)
}

func testStringList(values ...string) types.List {
	elems := []attr.Value{}
	for _, v := range values {
		elems = append(elems, types.StringValue(v))
	}
	return types.ListValueMust(types.StringType, elems)
}

// intelligent turns m into an INTELLIGENT policy over the given models.
func intelligent(m *routingPolicyResourceModel, axis types.String, models ...string) {
	m.Strategy = types.StringValue("INTELLIGENT")
	m.Axis = axis
	m.AllowedModels = testStringList(models...)
	m.PriorityOrder = types.SetNull(types.ObjectType{AttrTypes: priorityOrderAttrTypes})
}

func TestRoutingPolicyCreateRequest(t *testing.T) {
	tests := map[string]struct {
		plan func(m *routingPolicyResourceModel)
		want string
	}{
		"priority policy": {
			plan: func(m *routingPolicyResourceModel) {},
			want: `{"name": "tf-acc-policy", "description": "A policy", "slug": "tf-acc-policy", "strategy": "PRIORITY",
				"priority_order": [{"model": "openai/gpt-4o", "priority": 1}]}`,
		},
		"slug and description left out": {
			plan: func(m *routingPolicyResourceModel) {
				m.Description = types.StringNull()
				m.Slug = types.StringUnknown()
			},
			want: `{"name": "tf-acc-policy", "strategy": "PRIORITY", "priority_order": [{"model": "openai/gpt-4o", "priority": 1}]}`,
		},
		"intelligent policy without an axis": {
			plan: func(m *routingPolicyResourceModel) {
				intelligent(m, types.StringNull(), "openai/gpt-4o", "openai/gpt-4o-mini")
			},
			want: `{"name": "tf-acc-policy", "description": "A policy", "slug": "tf-acc-policy", "strategy": "INTELLIGENT",
				"allowed_models": ["openai/gpt-4o", "openai/gpt-4o-mini"]}`,
		},
		"intelligent policy with an axis": {
			plan: func(m *routingPolicyResourceModel) { intelligent(m, types.StringValue("PERFORMANCE"), "openai/gpt-4o") },
			want: `{"name": "tf-acc-policy", "description": "A policy", "slug": "tf-acc-policy", "strategy": "INTELLIGENT",
				"axis": "PERFORMANCE", "allowed_models": ["openai/gpt-4o"]}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan := testRoutingPolicy("openai/gpt-4o")
			tt.plan(&plan)
			body, diags := routingPolicyCreateRequest(t.Context(), plan)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

func TestRoutingPolicyUpdateRequest(t *testing.T) {
	tests := map[string]struct {
		state func(m *routingPolicyResourceModel)
		plan  func(m *routingPolicyResourceModel)
		want  string
	}{
		"no change sends nothing": {
			plan: func(m *routingPolicyResourceModel) {},
			want: `{}`,
		},
		"computed fields unknown in the plan send nothing": {
			plan: func(m *routingPolicyResourceModel) {
				m.Model = types.StringUnknown()
				m.ModifiedAt = types.StringUnknown()
				m.AllowedProviders = types.ListUnknown(types.StringType)
			},
			want: `{}`,
		},
		"rename sends only the name": {
			plan: func(m *routingPolicyResourceModel) { m.Name = types.StringValue("tf-acc-renamed") },
			want: `{"name": "tf-acc-renamed"}`,
		},
		"removing the description clears it with an empty string": {
			plan: func(m *routingPolicyResourceModel) { m.Description = types.StringNull() },
			want: `{"description": ""}`,
		},
		"rename the slug": {
			plan: func(m *routingPolicyResourceModel) { m.Slug = types.StringValue("tf-acc-new-slug") },
			want: `{"slug": "tf-acc-new-slug"}`,
		},
		"slug left out keeps the current one": {
			plan: func(m *routingPolicyResourceModel) { m.Slug = types.StringUnknown() },
			want: `{}`,
		},
		"priority_order change sends the strategy with it": {
			plan: func(m *routingPolicyResourceModel) {
				m.PriorityOrder = testPriorityOrder("openai/gpt-4o-mini", "openai/gpt-4o")
			},
			want: `{"strategy": "PRIORITY", "priority_order": [
				{"model": "openai/gpt-4o-mini", "priority": 1}, {"model": "openai/gpt-4o", "priority": 2}]}`,
		},
		"switch to intelligent": {
			plan: func(m *routingPolicyResourceModel) { intelligent(m, types.StringValue("CAPABILITY"), "openai/gpt-4o") },
			want: `{"strategy": "INTELLIGENT", "axis": "CAPABILITY", "allowed_models": ["openai/gpt-4o"]}`,
		},
		"axis change resends allowed_models": {
			state: func(m *routingPolicyResourceModel) { intelligent(m, types.StringValue("PERFORMANCE"), "openai/gpt-4o") },
			plan:  func(m *routingPolicyResourceModel) { intelligent(m, types.StringNull(), "openai/gpt-4o") },
			want:  `{"strategy": "INTELLIGENT", "allowed_models": ["openai/gpt-4o"]}`,
		},
		"switch back to priority": {
			state: func(m *routingPolicyResourceModel) { intelligent(m, types.StringNull(), "openai/gpt-4o") },
			plan:  func(m *routingPolicyResourceModel) {},
			want:  `{"strategy": "PRIORITY", "priority_order": [{"model": "openai/gpt-4o", "priority": 1}]}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			state, plan := testRoutingPolicy("openai/gpt-4o"), testRoutingPolicy("openai/gpt-4o")
			if tt.state != nil {
				tt.state(&state)
			}
			tt.plan(&plan)
			body, diags := routingPolicyUpdateRequest(t.Context(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			assertJSON(t, body, tt.want)
		})
	}
}

// testRoutingPolicyResponse decodes an API routing policy response; extra
// overrides fields of a PRIORITY policy.
func testRoutingPolicyResponse(t *testing.T, extra string) *client.RoutingPolicyV1Response {
	t.Helper()
	body := `{
		"id": "3d7c2a10-5b4e-4f6a-9c1d-2e8f7a6b5c44",
		"name": "tf-acc-policy",
		"description": "A policy",
		"strategy": "PRIORITY",
		"allowed_providers": null,
		"allowed_models": null,
		"priority_order": [{"model": "openai/gpt-4o", "priority": 1}],
		"axis": null,
		"slug": "tf-acc-policy",
		"model": "@alias/tf-acc-policy",
		"is_default": false,
		"created_at": "2026-10-01T12:00:00Z",
		"modified_at": "2026-10-01T12:00:00.123456Z"` + extra + `}`
	var p client.RoutingPolicyV1Response
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &p
}

func TestRoutingPolicyResourceFromAPI(t *testing.T) {
	ctx := t.Context()

	t.Run("priority policy", func(t *testing.T) {
		got, diags := routingPolicyResourceFromAPI(ctx, testRoutingPolicyResponse(t, ``), routingPolicyResourceModel{})
		if diags.HasError() {
			t.Fatalf("unexpected error: %v", diags)
		}
		if want := testRoutingPolicy("openai/gpt-4o"); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})

	t.Run("cleared description and empty lists read back as null", func(t *testing.T) {
		got, _ := routingPolicyResourceFromAPI(ctx, testRoutingPolicyResponse(t,
			`, "description": "", "allowed_models": [], "slug": null, "model": null, "is_default": true`), routingPolicyResourceModel{})
		if !got.Description.IsNull() || !got.AllowedModels.IsNull() || !got.Slug.IsNull() || !got.Model.IsNull() {
			t.Fatalf("expected nulls: %+v", got)
		}
		if !got.IsDefault.ValueBool() {
			t.Fatal("is_default should be true")
		}
	})

	intelligentResponse := `, "strategy": "INTELLIGENT", "axis": "COST", "priority_order": null,
		"allowed_models": ["openai/gpt-4o"], "allowed_providers": ["openai"]`

	t.Run("default axis stays unset", func(t *testing.T) {
		got, _ := routingPolicyResourceFromAPI(ctx, testRoutingPolicyResponse(t, intelligentResponse), routingPolicyResourceModel{Axis: types.StringNull()})
		if !got.Axis.IsNull() {
			t.Fatalf("axis = %v, want null", got.Axis)
		}
		if !got.AllowedModels.Equal(testStringList("openai/gpt-4o")) || !got.AllowedProviders.Equal(testStringList("openai")) || !got.PriorityOrder.IsNull() {
			t.Fatalf("routing fields: %+v", got)
		}
	})

	t.Run("configured axis is kept", func(t *testing.T) {
		got, _ := routingPolicyResourceFromAPI(ctx, testRoutingPolicyResponse(t, intelligentResponse), routingPolicyResourceModel{Axis: types.StringValue("COST")})
		if got.Axis.ValueString() != "COST" {
			t.Fatalf("axis = %v, want COST", got.Axis)
		}
	})
}

func TestCheckSlug(t *testing.T) {
	withSlug := func(slug types.String) routingPolicyResourceModel {
		m := testRoutingPolicy("openai/gpt-4o")
		m.Slug = slug
		return m
	}
	tests := map[string]struct {
		planned, got types.String
		want         string
	}{
		"slug used as configured": {types.StringValue("team"), types.StringValue("team"), ""},
		"slug left out":           {types.StringUnknown(), types.StringValue("tf-acc-policy"), ""},
		"taken slug got a suffix": {types.StringValue("team"), types.StringValue("team-2"), `created with the slug "team-2"`},
		"no slug at the alias limit": {
			types.StringValue("team"), types.StringNull(), "created without a slug",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			diags := checkSlug(withSlug(tt.planned), withSlug(tt.got))
			if tt.want == "" {
				if diags.HasError() {
					t.Fatalf("unexpected error: %v", diags)
				}
				return
			}
			if !diags.HasError() || !strings.Contains(diags[0].Detail(), tt.want) {
				t.Fatalf("expected an error containing %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestCheckDroppedModels(t *testing.T) {
	attrPath := path.Root("allowed_models")
	if diags := checkDroppedModels(attrPath, testStringList("a", "b"), testStringList("a", "b")); diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if diags := checkDroppedModels(attrPath, types.ListNull(types.StringType), types.ListNull(types.StringType)); diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	diags := checkDroppedModels(attrPath, testStringList("a", "b"), testStringList("a"))
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), ": b. Remove them from allowed_models.") {
		t.Fatalf("expected an error naming b, got %v", diags)
	}
}

// TestValidateRoutingPolicyUnknown checks that values not known at validate
// time, such as ones computed from another resource, aren't reported as set.
func TestValidateRoutingPolicyUnknown(t *testing.T) {
	priorityType := types.ObjectType{AttrTypes: priorityOrderAttrTypes}
	diags := validateRoutingPolicy(t.Context(), path.Empty(), types.StringValue("PRIORITY"),
		types.StringUnknown(), types.ListUnknown(types.StringType), testPriorityOrder("openai/gpt-4o"))
	if diags.HasError() {
		t.Fatalf("PRIORITY with unknown axis and allowed_models: %v", diags)
	}
	diags = validateRoutingPolicy(t.Context(), path.Empty(), types.StringValue("INTELLIGENT"),
		types.StringNull(), testStringList("openai/gpt-4o"), types.SetUnknown(priorityType))
	if diags.HasError() {
		t.Fatalf("INTELLIGENT with unknown priority_order: %v", diags)
	}
}

// TestRoutingPolicyResourceValidateConfig plans configs the API would reject:
// they must fail at plan time, before any API call.
func TestRoutingPolicyResourceValidateConfig(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"PRIORITY with allowed_models": {
			body: `strategy       = "PRIORITY"
  priority_order = [{ model = "openai/gpt-4o", priority = 1 }]
  allowed_models = ["openai/gpt-4o"]`,
			want: "allowed_models applies only to INTELLIGENT",
		},
		"PRIORITY with axis": {
			body: `strategy       = "PRIORITY"
  priority_order = [{ model = "openai/gpt-4o", priority = 1 }]
  axis           = "COST"`,
			want: "axis applies only to INTELLIGENT",
		},
		"PRIORITY without priority_order": {
			body: `strategy = "PRIORITY"`,
			want: "needs priority_order",
		},
		"INTELLIGENT with priority_order": {
			body: `strategy       = "INTELLIGENT"
  priority_order = [{ model = "openai/gpt-4o", priority = 1 }]`,
			want: "priority_order applies only to PRIORITY",
		},
		"model listed twice in priority_order": {
			body: `strategy = "PRIORITY"
  priority_order = [
    { model = "openai/gpt-4o", priority = 1 },
    { model = "openai/gpt-4o", priority = 2 },
  ]`,
			want: "Duplicate model in priority_order",
		},
		"model listed twice in allowed_models": {
			body: `strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o", "openai/gpt-4o"]`,
			want: "duplicate values",
		},
		"empty description": {
			body: `strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o"]
  description    = ""`,
			want: "description",
		},
		"INTELLIGENT without allowed_models": {
			body: `strategy = "INTELLIGENT"`,
			want: "needs allowed_models",
		},
		"uppercase slug": {
			body: `strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o"]
  slug           = "MyTeam"`,
			want: "must be 3 to 64 lowercase letters",
		},
		"slug too short": {
			body: `strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o"]
  slug           = "ab"`,
			want: "must be 3 to 64 lowercase letters",
		},
		"slug with the alias prefix": {
			body: `strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o"]
  slug           = "@alias/team"`,
			want: "must be 3 to 64 lowercase letters",
		},
		"reserved slug": {
			body: `strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o"]
  slug           = "default_routing"`,
			want: "default_routing",
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
resource "mergegateway_routing_policy" "test" {
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
