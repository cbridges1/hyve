// Package execution provides the in-memory registry hyve-server uses to
// track long-running operations (workflow runs, reconciles, module/workflow
// installs) so HTTP clients can poll status/logs or subscribe to a live
// WebSocket log stream. This is the only execution store hyve-server has, in
// every deployment mode — there is no persistent/durable store, and no
// special-cased integration that pushes execution data anywhere else. A
// caller that wants durable history reads it the same way any other client
// would (poll or subscribe) and persists what it reads on its own side.
package execution

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Status is the lifecycle state of an Execution.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Kind identifies what an Execution is running — mirrors the CLI command
// that triggered it.
type Kind string

const (
	KindReconcile       Kind = "reconcile"
	KindWorkflow        Kind = "workflow"
	KindModuleInstall   Kind = "module-install"
	KindWorkflowInstall Kind = "workflow-install"
)

var idCounter int64

func generateID() string {
	n := atomic.AddInt64(&idCounter, 1)
	return fmt.Sprintf("exec_%d_%d", time.Now().Unix(), n)
}

// Registry is an in-memory store of Executions, keyed by ID.
type Registry struct {
	mu    sync.RWMutex
	execs map[string]*Execution
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{execs: make(map[string]*Execution)}
}

// New creates and registers a new running Execution of the given kind.
func (r *Registry) New(kind Kind) *Execution {
	e := &Execution{
		ID:        generateID(),
		Kind:      kind,
		Status:    StatusRunning,
		StartedAt: time.Now(),
		subs:      make(map[int]chan LogLine),
	}
	r.mu.Lock()
	r.execs[e.ID] = e
	r.mu.Unlock()
	return e
}

// Get returns the Execution with the given ID, if any.
func (r *Registry) Get(id string) (*Execution, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.execs[id]
	return e, ok
}

// List returns all executions, most recently started first.
func (r *Registry) List() []*Execution {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Execution, 0, len(r.execs))
	for _, e := range r.execs {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}
