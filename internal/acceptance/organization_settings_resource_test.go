package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const organizationSettingsAddress = "mergegateway_organization_settings.test"

// organizationSettingsConfig renders mergegateway_organization_settings with
// the given body.
func organizationSettingsConfig(body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_organization_settings" "test" {
%s
}
`, body)
}

// expectOrganizationSettings checks an organization settings attribute in state.
func expectOrganizationSettings(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(organizationSettingsAddress, tfjsonpath.New(path), value)
}

// TestAccOrganizationSettingsResource walks the test org's settings through
// create (an update of the configured settings), a no-op re-plan, in-place
// updates of every setting, leaving settings out (which keeps them), import
// and destroy, which must leave the settings as they are. There is nothing
// to replace. The org's settings are restored afterwards.
func TestAccOrganizationSettingsResource(t *testing.T) {
	keepOrganizationSettings(t)
	name := randomName()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		// Destroying only forgets the settings: the org keeps its name.
		CheckDestroy: func(*terraform.State) error {
			var org struct {
				Name string `json:"name"`
			}
			if err := getJSON("/v1/organization", &org); err != nil {
				return err
			}
			if org.Name != name {
				return fmt.Errorf("organization name is %q after destroy, want it kept as %q", org.Name, name)
			}
			return nil
		},
		Steps: []resource.TestStep{
			// Set a few settings; the rest keep their values.
			{
				Config: organizationSettingsConfig(fmt.Sprintf(`
  name            = %q
  log_payloads    = true
  web_search_mode = "native"
  tag_definitions = [{ key = "tf-acc-team", values = ["support", "sales"] }]
`, name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationSettingsAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationSettings("id", knownvalue.NotNull()),
					expectOrganizationSettings("slug", knownvalue.NotNull()),
					expectOrganizationSettings("name", knownvalue.StringExact(name)),
					expectOrganizationSettings("log_payloads", knownvalue.Bool(true)),
					expectOrganizationSettings("web_search_mode", knownvalue.StringExact("native")),
					expectOrganizationSettings("tag_definitions", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"key":    knownvalue.StringExact("tf-acc-team"),
							"values": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("support"), knownvalue.StringExact("sales")}),
						}),
					})),
					expectOrganizationSettings("zero_data_retention", knownvalue.NotNull()),
					expectOrganizationSettings("vendor_restrictions", knownvalue.NotNull()),
				},
			},
			// Change every setting; clear the tags.
			{
				Config: organizationSettingsConfig(fmt.Sprintf(`
  name                           = %q
  log_payloads                   = false
  zero_data_retention            = true
  web_search_mode                = "merge"
  auto_handle_model_deprecations = true
  tag_definitions                = []
  vendor_restrictions = {
    allowed_vendors            = ["openai", "anthropic"]
    vendor_routing_preference  = ["anthropic", "openai"]
    prefer_byok                = true
    customer_key_usage_default = "prefer_byok"
  }
`, name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationSettings("log_payloads", knownvalue.Bool(false)),
					expectOrganizationSettings("zero_data_retention", knownvalue.Bool(true)),
					expectOrganizationSettings("web_search_mode", knownvalue.StringExact("merge")),
					expectOrganizationSettings("auto_handle_model_deprecations", knownvalue.Bool(true)),
					expectOrganizationSettings("tag_definitions", knownvalue.ListExact([]knownvalue.Check{})),
					expectOrganizationSettings("vendor_restrictions", knownvalue.ObjectExact(map[string]knownvalue.Check{
						"allowed_vendors":            knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("openai"), knownvalue.StringExact("anthropic")}),
						"ignored_vendors":            knownvalue.ListExact([]knownvalue.Check{}),
						"vendor_routing_preference":  knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("anthropic"), knownvalue.StringExact("openai")}),
						"allowed_regions":            knownvalue.ListExact([]knownvalue.Check{}),
						"ignored_regions":            knownvalue.ListExact([]knownvalue.Check{}),
						"prefer_byok":                knownvalue.Bool(true),
						"byok_only":                  knownvalue.Bool(false),
						"customer_key_usage_default": knownvalue.StringExact("prefer_byok"),
					})),
				},
			},
			// Import by any ID.
			{
				ResourceName:      organizationSettingsAddress,
				ImportState:       true,
				ImportStateId:     "organization",
				ImportStateVerify: true,
			},
			// Settings left out keep their values: nothing to change.
			{
				Config: organizationSettingsConfig(``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationSettings("name", knownvalue.StringExact(name)),
					expectOrganizationSettings("zero_data_retention", knownvalue.Bool(true)),
				},
			},
			// Vendor restrictions are set as a whole: what the block leaves out
			// takes its default.
			{
				Config: organizationSettingsConfig(`
  vendor_restrictions = {
    ignored_vendors = ["openai"]
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(organizationSettingsAddress, tfjsonpath.New("vendor_restrictions").AtMapKey("allowed_vendors"), knownvalue.ListExact([]knownvalue.Check{})),
					statecheck.ExpectKnownValue(organizationSettingsAddress, tfjsonpath.New("vendor_restrictions").AtMapKey("prefer_byok"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(organizationSettingsAddress, tfjsonpath.New("vendor_restrictions").AtMapKey("customer_key_usage_default"), knownvalue.Null()),
				},
			},
		},
	})
}

// TestAccOrganizationSettingsResource_drift changes a setting behind
// Terraform's back: the plan must restore it.
func TestAccOrganizationSettingsResource_drift(t *testing.T) {
	keepOrganizationSettings(t)
	name := randomName()
	config := organizationSettingsConfig(fmt.Sprintf(`
  name         = %q
  log_payloads = true
`, name))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/organization", fmt.Sprintf(`{"name": %q, "log_payloads": false}`, name+"-drifted"), http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationSettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationSettings("name", knownvalue.StringExact(name)),
					expectOrganizationSettings("log_payloads", knownvalue.Bool(true)),
				},
			},
		},
	})
}

// keepOrganizationSettings restores the test org's settings when the test
// ends: they belong to the org, so destroying the resource doesn't. Without
// TF_ACC it does nothing, and resource.Test skips the test.
func keepOrganizationSettings(t *testing.T) {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		return
	}
	testAccPreCheck(t)
	var settings map[string]any
	if err := getJSON("/v1/organization", &settings); err != nil {
		t.Fatal(err)
	}
	restore := map[string]any{}
	for _, field := range []string{"name", "tag_definitions", "log_payloads", "zero_data_retention", "web_search_mode",
		"auto_handle_model_deprecations", "vendor_restrictions"} {
		restore[field] = settings[field]
	}
	body, err := json.Marshal(restore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mustAPIRequest(t, http.MethodPatch, "/v1/organization", string(body), http.StatusOK) })
}

// getJSON GETs path from the API and decodes the response into v.
func getJSON(path string, v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	status, body, err := apiRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, status)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
