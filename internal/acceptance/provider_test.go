// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccProvider_EmptyPlan configures the provider against the API and plans
// a config with no resources, which must show no changes.
func TestAccProvider_EmptyPlan(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:   providerConfig(),
				PlanOnly: true,
			},
		},
	})
}
