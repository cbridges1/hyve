# Testing

## Quick reference

```bash
task check          # go vet + go test ./... — run this before every commit
task test:verbose   # -v output
task test:race      # -race
task test:cover     # -cover
gofmt -l .          # should print nothing; gofmt -w . to fix
```

Or without Task: `go vet ./... && go test ./...`.

CI-equivalent local loop for any change: `gofmt -l .` → `go vet ./...` →
`go test ./...`. All three must be clean before considering a change done.

## Unit tests

Standard Go table-driven tests, colocated with the code (`foo.go` /
`foo_test.go`), no separate test framework. A few conventions specific to
this codebase:

**Kubernetes-backed packages use `fake` clientsets, not a real cluster.**
`internal/k8sjob`, `internal/module` (`JobRunner`), and `internal/workflow`
(`KubernetesJobStepRunner`) test against
`k8s.io/client-go/kubernetes/fake` — construct a fake clientset, seed
whatever Job/Pod state a test needs, run the code under test, assert on the
resulting objects or captured log output. `internal/controller`'s
reconciler tests use `sigs.k8s.io/controller-runtime/pkg/client/fake`
similarly, for the controller-runtime `client.Client` interface. No test in
`go test ./...` talks to a real Kubernetes API — that's what the live smoke
tests (below) are for.

**SQLite-backed packages (`internal/database`, `internal/repository`,
`internal/session`) reset the process-wide singleton per test.** `Repository`
and `Session` both go through a `sync.Once`-guarded `database.GetDB()`
singleton, so pointing a test at a temp directory requires resetting that
singleton first:

```go
func newTestDB(t *testing.T) {
    database.ResetSingleton()
    database.SetConfigDir(t.TempDir())
    t.Cleanup(database.ResetSingleton)
}
```

Using `database.GetDBWithDir(dir)` directly (a separate, throwaway DB
handle) does *not* isolate `internal/session`/`internal/repository` tests,
since their `Save`/`Load`/`Clear`-style functions call the package-level
`database.GetDB()` singleton internally, not whatever handle a test happens
to hold — `SetConfigDir` has no effect until the singleton is reset.

**Secret comparisons use real hashing, not shortcuts.** Tests for session-
secret verification (`internal/api`) exercise the actual
`HashSessionSecret`/`crypto/subtle.ConstantTimeCompare` path — assert
against real hashes and real HTTP handler behavior (wrong secret → 401,
right secret → 200), not by reaching into internals to bypass the
comparison.

## Live smoke tests (`scripts/`, wired via `task test:*`)

These need a real, reachable Kubernetes cluster and are not part of
`go test ./...` — run them explicitly when touching cluster-mode code paths.
Each is self-cleaning (removes the CRDs/namespaces/resources it creates on
exit) unless noted otherwise.

| Task | Script | Covers |
|---|---|---|
| `task test:concurrency` | `scripts/test-concurrency.sh` | `--max-concurrent-reconciles` is actually safe under concurrent reconciles of different clusters (the reason `GITHUB_TOKEN`/secrets are threaded as explicit parameters instead of process env vars — see `docs/ARCHITECTURE.md`). |
| `task test:secretsfrom` | `scripts/test-secretsfrom.sh` | `runtime: client` workflows + `spec.secretsFrom` resolution end to end, isolated via a scratch `HYVE_HOME`. |
| `task test:api` | `scripts/test-api.sh` | The API + auth layer: login, authn/authz, cluster CRUD, module-auth kubeconfig. Runs the API as a local process; primary-cluster/proxy paths need a real in-cluster deployment and aren't covered here. |

Setup for anything beyond these three scripted checks (e.g. manually
verifying a controller/module/session change end to end):

```bash
task cluster:local   # create a local k3d cluster once, host 80/443 -> Traefik
task install:local   # build from local source, install controller + API onto it
```

`install:local` exposes the API via Ingress at `hyve-api.127.0.0.1.nip.io`
(nip.io, a public wildcard-DNS service — used deliberately over a bare
`.localhost` name, which several tools resolve inconsistently) — no
port-forward needed. It's persistent, not self-cleaning: re-run it after
every code change you want to verify live, and manually clean up any
scratch clusters/environments/sessions/secrets you create during
verification afterward (`kubectl delete`, `hyve env remove`, `hyve logout`,
etc.) — nothing in `install:local` itself does this for you.

### Manual live-verification pattern

The pattern used throughout this codebase's cluster-mode work (auth
redesign, module Job dispatch, live secret injection, ...):

1. `task install:local` after every change.
2. Exercise the actual CLI commands against the real cluster (`hyve login`,
   `hyve apply -f ...`, `hyve cluster create`, etc.) — not just unit tests —
   since controller-runtime RBAC, CRD schema, and Job-dispatch behavior only
   surface against a real API server.
3. Check the resulting Kubernetes state directly:
   `kubectl get <resource> -n hyve-system`,
   `kubectl get jobs -n hyve-system` during a reconcile to confirm Jobs are
   actually created and cleaned up, `kubectl logs` on the controller/API
   pods for anything unexpected.
4. Clean up every scratch resource created during verification, and leave
   whatever local environment you were actually using in a normal, working
   state afterward — verification should never leave the cluster or local
   config dirtier than it started.

### A note on RBAC and caching

When adding a new Kubernetes read to the controller or API, check whether it
should go through the cached controller-runtime `Client` (`mgr.GetClient()`)
or an uncached direct read (`mgr.GetAPIReader()` in the controller,
`client.New` in `hyve-api`, which has no cache at all). This matters for
what RBAC the read actually needs:

- A **cached** client's informer needs `list`+`watch` on the resource *type*
  to populate its cache — Kubernetes RBAC's `resourceNames` restriction has
  no effect on `list`/`watch`, only `get`, so a cached read of one
  specific object still requires access to every object of that type in the
  namespace.
- An **uncached** read of one specific object needs only `get` with
  `resourceNames: [...]` — no `list`/`watch` at all, since there's no
  informer to populate.

`hyve-api`'s `hyvesessions` RBAC (`get, create, delete`, no `list`/`watch`)
and the controller's `hyve-cli-secrets` RBAC (`get` with `resourceNames`,
via `APIReader`) are both deliberate examples of the second pattern — see
`deploy/helm/hyve/templates/api-rbac.yaml` and `controller-rbac.yaml`'s own
comments. Get this wrong in either direction and you'll either grant far
broader access than the read actually needs, or the read will fail with a
`Forbidden` that's easy to misdiagnose as a code bug rather than an RBAC gap.
