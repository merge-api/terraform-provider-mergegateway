default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

# Regenerate the API client, docs and THIRD_PARTY_LICENSES.
generate:
	cd tools; go generate ./...

# Regenerate the API client in internal/client from openapi.json.
client:
	cd tools; go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config ../oapi-codegen.yaml -o ../internal/client/client.gen.go ../openapi.json

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: fmt lint test testacc build install generate client
