# k8s-watson

`k8s-watson` is a terminal assistant for diagnosing and analysing Kubernetes clusters with an Ollama model.

> [!WARNING]
> The project is under active development and is currently an alpha version. Its behaviour and interface may change.

## What is available now

- an interactive terminal chat interface;
- local or remote Ollama endpoints;
- configurable model and request timeouts;
- optional debug logging.

Cluster-aware tooling is still being developed.

## Requirements

- Go (the version declared in [`go.mod`](go.mod));
- an available [Ollama](https://ollama.com/) endpoint and a downloaded compatible model.

## Build

```sh
make build
```

## Run

```sh
./k8s-watson --model qwen3
```

The model can also be supplied through `K8SWTSN_MODEL`:

```sh
K8SWTSN_MODEL=qwen3 ./k8s-watson
```

By default, the application connects to Ollama at `http://localhost:11434`. To use another endpoint, pass `--ollama-url` or set `K8SWTSN_OLLAMA_URL`.

```sh
./k8s-watson --model qwen3 --ollama-url http://localhost:11434
```

Run `./k8s-watson --help` to view all available options.

> [!NOTE]
> Questions entered in the application are sent to the configured Ollama endpoint. Use an HTTPS endpoint when the server is remote if you need encryption in transit.
