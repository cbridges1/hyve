package hostapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/cbridges1/hyve/internal/signin"
)

var cmGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

func TestWatchNeedsASessionAndTargets(t *testing.T) {
	srv := server(false)
	defer srv.Close()
	if resp, err := http.Get(srv.URL + "/v1/hyve/watch?w=/v1/configmaps//"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a session: %v %v, want 401", resp.StatusCode, err)
	}
	signed := server(true)
	defer signed.Close()
	for _, q := range []string{"", "?w=configmaps", "?w=/v1/configmaps"} {
		resp, err := http.Get(signed.URL + "/v1/hyve/watch" + q)
		if err != nil || resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: %v %v, want 400", q, resp.StatusCode, err)
		}
	}
}

func TestWatchStreamsChanges(t *testing.T) {
	dc := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{cmGVR: "ConfigMapList"})
	a := &API{
		Identify: func(*http.Request) (signin.Identity, bool) { return signin.Identity{Username: "alice"}, true },
		Dynamic:  func(signin.Identity) (dynamic.Interface, error) { return dc, nil },
	}
	mux := http.NewServeMux()
	a.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/hyve/watch?w=/v1/configmaps/apps/&w=/v1/configmaps/other/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	go func() {
		// Once both watches have started.
		time.Sleep(300 * time.Millisecond)
		cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a", "namespace": "apps"}}}
		_, _ = dc.Resource(cmGVR).Namespace("apps").Create(context.Background(), cm, metav1.CreateOptions{})
	}()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev watchEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.W != 0 || ev.Type != "ADDED" {
			t.Fatalf("event %+v, want watch 0 ADDED", ev)
		}
		return
	}
	t.Fatalf("stream ended without an event: %v", sc.Err())
}
