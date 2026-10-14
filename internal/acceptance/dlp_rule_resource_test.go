package acceptance

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const dlpRuleAddress = "mergegateway_dlp_rule.test"

// dlpRuleConfig renders a mergegateway_dlp_rule with the given body.
func dlpRuleConfig(body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_dlp_rule" "test" {
%s
}
`, body)
}

// expectDLPRule checks a DLP rule attribute in state.
func expectDLPRule(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(dlpRuleAddress, tfjsonpath.New(path), value)
}

// TestAccDLPRuleResource walks one custom rule through create, a no-op
// re-plan, in-place updates of every field, clearing the pattern and then
// the keywords, and imports along the way, then checks destroy. No field
// forces a replace.
func TestAccDLPRuleResource(t *testing.T) {
	name := randomName()
	renamed := name + "-renamed"
	sameID := statecheck.CompareValue(compare.ValuesSame())
	importStep := resource.TestStep{
		ResourceName:      dlpRuleAddress,
		ImportState:       true,
		ImportStateVerify: true,
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_dlp_rule", "/v1/dlp-rules"),
		Steps: []resource.TestStep{
			// Create a rule with a pattern and the defaults.
			{
				Config: dlpRuleConfig(fmt.Sprintf(`
  entity_type = %q
  pattern     = "TFACC-\\d{6}"
`, name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dlpRuleAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(dlpRuleAddress, tfjsonpath.New("id")),
					expectDLPRule("entity_type", knownvalue.StringExact(name)),
					expectDLPRule("pattern", knownvalue.StringExact(`TFACC-\d{6}`)),
					expectDLPRule("keywords", knownvalue.Null()),
					expectDLPRule("description", knownvalue.StringExact("")),
					expectDLPRule("action", knownvalue.StringExact("log")),
					expectDLPRule("enabled", knownvalue.Bool(true)),
					expectDLPRule("created_at", knownvalue.NotNull()),
				},
			},
			// Change every field. The keywords keep their order.
			{
				Config: dlpRuleConfig(fmt.Sprintf(`
  entity_type = %q
  description = "Terraform acceptance test rule"
  pattern     = "TFACC-\\d{8}"
  keywords    = ["tf-acc-zulu", "tf-acc-alpha"]
  action      = "redact"
  enabled     = false
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dlpRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(dlpRuleAddress, tfjsonpath.New("id")),
					expectDLPRule("entity_type", knownvalue.StringExact(renamed)),
					expectDLPRule("description", knownvalue.StringExact("Terraform acceptance test rule")),
					expectDLPRule("pattern", knownvalue.StringExact(`TFACC-\d{8}`)),
					expectDLPRule("keywords", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("tf-acc-zulu"), knownvalue.StringExact("tf-acc-alpha"),
					})),
					expectDLPRule("action", knownvalue.StringExact("redact")),
					expectDLPRule("enabled", knownvalue.Bool(false)),
				},
			},
			// Import with every field set.
			importStep,
			// Clear the pattern and description, keeping the keywords.
			{
				Config: dlpRuleConfig(fmt.Sprintf(`
  entity_type = %q
  keywords    = ["tf-acc-zulu"]
  action      = "block"
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dlpRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(dlpRuleAddress, tfjsonpath.New("id")),
					expectDLPRule("pattern", knownvalue.Null()),
					expectDLPRule("description", knownvalue.StringExact("")),
					expectDLPRule("keywords", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("tf-acc-zulu")})),
					expectDLPRule("enabled", knownvalue.Bool(true)),
				},
			},
			// Swap the keywords for a pattern.
			{
				Config: dlpRuleConfig(fmt.Sprintf(`
  entity_type = %q
  pattern     = "TFACC-\\d{4}"
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dlpRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(dlpRuleAddress, tfjsonpath.New("id")),
					expectDLPRule("pattern", knownvalue.StringExact(`TFACC-\d{4}`)),
					expectDLPRule("keywords", knownvalue.Null()),
				},
			},
			// Import with the optional fields cleared.
			importStep,
		},
	})
}

// TestAccDLPRuleResource_drift changes the rule behind Terraform's back: a
// change must plan an update that restores it, and a deletion must plan a
// create rather than fail.
func TestAccDLPRuleResource_drift(t *testing.T) {
	name := randomName()
	config := dlpRuleConfig(fmt.Sprintf(`
  entity_type = %q
  pattern     = "TFACC-\\d{6}"
  action      = "redact"
`, name))
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_dlp_rule", "/v1/dlp-rules"),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[dlpRuleAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/dlp-rules/"+id, `{"action": "log", "keywords": ["tf-acc-drifted"]}`, http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(dlpRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectDLPRule("action", knownvalue.StringExact("redact")),
					expectDLPRule("keywords", knownvalue.Null()),
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodDelete, "/v1/dlp-rules/"+id, "", http.StatusNoContent)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(dlpRuleAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
