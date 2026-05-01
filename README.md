# kubectl-diff-watch

A kubectl plugin that watches Kubernetes resources and shows colored diffs when they change.

## Features

- **Pure Go implementation** — no shell-outs to `kubectl`, `diff`, or other tools
- **Multiple output formats** — colored unified diff (default), [dyff](https://github.com/homeport/dyff) structural diff, or plain text
- **Watches any resource** — uses Kubernetes dynamic client, supports all resource types including CRDs
- **Label & field selectors** — filter which resources to watch
- **Multiple resources** — watch multiple resource types simultaneously
- **Clean diffs** — strips `managedFields`, `resourceVersion`, `generation`, and `observedGeneration` by default
- **Timestamps** — shows when each change occurred
- **kubectl plugin** — install as `kubectl diff-watch`

## Installation

```bash
go install github.com/databus23/kubectl-diff-watch@latest
```

Or build from source:

```bash
git clone https://github.com/databus23/kubectl-diff-watch
cd kubectl-diff-watch
go build -o kubectl-diff-watch .
```

Place the binary in your `$PATH` to use as a kubectl plugin.

## Usage

```bash
# Watch a specific pod
kubectl diff-watch pod mypod

# Watch all pods with a label selector
kubectl diff-watch pods -l app=nginx

# Watch a deployment in a specific namespace
kubectl diff-watch deployment myapp -n production

# Watch with dyff structural output
kubectl diff-watch pods -l app=nginx -o dyff

# Watch with no color (for piping to a file)
kubectl diff-watch nodes --no-color

# Watch across all namespaces
kubectl diff-watch pods -l app=nginx -A

# Watch multiple resource types
kubectl diff-watch pods,deployments -l app=nginx

# Watch a specific resource by name
kubectl diff-watch deployment/myapp

# Don't strip managedFields
kubectl diff-watch pod mypod --show-managed-fields

# Show resourceVersion/generation changes
kubectl diff-watch pod mypod --strip-server=false

# Increase diff context lines
kubectl diff-watch pod mypod -C 5
```

## Output Examples

### `diff` (default)

```diff
16:14:55 Deployment/nginx -n production changed
@@ -7,9 +7,9 @@
    spec:
      containers:
      - name: nginx
-       image: nginx:1.24
+       image: nginx:1.25
        ports:
        - containerPort: 80
        resources:
          requests:
-           memory: 128Mi
+           memory: 256Mi
            cpu: 100m
```

### `dyff`

```
16:14:55 Deployment/nginx -n production changed

spec.template.spec.containers.0.image
  ± value change
    - nginx:1.24
    + nginx:1.25

spec.template.spec.containers.0.resources.requests.memory
  ± value change
    - 128Mi
    + 256Mi
```

## Output Formats

### `diff` (default)

Colored unified diff output, similar to `git diff`:

- Added lines in green with `+` prefix
- Removed lines in red with `-` prefix
- Context lines with 2-space indent preserving YAML structure
- File headers in bold red/green
- Hunk headers in cyan

Use `--no-color` to disable colors (e.g. when piping to a file).

### `dyff`

Uses the [dyff](https://github.com/homeport/dyff) library for structural YAML comparison. Shows changes at the semantic level (by path, e.g. `data.foo`) rather than line-by-line, making it easier to understand complex changes.

## Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | `diff` | Output format: `diff`, `dyff` |
| `--diff-context` | `-C` | `3` | Number of context lines in diff |
| `--no-color` | | `false` | Disable colored output |
| `--selector` | `-l` | | Label selector to filter resources |
| `--field-selector` | | | Field selector to filter resources |
| `--namespace` | `-n` | | Namespace to watch in (also `$KUBENAMESPACE`) |
| `--all-namespaces` | `-A` | `false` | Watch across all namespaces |
| `--show-managed-fields` | | `false` | Keep managedFields in output (same as kubectl) |
| `--strip-server` | | `true` | Strip resourceVersion, generation, observedGeneration |
| `--timestamps` | | `true` | Show timestamps on each diff |
| `--kubeconfig` | | | Path to kubeconfig file (also `$KUBECONFIG`) |
| `--context` | | | Kubernetes context to use (also `$KUBECONTEXT`) |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECONFIG` | Path to kubeconfig file (standard kubectl variable) |
| `KUBECONTEXT` | Kubernetes context to use |
| `KUBENAMESPACE` | Namespace to watch in |

## How It Works

1. Resolves resource types using the Kubernetes discovery API (supports short names, plurals, CRDs)
2. Creates dynamic informers for each requested resource with optional label/field selectors
3. On each update event, serializes the resource to YAML, strips noisy server-managed fields, and computes a diff against the previous version
4. Skips events where the cleaned YAML is unchanged (e.g. only resourceVersion bumped)
5. Outputs the diff in the configured format

## Running Tests

Integration tests use [envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest) to spin up a real kube-apiserver:

```bash
# Install envtest binaries (one-time)
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
setup-envtest use

# Run tests
KUBEBUILDER_ASSETS="$(setup-envtest use --print path)" go test -v -timeout 120s ./tests/
```

## Acknowledgements

This project was inspired by:

- [kubectl-watch-diff](https://github.com/alexmt/kubectl-watch-diff) by Alex Matyushentsev
- [kube-watch-diff](https://github.com/leopoldxx/kube-watch-diff) by leopoldxx

## License

Apache License 2.0 — see [LICENSE](./LICENSE) for details.
