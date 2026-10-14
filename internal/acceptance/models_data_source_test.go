package acceptance

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccModelsDataSource lists every model, then filters to none.
func TestAccModelsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig() + `
data "mergegateway_models" "all" {}

data "mergegateway_models" "none" {
  min_context_length = 1000000000
  input_modality     = "text"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("data.mergegateway_models.all", "models.#", notZero),
					resource.TestCheckResourceAttrSet("data.mergegateway_models.all", "models.0.id"),
					resource.TestCheckResourceAttrSet("data.mergegateway_models.all", "models.0.model_provider"),
					resource.TestCheckResourceAttrSet("data.mergegateway_models.all", "models.0.status"),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.mergegateway_models.none", tfjsonpath.New("models"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
		},
	})
}

// notZero fails for a count of 0.
func notZero(count string) error {
	if count == "0" {
		return fmt.Errorf("want at least one, got none")
	}
	return nil
}
