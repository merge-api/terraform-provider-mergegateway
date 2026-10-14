package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
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

const projectDLPSettingsAddress = "mergegateway_project_dlp_settings.test"

// projectDLPSettingsConfig renders two projects, a and b, and, unless
// projectRef is empty, DLP settings for the project it names with the given
// override map.
func projectDLPSettingsConfig(name, projectRef, override string) string {
	config := providerConfig() + fmt.Sprintf(`
resource "mergegateway_project" "a" {
  name = "%[1]s-a"
}

resource "mergegateway_project" "b" {
  name = "%[1]s-b"
}
`, name)
	if projectRef == "" {
		return config
	}
	return config + fmt.Sprintf(`
resource "mergegateway_project_dlp_settings" "test" {
  project_id = mergegateway_project.%s.id
  override = {
%s
  }
}
`, projectRef, override)
}

// expectDLPOverride checks the override map in state.
func expectDLPOverride(entities map[string]knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(projectDLPSettingsAddress, tfjsonpath.New("override"), knownvalue.MapExact(entities))
}

func dlpEntity(enabled, action knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"enabled": enabled, "action": action})
}

// TestAccProjectDLPSettingsResource walks one project's DLP override through
// create, a no-op re-plan, in-place updates (adding, changing and removing
// entities, and clearing a field), import, and replace onto another project,
// then removes it and checks the project is back to the organization's rules.
func TestAccProjectDLPSettingsResource(t *testing.T) {
	name := randomName()
	e := dlpEntityTypes(t, 3)
	// id follows project_id: the same through updates, different after the move to project b.
	sameID := statecheck.CompareValue(compare.ValuesSame())
	replacedID := statecheck.CompareValue(compare.ValuesDiffer())
	var projectA, projectB string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkProjectDLPSettingsDestroy,
		Steps: []resource.TestStep{
			{
				Config: projectDLPSettingsConfig(name, "a", fmt.Sprintf(`
    %q = { enabled = true, action = "redact" }
    %q = { action = "log" }
`, e[0], e[1])),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectDLPSettingsAddress, tfjsonpath.New("id")),
					statecheck.CompareValuePairs(projectDLPSettingsAddress, tfjsonpath.New("id"),
						"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.CompareValuePairs(projectDLPSettingsAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.a", tfjsonpath.New("id"), compare.ValuesSame()),
					expectDLPOverride(map[string]knownvalue.Check{
						e[0]: dlpEntity(knownvalue.Bool(true), knownvalue.StringExact("redact")),
						e[1]: dlpEntity(knownvalue.Null(), knownvalue.StringExact("log")),
					}),
				},
				Check: func(s *terraform.State) error {
					projectA = s.RootModule().Resources["mergegateway_project.a"].Primary.ID
					projectB = s.RootModule().Resources["mergegateway_project.b"].Primary.ID
					return nil
				},
			},
			// Change an entity, remove one and add one.
			{
				Config: projectDLPSettingsConfig(name, "a", fmt.Sprintf(`
    %q = { enabled = false, action = "block" }
    %q = { enabled = true }
`, e[0], e[2])),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectDLPSettingsAddress, tfjsonpath.New("id")),
					expectDLPOverride(map[string]knownvalue.Check{
						e[0]: dlpEntity(knownvalue.Bool(false), knownvalue.StringExact("block")),
						e[2]: dlpEntity(knownvalue.Bool(true), knownvalue.Null()),
					}),
				},
			},
			// Clear a field: the entity follows the organization for it again.
			{
				Config: projectDLPSettingsConfig(name, "a", fmt.Sprintf(`
    %q = { action = "block" }
    %q = { enabled = true }
`, e[0], e[2])),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(projectDLPSettingsAddress, tfjsonpath.New("id")),
					replacedID.AddStateValue(projectDLPSettingsAddress, tfjsonpath.New("id")),
					expectDLPOverride(map[string]knownvalue.Check{
						e[0]: dlpEntity(knownvalue.Null(), knownvalue.StringExact("block")),
						e[2]: dlpEntity(knownvalue.Bool(true), knownvalue.Null()),
					}),
				},
			},
			// Import by project ID.
			{
				ResourceName:      projectDLPSettingsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Replace: moving to project b resets project a and overrides project b.
			{
				Config: projectDLPSettingsConfig(name, "b", fmt.Sprintf(`
    %q = { action = "block" }
    %q = { enabled = true }
`, e[0], e[2])),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					replacedID.AddStateValue(projectDLPSettingsAddress, tfjsonpath.New("id")),
					statecheck.CompareValuePairs(projectDLPSettingsAddress, tfjsonpath.New("id"),
						"mergegateway_project.b", tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.CompareValuePairs(projectDLPSettingsAddress, tfjsonpath.New("project_id"),
						"mergegateway_project.b", tfjsonpath.New("id"), compare.ValuesSame()),
					expectDLPOverride(map[string]knownvalue.Check{
						e[0]: dlpEntity(knownvalue.Null(), knownvalue.StringExact("block")),
						e[2]: dlpEntity(knownvalue.Bool(true), knownvalue.Null()),
					}),
				},
				Check: func(s *terraform.State) error { return checkNoDLPOverride(projectA) },
			},
			// Import on project b.
			{
				ResourceName:      projectDLPSettingsAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Remove the settings but keep the project: it is back to the organization's rules.
			{
				Config: projectDLPSettingsConfig(name, "", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionDestroy)},
				},
				Check: func(s *terraform.State) error { return checkNoDLPOverride(projectB) },
			},
		},
	})
}

