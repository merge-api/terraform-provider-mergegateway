//go:build generate

// Package tools pins the code generators used by `make generate` and runs them.
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)

// The API client in internal/client comes from openapi.json.
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config ../oapi-codegen.yaml -o ../internal/client/client.gen.go ../openapi.json

// Example configurations are formatted before they are copied into the docs.
//go:generate terraform fmt -recursive ../examples/

// The docs in docs/ come from the provider schema and the examples.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. -provider-name mergegateway

// THIRD_PARTY_LICENSES holds the license texts of the modules in the binary.
//go:generate bash ../scripts/third-party-licenses.sh
