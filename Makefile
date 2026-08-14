.PHONY: build test lint

build:
	go build -o k8s-watson ./cmd/k8s-watson

test:
	go test ./...

lint:
	golangci-lint run ./...
