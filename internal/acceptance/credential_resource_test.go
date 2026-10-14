// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

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

// The test org needs a Pro or Enterprise plan and BYOK allowed for both
// vendors. It may hold at most one credential per vendor, so these tests
// must not run in parallel with each other.
const (
	credentialAddress = "mergegateway_credential.test"
	credentialAPIBase = "https://example.openai.azure.com"
)

// credentialConfig renders a mergegateway_credential with the given body.
func credentialConfig(body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_credential" "test" {
%s
}
`, body)
}

// expectCredential checks a credential attribute in state.
func expectCredential(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New(path), value)
}

// TestAccCredentialResource walks one credential through create, a no-op
// re-plan, in-place updates of every field (rotating the key with a version
// change), a new key without one (not sent), removing settings (kept),
// replace (new vendor) and imports along the way, then checks destroy. The
// state must never hold a key.
func TestAccCredentialResource(t *testing.T) {
	name := randomName()
	renamed := name + "-renamed"
	sameID := statecheck.CompareValue(compare.ValuesSame())
	replacedID := statecheck.CompareValue(compare.ValuesDiffer())
	// An import can't recover the key version: the API never returns the key.
	importStep := resource.TestStep{
		ResourceName:            credentialAddress,
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: []string{"api_key_wo_version"},
	}
	updated := func(key string) string {
		return credentialConfig(fmt.Sprintf(`
  vendor              = "azure"
  name                = %q
  api_key_wo          = %q
  api_key_wo_version  = 2
  api_base            = "https://tf-acc.openai.azure.com"
  fallback_to_managed = false
  config = {
    api_version      = "2024-10-21"
    azure_deployment = "gpt-4o"
  }
`, renamed, key))
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   writeOnlyVersions,
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_credential", "/v1/credentials"),
		Steps: []resource.TestStep{
			// Create an Azure credential.
			{
				Config: credentialConfig(fmt.Sprintf(`
  vendor             = "azure"
  name               = %q
  api_key_wo         = "tf-acc-azure-key-0000-wxyz"
  api_key_wo_version = 1
  api_base           = %q
  config = {
    api_version = "2024-02-15-preview"
  }
`, name, credentialAPIBase)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(credentialAddress, tfjsonpath.New("id")),
					replacedID.AddStateValue(credentialAddress, tfjsonpath.New("id")),
					expectCredential("vendor", knownvalue.StringExact("azure")),
					expectCredential("name", knownvalue.StringExact(name)),
					expectCredential("api_key_wo", knownvalue.Null()),
					expectCredential("api_key_wo_version", knownvalue.Int64Exact(1)),
					expectCredential("credential_hint", knownvalue.StringExact("wxyz")),
					expectCredential("api_base", knownvalue.StringExact(credentialAPIBase)),
					expectCredential("fallback_to_managed", knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New("config").AtMapKey("api_version"), knownvalue.StringExact("2024-02-15-preview")),
					statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New("config").AtMapKey("azure_deployment"), knownvalue.Null()),
					expectCredential("created_at", knownvalue.NotNull()),
					expectCredential("updated_at", knownvalue.NotNull()),
				},
				Check: expectNoSecret(credentialAddress, "tf-acc-azure-key-0000-wxyz"),
			},
			// Rename, rotate the key (a new version), change api_base,
			// fallback and the config.
			{
				Config: updated("tf-acc-azure-key-rotated-abcd"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(credentialAddress, tfjsonpath.New("id")),
					expectCredential("name", knownvalue.StringExact(renamed)),
					expectCredential("api_key_wo_version", knownvalue.Int64Exact(2)),
					expectCredential("credential_hint", knownvalue.StringExact("abcd")),
					expectCredential("api_base", knownvalue.StringExact("https://tf-acc.openai.azure.com")),
					expectCredential("fallback_to_managed", knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New("config").AtMapKey("api_version"), knownvalue.StringExact("2024-10-21")),
					statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New("config").AtMapKey("azure_deployment"), knownvalue.StringExact("gpt-4o")),
				},
				Check: expectNoSecret(credentialAddress, "tf-acc-azure-key-rotated-abcd"),
			},
			// Import with every field set.
			importStep,
			// A new key without a version change plans nothing: it isn't sent.
			{
				Config:   updated("tf-acc-azure-key-unsent-0000"),
				PlanOnly: true,
			},
			// Removing name, api_base and config keeps their values: the API
			// can't clear them, so Terraform stops managing them.
			{
				Config: credentialConfig(`
  vendor              = "azure"
  api_key_wo          = "tf-acc-azure-key-rotated-abcd"
  api_key_wo_version  = 2
  fallback_to_managed = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectCredential("name", knownvalue.StringExact(renamed)),
					expectCredential("credential_hint", knownvalue.StringExact("abcd")),
					expectCredential("api_base", knownvalue.StringExact("https://tf-acc.openai.azure.com")),
					statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New("config").AtMapKey("api_version"), knownvalue.StringExact("2024-10-21")),
				},
			},
			// Replace: a new vendor creates a new credential, without the
			// settings the old one had.
			{
				Config: credentialConfig(fmt.Sprintf(`
  vendor             = "openai"
  name               = %q
  api_key_wo         = "tf-acc-openai-key-0000-efgh"
  api_key_wo_version = 1
`, name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddress, plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					replacedID.AddStateValue(credentialAddress, tfjsonpath.New("id")),
					expectCredential("vendor", knownvalue.StringExact("openai")),
					expectCredential("credential_hint", knownvalue.StringExact("efgh")),
					expectCredential("api_base", knownvalue.Null()),
					expectCredential("fallback_to_managed", knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(credentialAddress, tfjsonpath.New("config").AtMapKey("api_version"), knownvalue.Null()),
				},
				Check: expectNoSecret(credentialAddress, "tf-acc-openai-key-0000-efgh"),
			},
			// Import by ID.
			importStep,
		},
	})
}

// TestAccCredentialResource_drift changes the credential behind Terraform's
// back: a rename must plan an update that restores it, a key rotated outside
// Terraform goes unnoticed (Terraform holds no key to compare), and a
// deletion must plan a create rather than fail.
func TestAccCredentialResource_drift(t *testing.T) {
	name := randomName()
	config := credentialConfig(fmt.Sprintf(`
  vendor             = "azure"
  name               = %q
  api_key_wo         = "tf-acc-azure-key-0000-wxyz"
  api_key_wo_version = 1
`, name))
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   writeOnlyVersions,
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_credential", "/v1/credentials"),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[credentialAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/credentials/"+id, fmt.Sprintf(`{"name": %q}`, name+"-drifted"), http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectCredential("name", knownvalue.StringExact(name)),
				},
			},
			// A key rotated outside Terraform shows only in credential_hint:
			// the plan is empty, and a version change would set the
			// configured key again.
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/credentials/"+id, `{"api_key": "tf-acc-azure-key-drifted-zzzz"}`, http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectCredential("credential_hint", knownvalue.StringExact("zzzz")),
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodDelete, "/v1/credentials/"+id, "", http.StatusNoContent)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
