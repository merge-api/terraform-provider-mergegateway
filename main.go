// Command terraform-provider-mergegateway serves the Merge Gateway Terraform provider.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/merge-api/terraform-provider-mergegateway/internal/provider"
)

// version is the provider version. Release builds set it with
// -ldflags "-X main.version=..." (see .goreleaser.yml).
var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "run the provider in debug mode so a debugger such as delve can attach")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/merge-api/mergegateway",
		Debug:   *debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
