# Project Overview

`k8s-watson` is a CLI tool for diagnosing and analyzing the state of Kubernetes clusters.

# Documentation

Project documentation is stored in `docs/` and must be read before starting work.

# Code Comments

Do not add comments that merely restate what the code, identifier, type, or function signature already makes clear. Use comments only to explain non-obvious rationale, constraints, or behavior.

# Project Commands

- `make build` — build the `k8s-watson` binary.
- `make lint` — run static analysis with `golangci-lint`.
- `make test` — run all Go tests.

# Linting

Run the linter from the repository root with `make lint`. Before treating a linter failure as a code issue, run `go list ./...` to confirm that Go can load all packages. If `go list ./...` succeeds but the linter reports `no go files to analyze` inside the sandbox, rerun `make lint` outside the sandbox: `golangci-lint` needs access to the Go build cache. Do not run `go mod tidy` or modify module files without explicit approval.

# Change Verification

After each logically complete block of changes:

1. Format changed Go files with `gofmt -w`.
2. Run `make lint`.
3. Run `make test`.
