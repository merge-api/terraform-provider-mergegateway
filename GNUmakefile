default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

# Fetch the latest openapi.json from the backend's develop branch. Run `make client` after.
spec:
	gh api "repos/merge-api/merge-gateway-control-backend/contents/openapi.json?ref=develop" -H "Accept: application/vnd.github.raw" > openapi.json

# Regenerate the API client in internal/client from openapi.json.
client:
	cd tools; go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config ../oapi-codegen.yaml -o ../internal/client/client.gen.go ../openapi.json

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: fmt lint test testacc build install generate client spec