// TestAccProjectDLPSettingsResource_drift changes the override behind
// Terraform's back: a changed override must plan an update that restores it,
// a reset override must plan a create, and a deleted project must plan
// creating both again rather than fail.
func TestAccProjectDLPSettingsResource_drift(t *testing.T) {
	name := randomName()
	e := dlpEntityTypes(t, 1)
	config := projectDLPSettingsConfig(name, "a", fmt.Sprintf(`    %q = { action = "redact" }`, e[0]))
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkProjectDLPSettingsDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[projectDLPSettingsAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() { putDLPOverride(t, id, map[string]any{e[0]: map[string]any{"action": "block"}}) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectDLPOverride(map[string]knownvalue.Check{
						e[0]: dlpEntity(knownvalue.Null(), knownvalue.StringExact("redact")),
					}),
				},
			},
			{
				PreConfig: func() { resetDLPOverride(t, id) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionCreate)},
				},
			},
			{
				PreConfig: func() { deleteProject(t, id) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("mergegateway_project.a", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(projectDLPSettingsAddress, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

// checkProjectDLPSettingsDestroy verifies that every project DLP override in
// state is reset: GET returns 404 (the project is gone too) or an empty override.
func checkProjectDLPSettingsDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "mergegateway_project_dlp_settings" {
			continue
		}
		if err := checkNoDLPOverride(rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkNoDLPOverride checks through the API that a project has no DLP
// override, or doesn't exist.
func checkNoDLPOverride(projectID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, body, err := apiRequest(ctx, http.MethodGet, "/v1/projects/"+projectID+"/dlp-settings", nil)
	if err != nil {
		return fmt.Errorf("reading DLP settings of project %s: %w", projectID, err)
	}
	if status == http.StatusNotFound {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("reading DLP settings of project %s: HTTP %d", projectID, status)
	}
	var settings struct {
		Override map[string]any `json:"override"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return fmt.Errorf("decoding DLP settings of project %s: %w", projectID, err)
	}
	if len(settings.Override) > 0 {
		return fmt.Errorf("project %s still has a DLP override for %d entity types", projectID, len(settings.Override))
	}
	return nil
}

// dlpEntityTypes returns n DLP entity types the API knows, sorted. The spec
// doesn't list them, so they are read from a throwaway project's settings.
// It runs before resource.Test, so it skips the test itself without TF_ACC.
func dlpEntityTypes(t *testing.T, n int) []string {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skip("acceptance tests are skipped unless TF_ACC is set")
	}
	testAccPreCheck(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	body, err := json.Marshal(map[string]string{"name": randomName()})
	if err != nil {
		t.Fatal(err)
	}
	status, created, err := apiRequest(ctx, http.MethodPost, "/v1/projects", body)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("creating a project to list DLP entity types: HTTP %d, err %v", status, err)
	}
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &project); err != nil {
		t.Fatalf("decoding project: %v", err)
	}
	defer deleteProject(t, project.ID)

	status, got, err := apiRequest(ctx, http.MethodGet, "/v1/projects/"+project.ID+"/dlp-settings", nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("reading DLP settings: HTTP %d, err %v", status, err)
	}
	var settings struct {
		Entities []struct {
			EntityType string `json:"entity_type"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(got, &settings); err != nil {
		t.Fatalf("decoding DLP settings: %v", err)
	}
	entityTypes := make([]string, 0, len(settings.Entities))
	for _, e := range settings.Entities {
		entityTypes = append(entityTypes, e.EntityType)
	}
	if len(entityTypes) < n {
		t.Fatalf("need %d DLP entity types, the API lists %d", n, len(entityTypes))
	}
	sort.Strings(entityTypes)
	t.Logf("DLP entity types: %v", entityTypes)
	return entityTypes[:n]
}

// putDLPOverride replaces a project's DLP override through the API, outside Terraform.
func putDLPOverride(t *testing.T, projectID string, override map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	body, err := json.Marshal(map[string]any{"override": override})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := apiRequest(ctx, http.MethodPut, "/v1/projects/"+projectID+"/dlp-settings", body)
	if err != nil {
		t.Fatalf("updating DLP settings of project %s: %v", projectID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("updating DLP settings of project %s: HTTP %d", projectID, status)
	}
}

// resetDLPOverride resets a project's DLP override through the API, outside Terraform.
func resetDLPOverride(t *testing.T, projectID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, _, err := apiRequest(ctx, http.MethodDelete, "/v1/projects/"+projectID+"/dlp-settings", nil)
	if err != nil {
		t.Fatalf("resetting DLP settings of project %s: %v", projectID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("resetting DLP settings of project %s: HTTP %d", projectID, status)
	}
}
