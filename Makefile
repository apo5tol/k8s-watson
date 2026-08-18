.PHONY: build test lint

build:
	go build -o k8s-watson ./cmd/k8s-watson

test:
	NO_COLOR=1 go test ./... -json

lint:
	golangci-lint run ./...
