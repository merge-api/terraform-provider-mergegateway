package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const routingPolicyAddress = "mergegateway_routing_policy.test"

// routingPolicyConfig renders a mergegateway_routing_policy with the given name and body.
func routingPolicyConfig(name, body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_routing_policy" "test" {
  name = %q
%s
}
`, name, body)
}

// expectRoutingPolicy checks a routing policy attribute in state.
func expectRoutingPolicy(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(routingPolicyAddress, tfjsonpath.New(path), value)
}

// TestAccRoutingPolicyResource walks one routing policy through create, a
// no-op re-plan, in-place updates of every field, replace, and imports along
// the way, then checks destroy.
func TestAccRoutingPolicyResource(t *testing.T) {
	name := randomName()
	renamed := name + "-renamed"
	slug := name
	newSlug := name + "-v2"
	// The ID stays the same through updates, and changes when the policy is replaced.
	sameID := statecheck.CompareValue(compare.ValuesSame())
	replacedID := statecheck.CompareValue(compare.ValuesDiffer())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_routing_policy", "/v1/routing-policies"),
		Steps: []resource.TestStep{
			// Create a PRIORITY policy. priority_order is a set, so the
			// API's sorted order must not show a diff on the next plan.
			{
				Config: routingPolicyConfig(name, fmt.Sprintf(`
  description = "Created by the provider's acceptance tests"
  slug        = %q
  strategy    = "PRIORITY"
  priority_order = [
    { model = "openai/gpt-4o-mini", priority = 2 },
    { model = "openai/gpt-4o", priority = 1 },
  ]
`, slug)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(routingPolicyAddress, tfjsonpath.New("id")),
					expectRoutingPolicy("name", knownvalue.StringExact(name)),
					expectRoutingPolicy("description", knownvalue.StringExact("Created by the provider's acceptance tests")),
					expectRoutingPolicy("slug", knownvalue.StringExact(slug)),
					expectRoutingPolicy("model", knownvalue.StringExact("@alias/"+slug)),
					expectRoutingPolicy("is_default", knownvalue.NotNull()),
					expectRoutingPolicy("created_at", knownvalue.NotNull()),
					expectRoutingPolicy("axis", knownvalue.Null()),
					expectRoutingPolicy("allowed_models", knownvalue.Null()),
					expectRoutingPolicy("priority_order", knownvalue.SetExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{"model": knownvalue.StringExact("openai/gpt-4o"), "priority": knownvalue.Int64Exact(1)}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{"model": knownvalue.StringExact("openai/gpt-4o-mini"), "priority": knownvalue.Int64Exact(2)}),
					})),
				},
			},
			// Rename, change the description, the slug and the priority order.
			{
				Config: routingPolicyConfig(renamed, fmt.Sprintf(`
  description = "Updated by the provider's acceptance tests"
  slug        = %q
  strategy    = "PRIORITY"
  priority_order = [
    { model = "openai/gpt-4o-mini", priority = 1 },
    { model = "openai/gpt-4o", priority = 2 },
  ]
