package hostapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/cbridges1/hyve/internal/signin"
)

// maxWatches bounds how many watches one stream may hold.
const maxWatches = 32

// heartbeat keeps idle streams open through proxies.
const heartbeat = 25 * time.Second

// watchTarget is one w= parameter: group/version/resource/namespace/name,
// with empty segments for the core group, every namespace or every name.
type watchTarget struct {
	gvr             schema.GroupVersionResource
	namespace, name string
}

func parseTarget(s string) (watchTarget, error) {
	p := strings.Split(s, "/")
	if len(p) != 5 || p[1] == "" || p[2] == "" {
		return watchTarget{}, fmt.Errorf("w=%q: want group/version/resource/namespace/name", s)
	}
	return watchTarget{gvr: schema.GroupVersionResource{Group: p[0], Version: p[1], Resource: p[2]}, namespace: p[3], name: p[4]}, nil
}

// watchEvent is what the stream sends: which watch (its index among the w=
// parameters) saw what. No object data, so a stream never carries more
// than the user could list, and Secrets' values never travel.
type watchEvent struct {
	W     int    `json:"w"`
	Type  string `json:"type"`
	Error string `json:"error,omitempty"`
}

// watchStream is GET /v1/hyve/watch?w=...: Kubernetes watches, run as the
// signed-in user, multiplexed onto one server-sent event stream, so the UI
// learns of changes with a single connection (a browser allows six per
// host over HTTP/1.1). Event types are ADDED, MODIFIED, DELETED, RESYNC
// (the watch restarted and may have missed changes) and ERROR (it can't
// run, e.g. forbidden; it isn't retried).
func (a *API) watchStream(w http.ResponseWriter, r *http.Request, ident signin.Identity) {
	raw := r.URL.Query()["w"]
	if len(raw) == 0 || len(raw) > maxWatches {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("give 1 to %d w= parameters", maxWatches))
		return
	}
	targets := make([]watchTarget, len(raw))
	for i, s := range raw {
		t, err := parseTarget(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		targets[i] = t
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming isn't supported here")
		return
	}
	dc, err := a.dynamicAs(ident)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": watching\n\n")
	flusher.Flush()

	ctx := r.Context()
	events := make(chan watchEvent, 64)
	for i, t := range targets {
		go runWatch(ctx, dc, i, t, events)
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-events:
			b, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// runWatch watches one target until ctx ends, starting from the current
// resource version so existing objects aren't reported, and restarting
// (with a RESYNC) whenever the watch ends.
func runWatch(ctx context.Context, dc dynamic.Interface, i int, t watchTarget, events chan<- watchEvent) {
	send := func(ev watchEvent) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	ri := dynamic.ResourceInterface(dc.Resource(t.gvr))
	if t.namespace != "" {
		ri = dc.Resource(t.gvr).Namespace(t.namespace)
	}
	opts := metav1.ListOptions{}
	if t.name != "" {
		opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", t.name).String()
	}
	first := true
	for ctx.Err() == nil {
		lo := opts
		lo.Limit = 1
		list, err := ri.List(ctx, lo)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apierrors.IsMethodNotSupported(err) {
				send(watchEvent{W: i, Type: "ERROR", Error: err.Error()})
				return
			}
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}
		if !first && !send(watchEvent{W: i, Type: "RESYNC"}) {
			return
		}
		first = false
		wo := opts
		wo.ResourceVersion = list.GetResourceVersion()
		wo.AllowWatchBookmarks = true
		wi, err := ri.Watch(ctx, wo)
		if err != nil {
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}
		for ev := range wi.ResultChan() {
			switch ev.Type {
			case watch.Added, watch.Modified, watch.Deleted:
				if !send(watchEvent{W: i, Type: string(ev.Type)}) {
					wi.Stop()
					return
				}
			}
			// Bookmarks keep the watch alive; an Error ends it, and the
			// loop relists.
			if ev.Type == watch.Error {
				break
			}
		}
		wi.Stop()
		if !sleep(ctx, time.Second) {
			return
		}
	}
}

// dynamicAs is a dynamic client as the signed-in user.
func (a *API) dynamicAs(ident signin.Identity) (dynamic.Interface, error) {
	if a.Dynamic != nil {
		return a.Dynamic(ident)
	}
	cfg := rest.CopyConfig(a.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: ident.KubeUser(), Groups: ident.KubeGroups()}
	// Watches outlive the default client timeout.
	cfg.Timeout = 0
	return dynamic.NewForConfig(cfg)
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}
