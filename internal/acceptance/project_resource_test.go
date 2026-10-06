// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

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

const projectAddress = "mergegateway_project.test"

// projectConfig renders a mergegateway_project with the given name and body.
func projectConfig(name, body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_project" "test" {
  name = %q
%s
}
`, name, body)
}

// expectProject checks a project attribute in state.
func expectProject(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(projectAddress, tfjsonpath.New(path), value)
}

// TestAccProjectResource walks one project through create, a no-op re-plan,
// in-place updates of every field, replace, and imports along the way, then
// checks destroy.
func TestAccProjectResource(t *testing.T) {
	name := randomName()
	renamed := name + "-renamed"
	// The ID stays the same through updates, and changes when the project is replaced.
	sameID := statecheck.CompareValue(compare.ValuesSame())
	replacedID := statecheck.CompareValue(compare.ValuesDiffer())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_project", "/v1/projects"),
		Steps: []resource.TestStep{
			// Create with a budget. The thresholds are unsorted and repeated:
			// as a set, they must not show a diff on the next plan.
			{
				Config: projectConfig(name, `
  description = "Created by the provider's acceptance tests"
  budget_config = {
    amount           = 100
    period           = "monthly"
    enforcement_mode = "soft_limit"
    alert_thresholds = [90, 50, 80, 50]
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectAddress, tfjsonpath.New("id")),
					expectProject("name", knownvalue.StringExact(name)),
					expectProject("description", knownvalue.StringExact("Created by the provider's acceptance tests")),
					expectProject("slug", knownvalue.NotNull()),
					expectProject("organization_id", knownvalue.NotNull()),
					expectProject("is_active", knownvalue.Bool(true)),
					expectProject("uses_organization_default_routing", knownvalue.Bool(true)),
					expectProject("routing_policy", knownvalue.Null()),
					expectProject("budget_config", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"amount":           knownvalue.Float64Exact(100),
						"period":           knownvalue.StringExact("monthly"),
						"enforcement_mode": knownvalue.StringExact("soft_limit"),
						"alert_thresholds": knownvalue.SetExact([]knownvalue.Check{
							knownvalue.Int64Exact(50), knownvalue.Int64Exact(80), knownvalue.Int64Exact(90),
						}),
						"notification_emails": knownvalue.SetExact([]knownvalue.Check{}),
						"notify_org_admins":   knownvalue.Bool(true),
					})),
				},
			},
			// Rename, change the description and the budget, and deactivate.
			{
				Config: projectConfig(renamed, `
  description = "Updated by the provider's acceptance tests"
  is_active   = false
  budget_config = {
    amount              = 250.5
    period              = "weekly"
    enforcement_mode    = "hard_limit"
    alert_thresholds    = [75]
    notification_emails = ["tf-acc-alerts@example.com"]
    notify_org_admins   = false
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectAddress, tfjsonpath.New("id")),
					expectProject("name", knownvalue.StringExact(renamed)),
					expectProject("description", knownvalue.StringExact("Updated by the provider's acceptance tests")),
					expectProject("is_active", knownvalue.Bool(false)),
					expectProject("budget_config", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"amount":              knownvalue.Float64Exact(250.5),
						"period":              knownvalue.StringExact("weekly"),
						"enforcement_mode":    knownvalue.StringExact("hard_limit"),
						"alert_thresholds":    knownvalue.SetExact([]knownvalue.Check{knownvalue.Int64Exact(75)}),
						"notification_emails": knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("tf-acc-alerts@example.com")}),
						"notify_org_admins":   knownvalue.Bool(false),
					})),
				},
			},
			// Import with a budget, inactive.
			{
				ResourceName:      projectAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Remove the budget and the description, reactivate, and set a
			// PRIORITY routing policy.
			{
				Config: projectConfig(renamed, `
  routing_policy = {
    strategy = "PRIORITY"
    priority_order = [
      { model = "openai/gpt-4o-mini", priority = 2 },
      { model = "openai/gpt-4o", priority = 1 },
    ]
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectAddress, tfjsonpath.New("id")),
					expectProject("description", knownvalue.Null()),
					expectProject("is_active", knownvalue.Bool(true)),
					expectProject("budget_config", knownvalue.Null()),
					expectProject("uses_organization_default_routing", knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(projectAddress, tfjsonpath.New("routing_policy").AtMapKey("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(projectAddress, tfjsonpath.New("routing_policy").AtMapKey("priority_order"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{"model": knownvalue.StringExact("openai/gpt-4o"), "priority": knownvalue.Int64Exact(1)}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{"model": knownvalue.StringExact("openai/gpt-4o-mini"), "priority": knownvalue.Int64Exact(2)}),
					})),
				},
			},
			// Change the routing policy to INTELLIGENT.
			{
				Config: projectConfig(renamed, `
  routing_policy = {
    strategy       = "INTELLIGENT"
    axis           = "PERFORMANCE"
    allowed_models = ["openai/gpt-4o", "openai/gpt-4o-mini"]
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectAddress, tfjsonpath.New("id")),
					statecheck.ExpectKnownValue(projectAddress, tfjsonpath.New("routing_policy").AtMapKey("axis"), knownvalue.StringExact("PERFORMANCE")),
					statecheck.ExpectKnownValue(projectAddress, tfjsonpath.New("routing_policy").AtMapKey("allowed_providers"),
						knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("openai")})),
					statecheck.ExpectKnownValue(projectAddress, tfjsonpath.New("routing_policy").AtMapKey("priority_order"), knownvalue.Null()),
				},
			},
			// Import with an INTELLIGENT routing policy.
			{
				ResourceName:      projectAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Clear the routing policy: back to the organization default.
			{
				Config: projectConfig(renamed, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectAddress, tfjsonpath.New("id")),
					replacedID.AddStateValue(projectAddress, tfjsonpath.New("id")),
					expectProject("routing_policy", knownvalue.Null()),
					expectProject("uses_organization_default_routing", knownvalue.Bool(true)),
				},
			},
			// Replace: a tainted project is destroyed and created again with a new ID.
			{
				Config: projectConfig(renamed, ``),
				Taint:  []string{projectAddress},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					replacedID.AddStateValue(projectAddress, tfjsonpath.New("id")),
				},
			},
			// Import by project ID.
			{
				ResourceName:      projectAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccProjectResource_inactive creates a project with is_active = false,
// which the provider does as a create followed by an update.
func TestAccProjectResource_inactive(t *testing.T) {
	name := randomName()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_project", "/v1/projects"),
		Steps: []resource.TestStep{
			{
				Config: projectConfig(name, `  is_active = false`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectProject("is_active", knownvalue.Bool(false)),
				},
			},
		},
	})
}

// TestAccProjectResource_drift changes the project behind Terraform's back:
// a rename must plan an update that restores the name, and a deletion must
// plan a create rather than fail.
func TestAccProjectResource_drift(t *testing.T) {
	name := randomName()
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_project", "/v1/projects"),
		Steps: []resource.TestStep{
			{
				Config: projectConfig(name, ``),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[projectAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() { renameProject(t, id, name+"-drifted") },
				Config:    projectConfig(name, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectProject("name", knownvalue.StringExact(name)),
				},
			},
			{
				PreConfig: func() { deleteProject(t, id) },
				Config:    projectConfig(name, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

// renameProject renames a project through the API, outside Terraform.
func renameProject(t *testing.T, id, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := apiRequest(ctx, http.MethodPatch, "/v1/projects/"+id, body)
	if err != nil {
		t.Fatalf("renaming project %s: %v", id, err)
	}
	if status != http.StatusOK {
		t.Fatalf("renaming project %s: HTTP %d", id, status)
	}
}

// deleteProject deletes a project through the API, outside Terraform.
func deleteProject(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodDelete, "/v1/projects/"+id, nil)
	if err != nil {
		t.Fatalf("deleting project %s: %v", id, err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("deleting project %s: HTTP %d", id, status)
	}
}
