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
// project), removing limit, removing name (which keeps it), and imports along
// the way, then checks destroy.
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
					expectAPIKey("updated_at", knownvalue.NotNull()),
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
  name        = %q
  project_id  = mergegateway_project.b.id
  limit       = 25.5
  limit_reset = "daily"
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
			// Removing limit removes the key's cap in place. The API also clears
			// the window, but limit_reset keeps its configured value.
			{
				Config: apiKeyConfig(name, fmt.Sprintf(`
  name        = %q
  project_id  = mergegateway_project.b.id
  limit_reset = "daily"
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(apiKeyAddress, tfjsonpath.New("limit"), knownvalue.Null()),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectAPIKey("limit", knownvalue.Null()),
					expectAPIKey("limit_remaining", knownvalue.Null()),
					expectAPIKey("limit_reset", knownvalue.StringExact("daily")),
				},
				Check: checkAPIKeyNull("limit"),
			},
			// The API can't clear name: removing it keeps the current name, also
			// through an update of another field. Setting limit again sends its
			// window too, so it reads back as daily.
			{
				Config: apiKeyConfig(name, `
  project_id  = mergegateway_project.b.id
  limit       = 25.5
  limit_reset = "daily"
  disabled    = true
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
					expectAPIKey("limit", knownvalue.Float64Exact(25.5)),
					expectAPIKey("limit_reset", knownvalue.StringExact("daily")),
					expectAPIKey("disabled", knownvalue.Bool(true)),
				},
			},
			// With neither limit nor limit_reset, the key has no cap and the
			// next plan is empty.
			{
				Config: apiKeyConfig(name, `
  project_id = mergegateway_project.b.id
  disabled   = true
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(apiKeyAddress, tfjsonpath.New("limit"), knownvalue.Null()),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectAPIKey("limit", knownvalue.Null()),
					expectAPIKey("limit_reset", knownvalue.StringExact("monthly")),
				},
				Check: checkAPIKeyNull("limit"),
			},
			// Import by key hash.
			importStep,
		},
	})
}

// TestAccAPIKeyResource_expiresAt sets a key's expiry in a non-UTC offset,
// changes it in place, imports it and removes it, with an empty plan after
// each step.
func TestAccAPIKeyResource_expiresAt(t *testing.T) {
	name := randomName()
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	// The same time an hour ahead of UTC: the API returns it in UTC.
	expiresPlusOne := expires.In(time.FixedZone("+01:00", 3600)).Format(time.RFC3339)
	changed := expires.Add(30 * 24 * time.Hour).Format(time.RFC3339)
	config := func(expiresAt string) string {
		body := fmt.Sprintf("  name = %q\n", name)
		if expiresAt != "" {
			body += fmt.Sprintf("  expires_at = %q\n", expiresAt)
		}
		return providerConfig() + fmt.Sprintf(`
resource "mergegateway_api_key" "test" {
%s}
`, body)
	}
	sameID := statecheck.CompareValue(compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_api_key", "/v1/keys"),
		Steps: []resource.TestStep{
			// Create with an expiry. State keeps the configured offset.
			{
				Config: config(expiresPlusOne),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					expectAPIKey("expires_at", knownvalue.StringExact(expiresPlusOne)),
					expectAPIKey("updated_at", knownvalue.NotNull()),
				},
			},
			// Change it in place.
			{
				Config: config(changed),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					expectAPIKey("expires_at", knownvalue.StringExact(changed)),
				},
			},
			// Import with an expiry.
			{
				ResourceName:            apiKeyAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"key"},
			},
			// Removing it removes the expiry: the key never expires.
			{
				Config: config(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(apiKeyAddress, tfjsonpath.New("expires_at"), knownvalue.Null()),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(apiKeyAddress, tfjsonpath.New("id")),
					expectAPIKey("expires_at", knownvalue.Null()),
				},
				Check: checkAPIKeyNull("expires_at"),
			},
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

// checkAPIKeyNull checks against the API that the key's field is null.
func checkAPIKeyNull(field string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		key, ok := s.RootModule().Resources[apiKeyAddress]
		if !ok {
			return fmt.Errorf("%s not in state", apiKeyAddress)
		}
		status, body, err := apiRequest(ctx, http.MethodGet, "/v1/keys/"+key.Primary.ID, nil)
		if err != nil {
			return fmt.Errorf("reading API key %s: %w", key.Primary.ID, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("reading API key %s: HTTP %d", key.Primary.ID, status)
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			return fmt.Errorf("decode API key: %w", err)
		}
		if v, ok := got[field]; !ok || v != nil {
			return fmt.Errorf("API key %s: %s = %v, want null", key.Primary.ID, field, v)
		}
		return nil
	}
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
