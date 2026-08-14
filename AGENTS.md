# Project Overview

`k8s-watson` is a CLI tool for diagnosing and analyzing the state of Kubernetes clusters.

# Documentation

Project documentation is stored in `docs/` and must be read before starting work.

# Project Commands

- `make build` — build the `k8s-watson` binary.
- `make lint` — run static analysis with `golangci-lint`.
- `make test` — run all Go tests.

# Change Verification

After each logically complete block of changes:

1. Format changed Go files with `gofmt -w`.
2. Run `make lint`.
3. Run `make test`.
