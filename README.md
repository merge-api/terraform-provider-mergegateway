# Terraform Provider for Merge Gateway

The `mergegateway` provider manages [Merge Gateway](https://merge.dev) configuration as code.

Built on HashiCorp's Terraform Plugin Framework (https://github.com/hashicorp/terraform-plugin-framework).

- Provider, resources and data sources: `internal/provider/`
- API client, generated from `openapi.json` by [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen): `internal/client/`
- Examples (`examples/`) and generated documentation (`docs/`)

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0, and >= 1.11 for `mergegateway_credential` and `mergegateway_export_destination`, whose secrets are write-only
- [Go](https://golang.org/doc/install) >= 1.25

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

To update `openapi.json` from the backend's develop branch, run `make spec` (needs `gh`), then regenerate the API client with `make client`. Never edit `internal/client/client.gen.go` by hand.

To generate or update documentation (and the client), run `make generate`.

### Acceptance tests

Acceptance tests create real resources in a dedicated test org. See [CONTRIBUTING.md](CONTRIBUTING.md) for what the org needs and how to run them.

## License

MIT. Third-party libraries keep their own licenses; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
