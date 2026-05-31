# Contributing to Hyve

## Prerequisites

- Go 1.21+
- System `git` binary in `PATH`
- [Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`)

## Development Setup

```bash
git clone https://github.com/cbridges1/hyve.git
cd hyve
go mod tidy
task build
```

## Common Tasks

| Command | Description |
|---------|-------------|
| `task build` | Build the `hyve` binary |
| `task dev -- [args]` | Run directly with `go run` |
| `task test` | Run all tests |
| `task test:race` | Run tests with race detector |
| `task test:cover` | Run tests with coverage report |
| `task vet` | Run `go vet` |
| `task check` | Run vet + tests |
| `task tidy` | Tidy go modules |

## Project Structure

```
cmd/            — Cobra commands (cluster, git, workflow, template, module, ...)
internal/
  module/       — Module resolver, cache, executor
  reconcile/    — GitOps reconcile loop
  state/        — State repo management
  template/     — Cluster templates
  types/        — Shared types (ClusterDefinition, WorkflowsSpec, ...)
  workflow/     — Workflow executor
main.go
```

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for new behavior. Run `task check` before opening a PR.
3. Keep commits focused — one logical change per commit.
4. Update documentation in [`hyve.mintlify.app`](https://github.com/cbridges1/hyve-docs) source if your change affects user-facing behavior.

## Pull Requests

- Target the `main` branch.
- Title should be a short imperative sentence (e.g. `Add GKE node pool scaling`).
- Describe *why* the change is needed, not just what it does.
- Link any related issues.

## Reporting Issues

Open an issue at [github.com/cbridges1/hyve/issues](https://github.com/cbridges1/hyve/issues). Include:
- Hyve version (`hyve --version`)
- Provider and region
- Relevant command output or error message

## License

By contributing you agree that your contributions will be licensed under the [MIT License](LICENSE).
