// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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

const apiKeyAddress = "mergegateway_api_key.test"

// apiKeyConfig renders a mergegateway_api_key with the given body, next to two
// projects for it to be scoped to: "a", which has a routing policy, and "b".
func apiKeyConfig(name, body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_project" "a" {
  name = "%[1]s-a"
  routing_policy = {
    strategy       = "PRIORITY"
    priority_order = [{ model = "openai/gpt-4o", priority = 1 }]
  }
}

resource "mergegateway_project" "b" {
  name = "%[1]s-b"
}

resource "mergegateway_api_key" "test" {
%[2]s
}
`, name, body)
}

// expectAPIKey checks an API key attribute in state.
func expectAPIKey(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(apiKeyAddress, tfjsonpath.New(path), value)
}

func checkAPIKeyDestroy() resource.TestCheckFunc {
	return resource.ComposeAggregateTestCheckFunc(
		checkDestroy("mergegateway_api_key", "/v1/keys"),
		checkDestroy("mergegateway_project", "/v1/projects"),
	)
}

// TestAccAPIKeyResource walks one key through create, a no-op re-plan, in-place
// updates of every field, clearing the ones the API can clear, replace (new
// project), removing name (which keeps it), a plan-time error for removing
// limit, and imports along the way, then checks destroy.
func TestAccAPIKeyResource(t *testing.T) {
	name := randomName()
	renamed := name + "-renamed"
	// The ID and raw key stay the same through updates, and change when the key is replaced.
	sameID := statecheck.CompareValue(compare.ValuesSame())
	sameKey := statecheck.CompareValue(compare.ValuesSame())
	replacedID := statecheck.CompareValue(compare.ValuesDiffer())
	replacedKey := statecheck.CompareValue(compare.ValuesDiffer())
	// The raw key isn't returned after create, so an import can't recover it.
	importStep := resource.TestStep{
		ResourceName:            apiKeyAddress,
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: []string{"key"},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPIKeyDestroy(),
		Steps: []resource.TestStep{
			// Create a key scoped to project a. allowed_models is unsorted: as a
			// set, it must not show a diff on the next plan.
			{
				Config: apiKeyConfig(name, fmt.Sprintf(`
  name           = %q
  project_id     = mergegateway_project.a.id
  limit          = 10
  limit_reset    = "weekly"
  allowed_models = ["openai/gpt-4o-mini", "openai/gpt-4o"]
