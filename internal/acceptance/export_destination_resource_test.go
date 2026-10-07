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

// The test org needs telemetry export enabled. The tests send no telemetry:
// the secrets are fake, and the destinations are deleted at the end.
const exportDestinationAddress = "mergegateway_export_destination.test"

// exportDestinationConfig renders a mergegateway_export_destination with the given body.
func exportDestinationConfig(body string) string {
	return providerConfig() + fmt.Sprintf(`
resource "mergegateway_export_destination" "test" {
%s
}
`, body)
}

// expectExportDestination checks an export destination attribute in state.
func expectExportDestination(path string, value knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(exportDestinationAddress, tfjsonpath.New(path), value)
}

// TestAccExportDestinationResource walks one destination through create (with
// a site the API stores in lowercase), a no-op re-plan, in-place updates of
// every field (rotating the secret with a version change), a new secret
// without one (not sent), clearing ml_app, replace (new preset) and imports
// along the way, then checks destroy. The state must never hold a secret.
func TestAccExportDestinationResource(t *testing.T) {
	name := randomName()
	renamed := name + "-renamed"
	sameID := statecheck.CompareValue(compare.ValuesSame())
	replacedID := statecheck.CompareValue(compare.ValuesDiffer())
	// An import can't recover the secret version: the API never returns the secret.
	importStep := resource.TestStep{
		ResourceName:            exportDestinationAddress,
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: []string{"credential_secret_wo_version"},
	}
	cleared := func(secret string) string {
		return exportDestinationConfig(fmt.Sprintf(`
  name                         = %q
  site                         = "datadoghq.eu"
  credential_secret_wo         = %q
  credential_secret_wo_version = 2
`, renamed, secret))
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   writeOnlyVersions,
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_export_destination", "/v1/export-destinations"),
		Steps: []resource.TestStep{
			// Create a Datadog destination with the defaults. The API stores
			// the site in lowercase; the state keeps the configured form.
			{
				Config: exportDestinationConfig(fmt.Sprintf(`
  name                         = %q
  site                         = "DatadogHQ.com"
  ml_app                       = "tf-acc-app"
  credential_secret_wo         = "tf-acc-dd-secret-AB12"
  credential_secret_wo_version = 1
`, name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(exportDestinationAddress, plancheck.ResourceActionCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(exportDestinationAddress, tfjsonpath.New("id")),
					replacedID.AddStateValue(exportDestinationAddress, tfjsonpath.New("id")),
					expectExportDestination("organization_id", knownvalue.NotNull()),
					expectExportDestination("name", knownvalue.StringExact(name)),
					expectExportDestination("kind", knownvalue.StringExact("otlp")),
					expectExportDestination("otlp_preset", knownvalue.StringExact("datadog")),
					expectExportDestination("site", knownvalue.StringExact("DatadogHQ.com")),
					expectExportDestination("otlp_endpoint", knownvalue.Null()),
					expectExportDestination("ml_app", knownvalue.StringExact("tf-acc-app")),
					expectExportDestination("credential_secret_wo", knownvalue.Null()),
					expectExportDestination("credential_secret_wo_version", knownvalue.Int64Exact(1)),
					expectExportDestination("credential_hint", knownvalue.StringExact("AB12")),
					expectExportDestination("credential_username", knownvalue.Null()),
					expectExportDestination("enabled", knownvalue.Bool(true)),
					expectExportDestination("privacy_mode", knownvalue.Bool(true)),
					expectExportDestination("metrics_enabled", knownvalue.Bool(false)),
					expectExportDestination("sample_rate", knownvalue.Float64Exact(1)),
					expectExportDestination("consecutive_failures", knownvalue.Int64Exact(0)),
					expectExportDestination("created_at", knownvalue.NotNull()),
				},
				Check: expectNoSecret(exportDestinationAddress, "tf-acc-dd-secret-AB12"),
			},
			// Change every field and rotate the secret (a new version).
			{
				Config: exportDestinationConfig(fmt.Sprintf(`
  name                         = %q
  site                         = "datadoghq.eu"
  ml_app                       = "tf-acc-app-2"
  credential_secret_wo         = "tf-acc-dd-secret-CD34"
  credential_secret_wo_version = 2
  enabled                      = false
  privacy_mode                 = false
  metrics_enabled              = true
  sample_rate                  = 0.5
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(exportDestinationAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(exportDestinationAddress, tfjsonpath.New("id")),
					expectExportDestination("name", knownvalue.StringExact(renamed)),
					expectExportDestination("site", knownvalue.StringExact("datadoghq.eu")),
					expectExportDestination("ml_app", knownvalue.StringExact("tf-acc-app-2")),
					expectExportDestination("credential_hint", knownvalue.StringExact("CD34")),
					expectExportDestination("enabled", knownvalue.Bool(false)),
					expectExportDestination("privacy_mode", knownvalue.Bool(false)),
					expectExportDestination("metrics_enabled", knownvalue.Bool(true)),
					expectExportDestination("sample_rate", knownvalue.Float64Exact(0.5)),
				},
				Check: expectNoSecret(exportDestinationAddress, "tf-acc-dd-secret-CD34"),
			},
			// Import with every field set.
			importStep,
			// Clear ml_app and go back to the defaults.
			{
				Config: cleared("tf-acc-dd-secret-CD34"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(exportDestinationAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					sameID.AddStateValue(exportDestinationAddress, tfjsonpath.New("id")),
					expectExportDestination("ml_app", knownvalue.Null()),
					expectExportDestination("enabled", knownvalue.Bool(true)),
					expectExportDestination("privacy_mode", knownvalue.Bool(true)),
					expectExportDestination("metrics_enabled", knownvalue.Bool(false)),
					expectExportDestination("sample_rate", knownvalue.Float64Exact(1)),
				},
			},
			// A new secret without a version change plans nothing: it isn't sent.
			{
				Config:   cleared("tf-acc-dd-secret-unsent"),
				PlanOnly: true,
			},
			// Replace: a new preset creates a new destination.
			{
				Config: exportDestinationConfig(fmt.Sprintf(`
  name                         = %q
  otlp_preset                  = "grafana"
  site                         = "prod-us-east-0"
  credential_username          = "123456"
  credential_secret_wo         = "tf-acc-grafana-token-EF56"
  credential_secret_wo_version = 1
`, renamed)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(exportDestinationAddress, plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					replacedID.AddStateValue(exportDestinationAddress, tfjsonpath.New("id")),
					expectExportDestination("otlp_preset", knownvalue.StringExact("grafana")),
					expectExportDestination("site", knownvalue.StringExact("prod-us-east-0")),
					expectExportDestination("credential_username", knownvalue.StringExact("123456")),
					expectExportDestination("credential_hint", knownvalue.StringExact("EF56")),
				},
				Check: expectNoSecret(exportDestinationAddress, "tf-acc-grafana-token-EF56"),
			},
			// Import by ID.
			importStep,
		},
	})
}

// TestAccExportDestinationResource_drift changes the destination behind
// Terraform's back: a change must plan an update that restores it, a secret
// rotated outside Terraform goes unnoticed (Terraform holds no secret to
// compare), and a deletion must plan a create rather than fail.
func TestAccExportDestinationResource_drift(t *testing.T) {
	name := randomName()
	config := exportDestinationConfig(fmt.Sprintf(`
  name                         = %q
  site                         = "datadoghq.com"
  credential_secret_wo         = "tf-acc-dd-secret-AB12"
  credential_secret_wo_version = 1
`, name))
	var id string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   writeOnlyVersions,
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_export_destination", "/v1/export-destinations"),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[exportDestinationAddress].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/export-destinations/"+id, `{"enabled": false, "sample_rate": 0.1}`, http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(exportDestinationAddress, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectExportDestination("enabled", knownvalue.Bool(true)),
					expectExportDestination("sample_rate", knownvalue.Float64Exact(1)),
				},
			},
			// A secret rotated outside Terraform shows only in
			// credential_hint: the plan is empty, and a version change would
			// set the configured secret again.
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodPatch, "/v1/export-destinations/"+id, `{"credential_secret": "tf-acc-drifted-ZZ99"}`, http.StatusOK)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expectExportDestination("credential_hint", knownvalue.StringExact("ZZ99")),
				},
			},
			{
				PreConfig: func() {
					mustAPIRequest(t, http.MethodDelete, "/v1/export-destinations/"+id, "", http.StatusNoContent)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(exportDestinationAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
