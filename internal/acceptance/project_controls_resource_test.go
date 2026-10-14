package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
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

const projectControlsAddress = "mergegateway_project_controls.test"

// projectControlsConfig renders two projects, a and b, and a
// mergegateway_project_controls with the given body. The body sets
// project_id; leave it empty to leave the controls out.
func projectControlsConfig(name, body string) string {
	config := providerConfig() + fmt.Sprintf(`
resource "mergegateway_project" "a" {
  name = "%[1]s-a"
}

resource "mergegateway_project" "b" {
  name = "%[1]s-b"
}
`, name)
	if body != "" {
		config += fmt.Sprintf(`
resource "mergegateway_project_controls" "test" {
%s
}
`, body)
	}
	return config
}

// expectControls checks a project controls attribute in state.
func expectControls(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(projectControlsAddress, tfjsonpath.New(path), value)
}

func stringList(values ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, 0, len(values))
	for _, v := range values {
		checks = append(checks, knownvalue.StringExact(v))
	}
	return knownvalue.ListExact(checks)
}

// TestAccProjectControlsResource walks one project's controls through create,
// a no-op re-plan, in-place updates of every field, clearing fields, a replace
// onto another project, and imports, then checks that destroy clears the overrides.
func TestAccProjectControlsResource(t *testing.T) {
	name := randomName()
	// The controls' ID is their project's ID.
	onProjectA := statecheck.CompareValuePairs(projectControlsAddress, tfjsonpath.New("id"),
		"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame())
	onProjectB := statecheck.CompareValuePairs(projectControlsAddress, tfjsonpath.New("id"),
		"mergegateway_project.b", tfjsonpath.New("id"), compare.ValuesSame())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkProjectControlsDestroyed,
		Steps: []resource.TestStep{
			// Create with some fields of each unit. vendor_routing_preference
			// is not sorted: as a list, its order must not show a diff.
			{
				Config: projectControlsConfig(name, `
  project_id                = mergegateway_project.a.id
  allowed_regions           = ["US"]
  allowed_vendors           = ["openai", "anthropic"]
  vendor_routing_preference = ["openai", "anthropic"]
  zero_data_retention       = true
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					onProjectA,
					statecheck.CompareValuePairs(projectControlsAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame()),
					expectControls("allowed_regions", stringList("US")),
					expectControls("ignored_regions", knownvalue.Null()),
					expectControls("allowed_vendors", stringList("openai", "anthropic")),
					expectControls("ignored_vendors", knownvalue.Null()),
					expectControls("vendor_routing_preference", stringList("openai", "anthropic")),
					expectControls("zero_data_retention", knownvalue.Bool(true)),
				},
				Check: checkOverride("mergegateway_project.a", `{
					"regions": {"allowed_regions": ["US"], "ignored_regions": []},
					"vendors": {"allowed_vendors": ["openai", "anthropic"], "ignored_vendors": [], "vendor_routing_preference": ["openai", "anthropic"]},
					"zero_data_retention": true}`),
			},
			// Change every field; allowed_regions moves to ignored_regions.
			{
				Config: projectControlsConfig(name, `
  project_id                = mergegateway_project.a.id
  ignored_regions           = ["CA"]
  allowed_vendors           = ["anthropic", "openai"]
  ignored_vendors           = ["google"]
  vendor_routing_preference = ["anthropic", "openai"]
  zero_data_retention       = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					onProjectA,
					expectControls("allowed_regions", knownvalue.Null()),
					expectControls("ignored_regions", stringList("CA")),
					expectControls("allowed_vendors", stringList("anthropic", "openai")),
					expectControls("ignored_vendors", stringList("google")),
					expectControls("vendor_routing_preference", stringList("anthropic", "openai")),
					expectControls("zero_data_retention", knownvalue.Bool(false)),
				},
				Check: checkOverride("mergegateway_project.a", `{
					"regions": {"allowed_regions": [], "ignored_regions": ["CA"]},
					"vendors": {"allowed_vendors": ["anthropic", "openai"], "ignored_vendors": ["google"], "vendor_routing_preference": ["anthropic", "openai"]},
					"zero_data_retention": false}`),
			},
			// Import with every unit overridden.
			{
				ResourceName:      projectControlsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Remove the vendors and zero_data_retention: the project inherits
			// them from the organization again. An empty allowed_regions is
			// an override of its own.
			{
				Config: projectControlsConfig(name, `
  project_id      = mergegateway_project.a.id
  allowed_regions = []
  ignored_regions = ["CA"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					onProjectA,
					expectControls("allowed_regions", stringList()),
					expectControls("ignored_regions", stringList("CA")),
					expectControls("allowed_vendors", knownvalue.Null()),
					expectControls("ignored_vendors", knownvalue.Null()),
					expectControls("vendor_routing_preference", knownvalue.Null()),
					expectControls("zero_data_retention", knownvalue.Null()),
				},
				Check: checkOverride("mergegateway_project.a", `{"regions": {"allowed_regions": [], "ignored_regions": ["CA"]}}`),
			},
			// Replace: moving the controls to project b clears project a's.
			{
				Config: projectControlsConfig(name, `
  project_id          = mergegateway_project.b.id
  ignored_regions     = ["CA"]
  zero_data_retention = true
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					onProjectB,
					expectControls("ignored_regions", stringList("CA")),
					expectControls("zero_data_retention", knownvalue.Bool(true)),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					checkOverride("mergegateway_project.a", `{}`),
					checkOverride("mergegateway_project.b", `{
						"regions": {"allowed_regions": [], "ignored_regions": ["CA"]},
						"zero_data_retention": true}`),
				),
			},
			// Import by project ID.
			{
				ResourceName:      projectControlsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Destroy the controls but keep the project: its overrides are cleared.
			{
				Config: projectControlsConfig(name, ``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionDestroy)},
				},
				Check: checkOverride("mergegateway_project.b", `{}`),
			},
		},
	})
}

