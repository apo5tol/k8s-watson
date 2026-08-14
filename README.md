# k8s-watson

`k8s-watson` is an AI-powered terminal assistant for diagnosing and analysing Kubernetes clusters. It uses an LLM to help investigate cluster issues and work with `kubectl` results.

The project is under active development and is currently an alpha version.

## Build

```sh
make build
```

## Run

```sh
./k8s-watson --model qwen3
```

The model can also be specified with the `K8SWTSN_MODEL` environment variable.
