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

const piSettingsAddress = "mergegateway_project_pi_settings.test"

// piSettingsConfig renders two projects, a and b, and, if project is not
// empty, PI settings for mergegateway_project.<project> with the given body.
func piSettingsConfig(name, project, body string) string {
	config := providerConfig() + fmt.Sprintf(`
resource "mergegateway_project" "a" {
  name = "%[1]s-a"
}

resource "mergegateway_project" "b" {
  name = "%[1]s-b"
}
`, name)
	if project == "" {
		return config
	}
	return config + fmt.Sprintf(`
resource "mergegateway_project_pi_settings" "test" {
  project_id = mergegateway_project.%s.id
%s
}
`, project, body)
}

// expectPISettings checks a PI settings attribute in state.
func expectPISettings(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(piSettingsAddress, tfjsonpath.New(path), value)
}

// TestAccProjectPISettingsResource walks one set of overrides through create,
// a no-op re-plan, in-place updates of every field, clearing fields, replace
// (moving to another project), imports along the way and destroy, checking
// against the API that each project left behind was reset.
func TestAccProjectPISettingsResource(t *testing.T) {
	name := randomName()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkPISettingsDestroyed,
		Steps: []resource.TestStep{
			// Create with a few overrides; the rest inherit.
			{
				Config: piSettingsConfig(name, "a", `
  pi_mode               = "block"
  pi_block_threshold    = 0.7
  pi_allowlist_patterns = ["tf-acc allowed phrase"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.CompareValuePairs(piSettingsAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.CompareValuePairs(piSettingsAddress, tfjsonpath.New("id"),
						"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame()),
					expectPISettings("pi_mode", knownvalue.StringExact("block")),
					expectPISettings("pi_block_threshold", knownvalue.Float64Exact(0.7)),
					expectPISettings("pi_allowlist_patterns", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("tf-acc allowed phrase")})),
					expectPISettings("pi_indirect_mode", knownvalue.Null()),
					expectPISettings("pi_output_action", knownvalue.Null()),
					expectPISettings("pi_tier2a_block_threshold", knownvalue.Null()),
					expectPISettings("pi_tier2b_block_threshold", knownvalue.Null()),
					expectPISettings("pi_fail_closed", knownvalue.Null()),
					statecheck.ExpectKnownValue(piSettingsAddress, tfjsonpath.New("effective").AtMapKey("pi_mode"), knownvalue.StringExact("block")),
					statecheck.ExpectKnownValue(piSettingsAddress, tfjsonpath.New("effective").AtMapKey("pi_block_threshold"), knownvalue.Float64Exact(0.7)),
					statecheck.ExpectKnownValue(piSettingsAddress, tfjsonpath.New("effective").AtMapKey("pi_output_action"), knownvalue.NotNull()),
				},
			},
			// Change every override. The patterns are not sorted: they are kept in order.
			{
				Config: piSettingsConfig(name, "a", `
  pi_mode                   = "alert"
  pi_indirect_mode          = "alert"
  pi_output_action          = "block"
  pi_block_threshold        = 0.4
  pi_tier2a_block_threshold = 0.65
  pi_tier2b_block_threshold = 0.35
  pi_fail_closed            = true
  pi_allowlist_patterns     = ["tf-acc second", "tf-acc first"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectPISettings("pi_mode", knownvalue.StringExact("alert")),
					expectPISettings("pi_indirect_mode", knownvalue.StringExact("alert")),
					expectPISettings("pi_output_action", knownvalue.StringExact("block")),
					expectPISettings("pi_block_threshold", knownvalue.Float64Exact(0.4)),
					expectPISettings("pi_tier2a_block_threshold", knownvalue.Float64Exact(0.65)),
					expectPISettings("pi_tier2b_block_threshold", knownvalue.Float64Exact(0.35)),
					expectPISettings("pi_fail_closed", knownvalue.Bool(true)),
					expectPISettings("pi_allowlist_patterns", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("tf-acc second"), knownvalue.StringExact("tf-acc first"),
					})),
					statecheck.ExpectKnownValue(piSettingsAddress, tfjsonpath.New("effective").AtMapKey("pi_fail_closed"), knownvalue.Bool(true)),
				},
			},
			// Import with every override set.
			{
				ResourceName:      piSettingsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Remove most overrides so they inherit again; override the
			// allowlist with none and fail_closed with false.
			{
				Config: piSettingsConfig(name, "a", `
  pi_fail_closed        = false
  pi_allowlist_patterns = []
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectPISettings("pi_mode", knownvalue.Null()),
					expectPISettings("pi_indirect_mode", knownvalue.Null()),
					expectPISettings("pi_output_action", knownvalue.Null()),
					expectPISettings("pi_block_threshold", knownvalue.Null()),
					expectPISettings("pi_tier2a_block_threshold", knownvalue.Null()),
					expectPISettings("pi_tier2b_block_threshold", knownvalue.Null()),
					expectPISettings("pi_fail_closed", knownvalue.Bool(false)),
					expectPISettings("pi_allowlist_patterns", knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			// Import with an empty allowlist.
			{
				ResourceName:      piSettingsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Replace: moving the overrides to project b resets project a.
			{
				Config: piSettingsConfig(name, "b", `
  pi_mode = "alert"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.CompareValuePairs(piSettingsAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.b", tfjsonpath.New("id"), compare.ValuesSame()),
					expectPISettings("pi_mode", knownvalue.StringExact("alert")),
					expectPISettings("pi_fail_closed", knownvalue.Null()),
				},
				Check: checkProjectPIReset("mergegateway_project.a"),
			},
			// Import by project ID.
			{
				ResourceName:      piSettingsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Destroy the settings but keep the projects: project b is reset.
			{
				Config: piSettingsConfig(name, "", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionDestroy)},
				},
				Check: checkProjectPIReset("mergegateway_project.b"),
			},
		},
	})
}