`, name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					sameKey.AddStateValue(apiKeyAddress, tfjsonpath.New("key")),
					replacedID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					replacedKey.AddStateValue(apiKeyAddress, tfjsonpath.New("key")),
					statecheck.CompareValuePairs(apiKeyAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame()),
					expectAPIKey("key", knownvalue.NotNull()),
					expectAPIKey("label", knownvalue.NotNull()),
					expectAPIKey("name", knownvalue.StringExact(name)),
					expectAPIKey("limit", knownvalue.Float64Exact(10)),
					expectAPIKey("limit_reset", knownvalue.StringExact("weekly")),
					expectAPIKey("limit_remaining", knownvalue.NotNull()),
					expectAPIKey("usage", knownvalue.NotNull()),
					expectAPIKey("allowed_models", knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("openai/gpt-4o"), knownvalue.StringExact("openai/gpt-4o-mini"),
					})),
					expectAPIKey("disabled", knownvalue.Bool(false)),
					expectAPIKey("routing_policy_id", knownvalue.Null()),
					expectAPIKey("created_at", knownvalue.NotNull()),
					expectAPIKey("creator_user_id", knownvalue.Null()),
					expectAPIKey("expires_at", knownvalue.Null()),
				},
			},
			// Rename, change the limit, its window and the models, disable the
			// key and bind it to project a's routing policy.
			{
				Config: apiKeyConfig(name, fmt.Sprintf(`
  name              = %q
  project_id        = mergegateway_project.a.id
  limit             = 25.5
  limit_reset       = "daily"
  allowed_models    = ["openai/gpt-4o"]
  disabled          = true
  routing_policy_id = mergegateway_project.a.routing_policy.id
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					sameKey.AddStateValue(apiKeyAddress, tfjsonpath.New("key")),
					expectAPIKey("name", knownvalue.StringExact(renamed)),
					expectAPIKey("limit", knownvalue.Float64Exact(25.5)),
					expectAPIKey("limit_reset", knownvalue.StringExact("daily")),
					expectAPIKey("allowed_models", knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("openai/gpt-4o")})),
					expectAPIKey("disabled", knownvalue.Bool(true)),
					statecheck.CompareValuePairs(apiKeyAddress, tfjsonpath.New("routing_policy_id"),
						"mergegateway_project.a", tfjsonpath.New("routing_policy").AtMapKey("id"), compare.ValuesSame()),
				},
			},
			// Import with every field set.
			importStep,
			// Clear allowed_models and routing_policy_id, and re-enable the key.
			{
				Config: apiKeyConfig(name, fmt.Sprintf(`
  name       = %q
  project_id = mergegateway_project.a.id
  limit      = 25.5
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					sameKey.AddStateValue(apiKeyAddress, tfjsonpath.New("key")),
					expectAPIKey("name", knownvalue.StringExact(renamed)),
					expectAPIKey("limit", knownvalue.Float64Exact(25.5)),
					expectAPIKey("limit_reset", knownvalue.StringExact("monthly")),
					expectAPIKey("allowed_models", knownvalue.Null()),
					expectAPIKey("disabled", knownvalue.Bool(false)),
					expectAPIKey("routing_policy_id", knownvalue.Null()),
				},
			},
			// Import with the optional fields cleared.
			importStep,
			// Replace: moving the key to project b creates a new key.
			{
				Config: apiKeyConfig(name, fmt.Sprintf(`
  name       = %q
  project_id = mergegateway_project.b.id
  limit      = 25.5
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					replacedID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					replacedKey.AddStateValue(apiKeyAddress, tfjsonpath.New("key")),
					expectAPIKey("key", knownvalue.NotNull()),
					statecheck.CompareValuePairs(apiKeyAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.b", tfjsonpath.New("id"), compare.ValuesSame()),
				},
			},
			// The API can't clear limit, so removing it fails at plan time
			// rather than replacing the key.
			{
				Config: apiKeyConfig(name, fmt.Sprintf(`
  name       = %q
  project_id = mergegateway_project.b.id
`, renamed)),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Can't remove limit`),
			},
			// Nor can it clear name: removing it keeps the current name, also
			// through an update of another field.
			{
				Config: apiKeyConfig(name, `
  project_id = mergegateway_project.b.id
  limit      = 25.5
  disabled   = true
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(apiKeyAddress, tfjsonpath.New("name"), knownvalue.StringExact(renamed)),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectAPIKey("name", knownvalue.StringExact(renamed)),
					expectAPIKey("disabled", knownvalue.Bool(true)),
				},
			},
			// Import by key hash.
			importStep,
		},
	})
}

// TestAccAPIKeyResource_drift changes an organization-wide key behind
// Terraform's back: a rename must plan an update that restores the name, and
// a deletion must plan a create rather than fail.
func TestAccAPIKeyResource_drift(t *testing.T) {
	name := randomName()
	config := providerConfig() + fmt.Sprintf(`
resource "mergegateway_api_key" "test" {
  name = %q
}
`, name)
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_api_key", "/v1/keys"),
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					expectAPIKey("project_id", knownvalue.Null()),
				},
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[apiKeyAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() { renameAPIKey(t, id, name+"-drifted") },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectAPIKey("name", knownvalue.StringExact(name)),
				},
			},
			{
				PreConfig: func() { deleteAPIKey(t, id) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

// renameAPIKey renames a key through the API, outside Terraform.
func renameAPIKey(t *testing.T, id, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := apiRequest(ctx, http.MethodPatch, "/v1/keys/"+id, body)
	if err != nil {
		t.Fatalf("renaming API key %s: %v", id, err)
	}
	if status != http.StatusOK {
		t.Fatalf("renaming API key %s: HTTP %d", id, status)
	}
}

// deleteAPIKey deletes a key through the API, outside Terraform.
func deleteAPIKey(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodDelete, "/v1/keys/"+id, nil)
	if err != nil {
		t.Fatalf("deleting API key %s: %v", id, err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("deleting API key %s: HTTP %d", id, status)
	}
}