`, newSlug)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(routingPolicyAddress, tfjsonpath.New("id")),
					expectRoutingPolicy("name", knownvalue.StringExact(renamed)),
					expectRoutingPolicy("description", knownvalue.StringExact("Updated by the provider's acceptance tests")),
					expectRoutingPolicy("slug", knownvalue.StringExact(newSlug)),
					expectRoutingPolicy("model", knownvalue.StringExact("@alias/"+newSlug)),
					expectRoutingPolicy("priority_order", knownvalue.SetExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{"model": knownvalue.StringExact("openai/gpt-4o-mini"), "priority": knownvalue.Int64Exact(1)}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{"model": knownvalue.StringExact("openai/gpt-4o"), "priority": knownvalue.Int64Exact(2)}),
					})),
				},
			},
			// Import a PRIORITY policy.
			{
				ResourceName:      routingPolicyAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Switch to INTELLIGENT and remove the description. Leaving the
			// slug out keeps the current one.
			{
				Config: routingPolicyConfig(renamed, `
  strategy       = "INTELLIGENT"
  axis           = "PERFORMANCE"
  allowed_models = ["openai/gpt-4o", "openai/gpt-4o-mini"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(routingPolicyAddress, tfjsonpath.New("id")),
					expectRoutingPolicy("description", knownvalue.Null()),
					expectRoutingPolicy("slug", knownvalue.StringExact(newSlug)),
					expectRoutingPolicy("strategy", knownvalue.StringExact("INTELLIGENT")),
					expectRoutingPolicy("axis", knownvalue.StringExact("PERFORMANCE")),
					expectRoutingPolicy("allowed_models", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("openai/gpt-4o"), knownvalue.StringExact("openai/gpt-4o-mini"),
					})),
					expectRoutingPolicy("allowed_providers", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("openai")})),
					expectRoutingPolicy("priority_order", knownvalue.Null()),
				},
			},
			// Drop the axis (the API's default, COST, must not show a diff)
			// and change allowed_models.
			{
				Config: routingPolicyConfig(renamed, `
  strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o-mini"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(routingPolicyAddress, tfjsonpath.New("id")),
					replacedID.AddStateValue(routingPolicyAddress, tfjsonpath.New("id")),
					expectRoutingPolicy("axis", knownvalue.Null()),
					expectRoutingPolicy("allowed_models", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("openai/gpt-4o-mini")})),
				},
			},
			// Import an INTELLIGENT policy.
			{
				ResourceName:      routingPolicyAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Replace: a tainted policy is destroyed and created again with a new ID.
			{
				Config: routingPolicyConfig(renamed, `
  strategy       = "INTELLIGENT"
  allowed_models = ["openai/gpt-4o-mini"]
`),
				Taint: []string{routingPolicyAddress},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					replacedID.AddStateValue(routingPolicyAddress, tfjsonpath.New("id")),
				},
			},
			// Import by routing policy ID.
			{
				ResourceName:      routingPolicyAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccRoutingPolicyResource_drift creates a policy without a slug, then
// changes it behind Terraform's back: a rename or a new strategy must plan an
// update that restores it, and a deletion must plan a create rather than fail.
func TestAccRoutingPolicyResource_drift(t *testing.T) {
	name := randomName()
	body := `
  strategy       = "PRIORITY"
  priority_order = [{ model = "openai/gpt-4o", priority = 1 }]
`
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_routing_policy", "/v1/routing-policies"),
		Steps: []resource.TestStep{
			{
				Config: routingPolicyConfig(name, body),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[routingPolicyAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() { patchRoutingPolicy(t, id, map[string]any{"name": name + "-drifted"}) },
				Config:    routingPolicyConfig(name, body),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectRoutingPolicy("name", knownvalue.StringExact(name)),
				},
			},
			{
				PreConfig: func() {
					patchRoutingPolicy(t, id, map[string]any{"strategy": "INTELLIGENT", "allowed_models": []string{"openai/gpt-4o"}})
				},
				Config: routingPolicyConfig(name, body),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectRoutingPolicy("strategy", knownvalue.StringExact("PRIORITY")),
					expectRoutingPolicy("allowed_models", knownvalue.Null()),
				},
			},
			{
				PreConfig: func() { deleteRoutingPolicy(t, id) },
				Config:    routingPolicyConfig(name, body),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(routingPolicyAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

// patchRoutingPolicy changes a routing policy through the API, outside Terraform.
func patchRoutingPolicy(t *testing.T, id string, fields map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := apiRequest(ctx, http.MethodPatch, "/v1/routing-policies/"+id, body)
	if err != nil {
		t.Fatalf("updating routing policy %s: %v", id, err)
	}
	if status != http.StatusOK {
		t.Fatalf("updating routing policy %s: HTTP %d", id, status)
	}
}

// deleteRoutingPolicy deletes a routing policy through the API, outside Terraform.
func deleteRoutingPolicy(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodDelete, "/v1/routing-policies/"+id, nil)
	if err != nil {
		t.Fatalf("deleting routing policy %s: %v", id, err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("deleting routing policy %s: HTTP %d", id, status)
	}
}
