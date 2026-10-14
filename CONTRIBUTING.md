# Contributing

## Building and testing

You need [Go](https://golang.org/doc/install) >= 1.25, [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.11 and [golangci-lint](https://golangci-lint.run/).

```shell
go build ./...      # build
go test ./...       # unit tests; acceptance tests skip without TF_ACC
make lint           # golangci-lint
make generate       # regenerate docs/ (and the API client); commit the result
```

The API client in `internal/client/` is generated from `openapi.json`: run `make spec` (needs `gh`) to fetch the latest spec, then `make client`. Never edit `internal/client/client.gen.go` by hand.

## Acceptance tests

Acceptance tests (`internal/acceptance/`) create and delete real resources through the API, so they only run with `TF_ACC=1`:

```shell
export MERGE_GATEWAY_BASE_URL=https://api-gateway-develop.merge.dev   # default http://localhost:8001
export MERGE_GATEWAY_MANAGEMENT_KEY=...                              # a management key for the test org
TF_ACC=1 go test -count=1 -v -timeout 30m ./internal/acceptance/...
```

Every test resource is named `tf-acc-*`. Each run first deletes the `tf-acc-` resources a previous run left behind, so only one run may use a test org at a time: two runs at once delete each other's resources.

**Never run them against a production org.** Use a dedicated test org on dev, or a local stack.

### The test org

The dev test org needs:

- a Pro or Enterprise plan, for `mergegateway_credential` (bring-your-own-key)
- the `telemetry_export` feature, for `mergegateway_export_destination`
- a payment method, or the payment-method bypass, so API keys can be created
- a management key holding every scope

### In CI

`.github/workflows/acceptance.yml` runs the suite against dev on pull requests from this repo, pushes to main, nightly and on demand, with the `MERGE_GATEWAY_MANAGEMENT_KEY` repo secret. Runs wait in line for the test org.

Pull requests from forks don't get the secret, so their acceptance run is skipped; a maintainer runs the suite on the change before merging it.

### Against a local stack

```shell
# In merge-gateway, with merge-gateway-control-backend checked out next to it:
docker compose -f docker-compose.local.yml up -d --build --wait control-plane data-plane-grpc

# Back in this repo:
export MERGE_GATEWAY_BASE_URL=http://localhost:8001
export MERGE_GATEWAY_MANAGEMENT_KEY="$(scripts/seed-acceptance.sh)"
TF_ACC=1 go test -count=1 -v -timeout 30m ./internal/acceptance/...
```

`scripts/seed-acceptance.sh` registers a test org on the local stack and prints a management key with every scope. It only works against a local stack.
