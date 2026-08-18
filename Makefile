.PHONY: build test lint verify

verify: test lint build

build:
	go build -o k8s-watson ./cmd/k8s-watson

test:
	NO_COLOR=1 go test ./... -json

lint:
	NO_COLOR=1 golangci-lint run --output.json.path stdout ./...
