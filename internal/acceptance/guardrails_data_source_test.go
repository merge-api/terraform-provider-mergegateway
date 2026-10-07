// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccGuardrailsDataSource reads the guardrails of a project with a PI
// override: the project is listed with the override in force.
func TestAccGuardrailsDataSource(t *testing.T) {
	name := randomName()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkDestroy("mergegateway_project", "/v1/projects"),
		Steps: []resource.TestStep{
			{
				Config: providerConfig() + fmt.Sprintf(`
resource "mergegateway_project" "test" {
  name = %q
}

resource "mergegateway_project_pi_settings" "test" {
  project_id = mergegateway_project.test.id
  pi_mode    = "block"
}

data "mergegateway_guardrails" "all" {
  depends_on = [mergegateway_project_pi_settings.test]
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemNestedAttrs("data.mergegateway_guardrails.all", "guardrails.*", map[string]string{
						"project_name":        name,
						"pi_settings.pi_mode": "block",
					}),
					resource.TestCheckTypeSetElemAttrPair("data.mergegateway_guardrails.all", "guardrails.*.project_id",
						"mergegateway_project.test", "id"),
				),
			},
		},
	})
}