// TestAccProjectControlsResource_drift changes the controls behind
// Terraform's back: a changed override and a cleared one must each plan an
// update that sets the configured overrides again, and a deleted project
// must plan a create rather than fail.
func TestAccProjectControlsResource_drift(t *testing.T) {
	name := randomName()
	var projectID string
	config := projectControlsConfig(name, `
  project_id          = mergegateway_project.a.id
  allowed_vendors     = ["openai"]
  zero_data_retention = true
`)
	restored := []statecheck.StateCheck{
		expectControls("allowed_vendors", stringList("openai")),
		expectControls("ignored_vendors", knownvalue.Null()),
		expectControls("zero_data_retention", knownvalue.Bool(true)),
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkProjectControlsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					projectID = s.RootModule().Resources[projectControlsAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					putProjectControls(t, projectID, `{"ignored_vendors": ["openai"], "zero_data_retention": false}`)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: restored,
			},
			{
				PreConfig: func() { deleteProjectControls(t, projectID) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: restored,
			},
			{
				PreConfig: func() { deleteProject(t, projectID) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("mergegateway_project.a", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(projectControlsAddress, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

// projectControlsOverride reads a project's override through the API.
func projectControlsOverride(ctx context.Context, projectID string) (int, map[string]any, error) {
	status, body, err := apiRequest(ctx, http.MethodGet, "/v1/projects/"+projectID+"/controls", nil)
	if err != nil || status != http.StatusOK {
		return status, nil, err
	}
	var resp struct {
		Override map[string]any `json:"override"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return status, nil, fmt.Errorf("decode controls: %w", err)
	}
	return status, resp.Override, nil
}

// checkOverride checks against the API that the override of the project at
// projectAddress is exactly want (JSON).
func checkOverride(projectAddress, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		project, ok := s.RootModule().Resources[projectAddress]
		if !ok {
			return fmt.Errorf("%s not in state", projectAddress)
		}
		status, got, err := projectControlsOverride(ctx, project.Primary.ID)
		if err != nil {
			return fmt.Errorf("reading controls of %s: %w", projectAddress, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("reading controls of %s: HTTP %d", projectAddress, status)
		}
		var wantValue map[string]any
		if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, wantValue) {
			return fmt.Errorf("override of %s\n got: %v\nwant: %v", projectAddress, got, wantValue)
		}
		return nil
	}
}

// checkProjectControlsDestroyed verifies that every mergegateway_project_controls
// in state has no overrides left after destroy: its project is gone (404) or
// its override is empty.
func checkProjectControlsDestroyed(s *terraform.State) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "mergegateway_project_controls" {
			continue
		}
		status, override, err := projectControlsOverride(ctx, rs.Primary.ID)
		if err != nil {
			return fmt.Errorf("checking the controls of project %s were cleared: %w", rs.Primary.ID, err)
		}
		if status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK {
			return fmt.Errorf("checking the controls of project %s were cleared: HTTP %d", rs.Primary.ID, status)
		}
		if len(override) != 0 {
			return fmt.Errorf("project %s still has overrides after destroy: %v", rs.Primary.ID, override)
		}
	}
	return nil
}

// putProjectControls replaces a project's override through the API, outside Terraform.
func putProjectControls(t *testing.T, projectID, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodPut, "/v1/projects/"+projectID+"/controls", []byte(body))
	if err != nil {
		t.Fatalf("setting controls of project %s: %v", projectID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("setting controls of project %s: HTTP %d", projectID, status)
	}
}

// deleteProjectControls clears a project's override through the API, outside Terraform.
func deleteProjectControls(t *testing.T, projectID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodDelete, "/v1/projects/"+projectID+"/controls", nil)
	if err != nil {
		t.Fatalf("clearing controls of project %s: %v", projectID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("clearing controls of project %s: HTTP %d", projectID, status)
	}
}
