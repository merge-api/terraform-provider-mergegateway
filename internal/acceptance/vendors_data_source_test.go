// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccVendorsDataSource lists the vendors.
func TestAccVendorsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig() + `
data "mergegateway_vendors" "all" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("data.mergegateway_vendors.all", "vendors.#", notZero),
					resource.TestCheckResourceAttrSet("data.mergegateway_vendors.all", "vendors.0.slug"),
					resource.TestCheckResourceAttrSet("data.mergegateway_vendors.all", "vendors.0.vendor_type"),
					resource.TestCheckResourceAttrSet("data.mergegateway_vendors.all", "vendors.0.allow_byok"),
				),
			},
		},
	})
}
