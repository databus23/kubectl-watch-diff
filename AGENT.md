# Agent Guidelines

## Project Overview

kubectl-diff-watch is a kubectl plugin written in Go that watches Kubernetes resources and displays colored diffs when they change. It uses the Kubernetes dynamic client (client-go) directly — no shell-outs.

## Project Structure

```
.
├── main.go                  # CLI entry point (cobra), flag parsing
├── pkg/
│   ├── diff/
│   │   ├── diff.go          # Differ interface + factory function
│   │   ├── color.go         # Colored unified diff (default output)
│   │   ├── dyff.go          # dyff structural diff output
│   │   └── simple.go        # Plain text unified diff (no color)
│   └── watch/
│       └── watcher.go       # Kubernetes watcher using dynamic informers
├── tests/
│   └── integration_test.go  # Integration tests using envtest
├── go.mod
├── go.sum
├── .gitignore
└── README.md
```

## Key Design Decisions

- **No shell-outs**: Everything is done in Go — Kubernetes API access via client-go, diff computation via go-difflib, structural diff via dyff library.
- **No cli-runtime/genericclioptions**: We only expose `--kubeconfig`, `--context`, `--namespace` instead of the full set of 20+ kubectl flags. Config is built using `client-go/tools/clientcmd` directly.
- **Pluggable diff output**: The `diff.Differ` interface allows adding new output formats easily. Currently: `diff` (colored unified diff, default) and `dyff` (structural). Color can be disabled with `--no-color`.
- **Clean diffs by default**: `managedFields`, `resourceVersion`, `generation`, and `observedGeneration` are stripped before diffing to reduce noise.
- **Informer-based watch**: Uses `dynamicinformer` which handles reconnection and bookmarks automatically.
- **RestConfig injection for tests**: The `watch.Config` struct accepts a pre-built `*rest.Config` so tests can use envtest without writing kubeconfig files.

## Building

```bash
go build -o kubectl-diff-watch .
```

## Running Tests

Tests require envtest binaries (kube-apiserver + etcd):

```bash
KUBEBUILDER_ASSETS="$(setup-envtest use --print path)" go test -v -timeout 120s ./tests/
```

## Adding a New Output Format

1. Create a new file `pkg/diff/myformat.go`
2. Implement the `diff.Differ` interface (`Diff(w io.Writer, header string, old, new string) error`)
3. Register it in the `New()` factory in `pkg/diff/diff.go`
4. Add it to the `--output` flag description in `main.go`

## Environment Variables

- `KUBECONFIG` — handled natively by client-go's loading rules
- `KUBECONTEXT` — falls back when `--context` flag is not set
- `KUBENAMESPACE` — falls back when `--namespace` flag is not set

## Dependencies (direct)

- `github.com/spf13/cobra` — CLI framework
- `github.com/pmezard/go-difflib` — unified diff computation
- `github.com/mgutz/ansi` — ANSI color codes
- `github.com/homeport/dyff` + `github.com/gonvenience/ytbx` — structural YAML diff
- `k8s.io/client-go` — Kubernetes client (dynamic, discovery, informers)
- `k8s.io/apimachinery` — Kubernetes API types
- `sigs.k8s.io/yaml` — YAML marshaling
- `sigs.k8s.io/controller-runtime` — envtest (test dependency only)
