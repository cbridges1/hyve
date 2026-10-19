package signin

import (
	"context"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
)

// What makes a namespace one of the user's, for the UI: they can list
// clusters there (an environment) or templates (a template namespace).
// Rancher's API filters lists by access the same way; Kubernetes itself
// only answers "list everything" or "forbidden".
var namespaceChecks = []authorizationv1.ResourceAttributes{
	{Group: "cluster.x-k8s.io", Resource: "clusters", Verb: "list"},
	{Group: "hyve.io", Resource: "clustertemplates", Verb: "list"},
}

const namespacesCacheTTL = 30 * time.Second

// NamespaceInfo is one entry of GET /v1/namespaces.
type NamespaceInfo struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

type namespacesCache struct {
	mu      sync.Mutex
	entries map[string]cachedNamespaces
}

type cachedNamespaces struct {
	list  []NamespaceInfo
	until time.Time
}

// handleNamespaces lists the namespaces the caller can work in, for a UI
// whose user can't list namespaces cluster-wide.
func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	key := ident.Username
	s.nsCache.mu.Lock()
	if c, ok := s.nsCache.entries[key]; ok && time.Now().Before(c.until) {
		s.nsCache.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"items": c.list})
		return
	}
	s.nsCache.mu.Unlock()

	list, err := s.userNamespaces(r.Context(), ident)
	if err != nil {
		log.Printf("signin: namespaces for %s: %v", ident.Username, err)
		writeError(w, http.StatusInternalServerError, "ServerError", "couldn't list namespaces")
		return
	}
	s.nsCache.mu.Lock()
	if s.nsCache.entries == nil {
		s.nsCache.entries = map[string]cachedNamespaces{}
	}
	s.nsCache.entries[key] = cachedNamespaces{list: list, until: time.Now().Add(namespacesCacheTTL)}
	s.nsCache.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) userNamespaces(ctx context.Context, ident Identity) ([]NamespaceInfo, error) {
	var nss corev1.NamespaceList
	if err := s.Store.Client.List(ctx, &nss); err != nil {
		return nil, err
	}
	out := []NamespaceInfo{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var firstErr error
	for _, ns := range nss.Items {
		wg.Add(1)
		go func(ns corev1.Namespace) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, attrs := range namespaceChecks {
				attrs.Namespace = ns.Name
				sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
					User: ident.KubeUser(), Groups: ident.KubeGroups(), ResourceAttributes: &attrs,
				}}
				if err := s.Store.Client.Create(ctx, sar); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				if sar.Status.Allowed {
					mu.Lock()
					out = append(out, NamespaceInfo{Name: ns.Name, Labels: ns.Labels})
					mu.Unlock()
					return
				}
			}
		}(ns)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
