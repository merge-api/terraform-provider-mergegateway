# Terraform Provider for Merge Gateway

The `mergegateway` provider manages [Merge Gateway](https://merge.dev) configuration as code. It is built on the [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework), starting from HashiCorp's [scaffolding template](https://github.com/hashicorp/terraform-provider-scaffolding-framework).

- Provider, resources and data sources: `internal/provider/`
- API client, generated from `openapi.json` by [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen): `internal/client/`
- Examples (`examples/`) and generated documentation (`docs/`)

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.24

## Building the Provider

1. Clone the repository
1. Enter the repository directory
1. Build the provider using the Go `install` command:

```shell
go install
```

## Adding Dependencies

This provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

## Using the Provider

```terraform
provider "mergegateway" {
  # Or set MERGE_GATEWAY_MANAGEMENT_KEY.
  api_key = var.merge_gateway_management_key

  # Optional. Defaults to https://api-gateway.merge.dev.
  # base_url = "https://api-gateway.merge.dev"
}
```

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To regenerate the API client after `openapi.json` changes, run `make client`. Never edit `internal/client/client.gen.go` by hand.

To generate or update documentation (and the client), run `make generate`.

### Acceptance tests

Acceptance tests (`internal/acceptance/`) create real resources, so they only run with `TF_ACC=1`. They need `MERGE_GATEWAY_MANAGEMENT_KEY`, a management key for a dedicated test org, and `MERGE_GATEWAY_BASE_URL` (default `http://localhost:8001`). Test resources are named `tf-acc-*`; leftovers are deleted at the start of each run.

**In CI**, `.github/workflows/acceptance.yml` runs them against the deployed dev Gateway (`https://api-gateway-develop.merge.dev`) on pull requests from this repo, pushes to main, nightly and on demand. It uses the `MERGE_GATEWAY_MANAGEMENT_KEY` repo secret: a management key, created by an admin in the dev dashboard, for a separate "Terraform acceptance tests" org. Only one run uses the org at a time.

**Locally**, run them against dev the same way with your own test-org key, or against the full Gateway stack in Docker:

```shell
# In merge-gateway, with merge-gateway-control-backend checked out next to it:
docker compose -f docker-compose.local.yml up -d --build --wait control-plane data-plane-grpc

# Back in this repo:
export MERGE_GATEWAY_BASE_URL=http://localhost:8001
export MERGE_GATEWAY_MANAGEMENT_KEY="$(scripts/seed-acceptance.sh)"
TF_ACC=1 go test -v ./internal/acceptance/...
```

`scripts/seed-acceptance.sh` registers a test org on the local stack and prints a management key with every scope. It only works against a local stack.