// TestAccProjectPISettingsResource_drift changes the overrides behind
// Terraform's back: a change must plan an update that restores them, and a
// reset (or a deleted project) must plan a create rather than fail.
func TestAccProjectPISettingsResource_drift(t *testing.T) {
	name := randomName()
	config := piSettingsConfig(name, "a", `
  pi_mode = "block"
`)
	var projectID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkPISettingsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					projectID = s.RootModule().Resources[piSettingsAddress].Primary.ID
					return nil
				},
			},
			// Changed outside Terraform: the plan restores pi_mode.
			{
				PreConfig: func() { putProjectPISettings(t, projectID, `{"pi_mode": "alert", "pi_fail_closed": true}`) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectPISettings("pi_mode", knownvalue.StringExact("block")),
					expectPISettings("pi_fail_closed", knownvalue.Null()),
				},
			},
			// Reset outside Terraform: the plan sets the overrides again.
			{
				PreConfig: func() { resetProjectPISettings(t, projectID) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectPISettings("pi_mode", knownvalue.StringExact("block")),
				},
			},
			// The project deleted outside Terraform: both are created again.
			{
				PreConfig: func() { deleteProject(t, projectID) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("mergegateway_project.a", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(piSettingsAddress, plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// checkPISettingsDestroyed verifies against the API that the project of every
// mergegateway_project_pi_settings in state is gone or inherits everything.
func checkPISettingsDestroyed(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "mergegateway_project_pi_settings" {
			continue
		}
		if err := checkPIReset(rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkProjectPIReset verifies against the API that the project at address
// has no PI overrides.
func checkProjectPIReset(address string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[address]
		if !ok {
			return fmt.Errorf("%s not in state", address)
		}
		return checkPIReset(rs.Primary.ID)
	}
}

// checkPIReset returns an error unless the project is gone (404) or its PI
// override is empty, that is, every field null or left out.
func checkPIReset(projectID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	path := "/v1/projects/" + projectID + "/pi-settings"
	status, body, err := apiRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("checking PI settings of project %s were reset: %w", projectID, err)
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, status)
	}

	var resp struct {
		Override map[string]any `json:"override"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	for field, value := range resp.Override {
		if value != nil {
			return fmt.Errorf("project %s still overrides %s after destroy", projectID, field)
		}
	}
	return nil
}

// putProjectPISettings replaces a project's PI overrides through the API, outside Terraform.
func putProjectPISettings(t *testing.T, projectID, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodPut, "/v1/projects/"+projectID+"/pi-settings", []byte(body))
	if err != nil {
		t.Fatalf("updating PI settings of project %s: %v", projectID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("updating PI settings of project %s: HTTP %d", projectID, status)
	}
}

// resetProjectPISettings resets a project's PI overrides through the API, outside Terraform.
func resetProjectPISettings(t *testing.T, projectID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodDelete, "/v1/projects/"+projectID+"/pi-settings", nil)
	if err != nil {
		t.Fatalf("resetting PI settings of project %s: %v", projectID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("resetting PI settings of project %s: HTTP %d", projectID, status)
	}
}
