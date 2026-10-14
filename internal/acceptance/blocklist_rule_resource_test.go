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

// The rules match only made-up tf-acc- customer IDs, so they block nothing
// real. A rule has no name, so its reason carries the tf-acc- prefix the
// sweeper looks for.
const blocklistRuleAddress = "mergegateway_blocklist_rule.test"

// blocklistRuleConfig renders a mergegateway_blocklist_rule with the given body.
func blocklistRuleConfig(body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_blocklist_rule" "test" {
%s
}
`, body)
}

// expectBlocklistRule checks a blocklist rule attribute in state.
func expectBlocklistRule(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(blocklistRuleAddress, tfjsonpath.New(path), value)
}

// TestAccBlocklistRuleResource walks one rule through create, a no-op
// re-plan, in-place updates of every field (block to pin and back), emptying
// lists, and imports along the way, then checks destroy. No field forces a
// replace.
func TestAccBlocklistRuleResource(t *testing.T) {
	reason := randomName()
	sameID := statecheck.CompareValue(compare.ValuesSame())
	importStep := resource.TestStep{
		ResourceName:      blocklistRuleAddress,
		ImportState:       true,
		ImportStateVerify: true,
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_blocklist_rule", "/v1/blocklist-rules"),
		Steps: []resource.TestStep{
			// Block two customers. The list keeps its order.
			{
				Config: blocklistRuleConfig(fmt.Sprintf(`
  customer_ids = ["tf-acc-customer-b", "tf-acc-customer-a"]
  reason       = %q
`, reason)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(blocklistRuleAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(blocklistRuleAddress, tfjsonpath.New("id")),
					expectBlocklistRule("rule_type", knownvalue.StringExact("block")),
					expectBlocklistRule("customer_ids", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("tf-acc-customer-b"), knownvalue.StringExact("tf-acc-customer-a"),
					})),
					expectBlocklistRule("providers", knownvalue.ListExact([]knownvalue.Check{})),
					expectBlocklistRule("models", knownvalue.ListExact([]knownvalue.Check{})),
					expectBlocklistRule("reason", knownvalue.StringExact(reason)),
					expectBlocklistRule("created_at", knownvalue.NotNull()),
				},
			},
			// Change every field: pin one customer to two models of one provider.
			{
				Config: blocklistRuleConfig(fmt.Sprintf(`
  rule_type    = "pin"
  customer_ids = ["tf-acc-customer-a"]
  providers    = ["openai"]
  models       = ["openai/gpt-4o-mini", "openai/gpt-4o"]
  reason       = "%s-pinned"
`, reason)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(blocklistRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(blocklistRuleAddress, tfjsonpath.New("id")),
					expectBlocklistRule("rule_type", knownvalue.StringExact("pin")),
					expectBlocklistRule("customer_ids", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("tf-acc-customer-a")})),
					expectBlocklistRule("providers", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("openai")})),
					expectBlocklistRule("models", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("openai/gpt-4o-mini"), knownvalue.StringExact("openai/gpt-4o"),
					})),
					expectBlocklistRule("reason", knownvalue.StringExact(reason+"-pinned")),
				},
			},
			// Import with every field set.
			importStep,
			// Back to a block rule; the lists left out are emptied.
			{
				Config: blocklistRuleConfig(fmt.Sprintf(`
  customer_ids = ["tf-acc-customer-c"]
  reason       = %q
`, reason)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(blocklistRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(blocklistRuleAddress, tfjsonpath.New("id")),
					expectBlocklistRule("rule_type", knownvalue.StringExact("block")),
					expectBlocklistRule("providers", knownvalue.ListExact([]knownvalue.Check{})),
					expectBlocklistRule("models", knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			// Import with the lists emptied.
			importStep,
		},
	})
}

// TestAccBlocklistRuleResource_drift changes the rule behind Terraform's
// back: a change must plan an update that restores it, and a deletion must
// plan a create rather than fail.
func TestAccBlocklistRuleResource_drift(t *testing.T) {
	reason := randomName()
	config := blocklistRuleConfig(fmt.Sprintf(`
  customer_ids = ["tf-acc-customer-a"]
  reason       = %q
`, reason))
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_blocklist_rule", "/v1/blocklist-rules"),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[blocklistRuleAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/blocklist-rules/"+id, `{"customer_ids": ["tf-acc-customer-drifted"]}`, http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(blocklistRuleAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectBlocklistRule("customer_ids", knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("tf-acc-customer-a")})),
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodDelete, "/v1/blocklist-rules/"+id, "", http.StatusNoContent)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(blocklistRuleAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
