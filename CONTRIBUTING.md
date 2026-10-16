# Contributing

## Building and testing

You need [Go](https://golang.org/doc/install) >= 1.25, [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.11 and [golangci-lint](https://golangci-lint.run/).

```shell
go build ./...      # build
go test ./...       # unit tests; acceptance tests skip without TF_ACC
make lint           # golangci-lint
make generate       # regenerate docs/ (and the API client); commit the result
```

The API client in `internal/client/` is generated from `openapi.json` by `make client`. Never edit `internal/client/client.gen.go` by hand. `openapi.json` is synced from the Merge Gateway API by pull request.

## Acceptance tests

Acceptance tests (`internal/acceptance/`) create and delete real resources through the API, so they only run with `TF_ACC=1`:

```shell
export MERGE_GATEWAY_MANAGEMENT_KEY=...   # a management key with every scope
export MERGE_GATEWAY_BASE_URL=...         # optional; defaults to http://localhost:8001, not the provider's https://api-gateway.merge.dev
TF_ACC=1 go test -count=1 -v -timeout 30m ./internal/acceptance/...
```

Every test resource is named `tf-acc-*`. Each run first deletes the `tf-acc-` resources a previous run left behind, so only one run may use an organization at a time: two runs at once delete each other's resources.

**Never run them against an organization you use for real work.** Use a dedicated test organization. It needs a payment method so it can create API keys, a Pro or Enterprise plan for `mergegateway_credential`, and the telemetry export feature for `mergegateway_export_destination`.

CI (`.github/workflows/acceptance.yml`) runs the suite for maintainers. Pull requests from forks don't get its secrets, so their acceptance run is skipped; a maintainer runs the suite on the change before merging it.
