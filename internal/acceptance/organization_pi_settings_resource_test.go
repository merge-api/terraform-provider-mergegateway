package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const organizationPISettingsAddress = "mergegateway_organization_pi_settings.test"

// organizationPISettingsConfig renders mergegateway_organization_pi_settings
// with the given body.
func organizationPISettingsConfig(body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_organization_pi_settings" "test" {
%s
}
`, body)
}

// expectOrganizationPISettings checks an organization PI settings attribute in state.
func expectOrganizationPISettings(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(organizationPISettingsAddress, tfjsonpath.New(path), value)
}

// TestAccOrganizationPISettingsResource walks the test org's PI policy
// through create, a no-op re-plan, in-place updates of every setting, going
// back to the defaults, import and destroy, which must reset the policy.
// There is nothing to replace. The org's policy is restored afterwards.
func TestAccOrganizationPISettingsResource(t *testing.T) {
	keepOrganizationPISettings(t)
	importStep := resource.TestStep{
		ResourceName:      organizationPISettingsAddress,
		ImportState:       true,
		ImportStateId:     "organization",
		ImportStateVerify: true,
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkOrganizationPIReset,
		Steps: []resource.TestStep{
			// Set a few settings; the rest take their defaults.
			{
				Config: organizationPISettingsConfig(`
  pi_mode               = "block"
  pi_block_threshold    = 0.7
  pi_allowlist_patterns = ["tf-acc allowed phrase"]
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationPISettingsAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationPISettings("id", knownvalue.StringExact("organization")),
					expectOrganizationPISettings("pi_mode", knownvalue.StringExact("block")),
					expectOrganizationPISettings("pi_block_threshold", knownvalue.Float64Exact(0.7)),
					expectOrganizationPISettings("pi_allowlist_patterns", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("tf-acc allowed phrase")})),
					expectOrganizationPISettings("pi_indirect_mode", knownvalue.StringExact("off")),
					expectOrganizationPISettings("pi_input_action", knownvalue.StringExact("block")),
					expectOrganizationPISettings("pi_output_action", knownvalue.StringExact("redact")),
					expectOrganizationPISettings("pi_pass_threshold", knownvalue.Float64Exact(0.3)),
					expectOrganizationPISettings("pi_safer_vendor_route", knownvalue.Null()),
				},
			},
			// Change every setting. The patterns are kept in order.
			{
				Config: organizationPISettingsConfig(`
  pi_mode                   = "alert"
  pi_indirect_mode          = "alert"
  pi_input_action           = "route"
  pi_output_action          = "block"
  pi_block_threshold        = 0.4
  pi_pass_threshold         = 0.2
  pi_tier2a_block_threshold = 0.65
  pi_tier2b_block_threshold = 0.35
  pi_fail_closed            = true
  pi_log_full_text_on_block = true
  pi_allowlist_patterns     = ["tf-acc second", "tf-acc first"]
  pi_safer_vendor_route     = "anthropic"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationPISettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationPISettings("pi_mode", knownvalue.StringExact("alert")),
					expectOrganizationPISettings("pi_indirect_mode", knownvalue.StringExact("alert")),
					expectOrganizationPISettings("pi_input_action", knownvalue.StringExact("route")),
					expectOrganizationPISettings("pi_output_action", knownvalue.StringExact("block")),
					expectOrganizationPISettings("pi_block_threshold", knownvalue.Float64Exact(0.4)),
					expectOrganizationPISettings("pi_pass_threshold", knownvalue.Float64Exact(0.2)),
					expectOrganizationPISettings("pi_tier2a_block_threshold", knownvalue.Float64Exact(0.65)),
					expectOrganizationPISettings("pi_tier2b_block_threshold", knownvalue.Float64Exact(0.35)),
					expectOrganizationPISettings("pi_fail_closed", knownvalue.Bool(true)),
					expectOrganizationPISettings("pi_log_full_text_on_block", knownvalue.Bool(true)),
					expectOrganizationPISettings("pi_allowlist_patterns", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("tf-acc second"), knownvalue.StringExact("tf-acc first"),
					})),
					expectOrganizationPISettings("pi_safer_vendor_route", knownvalue.StringExact("anthropic")),
				},
			},
			// Import with every setting changed.
			importStep,
			// Settings left out go back to their defaults.
			{
				Config: organizationPISettingsConfig(`
  pi_mode = "alert"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationPISettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationPISettings("pi_indirect_mode", knownvalue.StringExact("off")),
					expectOrganizationPISettings("pi_block_threshold", knownvalue.Float64Exact(0.57)),
					expectOrganizationPISettings("pi_fail_closed", knownvalue.Bool(false)),
					expectOrganizationPISettings("pi_allowlist_patterns", knownvalue.ListExact([]knownvalue.Check{})),
					expectOrganizationPISettings("pi_safer_vendor_route", knownvalue.Null()),
				},
			},
			// Import with the defaults.
			importStep,
		},
	})
}

// TestAccOrganizationPISettingsResource_drift changes the policy behind
// Terraform's back: a change or a reset must plan an update that sets it again.
func TestAccOrganizationPISettingsResource_drift(t *testing.T) {
	keepOrganizationPISettings(t)
	config := organizationPISettingsConfig(`
  pi_mode = "block"
`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkOrganizationPIReset,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPut, "/v1/organization/pi-settings", `{"pi_mode": "alert", "pi_fail_closed": true}`, http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationPISettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationPISettings("pi_mode", knownvalue.StringExact("block")),
					expectOrganizationPISettings("pi_fail_closed", knownvalue.Bool(false)),
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodDelete, "/v1/organization/pi-settings", "", http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(organizationPISettingsAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectOrganizationPISettings("pi_mode", knownvalue.StringExact("block")),
				},
			},
		},
	})
}

// checkOrganizationPIReset verifies against the API that destroy reset the
// org's PI policy to the defaults (PI off, no patterns).
func checkOrganizationPIReset(*terraform.State) error {
	var policy struct {
		PiMode              string   `json:"pi_mode"`
		PiIndirectMode      string   `json:"pi_indirect_mode"`
		PiAllowlistPatterns []string `json:"pi_allowlist_patterns"`
	}
	if err := getJSON("/v1/organization/pi-settings", &policy); err != nil {
		return err
	}
	if policy.PiMode != "off" || policy.PiIndirectMode != "off" || len(policy.PiAllowlistPatterns) != 0 {
		return fmt.Errorf("organization PI policy not reset after destroy: %+v", policy)
	}
	return nil
}

// keepOrganizationPISettings restores the test org's PI policy when the test
// ends. Without TF_ACC it does nothing, and resource.Test skips the test.
func keepOrganizationPISettings(t *testing.T) {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		return
	}
	testAccPreCheck(t)
	var policy map[string]any
	if err := getJSON("/v1/organization/pi-settings", &policy); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mustAPIRequest(t, http.MethodPut, "/v1/organization/pi-settings", string(body), http.StatusOK) })
}
